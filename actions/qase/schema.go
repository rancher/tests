package qase

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	upstream "github.com/qase-tms/qase-go/qase-api-client"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

var schemaUpdateMu sync.Mutex

type schemaFile struct {
	path   string
	suites []TestSuiteSchema
}

func shouldIncludeSchemaFile(fileName, prefix string) bool {
	if !strings.Contains(fileName, schemas) {
		return false
	}

	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return true
	}

	return strings.HasPrefix(fileName, prefix+"_")
}

func readSchemaFile(path string) ([]TestSuiteSchema, error) {
	var fileSuiteSchemas []TestSuiteSchema

	fileContent, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	fileContentString := string(fileContent)
	fileContentString = strings.ReplaceAll(fileContentString, "custom_field", "customfield")
	fileContent = []byte(fileContentString)

	err = yaml.Unmarshal(fileContent, &fileSuiteSchemas)
	if err != nil {
		return nil, err
	}

	return fileSuiteSchemas, nil
}

func getSchemaFiles(basePath, prefix string) ([]schemaFile, error) {
	var files []schemaFile

	err := filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info == nil || info.IsDir() {
			return nil
		}
		if !shouldIncludeSchemaFile(info.Name(), prefix) {
			return nil
		}

		suites, readErr := readSchemaFile(path)
		if readErr != nil {
			return readErr
		}

		files = append(files, schemaFile{path: path, suites: suites})
		return nil
	})
	if err != nil {
		return nil, err
	}

	return files, nil
}

// GetSchemasByPrefix retrieves tests from schema files matching the provided prefix.
func GetSchemasByPrefix(basePath, prefix string) ([]TestSuiteSchema, error) {
	var suiteSchemas []TestSuiteSchema
	files, err := getSchemaFiles(basePath, prefix)
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		suiteSchemas = append(suiteSchemas, file.suites...)
	}

	return suiteSchemas, nil
}

func getSchemaPathByPrefix(basePath, prefix string) (string, error) {
	var schemaPath string

	err := filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info == nil || info.IsDir() {
			return nil
		}
		if shouldIncludeSchemaFile(info.Name(), prefix) {
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

// writeFileAtomic writes data to a temp file in the same directory and renames it over path, so
// concurrent readers never observe a truncated or partially written file.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".qase-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}

	return nil
}

// UpdateSchemaParameters updates the parameters of a test's schema file
func UpdateSchemaParameters(testName string, params []upstream.TestCaseParameterCreate) error {
	_, schemaFilepath, _, _ := runtime.Caller(1)
	packagePath := filepath.Dir(schemaFilepath)
	schemaPrefix := os.Getenv(SchemaPrefixEnvVar)
	if strings.TrimSpace(schemaPrefix) == "" {
		logrus.Warnf("QASE schema prefix not provided; skipping parameter reporting for test %s", testName)
		return nil
	}

	schemaUpdateMu.Lock()
	defer schemaUpdateMu.Unlock()

	schemaFiles, err := getSchemaFiles(packagePath, schemaPrefix)
	if err != nil {
		return err
	}
	if len(schemaFiles) > 1 {
		return fmt.Errorf("multiple schema files found under %s for prefix %q", packagePath, strings.TrimSpace(schemaPrefix))
	}

	qaseSuiteSchemas, err := GetSchemasByPrefix(packagePath, schemaPrefix)
	if err != nil {
		return err
	}

	for j, qaseSuiteSchema := range qaseSuiteSchemas {
		for k, testCase := range qaseSuiteSchema.Cases {
			if testCase.Title == testName {
				qaseSuiteSchemas[j].Cases[k].Parameters = params
			}
		}
	}

	if len(qaseSuiteSchemas) == 0 {
		return fmt.Errorf("refusing to overwrite empty schema under %s for prefix %q", packagePath, strings.TrimSpace(schemaPrefix))
	}

	outputContent, err := yaml.Marshal(qaseSuiteSchemas)
	if err != nil {
		return err
	}

	schemaFile, err := getSchemaPathByPrefix(packagePath, schemaPrefix)
	if err != nil {
		return err
	}
	if schemaFile == "" {
		return fmt.Errorf("no schema file found under %s for prefix %q", packagePath, strings.TrimSpace(schemaPrefix))
	}

	return writeFileAtomic(schemaFile, outputContent)
}

// GetTestSchema searches a set of suite schemas for a specific test
func GetTestSchema(testName string, suiteSchemas []TestSuiteSchema) (*upstream.TestCaseCreate, error) {
	for _, suiteSchema := range suiteSchemas {
		for _, testCase := range suiteSchema.Cases {
			if testCase.Title == testName {
				return &testCase, nil
			}

			if testCase.CustomField != nil {
				customField := *testCase.CustomField
				automationTestName, ok := customField[strconv.Itoa(int(AutomationTestNameID))]
				if ok && automationTestName == testName {
					return &testCase, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("unable to find test case %s", testName)
}

// UploadSchema uploads all schema files on a path to qase
func UploadSchemas(client *Service, basePath string) error {
	caseSuiteSchemas, err := GetSchemasByPrefix(basePath, os.Getenv(SchemaPrefixEnvVar))
	if err != nil {
		logrus.Error("Error retrieving test schemas: ", err)
		return err
	}

	for _, suite := range caseSuiteSchemas {
		for _, project := range suite.Projects {
			logrus.Infof("Uploading suite %s to project %s", suite.Suite, project)
			suiteID, err := createSuitePath(client, suite.Suite, project)
			if err != nil {
				return err
			}

			var testCases []upstream.TestCaseCreate
			for _, test := range suite.Cases {
				if test.Title == "" {
					continue
				}
				test.SuiteId = &suiteID
				for i, step := range test.Steps {
					if step.Data != nil {
						if isFile(strings.TrimSpace(*step.Data), basePath) {
							fileContent, err := os.ReadFile(filepath.Join(basePath, strings.TrimSpace(*step.Data)))
							if err != nil {
								logrus.Error("Error reading file: ", err)
								return err
							}

							testData := strings.TrimSpace(*step.Data) + "\n\n" + string(fileContent)
							test.Steps[i].Data = &testData
						}
					}
				}
				testCases = append(testCases, test)
			}

			err = client.UploadTests(project, testCases)
			if err != nil {
				logrus.Error("Error uploading tests:", err)
			}
		}
	}

	return err
}

// isFile takes any string and determines if it is pointing to a file or not
func isFile(str, basePath string) bool {
	regex := regexp.MustCompile(`^\S*\/\S*\.\S*$`)
	potentialFile := regex.FindAllStringSubmatch(str, -1)
	if len(potentialFile) > 0 {
		fp := filepath.Join(basePath, str)
		if _, err := os.Stat(fp); err == nil {
			return true
		}
	}
	return false
}
