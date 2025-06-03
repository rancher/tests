package qase

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sirupsen/logrus"
	upstream "go.qase.io/client"
	"gopkg.in/yaml.v2"
)

// getRawSchemaData retrieves the tests from schemas.yaml files defined within each Go package.
func GetSchemas(basePath string) ([]TestProjectSchema, error) {
	var projectSchemas []TestProjectSchema
	err := filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if strings.Contains(info.Name(), schemas) {
			var projectSchema TestProjectSchema

			fileContent, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			err = yaml.Unmarshal(fileContent, &projectSchema)
			projectSchemas = append(projectSchemas, projectSchema)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return projectSchemas, nil
}

// getSchemaPath retrieves the schema file path from a test case
func getSchemaPath(basePath string) (string, error) {
	var schemaPath string

	err := filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if strings.Contains(info.Name(), schemas) {
			schemaPath = path
			return nil
		}

		return nil
	})

	if err != nil {
		return "", err
	}

	return schemaPath, nil
}

// UpdateSchemaParameters updates the parameters of a test's schema file
func UpdateSchemaParameters(testName string, params []upstream.Params) error {
	_, schemaFilepath, _, _ := runtime.Caller(1)
	packagePath := filepath.Dir(schemaFilepath)

	qaseProjectSchemas, err := GetSchemas(packagePath)
	if len(qaseProjectSchemas) > 1 {
		return fmt.Errorf("%s contains multiple schema files", packagePath)
	}
	if err != nil {
		return err
	}

	for i, qaseProjectSchema := range qaseProjectSchemas {
		for j, qaseSuiteSchema := range qaseProjectSchema.Suites {
			for k, testCase := range qaseSuiteSchema.Cases {
				if testCase.Title == testName {
					qaseProjectSchemas[i].Suites[j].Cases[k].Params = params
				}
			}
		}
	}

	outputContent, err := yaml.Marshal(qaseProjectSchemas[0])
	if err != nil {
		return err
	}

	schemaFile, err := getSchemaPath(packagePath)
	if err != nil {
		return err
	}

	err = os.WriteFile(schemaFile, outputContent, os.ModePerm)
	if err != nil {
		return err
	}

	return nil
}

// GetTestSchema searches a given project schema for a specific test
func GetTestSchema(testName string, projectSchemas []TestProjectSchema) (*upstream.TestCaseCreate, error) {
	for _, projectSchema := range projectSchemas {
		for _, suiteSchema := range projectSchema.Suites {
			for _, testCase := range suiteSchema.Cases {
				if testCase.Title == testName {
					return &testCase, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("unable to find test case %s", testName)
}

// UploadSchema uploads all schema files on a path to qase
func UploadSchemas(client *Service, basePath string) error {
	caseProjectSchemas, err := GetSchemas(basePath)
	if err != nil {
		logrus.Error("Error retrieving test schemas: ", err)
		return err
	}

	for _, caseProjectSchema := range caseProjectSchemas {
		var testCases []upstream.TestCaseCreate
		for _, suite := range caseProjectSchema.Suites {
			suiteID, err := createSuitePath(client, suite.Suite, caseProjectSchema.Project)
			if err != nil {
				return err
			}

			for _, test := range suite.Cases {
				test.SuiteId = suiteID
				testCases = append(testCases, test)
			}
		}

		err = client.UploadTests(caseProjectSchema.Project, testCases)
		if err != nil {
			logrus.Error("Error uploading tests:", err)
		}
	}

	return err
}
