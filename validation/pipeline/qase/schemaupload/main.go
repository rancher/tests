package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/rancher/tests/validation/pipeline/qase"
	"github.com/sirupsen/logrus"
)

const (
	schemas          string = "schemas.md"
	testSuiteRegex   string = `## Test Suite: (.+)\n`
	qaseProjectRegex string = `# (.+) Schemas\n`
	testCaseRegex    string = `\n### (.+)\n\n(.+)`
	testStepsRegex   string = `\| (\d+) +\| (.+) +\| (.+) +\| (.+) +\|`
	projectSeperator string = "\n---\n"
	fileSyntaxRegex  string = `^\S*\/\S*\.\S*$`
)

var (
	_, callerFilePath, _, _ = runtime.Caller(0)
	basepath                = filepath.Join(filepath.Dir(callerFilePath), "..", "..", "..", "..")
)

func main() {
	client := qase.SetupQaseClient()

	schemaMap, err := getRawSchemaData()
	if err != nil {
		logrus.Error("Error retrieving test schemas: ", err)
		return
	}

	for project, schemas := range schemaMap {
		var cases []qase.TestCase
		for _, schema := range schemas {
			suite := extractSubstrings(schema, testSuiteRegex)[0][1]
			testSuite, suiteErr := client.GetTestSuite(project, suite)
			var testSuiteId int64
			if testSuite != nil {
				testSuiteId = testSuite.Id
			}
			if suiteErr != nil && testSuite != nil {
				logrus.Error("Could not determine test suite:", suiteErr)
				return
			} else if suiteErr != nil {
				logrus.Debug("Error obtaining test suite:", suiteErr)
				testSuiteId, _ = client.CreateTestSuite(project, suite)
			}
			parsedCases, err := parseSchema(schema, testSuiteId)
			if err != nil {
				logrus.Error("Error parsing schemas: ", err)
				return
			}
			cases = append(cases, parsedCases...)
		}

		err = client.UploadTests(project, cases)
		if err != nil {
			logrus.Error("Error uploading tests:", err)
		}
	}
}

// getRawSchemaData retrieves the tests from schemas.md files defined within each Go package.
// returns a map of the string content within each suite: Key: [Qase Project] Value: []{TestSuitesAndCases}
// where each project is separated in the schemas file by the common yaml separator of: \n---\n
func getRawSchemaData() (map[string][]string, error) {
	schemaMap := make(map[string][]string)
	var content []byte
	err := filepath.Walk(basepath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(info.Name(), schemas) {
			content, err = os.ReadFile(path)
			if err != nil {
				return err
			}
			c := string(content)
			project := extractSubstrings(c, qaseProjectRegex)[0][1]
			if _, ok := schemaMap[project]; ok {
				schemaMap[project] = append(schemaMap[project], strings.Split(c, projectSeperator)...)
			} else {
				schemaMap[project] = strings.Split(c, projectSeperator)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return schemaMap, nil
}

func parseSchema(schema string, suiteId int64) ([]qase.TestCase, error) {
	tests := extractSubstrings(schema, testCaseRegex)
	steps := extractSubstrings(schema, testStepsRegex)
	var testCases []qase.TestCase
	var testCase qase.TestCase
	for _, test := range tests {
		testCase.SuiteId = suiteId
		testCase.Title = test[1]
		testCase.Description = test[2]
		testCase.Automation = 2
		testCases = append(testCases, testCase)
	}

	index := 0
	var testSteps qase.TestCaseSteps
	for _, step := range steps {
		stepPosition, err := strconv.ParseInt(step[1], 10, 32)
		if err != nil {
			logrus.Error("Error converting string to int: ", err)
			return nil, err
		}
		if stepPosition == 1 && testSteps != (qase.TestCaseSteps{}) {
			index++
			testSteps = qase.TestCaseSteps{}
		}
		testSteps.Position = int32(stepPosition)
		testSteps.Action = strings.TrimSpace(step[2])

		if isFile(strings.TrimSpace(step[3])) {
			fileContent, err := os.ReadFile(filepath.Join(basepath, strings.TrimSpace(step[3])))
			if err != nil {
				logrus.Error("Error reading file: ", err)
				return nil, err
			}
			testSteps.InputData = strings.TrimSpace(step[3]) + "\n\n" + string(fileContent)
		} else {
			testSteps.InputData = strings.TrimSpace(step[3])
		}
		testSteps.ExpectedResult = strings.TrimSpace(step[4])
		testCases[index].Steps = append(testCases[index].Steps, testSteps)
	}

	return testCases, nil
}

// extractSubstrings takes in a string and a regex pattern and returns all matching substrings
func extractSubstrings(text, pattern string) [][]string {
	regex := regexp.MustCompile(pattern)
	return regex.FindAllStringSubmatch(text, -1)
}

// isFile takes any string and determines if it is pointing to a file or not
func isFile(str string) bool {
	potentialFile := extractSubstrings(str, fileSyntaxRegex)
	if len(potentialFile) > 0 {
		fp := filepath.Join(basepath, str)
		if _, err := os.Stat(fp); err == nil {
			return true
		}
	}
	return false
}
