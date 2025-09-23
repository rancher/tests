package main

import (
	"context"
	"flag"
	"log"
	"os"

	qasedefaults "github.com/rancher/tests/actions/qase"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

var (
	testRunName = os.Getenv(qasedefaults.TestRunNameEnvVar)
	qaseToken   = os.Getenv(qasedefaults.QaseTokenEnvVar)
)

type RecurringTestRun struct {
	ID int64 `json:"id" yaml:"id"`
}

func main() {
	// commandline flags
	startRun := flag.Bool("startRun", false, "commandline flag that determines when to start a run, and conversely when to end it.")
	flag.Parse()

	client := qasedefaults.SetupQaseClient()

	if *startRun {
		// create test run
		resp, err := client.CreateTestRun(testRunName, qasedefaults.RancherManagerProjectID)
		if err != nil {
			logrus.Error("error creating test run: ", err)
		}

		newRunID := resp.Result.Id
		recurringTestRun := RecurringTestRun{}
		recurringTestRun.ID = newRunID
		err = writeToConfigFile(recurringTestRun)
		if err != nil {
			logrus.Error("error writiing test run config: ", err)
		}
	} else {

		testRunConfig, err := readConfigFile()
		if err != nil {
			logrus.Fatalf("error reporting converting string to int32: %v", err)
		}
		// complete test run
		_, _, err = client.Client.RunsApi.CompleteRun(context.TODO(), qasedefaults.RancherManagerProjectID, int32(testRunConfig.ID))
		if err != nil {
			log.Fatalf("error completing test run: %v", err)
		}
	}

}

func writeToConfigFile(config RecurringTestRun) error {
	yamlConfig, err := yaml.Marshal(config)

	if err != nil {
		return err
	}

	return os.WriteFile("testrunconfig.yaml", yamlConfig, 0644)
}

func readConfigFile() (*RecurringTestRun, error) {
	configString, err := os.ReadFile("testrunconfig.yaml")
	if err != nil {
		return nil, err
	}

	var testRunConfig RecurringTestRun
	err = yaml.Unmarshal(configString, &testRunConfig)
	if err != nil {
		return nil, err
	}

	return &testRunConfig, nil
}
