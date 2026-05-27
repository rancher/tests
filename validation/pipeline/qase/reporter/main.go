package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/qase-tms/qase-go/pkg/qase-go/clients"
	api_v1_client "github.com/qase-tms/qase-go/qase-api-client"
	"github.com/rancher/shepherd/extensions/defaults"
	qaseactions "github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/qase/testresult"
	"github.com/rancher/tests/validation/pipeline/slack"
	"github.com/sirupsen/logrus"
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

type qaseCaseLookup struct {
	ID    int64
	Title string
}

type rawCaseListResponse struct {
	Result struct {
		Entities []rawCase `json:"entities"`
	} `json:"result"`
}

type rawCase struct {
	ID           *int64                `json:"id"`
	Title        *string               `json:"title"`
	CustomFields []rawCustomFieldValue `json:"custom_fields"`
}

type rawCustomFieldValue struct {
	ID    *int64  `json:"id"`
	Value *string `json:"value"`
}

func main() {
	if runIDEnvVar != "" {
		cfg := clients.ClientConfig{
			APIToken: qaseToken,
		}
		client, err := clients.NewV1Client(cfg)
		if err != nil {
			logrus.Fatalf("error creating Qase client: %v", err)
		}

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

func getAllAutomationTestCases(client *clients.V1Client) (map[string]qaseCaseLookup, error) {
	testCaseNameMap := map[string]qaseCaseLookup{}
	baseURL, err := client.GetAPIClient().GetConfig().ServerURLWithContext(context.Background(), "CasesAPIService.GetCases")
	if err != nil {
		return nil, fmt.Errorf("resolve Qase cases base URL: %w", err)
	}

	httpClient := client.GetAPIClient().GetConfig().HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	offset := 0
	limit := 100

	for {
		requestURL := fmt.Sprintf("%s/case/%s?offset=%d&limit=%d", baseURL, qaseactions.RancherManagerProjectID, offset, limit)

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, fmt.Errorf("build Qase cases request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Token", qaseToken)

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch Qase cases page at offset %d: %w", offset, err)
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read Qase cases response at offset %d: %w", offset, readErr)
		}
		if resp.StatusCode >= http.StatusBadRequest {
			return nil, fmt.Errorf("Qase cases request failed at offset %d: %s: %s", offset, resp.Status, strings.TrimSpace(string(body)))
		}

		var page rawCaseListResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("decode Qase cases page at offset %d: %w", offset, err)
		}

		entities := page.Result.Entities
		if len(entities) == 0 {
			break
		}

		for _, testCase := range entities {
			if testCase.ID == nil || testCase.Title == nil {
				continue
			}

			lookup := qaseCaseLookup{
				ID:    *testCase.ID,
				Title: *testCase.Title,
			}
			automationTestNameCustomField := getAutomationTestName(testCase.CustomFields)
			if automationTestNameCustomField != "" {
				testCaseNameMap[automationTestNameCustomField] = lookup
				continue
			}
			testCaseNameMap[*testCase.Title] = lookup
		}

		offset += len(entities)
		if len(entities) < limit {
			break
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

func reportTestQases(client *clients.V1Client, testRunID int64) (int, error) {
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
			httpCode, err := updateTestInRun(client, *goTestResult, testQase.ID, testRunID)
			if err != nil {
				return httpCode, err
			}

			if goTestResult.Status == failStatus {
				resultTestMap = append(resultTestMap, goTestResult)
			}
		} else {
			// create test case
			logrus.Infof("Creating new test case: %s", goTestResult.Name)
			caseID, err := writeTestCaseToQase(client, *goTestResult)
			if err != nil {
				return 0, err
			}

			// update test status
			httpCode, err := updateTestInRun(client, *goTestResult, *caseID.Result.Id, testRunID)
			if err != nil {
				return httpCode, err
			}

			if goTestResult.Status == failStatus {
				resultTestMap = append(resultTestMap, goTestResult)
			}
		}
	}
	authCtx := context.WithValue(context.TODO(), api_v1_client.ContextAPIKeys, map[string]api_v1_client.APIKey{"TokenAuth": {Key: qaseToken}})
	resp, httpResponse, err := client.GetAPIClient().RunsAPI.GetRun(authCtx, qaseactions.RancherManagerProjectID, int32(testRunID)).Execute()
	if err != nil {
		var statusCode int
		if httpResponse != nil {
			statusCode = httpResponse.StatusCode
		}
		return statusCode, fmt.Errorf("error getting test run: %v", err)
	}
	if strings.Contains(*resp.Result.Title, "-head") {
		return 0, slack.PostSlackMessage(resultTestMap, testRunID, *resp.Result.Title)
	}

	return http.StatusOK, nil
}

func writeTestSuiteToQase(client *clients.V1Client, testResult testresult.GoTestResult) (*int64, error) {
	parentSuite := int64(automationSuiteID)
	var id int64
	for _, suiteGo := range testResult.TestSuite {
		authCtx := context.WithValue(context.TODO(), api_v1_client.ContextAPIKeys, map[string]api_v1_client.APIKey{"TokenAuth": {Key: qaseToken}})
		qaseSuites, _, err := client.GetAPIClient().SuitesAPI.GetSuites(authCtx, qaseactions.RancherManagerProjectID).Search(suiteGo).Execute()
		if err != nil {
			return nil, err
		}

		var testSuiteWasFound bool
		var qaseSuiteFound api_v1_client.Suite
		for _, qaseSuite := range qaseSuites.Result.Entities {
			if *qaseSuite.Title == suiteGo {
				testSuiteWasFound = true
				qaseSuiteFound = qaseSuite
			}
		}
		if !testSuiteWasFound {
			suiteBody := api_v1_client.SuiteCreate{
				Title:    suiteGo,
				ParentId: *api_v1_client.NewNullableInt64(&parentSuite),
			}
			authCtx := context.WithValue(context.TODO(), api_v1_client.ContextAPIKeys, map[string]api_v1_client.APIKey{"TokenAuth": {Key: qaseToken}})
			idResponse, _, err := client.GetAPIClient().SuitesAPI.CreateSuite(authCtx, qaseactions.RancherManagerProjectID).SuiteCreate(suiteBody).Execute()
			if err != nil {
				return nil, err
			}
			id = *idResponse.Result.Id
			parentSuite = id
		} else {
			id = *qaseSuiteFound.Id
		}
	}

	return &id, nil
}

func writeTestCaseToQase(client *clients.V1Client, testResult testresult.GoTestResult) (*api_v1_client.IdResponse, error) {
	testSuiteID, err := writeTestSuiteToQase(client, testResult)
	if err != nil {
		return nil, err
	}
	var zero int32 = 0
	var two int32 = 2
	customFields := map[string]string{
		fmt.Sprintf("%d", testSourceID):         testSource,
		fmt.Sprintf("%d", automationTestNameID): testResult.Name,
	}

	testQaseBody := api_v1_client.TestCaseCreate{
		Title:       testResult.Name,
		SuiteId:     testSuiteID,
		IsFlaky:     &zero,
		Automation:  &two,
		CustomField: &customFields,
	}
	authCtx := context.WithValue(context.TODO(), api_v1_client.ContextAPIKeys, map[string]api_v1_client.APIKey{"TokenAuth": {Key: qaseToken}})
	caseID, resp, err := client.GetAPIClient().CasesAPI.CreateCase(authCtx, qaseactions.RancherManagerProjectID).TestCaseCreate(testQaseBody).Execute()
	if err != nil {
		if resp != nil {
			logrus.Errorf("Qase API error response: %v", resp.Status)
		}
		logrus.Errorf("Failed to create test case: %v", err)
		return nil, err
	}

	return caseID, nil
}

func updateTestInRun(client *clients.V1Client, testResult testresult.GoTestResult, qaseTestCaseID, testRunID int64) (int, error) {
	status := fmt.Sprintf("%sed", testResult.Status)
	var elapsedTime float64
	if testResult.Elapsed != "" {
		var err error
		elapsedTime, err = strconv.ParseFloat(testResult.Elapsed, 64)
		if err != nil {
			return 0, err
		}
	}

	resultBody := api_v1_client.ResultCreate{
		CaseId:  &qaseTestCaseID,
		Status:  status,
		Comment: *api_v1_client.NewNullableString(&testResult.StackTrace),
		Time:    *api_v1_client.NewNullableInt64(api_v1_client.PtrInt64(int64(elapsedTime))),
	}

	authCtx := context.WithValue(context.TODO(), api_v1_client.ContextAPIKeys, map[string]api_v1_client.APIKey{"TokenAuth": {Key: qaseToken}})
	_, resp, err := client.GetAPIClient().ResultsAPI.CreateResult(authCtx, qaseactions.RancherManagerProjectID, int32(testRunID)).ResultCreate(resultBody).Execute()
	if err != nil {
		if resp != nil {
			return resp.StatusCode, err
		}
		return 0, err
	}

	return http.StatusOK, nil
}

func getAutomationTestName(customFields []rawCustomFieldValue) string {
	for _, field := range customFields {
		if field.ID != nil && *field.ID == automationTestNameID && field.Value != nil {
			return *field.Value
		}
	}
	return ""
}
