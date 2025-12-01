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

	autoScalingConfig := new(scaling.AutoscalingConfig)
	operations.LoadObjectFromMap(scaling.AutoScalingConfigurationKey, cattleConfig, autoScalingConfig)

	configureAutoscaler, err := features.IsEnabled(client, features.ClusterAutoscaling)
	if err != nil {
		logrus.Errorf("Failed, to get %s state", features.ClusterAutoscaling)
		os.Exit(1)
	}

	if !configureAutoscaler {
		if autoScalingConfig.ChartRepository == "" || autoScalingConfig.Image == "" {
			logrus.Errorf("Autoscaling is not configured and no autoscaling config was provided")
			os.Exit(1)
		}

		logrus.Infof("Setting autoscaler environment variables")
		err = features.ConfigureAutoscaler(client, autoScalingConfig.ChartRepository, autoScalingConfig.Image)
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
	}

	exitCode := m.Run()

	if !configureAutoscaler {
		err = features.UpdateFeatureFlag(client, features.ClusterAutoscaling, false)
		if err != nil {
			logrus.Errorf("Failed, to disable %s: %s", features.ClusterAutoscaling, err)
			os.Exit(1)
		}
	}

	os.Exit(exitCode)
}
