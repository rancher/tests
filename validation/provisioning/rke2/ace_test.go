//go:build validation || ace

package rke2

import (
	"os"
	"testing"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	v1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	tfpConfig "github.com/rancher/tfp-automation/config"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type aceTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func aceSetup(t *testing.T) aceTest {
	var r aceTest
	testSession := session.NewSession()
	r.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)
	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	r.cattleConfig, err = defaults.LoadPackageDefaults(r.cattleConfig, "")
	require.NoError(t, err)

	r.cattleConfig, err = defaults.LoadSecretsManagerDefaults(r.cattleConfig)
	require.NoError(t, err)

	err = defaults.VerifyCattleConfig(r.cattleConfig, nil)
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, r.cattleConfig, loggingConfig)
	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	r.cattleConfig, err = defaults.SetK8sDefault(client, defaults.RKE2, r.cattleConfig)
	require.NoError(t, err)

	r.standardUserClient, _, _, err = standard.CreateStandardUser(r.client)
	require.NoError(t, err)

	return r
}

func TestACE(t *testing.T) {
	r := aceSetup(t)

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}
	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)

	clusterConfig.Networking = &provisioninginput.Networking{
		LocalClusterAuthEndpoint: &v1.LocalClusterAuthEndpoint{
			Enabled: true,
		},
	}
	clusterConfig.MachinePools = nodeRolesStandard

	provider := provisioning.CreateProvider(clusterConfig.Provider)
	credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
	machineConfigSpec := provider.LoadMachineConfigFunc(r.cattleConfig)

	logrus.Info("Provisioning downstream cluster")
	cluster, err := provisioning.CreateProvisioningCluster(r.standardUserClient, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
	require.NoError(t, err)

	logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
	err = provisioning.VerifyClusterReady(r.client, cluster)
	require.NoError(t, err)

	logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
	err = deployment.VerifyClusterDeployments(r.standardUserClient, cluster)
	require.NoError(t, err)

	logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
	err = pods.VerifyClusterPods(r.client, cluster)
	require.NoError(t, err)

	logrus.Infof("Verifying service account token secret (%s)", cluster.Name)
	err = clusters.VerifyServiceAccountTokenSecret(r.client, cluster.Name)
	require.NoError(t, err)

	r.client, err = r.client.ReLogin()
	require.NoError(t, err)

	provClusterObj, err := r.client.Steve.SteveType(stevetypes.Provisioning).ByID(cluster.ID)
	require.NoError(t, err)
	require.NotNil(t, provClusterObj)

	clusterStatus := &provv1.ClusterStatus{}
	err = steveV1.ConvertToK8sType(provClusterObj.Status, clusterStatus)
	require.NoError(t, err)

	awsEC2Configs := new(ec2.AWSEC2Configs)
	operations.LoadObjectFromMap(ec2.ConfigurationFileKey, r.cattleConfig, awsEC2Configs)
	require.NotEmpty(t, awsEC2Configs.AWSEC2Config)

	terraformConfig := new(tfpConfig.TerraformConfig)
	operations.LoadObjectFromMap(tfpConfig.TerraformConfigurationFileKey, r.cattleConfig, terraformConfig)
	require.NotEmpty(t, terraformConfig.PrivateKeyPath)
	require.NotEmpty(t, terraformConfig.AWSConfig.AWSUser)
	pemFilePath := terraformConfig.PrivateKeyPath
	logrus.Infof("PATH: %s", pemFilePath)

	sshUser := terraformConfig.AWSConfig.AWSUser

	t.Cleanup(func() {
		logrus.Infof("Running cleanup")

		if cluster != nil {
			extClusters.DeleteK3SRKE2Cluster(r.client, cluster.ID)
		}

		r.session.Cleanup()
	})

	t.Run("Local_Available", func(t *testing.T) {
		logrus.Infof("Verifying ACE (%s)", cluster.Name)
		provisioning.VerifyACE(t, r.client, cluster)
	})

	t.Run("Local_Unavailable", func(t *testing.T) {
		logrus.Infof("Verifying ACE (%s), with local unavailable", cluster.Name)
		provisioning.VerifyACELocalUnavailable(t, r.client, cluster, clusterStatus, pemFilePath, sshUser)
	})

	params := provisioning.GetProvisioningSchemaParams(r.standardUserClient, r.cattleConfig)
	err = qase.UpdateSchemaParameters("RKE2_ACE", params)
	if err != nil {
		logrus.Warningf("Failed to upload schema parameters %s", err)
	}
}
