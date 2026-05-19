//go:build validation || (recurring && dualstack) || dualstack

package dualstack

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	tfpConfig "github.com/rancher/tfp-automation/config"
	"github.com/rancher/tfp-automation/framework/cleanup"
	tfpCustom "github.com/rancher/tfp-automation/tests/infrastructure/downstream/custom"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type customRKE2DualstackTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func customRKE2DualstackSetup(t *testing.T) customRKE2DualstackTest {
	var r customRKE2DualstackTest

	testSession := session.NewSession()
	r.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	r.cattleConfig, err = defaults.LoadPackageDefaults(r.cattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, r.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	r.standardUserClient, _, _, err = standard.CreateStandardUser(r.client)
	require.NoError(t, err)

	return r
}

func TestCustomRKE2Dualstack(t *testing.T) {
	t.Parallel()
	r := customRKE2DualstackSetup(t)

	nodeRolesStandard := []tfpConfig.Nodepool{{Quantity: 3, Etcd: true}, {Quantity: 2, Controlplane: true}, {Quantity: 3, Worker: true}}

	_, terraform, _, _ := tfpConfig.LoadTFPConfigs(r.cattleConfig)
	cidrCluster := terraform.AWSConfig.ClusterCIDR
	cidrService := terraform.AWSConfig.ServiceCIDR

	tests := []struct {
		name            string
		client          *rancher.Client
		nodePools       []tfpConfig.Nodepool
		clusterCIDR     string
		serviceCIDR     string
		stackPreference string
	}{
		{"RKE2_Dual_Stack_Custom_CIDR", r.standardUserClient, nodeRolesStandard, cidrCluster, cidrService, ""},
		{"RKE2_Dual_Stack_Custom_IPv4_Stack_Preference", r.standardUserClient, nodeRolesStandard, "", "", "ipv4"},
		{"RKE2_Dual_Stack_Custom_Dual_Stack_Preference", r.standardUserClient, nodeRolesStandard, "", "", "dual"},
		{"RKE2_Dual_Stack_Custom_CIDR_Dual_Stack_Preference", r.standardUserClient, nodeRolesStandard, cidrCluster, cidrService, "dual"},
		{"RKE2_Dual_Stack_Custom_CIDR_IPv6_First_Dual_Stack_Preference", r.standardUserClient, nodeRolesStandard, SetCIDROrder(cidrCluster, true), SetCIDROrder(cidrService, true), "dual"},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			r.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rancherConfig, terraformConfig, terratestConfig, _ := tfpConfig.LoadTFPConfigs(r.cattleConfig)
			terratestConfig.Nodepools = tt.nodePools
			terraformConfig.AWSConfig.EnablePrimaryIPv6 = true
			terraformConfig.AWSConfig.ClusterCIDR = tt.clusterCIDR
			terraformConfig.AWSConfig.ServiceCIDR = tt.serviceCIDR
			if terraformConfig.AWSConfig.Networking != nil {
				terraformConfig.AWSConfig.Networking.StackPreference = tt.stackPreference
			}

			logrus.Info("Provisioning custom cluster")
			nestedRancherModuleDir, perTestTerraformOptions, _, cluster := tfpCustom.CreateCustomCluster(t, tt.client, rancherConfig, terraformConfig, terratestConfig, defaults.RKE2, "validation/provisioning/dualstack")
			defer os.RemoveAll(nestedRancherModuleDir)
			defer cleanup.Cleanup(t, perTestTerraformOptions, nestedRancherModuleDir)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err := provisioning.VerifyClusterReady(r.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(r.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(r.client, cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetCustomSchemaParams(tt.client, r.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}
