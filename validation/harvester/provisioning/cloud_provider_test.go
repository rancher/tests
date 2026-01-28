//go:build validation || recurring || pit.harvester.daily

package harvester

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/defaults/providers"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type HarvesterProvisioningTestSuite struct {
	suite.Suite
	client       *rancher.Client
	session      *session.Session
	cattleConfig map[string]any
}

func (p *HarvesterProvisioningTestSuite) TearDownSuite() {
	p.session.Cleanup()
}

func (p *HarvesterProvisioningTestSuite) SetupSuite() {
	testSession := session.NewSession()
	p.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(p.T(), err)

	p.client = client

	p.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	p.cattleConfig, err = defaults.SetK8sDefault(client, defaults.RKE2, p.cattleConfig)
	require.NoError(p.T(), err)
}

func (p *HarvesterProvisioningTestSuite) TestCloudProvider() {

	nodeRolesDedicated := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesDedicated[0].MachinePoolConfig.Quantity = 1
	nodeRolesDedicated[1].MachinePoolConfig.Quantity = 2
	nodeRolesDedicated[2].MachinePoolConfig.Quantity = 2
	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, p.cattleConfig, clusterConfig)
	var err error

	clusterConfig.Provider = providers.Harvester
	clusterConfig.MachinePools = nodeRolesDedicated

	provider := provisioning.CreateProvider(clusterConfig.Provider)
	credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
	machineConfigSpec := provider.LoadMachineConfigFunc(p.cattleConfig)

	logrus.Infof("Provisioning cluster")
	cluster, err := provisioning.CreateProvisioningCluster(p.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
	require.NoError(p.T(), err)

	logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
	err = provisioning.VerifyClusterReady(p.client, cluster)
	require.NoError(p.T(), err)

	logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
	err = deployment.VerifyClusterDeployments(p.client, cluster)
	require.NoError(p.T(), err)

	logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
	err = pods.VerifyClusterPods(p.client, cluster)
	require.NoError(p.T(), err)

	logrus.Infof("Verifying cloud provider (%s)", cluster.Name)
	provider.VerifyCloudProviderFunc(p.T(), p.client, cluster)

	params := provisioning.GetProvisioningSchemaParams(p.client, p.cattleConfig)
	err = qase.UpdateSchemaParameters("Harvester_oot", params)
	if err != nil {
		logrus.Warningf("Failed to upload schema parameters %s", err)
	}

}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestHarvesterProvisioningTestSuite(t *testing.T) {
	suite.Run(t, new(HarvesterProvisioningTestSuite))
}
