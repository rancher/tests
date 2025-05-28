package qase

import (
	"context"
	"fmt"
	"os"

	"github.com/antihax/optional"
	"github.com/sirupsen/logrus"
	upstream "go.qase.io/client"
)

type TestCaseSteps struct {
	Action         string `json:"action,omitempty"`
	ExpectedResult string `json:"expected_result,omitempty"`
	InputData      string `json:"input_data,omitempty"`
	Position       int32  `json:"position,omitempty"`
}

type TestCase struct {
	Description string          `json:"description,omitempty"`
	Title       string          `json:"title,omitempty"`
	SuiteId     int64           `json:"suite_id,omitempty"`
	Automation  int32           `json:"automation,omitempty"`
	Steps       []TestCaseSteps `json:"steps,omitempty"`
}

type service struct {
	client *upstream.APIClient
}

var qaseToken = os.Getenv(QaseTokenEnvVar)

// SetupQaseClient creates a new Qase client from the api token environment variable QASE_AUTOMATION_TOKEN
func SetupQaseClient() *service {
	cfg := upstream.NewConfiguration()
	cfg.AddDefaultHeader("Token", qaseToken)
	return &service{
		client: upstream.NewAPIClient(cfg),
	}
}

// GetTestSuite retrieves a Test Suite by name within a specified Qase Project if it exists
func (q *service) GetTestSuite(project, suite string) (*upstream.Suite, error) {
	logrus.Debugf("Getting test suite \"%s\" in project %s\n", suite, project)
	localVarOptionals := &upstream.SuitesApiGetSuitesOpts{
		FiltersSearch: optional.NewString(suite),
	}
	qaseSuites, _, err := q.client.SuitesApi.GetSuites(context.TODO(), project, localVarOptionals)
	if err != nil {
		return nil, err
	}

	resultLength := len(qaseSuites.Result.Entities)
	if resultLength == 1 {
		return &qaseSuites.Result.Entities[0], nil
	} else if resultLength > 1 {
		return &qaseSuites.Result.Entities[0], fmt.Errorf("test suite \"%s\" found multiple times in project %s, but should only exist once", suite, project)
	}

	return nil, fmt.Errorf("test suite \"%s\" not found in project %s", suite, project)
}

// CreateTestSuite creates a new Test Suite within a specified Qase Project
func (q *service) CreateTestSuite(project, suite string) (int64, error) {
	logrus.Debugf("Creating test suite \"%s\" in project %s\n", suite, project)
	suiteBody := upstream.SuiteCreate{Title: suite}
	resp, _, err := q.client.SuitesApi.CreateSuite(context.TODO(), suiteBody, project)
	if err != nil {
		return 0, fmt.Errorf("failed to create test suite: \"%s\". Error: %v", suite, err)
	}
	return resp.Result.Id, nil
}

// UploadTests either creates new Test Cases and their associated Suite or updates them if they already exist
func (q *service) UploadTests(project string, testCases []TestCase) error {
	for _, tc := range testCases {
		logrus.Info("Uploading test case:\n\tProject: ", project, "\n\tTitle: ", tc.Title, "\n\tDescription: ", tc.Description, "\n\tSuiteId: ", tc.SuiteId, "\n\tAutomation: ", tc.Automation, "\n\tSteps: ", tc.Steps)

		existingCase, err := q.getTestCase(project, tc.Title)
		if err == nil {
			var qaseTest upstream.TestCaseUpdate
			qaseTest.Title = tc.Title
			qaseTest.Description = tc.Description
			qaseTest.SuiteId = tc.SuiteId
			qaseTest.Automation = tc.Automation
			for _, step := range tc.Steps {
				var qaseSteps upstream.TestCaseUpdateSteps
				qaseSteps.Action = step.Action
				qaseSteps.ExpectedResult = step.ExpectedResult
				qaseSteps.Data = step.InputData
				qaseSteps.Position = step.Position
				qaseTest.Steps = append(qaseTest.Steps, qaseSteps)
			}
			err = q.updateTestCase(project, qaseTest, int32(existingCase.Id))
			if err != nil {
				return err
			}
		} else if existingCase != nil {
			return err
		} else {
			var qaseTest upstream.TestCaseCreate
			qaseTest.Title = tc.Title
			qaseTest.Description = tc.Description
			qaseTest.SuiteId = tc.SuiteId
			qaseTest.Automation = tc.Automation
			for _, step := range tc.Steps {
				var qaseSteps upstream.TestCaseCreateSteps
				qaseSteps.Action = step.Action
				qaseSteps.ExpectedResult = step.ExpectedResult
				qaseSteps.Data = step.InputData
				qaseSteps.Position = step.Position
				qaseTest.Steps = append(qaseTest.Steps, qaseSteps)
			}
			err = q.createTestCase(project, qaseTest)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// getTestCase retrieves a Test Case by name within a specified Qase Project if it exists
func (q *service) getTestCase(project, test string) (*upstream.TestCase, error) {
	logrus.Debugf("Getting test case \"%s\" in project %s\n", test, project)
	localVarOptionals := &upstream.CasesApiGetCasesOpts{
		FiltersSearch: optional.NewString(test),
	}
	qaseTestCases, _, err := q.client.CasesApi.GetCases(context.TODO(), project, localVarOptionals)
	if err != nil {
		return nil, err
	}

	resultLength := len(qaseTestCases.Result.Entities)
	if resultLength == 1 {
		return &qaseTestCases.Result.Entities[0], nil
	} else if resultLength > 1 {
		return &qaseTestCases.Result.Entities[0], fmt.Errorf("test case \"%s\" found multiple times in project %s, but should only exist once", test, project)
	}

	return nil, fmt.Errorf("test case \"%s\" not found in project %s", test, project)
}

func (q *service) createTestCase(project string, testCase upstream.TestCaseCreate) error {
	_, _, err := q.client.CasesApi.CreateCase(context.TODO(), testCase, project)
	if err != nil {
		return fmt.Errorf("failed to create test case: \"%s\". Error: %v", testCase.Title, err)
	}
	return nil
}

func (q *service) updateTestCase(project string, testCase upstream.TestCaseUpdate, id int32) error {
	_, _, err := q.client.CasesApi.UpdateCase(context.TODO(), testCase, project, id)
	if err != nil {
		return fmt.Errorf("failed to update test case: \"%s\". Error: %v", testCase.Title, err)
	}
	return nil
}
