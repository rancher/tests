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

var (
	_, callerFilePath, _, _ = runtime.Caller(0)
	basepath                = filepath.Join(filepath.Dir(callerFilePath), "..", "..", "..", "..")
)

func main() {
	qase.SetupQaseClient()

	schemaMap, err := getRawSchemaData()
	if err != nil {
		logrus.Error("Error retrieving test schemas: ", err)
		return
	}

	for project, schemas := range schemaMap {
		var cases []qase.TestCase
		for _, schema := range schemas {
			suite := extractSubstrings(schema, `## Test Suite: (.+)\n`)[0][1]
			testSuite, suiteErr := qase.GetTestSuite(project, suite)
			testSuiteId := testSuite.Id
			if suiteErr != nil {
				testSuiteId, _ = qase.CreateTestSuite(project, suite)
			}
			parsedCases, err := parseSchema(schema, testSuiteId)
			if err != nil {
				logrus.Error("Error parsing schemas: ", err)
				return
			}
			cases = append(cases, parsedCases...)
		}

		err = qase.UploadTests(project, cases)
		if err != nil {
			logrus.Errorln("Error uploading tests:", err)
		}
	}
}

// getRawSchemaData retrieves the tests from schemas.md files defined within each Go package and returns a map of the string content within each suite
func getRawSchemaData() (map[string][]string, error) {
	schemaMap := make(map[string][]string)
	var content []byte
	err := filepath.Walk(basepath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(info.Name(), "schemas.md") {
			content, err = os.ReadFile(path)
			if err != nil {
				return err
			}
			c := string(content)
			project := extractSubstrings(c, `# (.+) Schemas\n`)[0][1]
			if _, ok := schemaMap[project]; ok {
				schemaMap[project] = append(schemaMap[project], strings.Split(c, "\n---\n")...)
			} else {
				schemaMap[project] = strings.Split(c, "\n---\n")
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
	tests := extractSubstrings(schema, `\n### (.+)\n\n(.+)`)
	steps := extractSubstrings(schema, `\| (\d+) +\| (.+) +\| (.+) +\| (.+) +\|`)
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
	potentialFile := extractSubstrings(str, `^\S*\/\S*\.\S*$`)
	if len(potentialFile) > 0 {
		fp := filepath.Join(basepath, str)
		if _, err := os.Stat(fp); err == nil {
			return true
		}
	}
	return false
}
