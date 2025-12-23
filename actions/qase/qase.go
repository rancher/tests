package qase

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	upstream "github.com/qase-tms/qase-go/qase-api-client"
	"github.com/sirupsen/logrus"
)

type TestSuiteSchema struct {
	Projects []string                  `json:"projects,omitempty" yaml:"projects,omitempty"`
	Suite    string                    `json:"suite,omitempty" yaml:"suite,omitempty"`
	Cases    []upstream.TestCaseCreate `json:"cases,omitempty" yaml:"cases,omitempty"`
}

type Service struct {
	Client *upstream.APIClient
}

const (
	schemas        = "schemas.yaml"
	requestLimit   = 100
	runSourceID    = 16
	recurringRunID = 1
)

// SetupQaseClient creates a new Qase client from the api token environment variable QASE_AUTOMATION_TOKEN
func SetupQaseClient() *Service {
	cfg := upstream.NewConfiguration()
	cfg.AddDefaultHeader("Token", os.Getenv(QaseTokenEnvVar))
	return &Service{
		Client: upstream.NewAPIClient(cfg),
	}
}

// GetTestSuite retrieves a Test Suite by name within a specified Qase Project if it exists
func (q *Service) GetTestSuite(project, suite string, parentID upstream.NullableInt64) (*upstream.Suite, error) {
	logrus.Debugf("Getting test suite \"%s\" in project %s\n", suite, project)

	var numOfSuites int32 = 1
	var offSetCount int32 = 0
	suiteRequest := q.Client.SuitesAPI.GetSuites(context.TODO(), project)

	for numOfSuites > 0 {
		suiteRequest = suiteRequest.Offset(offSetCount)
		suiteRequest = suiteRequest.Limit(requestLimit)
		suiteRequest = suiteRequest.Search(suite)

		suites, _, err := suiteRequest.Execute()
		if err != nil {
			return nil, err
		}

		for _, result := range suites.Result.Entities {
			resultID := result.ParentId.Get()
			parentID := parentID.Get()

			isMatchingID := false
			if parentID == nil && resultID == parentID {
				isMatchingID = true
			} else if parentID != nil && resultID != nil {
				if *parentID == *resultID {
					isMatchingID = true
				}
			}

			if isMatchingID && *result.Title == suite {
				return &result, nil
			}
		}

		numOfSuites = *suites.Result.Count
		offSetCount += numOfSuites
	}

	return nil, fmt.Errorf("test suite \"%s\" not found in project %s", suite, project)
}

// CreateTestSuite creates a new Test Suite within a specified Qase Project
func (q *Service) CreateTestSuite(project string, suite upstream.SuiteCreate) (int64, error) {
	logrus.Debugf("Creating test suite \"%s\" in project %s\n", suite.Title, project)
	suiteRequest := q.Client.SuitesAPI.CreateSuite(context.TODO(), project)

	suiteRequest = suiteRequest.SuiteCreate(suite)
	id, _, err := suiteRequest.Execute()
	if err != nil {
		return 0, fmt.Errorf("failed to create test suite: \"%s\". Error: %v", suite.Title, err)
	}
	return *id.Result.Id, nil
}

// createTestCase creates a new test in qase
func (q *Service) createTestCase(project string, testCase upstream.TestCaseCreate) error {
	testRequest := q.Client.CasesAPI.CreateCase(context.TODO(), project)

	testRequest = testRequest.TestCaseCreate(testCase)
	_, _, err := testRequest.Execute()
	if err != nil {
		return fmt.Errorf("failed to create test case: \"%s\". Error: %v", testCase.Title, err)
	}

	return nil
}

// updateTestCase updates an existing test in qase
func (q *Service) updateTestCase(project string, testCase upstream.TestCaseUpdate, id int32) error {
	testRequest := q.Client.CasesAPI.UpdateCase(context.TODO(), project, id)

	testRequest = testRequest.TestCaseUpdate(testCase)
	_, _, err := testRequest.Execute()
	if err != nil {
		return fmt.Errorf("failed to update test case: \"%s\". Error: %v", *testCase.Title, err)
	}
	return nil
}

// createSuitePath creates a series of nested test suites from a / deliniated string
func createSuitePath(client *Service, suiteName, project string) (int64, error) {
	suites := strings.Split(suiteName, "/")
	testSuiteId := int64(0)
	var parentID *int64

	for _, suite := range suites {
		testSuite, err := client.GetTestSuite(project, suite, *upstream.NewNullableInt64(parentID))
		if testSuite != nil {
			testSuiteId = *testSuite.Id
		}

		if err != nil && testSuite != nil {
			logrus.Error("Could not determine test suite:", err)
			return 0, err
		} else if err != nil {
			logrus.Debugf("Error obtaining test suite: %s", err)
			suiteBody := upstream.SuiteCreate{Title: suite}
			if testSuiteId != 0 {
				suiteBody.ParentId = *upstream.NewNullableInt64(&testSuiteId)
			}
			testSuiteId, _ = client.CreateTestSuite(project, suiteBody)
		}

		parentID = &testSuiteId
	}

	return testSuiteId, nil
}

// UploadTests either creates new Test Cases and their associated Suite or updates them if they already exist
func (q *Service) UploadTests(project string, testCases []upstream.TestCaseCreate) error {
	for _, tc := range testCases {
		matchingCases, err := q.getTestCases(project, tc)
		if err == nil && len(matchingCases) == 1 {
			logrus.Info("Updating test case:\n\tProject: ", project, "\n\tTitle: ", tc.Title, "\n\tSuiteId: ", *tc.SuiteId)
			var qaseTest upstream.TestCaseUpdate
			marshaledConfig, err := json.Marshal(tc)
			if err != nil {
				return err
			}

			err = json.Unmarshal(marshaledConfig, &qaseTest)
			if err != nil {
				return err
			}

			err = q.updateTestCase(project, qaseTest, int32(*matchingCases[0].Id))
			if err != nil {
				logrus.Error(err)
				continue
			}
		} else if len(matchingCases) > 1 {
			logrus.Warningf("Skipping %s: Multiple instances of the same test case found in project %s", tc.Title, project)
			continue
		} else {
			logrus.Info("Creating test case:\n\tProject: ", project, "\n\tTitle: ", tc.Title, "\n\tDescription: ", *tc.Description, "\n\tSuiteId: ")
			err = q.createTestCase(project, tc)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// getTestCases retrieves a Test Case by name within a specified Qase Project if it exists
func (q *Service) getTestCases(project string, test upstream.TestCaseCreate) ([]upstream.TestCase, error) {
	logrus.Debugf("Getting test case \"%s\" in project %s\n", test.Title, project)
	testRequest := q.Client.CasesAPI.GetCases(context.TODO(), project)

	testRequest = testRequest.Search(test.Title)

	testCases, _, err := testRequest.Execute()
	if err != nil {
		return nil, err
	}

	resultLength := len(testCases.Result.Entities)
	if resultLength == 1 {
		return testCases.Result.Entities, nil
	} else if resultLength > 1 {
		var titleMatchingEntities []upstream.TestCase
		for _, entity := range testCases.Result.Entities {
			if entity.Title == &test.Title {
				titleMatchingEntities = append(titleMatchingEntities, entity)
			}
		}

		return testCases.Result.Entities, nil
	}

	return nil, fmt.Errorf("test case \"%s\" not found in project %s", test.Title, project)
}

func (q *Service) CreateTestRun(testRunName string, projectID string, runDescription string) (*upstream.IdResponse, error) {
	runCreateBody := upstream.RunCreate{
		Title: testRunName,
	}

	if projectID == RancherManagerProjectID {
		runCreateBody = upstream.RunCreate{
			Title: testRunName,
			CustomField: &map[string]string{
				fmt.Sprintf("%d", runSourceID): fmt.Sprintf("%d", recurringRunID),
			},
		}
	}

	if runDescription != "" {
		runCreateBody.SetDescription(runDescription)
	}

	runRequest := q.Client.RunsAPI.CreateRun(context.TODO(), projectID)
	runRequest = runRequest.RunCreate(runCreateBody)
	resp, _, err := runRequest.Execute()
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// CompleteTestRun complete the Qase test run
func (q *Service) CompleteTestRun(projectIDEnvVar string, testRunID int32) error {
	runRequest := q.Client.RunsAPI.CompleteRun(context.TODO(), projectIDEnvVar, testRunID)
	_, _, err := runRequest.Execute()
	if err != nil {
		return err
	}

	return nil
}
