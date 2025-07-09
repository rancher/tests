package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/antihax/optional"
	"github.com/rancher/shepherd/extensions/defaults"
	qaseactions "github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/qase/testresult"
	"github.com/rancher/tests/validation/pipeline/slack"
	"github.com/sirupsen/logrus"
	qase "go.qase.io/client"
	yaml "gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	automationSuiteID    = int32(554)
	failStatus           = "fail"
	passStatus           = "pass"
	skipStatus           = "skip"
	automationTestNameID = 15
	testSourceID         = 14
	testSource           = "GoValidation"
	multiSubTestPattern  = `(\w+/\w+/\w+){1,}`
	subtestPattern       = `(\w+/\w+){1,1}`
	testResultsJSON      = "results.json"
)

var (
	multiSubTestReg = regexp.MustCompile(multiSubTestPattern)
	subTestReg      = regexp.MustCompile(subtestPattern)
	qaseToken       = os.Getenv(qaseactions.QaseTokenEnvVar)
	runIDEnvVar     = os.Getenv(qaseactions.TestRunEnvVar)
)

func main() {
	if runIDEnvVar != "" {
		cfg := qase.NewConfiguration()
		cfg.AddDefaultHeader("Token", qaseToken)
		client := qase.NewAPIClient(cfg)

		runID, err := strconv.ParseInt(runIDEnvVar, 10, 64)
		if err != nil {
			logrus.Fatalf("error converting run ID string to int64: %v", err)
		}

		err = wait.PollUntilContextTimeout(
			context.Background(),
			defaults.FiveSecondTimeout,
			defaults.TenMinuteTimeout,
			true,
			func(ctx context.Context) (bool, error) {
				statusCode, err := reportTestQases(client, runID)
				if err == nil {
					logrus.Info("Reported results to Qase successfully.")
					return true, nil
				}

				if statusCode == http.StatusTooManyRequests {
					logrus.Warn("429 Too Many Requests – retrying...")
					return false, nil
				}

				logrus.Errorf("Non-retryable error (HTTP %d): %v", statusCode, err)
				return false, err
			},
		)
		if err != nil {
			logrus.Fatalf("Failed after polling: %v", err)
		}
	}
}

func getAllAutomationTestCases(client *qase.APIClient) (map[string]qase.TestCase, error) {
	testCases := []qase.TestCase{}
	testCaseNameMap := map[string]qase.TestCase{}
	var numOfTestsCases int32 = 1
	offSetCount := 0
	for numOfTestsCases > 0 {
		offset := optional.NewInt32(int32(offSetCount))
		localVarOptionals := &qase.CasesApiGetCasesOpts{
			Offset: offset,
		}
		tempResult, _, err := client.CasesApi.GetCases(context.TODO(), qaseactions.RancherManagerProjectID, localVarOptionals)
		if err != nil {
			return nil, err
		}

		testCases = append(testCases, tempResult.Result.Entities...)
		numOfTestsCases = tempResult.Result.Count
		offSetCount += 10
	}

	for _, testCase := range testCases {
		automationTestNameCustomField := getAutomationTestName(testCase.CustomFields)
		if automationTestNameCustomField != "" {
			testCaseNameMap[automationTestNameCustomField] = testCase
		} else {
			testCaseNameMap[testCase.Title] = testCase
		}

	}

	return testCaseNameMap, nil
}

func readTestCase() ([]testresult.GoTestOutput, error) {
	file, err := os.Open(testResultsJSON)
	if err != nil {
		return nil, err
	}

	fscanner := bufio.NewScanner(file)
	testCases := []testresult.GoTestOutput{}
	for fscanner.Scan() {
		var testCase testresult.GoTestOutput
		err = yaml.Unmarshal(fscanner.Bytes(), &testCase)
		if err != nil {
			return nil, err
		}
		testCases = append(testCases, testCase)
	}

	return testCases, nil
}

func parseCorrectTestCases(testCases []testresult.GoTestOutput) map[string]*testresult.GoTestResult {
	finalTestCases := map[string]*testresult.GoTestResult{}
	var deletedTest string
	var timeoutFailure bool
	for _, testCase := range testCases {
		if testCase.Action == "run" && strings.Contains(testCase.Test, "/") {
			newTestResult := &testresult.GoTestResult{Name: testCase.Test}
			finalTestCases[testCase.Test] = newTestResult
		} else if testCase.Action == "output" && strings.Contains(testCase.Test, "/") {
			goTestResult := finalTestCases[testCase.Test]
			goTestResult.StackTrace += testCase.Output
		} else if testCase.Action == skipStatus {
			delete(finalTestCases, testCase.Test)
		} else if (testCase.Action == failStatus || testCase.Action == passStatus) && strings.Contains(testCase.Test, "/") {
			goTestResult := finalTestCases[testCase.Test]

			if goTestResult != nil {
				substring := subTestReg.FindString(goTestResult.Name)
				goTestResult.StackTrace += testCase.Output
				goTestResult.Status = testCase.Action
				goTestResult.Elapsed = testCase.Elapsed

				if multiSubTestReg.MatchString(goTestResult.Name) && substring != deletedTest {
					deletedTest = subTestReg.FindString(goTestResult.Name)
					delete(finalTestCases, deletedTest)
				}

			}
		} else if testCase.Action == failStatus && testCase.Test == "" {
			timeoutFailure = true
		}
	}

	for _, testCase := range finalTestCases {
		testSuite := strings.Split(testCase.Name, "/")
		testName := testSuite[len(testSuite)-1]
		testCase.Name = testName
		testCase.TestSuite = testSuite[0 : len(testSuite)-1]
		if timeoutFailure && testCase.Status == "" {
			testCase.Status = failStatus
		}
	}

	return finalTestCases
}

func reportTestQases(client *qase.APIClient, testRunID int64) (int, error) {
	tempTestCases, err := readTestCase()
	if err != nil {
		return 0, err
	}

	goTestResults := parseCorrectTestCases(tempTestCases)

	qaseTestCases, err := getAllAutomationTestCases(client)
	if err != nil {
		return 0, err
	}

	resultTestMap := []*testresult.GoTestResult{}
	for _, goTestResult := range goTestResults {
		if testQase, ok := qaseTestCases[goTestResult.Name]; ok {
			// update test status
			httpCode, err := updateTestInRun(client, *goTestResult, testQase.Id, testRunID)
			if err != nil {
				return httpCode, err
			}

			if goTestResult.Status == failStatus {
				resultTestMap = append(resultTestMap, goTestResult)
			}
		} else {
			// write test case
			caseID, err := writeTestCaseToQase(client, *goTestResult)
			if err != nil {
				return 0, err
			}

			httpCode, err := updateTestInRun(client, *goTestResult, caseID.Result.Id, testRunID)
			if err != nil {
				return httpCode, err
			}

			if goTestResult.Status == failStatus {
				resultTestMap = append(resultTestMap, goTestResult)
			}
		}
	}
	resp, httpResponse, err := client.RunsApi.GetRun(context.TODO(), qaseactions.RancherManagerProjectID, int32(testRunID))
	if err != nil {
		var statusCode int
		if httpResponse != nil {
			statusCode = httpResponse.StatusCode
		}
		return statusCode, fmt.Errorf("error getting test run: %v", err)
	}
	if strings.Contains(resp.Result.Title, "-head") {
		return 0, slack.PostSlackMessage(resultTestMap, testRunID, resp.Result.Title)
	}

	return http.StatusOK, nil
}

func writeTestSuiteToQase(client *qase.APIClient, testResult testresult.GoTestResult) (*int64, error) {
	parentSuite := int64(automationSuiteID)
	var id int64
	for _, suiteGo := range testResult.TestSuite {
		localVarOptionals := &qase.SuitesApiGetSuitesOpts{
			FiltersSearch: optional.NewString(suiteGo),
		}

		qaseSuites, _, err := client.SuitesApi.GetSuites(context.TODO(), qaseactions.RancherManagerProjectID, localVarOptionals)
		if err != nil {
			return nil, err
		}

		var testSuiteWasFound bool
		var qaseSuiteFound qase.Suite
		for _, qaseSuite := range qaseSuites.Result.Entities {
			if qaseSuite.Title == suiteGo {
				testSuiteWasFound = true
				qaseSuiteFound = qaseSuite
			}
		}
		if !testSuiteWasFound {
			suiteBody := qase.SuiteCreate{
				Title:    suiteGo,
				ParentId: int64(parentSuite),
			}
			idResponse, _, err := client.SuitesApi.CreateSuite(context.TODO(), suiteBody, qaseactions.RancherManagerProjectID)
			if err != nil {
				return nil, err
			}
			id = idResponse.Result.Id
			parentSuite = id
		} else {
			id = qaseSuiteFound.Id
		}
	}

	return &id, nil
}

func writeTestCaseToQase(client *qase.APIClient, testResult testresult.GoTestResult) (*qase.IdResponse, error) {
	testSuiteID, err := writeTestSuiteToQase(client, testResult)
	if err != nil {
		return nil, err
	}

	testQaseBody := qase.TestCaseCreate{
		Title:      testResult.Name,
		SuiteId:    *testSuiteID,
		IsFlaky:    int32(0),
		Automation: int32(2),
		CustomField: map[string]string{
			fmt.Sprintf("%d", testSourceID): testSource,
		},
	}
	caseID, _, err := client.CasesApi.CreateCase(context.TODO(), testQaseBody, qaseactions.RancherManagerProjectID)
	if err != nil {
		return nil, err
	}
	return &caseID, err
}

func updateTestInRun(client *qase.APIClient, testResult testresult.GoTestResult, qaseTestCaseID, testRunID int64) (int, error) {
	status := fmt.Sprintf("%sed", testResult.Status)
	var elapsedTime float64
	if testResult.Elapsed != "" {
		var err error
		elapsedTime, err = strconv.ParseFloat(testResult.Elapsed, 64)
		if err != nil {
			return 0, err
		}
	}

	resultBody := qase.ResultCreate{
		CaseId:  qaseTestCaseID,
		Status:  status,
		Comment: testResult.StackTrace,
		Time:    int64(elapsedTime),
	}

	_, resp, err := client.ResultsApi.CreateResult(context.TODO(), resultBody, qaseactions.RancherManagerProjectID, testRunID)
	if err != nil {
		if resp != nil {
			return resp.StatusCode, err
		}
		return 0, err
	}

	return http.StatusOK, nil
}

func getAutomationTestName(customFields []qase.CustomFieldValue) string {
	for _, field := range customFields {
		if field.Id == automationTestNameID {
			return field.Value
		}
	}
	return ""
}
