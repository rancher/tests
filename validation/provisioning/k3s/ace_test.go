//go:build validation || recurring

package k3s

import (
	"os"
	"path/filepath"
	"testing"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	v1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
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
	var k aceTest
	testSession := session.NewSession()
	k.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)
	k.client = client

	k.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))
	k.cattleConfig, err = defaults.LoadPackageDefaults(k.cattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, k.cattleConfig, loggingConfig)
	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	k.cattleConfig, err = defaults.SetK8sDefault(client, defaults.K3S, k.cattleConfig)
	require.NoError(t, err)

	k.standardUserClient, _, _, err = standard.CreateStandardUser(k.client)
	require.NoError(t, err)

	return k
}

func TestACE(t *testing.T) {
	k := aceSetup(t)

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}
	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, k.cattleConfig, clusterConfig)

	clusterConfig.Networking = &provisioninginput.Networking{
		LocalClusterAuthEndpoint: &v1.LocalClusterAuthEndpoint{
			Enabled: true,
		},
	}
	clusterConfig.MachinePools = nodeRolesStandard

	provider := provisioning.CreateProvider(clusterConfig.Provider)
	credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
	machineConfigSpec := provider.LoadMachineConfigFunc(k.cattleConfig)

	logrus.Info("Provisioning downstream cluster")
	cluster, err := provisioning.CreateProvisioningCluster(k.standardUserClient, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
	require.NoError(t, err)

	logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
	err = provisioning.VerifyClusterReady(k.client, cluster)
	require.NoError(t, err)

	logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
	err = deployment.VerifyClusterDeployments(k.standardUserClient, cluster)
	require.NoError(t, err)

	logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
	err = pods.VerifyClusterPods(k.client, cluster)
	require.NoError(t, err)

	logrus.Infof("Verifying service account token secret (%s)", cluster.Name)
	err = clusters.VerifyServiceAccountTokenSecret(k.client, cluster.Name)
	require.NoError(t, err)

	k.client, err = k.client.ReLogin()
	require.NoError(t, err)

	provClusterObj, err := k.client.Steve.SteveType("provisioning.cattle.io.cluster").ByID(cluster.ID)
	require.NoError(t, err)

	clusterStatus := &provv1.ClusterStatus{}
	err = steveV1.ConvertToK8sType(provClusterObj.Status, clusterStatus)
	require.NoError(t, err)

	pemFilePath := filepath.Join(
		k.cattleConfig["sshPath"].(map[string]any)["sshPath"].(string),
		k.cattleConfig["awsEC2Configs"].(map[string]any)["awsEC2Config"].([]map[string]any)[0]["awsSSHKeyName"].(string),
	)

	sshUser := k.cattleConfig["awsEC2Configs"].(map[string]any)["awsEC2Config"].([]map[string]any)[0]["awsUser"].(string)
	t.Cleanup(func() {
		logrus.Infof("Running cleanup")

		if cluster != nil {
			extClusters.DeleteK3SRKE2Cluster(k.client, cluster.ID)
		}

		k.session.Cleanup()
	})

	t.Run("Local_Available", func(t *testing.T) {
		logrus.Infof("Verifying ACE (%s)", cluster.Name)
		provisioning.VerifyACE(t, k.client, cluster)
	})

	t.Run("Local_Unavailable", func(t *testing.T) {
		logrus.Infof("Verifying ACE (%s), with local unavailable", cluster.Name)
		provisioning.VerifyACELocalUnavailable(t, k.client, cluster, clusterStatus, pemFilePath, sshUser)
	})

	params := provisioning.GetProvisioningSchemaParams(k.standardUserClient, k.cattleConfig)
	err = qase.UpdateSchemaParameters("K3S_ACE", params)
	if err != nil {
		logrus.Warningf("Failed to upload schema parameters %s", err)
	}
}
