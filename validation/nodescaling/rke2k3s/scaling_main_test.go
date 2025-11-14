package rke2k3s

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/features"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/scaling"
	"github.com/sirupsen/logrus"
)

func TestMain(m *testing.M) {
	testSession := session.NewSession()
	client, err := rancher.NewClient("", testSession)
	if err != nil {
		logrus.Errorf("Failed, during client setup: %s", err)
		os.Exit(1)
	}

	cattleConfig := config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	if err != nil {
		logrus.Errorf("Failed, to set logger")
		os.Exit(1)
	}

	scalingConfig := new(scaling.Config)
	operations.LoadObjectFromMap(scaling.ScalingConfigurationKey, cattleConfig, scalingConfig)

	logrus.Infof("Setting autoscaler environment variables")
	err = features.ConfigureAutoscaler(client, scalingConfig.AutoscalerChartRepository, scalingConfig.AutoscalerImage)
	if err != nil {
		logrus.Errorf("Failed, to set autoscaler environment variables: %s", err)
		os.Exit(1)
	}

	logrus.Infof("Enabling, %s", features.ClusterAutoscaling)
	err = features.UpdateFeatureFlag(client, features.ClusterAutoscaling, true)
	if err != nil {
		logrus.Errorf("Failed, to enable %s: %s", features.ClusterAutoscaling, err)
		os.Exit(1)
	}

	exitCode := m.Run()

	err = features.UpdateFeatureFlag(client, features.ClusterAutoscaling, false)
	if err != nil {
		logrus.Errorf("Failed, to disable %s: %s", features.ClusterAutoscaling, err)
		os.Exit(1)
	}

	os.Exit(exitCode)
}
