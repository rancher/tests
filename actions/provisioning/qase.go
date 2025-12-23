package provisioning

import (
	"strings"

	upstream "github.com/qase-tms/qase-go/qase-api-client"
	rancherEc2 "github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tfp-automation/config"
	"github.com/sirupsen/logrus"
)

// GetProvisioningSchemaParams gets a set of params from the cattle config and returns a qase params object
func GetProvisioningSchemaParams(client *rancher.Client, cattleConfig map[string]any) []upstream.TestCaseParameterCreate {
	var params []upstream.TestCaseParameterCreate
	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, cattleConfig, clusterConfig)

	terraformConfig := new(config.TerraformConfig)
	operations.LoadObjectFromMap(config.TerraformConfigurationFileKey, cattleConfig, terraformConfig)

	params = append(params,
		getRunType(terraformConfig),
		getRancherType(terraformConfig),
		getOSNameParam(client, clusterConfig),
		getProviderParam(clusterConfig),
		getK8sParam(clusterConfig),
		getCNIParam(clusterConfig),
		getTurtlesParam(terraformConfig),
	)

	return params
}

// GetCustomSchemaParams gets a set of params from the cattle config and returns a qase params object
func GetCustomSchemaParams(client *rancher.Client, cattleConfig map[string]any) []upstream.TestCaseParameterCreate {
	var params []upstream.TestCaseParameterCreate
	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, cattleConfig, clusterConfig)

	terraformConfig := new(config.TerraformConfig)
	operations.LoadObjectFromMap(config.TerraformConfigurationFileKey, cattleConfig, terraformConfig)

	params = append(params,
		getRunType(terraformConfig),
		getRancherType(terraformConfig),
		getOSNameCustomParam(client, cattleConfig, clusterConfig),
		getProviderParam(clusterConfig),
		getK8sParam(clusterConfig),
		getCNIParam(clusterConfig),
		getTurtlesParam(terraformConfig),
	)

	return params
}

func getRunType(terraform *config.TerraformConfig) upstream.TestCaseParameterCreate {
	var version string
	if terraform.Standalone != nil && terraform.Standalone.UpgradedRancherTagVersion == "" {
		version = terraform.Standalone.RancherTagVersion
	} else if terraform.Standalone != nil && terraform.Standalone.UpgradedRancherTagVersion != "" {
		version = terraform.Standalone.UpgradedRancherTagVersion
	}

	switch {
	case terraform.Standalone == nil:
		return upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: "Scheduled Testing", Values: []string{""}}}
	case version != "" && !strings.Contains(version, "-alpha") && !strings.Contains(version, "-rc"):
		return upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: "Scheduled Testing", Values: []string{version}}}
	case version != "" && strings.Contains(version, "-alpha"):
		return upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: "Release Testing", Values: []string{version}}}
	case version != "" && strings.Contains(version, "-rc"):
		return upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: "RC Testing", Values: []string{version}}}
	}

	return upstream.TestCaseParameterCreate{}
}

func getRancherType(terraform *config.TerraformConfig) upstream.TestCaseParameterCreate {
	var image, version, title string
	isUpgrade := false
	prevImage := ""
	prevVersion := ""

	if terraform.Standalone != nil && terraform.Standalone.UpgradedRancherImage == "" && terraform.Standalone.UpgradedRancherTagVersion == "" {
		image = terraform.Standalone.RancherImage
		version = terraform.Standalone.RancherTagVersion
	} else if terraform.Standalone != nil && terraform.Standalone.UpgradedRancherImage != "" && terraform.Standalone.UpgradedRancherTagVersion != "" {
		image = terraform.Standalone.UpgradedRancherImage
		version = terraform.Standalone.UpgradedRancherTagVersion
		prevImage = terraform.Standalone.RancherImage
		prevVersion = terraform.Standalone.RancherTagVersion
		isUpgrade = true
	}

	if image != "" {
		if isUpgrade {
			var fromType, toType string
			switch prevImage {
			case "rancher/rancher":
				fromType = "Rancher Community"
			default:
				fromType = "Rancher Prime"
			}

			switch image {
			case "rancher/rancher":
				toType = "Rancher Community"
			default:
				toType = "Rancher Prime"
			}

			title = "Upgraded From " + fromType + ": " + prevVersion + " to " + toType
		} else {
			switch image {
			case "rancher/rancher":
				title = "Rancher Community"
			default:
				title = "Rancher Prime"
			}
		}

		return upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: title, Values: []string{version}}}
	}

	return upstream.TestCaseParameterCreate{}
}

func getOSNameParam(client *rancher.Client, clusterConfig *clusters.ClusterConfig) upstream.TestCaseParameterCreate {
	provider := CreateProvider(clusterConfig.Provider)
	credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
	machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

	if strings.Contains(clusterConfig.Provider, "aws") {
		osNames, err := provider.GetOSNamesFunc(client, credentialSpec, machineConfigSpec)
		if err != nil {
			logrus.Warningf("Error getting OS Name %s", err)
			return upstream.TestCaseParameterCreate{}
		}

		return upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: "OS", Values: osNames}}
	}

	return upstream.TestCaseParameterCreate{}
}

func getOSNameCustomParam(client *rancher.Client, cattleConfig map[string]any, clusterConfig *clusters.ClusterConfig) upstream.TestCaseParameterCreate {
	customConfig := new(rancherEc2.AWSEC2Configs)
	operations.LoadObjectFromMap(defaults.AWSEC2Configs, cattleConfig, customConfig)
	externalNodeProvider := ExternalNodeProviderSetup(clusterConfig.NodeProvider)

	if strings.Contains(clusterConfig.Provider, "aws") {
		osNames, err := externalNodeProvider.GetOSNamesFunc(client, *customConfig)
		if err != nil {
			logrus.Warningf("Error getting OS Name %s", err)
			return upstream.TestCaseParameterCreate{}
		}

		return upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: "OS", Values: osNames}}
	}

	return upstream.TestCaseParameterCreate{}
}

func getK8sParam(clusterConfig *clusters.ClusterConfig) upstream.TestCaseParameterCreate {
	return upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: "K8sVersion", Values: []string{clusterConfig.KubernetesVersion}}}
}

func getProviderParam(clusterConfig *clusters.ClusterConfig) upstream.TestCaseParameterCreate {
	return upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: "Provider", Values: []string{clusterConfig.Provider}}}
}

func getCNIParam(clusterConfig *clusters.ClusterConfig) upstream.TestCaseParameterCreate {
	return upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: "CNI", Values: []string{clusterConfig.CNI}}}
}

func getTurtlesParam(terraform *config.TerraformConfig) upstream.TestCaseParameterCreate {
	var prevTurtles, turtles, upgradedTurtles, title, value string

	if terraform.Standalone != nil && terraform.Standalone.FeatureFlags != nil {
		if terraform.Standalone.FeatureFlags.UpgradedTurtles == "" {
			turtles = terraform.Standalone.FeatureFlags.Turtles

			title = "Turtles status: "
			value = turtles

			return upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: title, Values: []string{value}}}
		} else if terraform.Standalone.FeatureFlags.UpgradedTurtles != "" {
			upgradedTurtles = terraform.Standalone.FeatureFlags.UpgradedTurtles
			prevTurtles = terraform.Standalone.FeatureFlags.Turtles

			title = "Turtles status pre-upgrade: " + prevTurtles + " to Turtles status post-upgrade: "
			value = upgradedTurtles

			return upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: title, Values: []string{value}}}
		}
	}

	return upstream.TestCaseParameterCreate{}
}
