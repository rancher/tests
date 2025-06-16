package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/antihax/optional"
	defaults "github.com/rancher/tests/validation/pipeline/qase"
	"github.com/rancher/tests/validation/pipeline/qase/testcase"
	"github.com/sirupsen/logrus"
	qase "go.qase.io/client"
	"gopkg.in/yaml.v2"
)

var (
	qaseToken               = os.Getenv(defaults.QaseTokenEnvVar)
	runIDEnvVar             = os.Getenv(defaults.TestRunEnvVar)
	projectIDEnvVar         = os.Getenv(defaults.ProjectIDEnvVar)
	_, callerFilePath, _, _ = runtime.Caller(0)
	basepath                = filepath.Join(filepath.Dir(callerFilePath), "..", "..", "..", "..")
)

func main() {
	if runIDEnvVar != "" {
		cfg := qase.NewConfiguration()
		cfg.AddDefaultHeader("Token", qaseToken)
		client := qase.NewAPIClient(cfg)

		runID, err := strconv.ParseInt(runIDEnvVar, 10, 64)
		if err != nil {
			logrus.Fatalf("error reporting converting string to int64: %v", err)
		}

		err = reportTestQases(client, runID)
		if err != nil {
			logrus.Error("error reporting: ", err)
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
		tempResult, _, err := client.CasesApi.GetCases(context.TODO(), projectIDEnvVar, localVarOptionals)
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

func readTestResults() ([]testcase.GoTestOutput, error) {
	file, err := os.Open(defaults.TestResultsJSON)
	if err != nil {
		return nil, err
	}

	fscanner := bufio.NewScanner(file)
	outputLines := []testcase.GoTestOutput{}
	for fscanner.Scan() {
		var testCase testcase.GoTestOutput
		err = yaml.Unmarshal(fscanner.Bytes(), &testCase)
		if err != nil {
			return nil, err
		}
		outputLines = append(outputLines, testCase)
	}

	return outputLines, nil
}

func parseTestResults(outputs []testcase.GoTestOutput) map[string]*testcase.GoTestCase {
	finalTestCases := map[string]*testcase.GoTestCase{}
	var timeoutFailure bool

	for _, output := range outputs {
		testCase := strings.Split(output.Test, "/")
		baseTestCase := testCase[0]
		logrus.Info(output.Time)
		if output.Action == "run" && len(testCase) == 1 {
			newTestCase := &testcase.GoTestCase{Name: output.Test}
			finalTestCases[output.Test] = newTestCase
		} else if output.Action == "output" && baseTestCase != "" {
			goTestCase := finalTestCases[baseTestCase]
			goTestCase.StackTrace += output.Output
		} else if output.Action == defaults.SkipStatus {
			if len(testCase) == 1 {
				delete(finalTestCases, output.Test)
			} else {
				goTestCase := finalTestCases[baseTestCase]
				goTestCase.StackTrace += output.Output
			}
		} else if (output.Action == defaults.FailStatus || output.Action == defaults.PassStatus) && baseTestCase != "" {
			if len(testCase) == 1 {
				goTestCase := finalTestCases[output.Test]
				goTestCase.StackTrace += output.Output
				goTestCase.Status = output.Action
				goTestCase.Elapsed = output.Elapsed
			} else {
				goTestCase := finalTestCases[baseTestCase]
				goTestCase.StackTrace += output.Output
			}
		} else if output.Action == defaults.FailStatus && output.Test == "" {
			timeoutFailure = true
		}
	}

	for _, testCase := range finalTestCases {
		testSuite := strings.Split(testCase.Name, "/")
		testName := testSuite[len(testSuite)-1]
		testCase.Name = testName
		testCase.TestSuite = testSuite[0 : len(testSuite)-1]
		if timeoutFailure && testCase.Status == "" {
			testCase.Status = defaults.FailStatus
		}
	}

	return finalTestCases
}

func reportTestQases(client *qase.APIClient, testRunID int64) error {
	resultsOutputs, err := readTestResults()
	if err != nil {
		return nil
	}

	goTestCases := parseTestResults(resultsOutputs)

	qaseTestCases, err := getAllAutomationTestCases(client)
	if err != nil {
		return err
	}

	for _, goTestCase := range goTestCases {
		if testQase, ok := qaseTestCases[goTestCase.Name]; ok {
			// update test status
			err = updateTestInRun(client, *goTestCase, testQase.Id, testRunID)
			if err != nil {
				return err
			}
		} else {
			err = errors.New(fmt.Sprintf("Test case not found in qase: %s", goTestCase.Name))
			logrus.Warning(err)
		}
	}

	return nil
}

func updateTestInRun(client *qase.APIClient, testCase testcase.GoTestCase, qaseTestCaseID, testRunID int64) error {
	status := fmt.Sprintf("%sed", testCase.Status)
	var elapsedTime float64
	if testCase.Elapsed != "" {
		var err error
		elapsedTime, err = strconv.ParseFloat(testCase.Elapsed, 64)
		if err != nil {
			return err
		}
	}

	resultBody := qase.ResultCreate{
		CaseId:  qaseTestCaseID,
		Status:  status,
		Comment: testCase.StackTrace,
		Time:    int64(elapsedTime),
	}

	_, _, err := client.ResultsApi.CreateResult(context.TODO(), resultBody, projectIDEnvVar, testRunID)
	if err != nil {
		return err
	}

	return nil
}

func getAutomationTestName(customFields []qase.CustomFieldValue) string {
	for _, field := range customFields {
		if field.Id == defaults.AutomationTestNameID {
			return field.Value
		}
	}
	return ""
}
