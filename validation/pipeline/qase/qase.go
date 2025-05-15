package qase

import (
	"context"
	"fmt"
	"os"

	"github.com/antihax/optional"
	"github.com/sirupsen/logrus"
	qase "go.qase.io/client"
)

type TestCaseSteps struct {
	Action         string `json:"action,omitempty"`
	ExpectedResult string `json:"expected_result,omitempty"`
	InputData      string `json:"input_data,omitempty"`
	Position       int32  `json:"position,omitempty"`
}

type TestCase struct {
	Description string          `json:"description,omitempty"`
	Title       string          `json:"title"`
	SuiteId     int64           `json:"suite_id,omitempty"`
	Automation  int32           `json:"automation,omitempty"`
	Steps       []TestCaseSteps `json:"steps,omitempty"`
}

var (
	qaseToken = os.Getenv(QaseTokenEnvVar)
	Client    *qase.APIClient
)

// SetupQaseClient creates a new Qase client from the api token environment variable QASE_AUTOMATION_TOKEN
func SetupQaseClient() {
	cfg := qase.NewConfiguration()
	cfg.AddDefaultHeader("Token", qaseToken)
	Client = qase.NewAPIClient(cfg)
}

// GetTestCase retrieves a Test Case by name within a specified Qase Project if it exists
func GetTestCase(project, test string) (qase.TestCase, error) {
	logrus.Debugf("Getting test case \"%s\" in project %s\n", test, project)
	localVarOptionals := &qase.CasesApiGetCasesOpts{
		FiltersSearch: optional.NewString(test),
	}
	qaseTestCases, _, err := Client.CasesApi.GetCases(context.TODO(), project, localVarOptionals)
	if err != nil {
		return qase.TestCase{}, err
	}

	for _, qaseTestCase := range qaseTestCases.Result.Entities {
		if qaseTestCase.Title == test {
			return qaseTestCase, nil
		}
	}
	return qase.TestCase{}, fmt.Errorf("test case \"%s\" not found in project %s", test, project)
}

// GetAllTestCases retrieves all Test Cases within a specified Qase Project and returns a slice of them
func GetAllTestCases(project string) ([]qase.TestCase, error) {
	logrus.Debugln("Retrieving test cases from Qase for project id: ", project)
	testCases := []qase.TestCase{}
	var numOfTestCases int32 = 1
	offSetCount := 0
	for numOfTestCases > 0 {
		offset := optional.NewInt32(int32(offSetCount))
		localVarOptionals := &qase.CasesApiGetCasesOpts{
			Offset: offset,
		}
		tempResult, _, err := Client.CasesApi.GetCases(context.TODO(), project, localVarOptionals)
		if err != nil {
			return nil, err
		}

		testCases = append(testCases, tempResult.Result.Entities...)
		numOfTestCases = tempResult.Result.Count
		offSetCount += 10
	}

	return testCases, nil
}

// GetTestSuite retrieves a Test Suite by name within a specified Qase Project if it exists
func GetTestSuite(project, suite string) (qase.Suite, error) {
	logrus.Debugf("Getting test suite \"%s\" in project %s\n", suite, project)
	localVarOptionals := &qase.SuitesApiGetSuitesOpts{
		FiltersSearch: optional.NewString(suite),
	}
	qaseSuites, _, err := Client.SuitesApi.GetSuites(context.TODO(), project, localVarOptionals)
	if err != nil {
		return qase.Suite{}, err
	}

	for _, qaseSuite := range qaseSuites.Result.Entities {
		if qaseSuite.Title == suite {
			return qaseSuite, nil
		}
	}
	return qase.Suite{}, fmt.Errorf("test suite \"%s\" not found in project %s", suite, project)
}

// CreateTestSuite retrieves a Test Suite by name within a specified Qase Project if it exists
func CreateTestSuite(project, suite string) (int64, error) {
	logrus.Debugf("Creating test suite \"%s\" in project %s\n", suite, project)
	suiteBody := qase.SuiteCreate{Title: suite}
	resp, _, err := Client.SuitesApi.CreateSuite(context.TODO(), suiteBody, project)
	if err != nil {
		return 0, fmt.Errorf("failed to create test suite: \"%s\". Error: %v", suite, err)
	}
	return resp.Result.Id, nil
}

// UploadTests either creates new Test Cases and their associated Suite or updates them if they already exist
func UploadTests(project string, testCases []TestCase) error {
	for _, tc := range testCases {
		logrus.Info("Uploading test case:\n\tProject: ", project, "\n\tTitle: ", tc.Title, "\n\tDescription: ", tc.Description, "\n\tSuiteId: ", tc.SuiteId, "\n\tAutomation: ", tc.Automation, "\n\tSteps: ", tc.Steps)

		existingCase, err := GetTestCase(project, tc.Title)
		if err == nil {
			var qaseTest qase.TestCaseUpdate
			qaseTest.Title = tc.Title
			qaseTest.Description = tc.Description
			qaseTest.SuiteId = tc.SuiteId
			qaseTest.Automation = tc.Automation
			for _, step := range tc.Steps {
				var qaseSteps qase.TestCaseUpdateSteps
				qaseSteps.Action = step.Action
				qaseSteps.ExpectedResult = step.ExpectedResult
				qaseSteps.Data = step.InputData
				qaseSteps.Position = step.Position
				qaseTest.Steps = append(qaseTest.Steps, qaseSteps)
			}
			err = updateTestCases(project, qaseTest, int32(existingCase.Id))
			if err != nil {
				return err
			}
		} else {
			var qaseTest qase.TestCaseCreate
			qaseTest.Title = tc.Title
			qaseTest.Description = tc.Description
			qaseTest.SuiteId = tc.SuiteId
			qaseTest.Automation = tc.Automation
			for _, step := range tc.Steps {
				var qaseSteps qase.TestCaseCreateSteps
				qaseSteps.Action = step.Action
				qaseSteps.ExpectedResult = step.ExpectedResult
				qaseSteps.Data = step.InputData
				qaseSteps.Position = step.Position
				qaseTest.Steps = append(qaseTest.Steps, qaseSteps)
			}
			err = createTestCases(project, qaseTest)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func createTestCases(project string, testCase qase.TestCaseCreate) error {
	_, _, err := Client.CasesApi.CreateCase(context.TODO(), testCase, project)
	if err != nil {
		return fmt.Errorf("failed to create test case: \"%s\". Error: %v", testCase.Title, err)
	}
	return nil
}

func updateTestCases(project string, testCase qase.TestCaseUpdate, id int32) error {
	_, _, err := Client.CasesApi.UpdateCase(context.TODO(), testCase, project, id)
	if err != nil {
		return fmt.Errorf("failed to update test case: \"%s\". Error: %v", testCase.Title, err)
	}
	return nil
}
