//go:build validation

package remotedialerproxy

import (
	"os"
	"testing"

	v1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
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

type rdpTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func rdpSetup(t *testing.T) rdpTest {
	var r rdpTest

	s := session.NewSession()
	r.session = s

	client, err := rancher.NewClient("", s)
	require.NoError(t, err)
	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))
	r.cattleConfig, err = defaults.LoadPackageDefaults(r.cattleConfig, "")
	require.NoError(t, err)

	logCfg := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, r.cattleConfig, logCfg)
	require.NoError(t, logging.SetLogger(logCfg))

	r.cattleConfig, err = defaults.SetK8sDefault(client, defaults.RKE2, r.cattleConfig)
	require.NoError(t, err)

	r.standardUserClient, _, _, err = standard.CreateStandardUser(r.client)
	require.NoError(t, err)

	return r
}

func TestRemotedialerProxy(t *testing.T) {
	r := rdpSetup(t)

	nodeRoles := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	nodeRoles[0].MachinePoolConfig.Quantity = 3
	nodeRoles[1].MachinePoolConfig.Quantity = 2
	nodeRoles[2].MachinePoolConfig.Quantity = 3

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)

	clusterConfig.Networking = &provisioninginput.Networking{
		LocalClusterAuthEndpoint: &v1.LocalClusterAuthEndpoint{
			Enabled: true,
		},
	}

	clusterConfig.MachinePools = nodeRoles

	provider := provisioning.CreateProvider(clusterConfig.Provider)
	cred := cloudcredentials.LoadCloudCredential(string(provider.Name))
	machineCfg := provider.LoadMachineConfigFunc(r.cattleConfig)

	rdpVersionSetting, err := r.client.Management.Setting.ByID("remotedialer-proxy-version")
	require.NoError(t, err)
	logrus.Infof("Remotedialer Proxy Version: %s", rdpVersionSetting.Value)

	logrus.Info("Provisioning downstream cluster...")
	cluster, err := provisioning.CreateProvisioningCluster(
		r.standardUserClient,
		provider,
		cred,
		clusterConfig,
		machineCfg,
		nil,
	)
	require.NoError(t, err)

	require.NoError(t, provisioning.VerifyClusterReady(r.client, cluster))
	require.NoError(t, deployment.VerifyClusterDeployments(r.standardUserClient, cluster))
	require.NoError(t, pods.VerifyClusterPods(r.client, cluster))
	require.NoError(t, clusters.VerifyServiceAccountTokenSecret(r.client, cluster.Name))

	t.Cleanup(func() {
		if cluster != nil {
			extClusters.DeleteK3SRKE2Cluster(r.client, cluster.ID)
		}
		r.session.Cleanup()
	})

	t.Run("RemotedialerProxy_Validations", func(t *testing.T) {
		remotedialerProxyValidations(t, r.client, cluster)
	})

	params := provisioning.GetProvisioningSchemaParams(r.standardUserClient, r.cattleConfig)
	_ = qase.UpdateSchemaParameters("RemotedialerProxy_Validations", params)
}
