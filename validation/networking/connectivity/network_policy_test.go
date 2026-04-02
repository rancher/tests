//go:build (validation || infra.rke2k3s || cluster.any || sanity || pit.daily || pit.elemental.daily || pit.harvester.daily) && !stress && !extended

package connectivity

import (
	"os"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	client "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/networking"
	projectsapi "github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/workloads"
	"github.com/rancher/tests/actions/workloads/daemonset"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
)

type NetworkPolicyTestSuite struct {
	suite.Suite
	session          *session.Session
	client           *rancher.Client
	cluster          *client.Cluster
	cattleConfig     map[string]any
	downstreamClient *v1.Client
	namespace        *corev1.Namespace
}

func (n *NetworkPolicyTestSuite) TearDownSuite() {
	n.session.Cleanup()
}

func (n *NetworkPolicyTestSuite) SetupSuite() {
	testSession := session.NewSession()
	n.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(n.T(), err)

	n.client = client

	n.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	n.cattleConfig, err = defaults.LoadPackageDefaults(n.cattleConfig, "")
	require.NoError(n.T(), err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, n.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(n.T(), err)

	clusterID, err := clusters.GetClusterIDByName(n.client, n.client.RancherConfig.ClusterName)
	require.NoError(n.T(), err, "Error getting cluster ID")

	n.cluster, err = n.client.Management.Cluster.ByID(clusterID)
	require.NoError(n.T(), err)

	n.downstreamClient, err = n.client.Steve.ProxyDownstream(n.cluster.ID)
	require.NoError(n.T(), err)

	_, n.namespace, err = projectsapi.CreateProjectAndNamespace(n.client, n.cluster.ID)
	require.NoError(n.T(), err)
}

func (n *NetworkPolicyTestSuite) TestPingPodsFromCPNode() {
	networkPolicyTests := []struct {
		name string
	}{
		{"Network_Policy_Connectivity"},
	}

	for _, networkPolicyTest := range networkPolicyTests {
		n.Suite.Run(networkPolicyTest.name, func() {
			workloadConfigs := new(workloads.Workloads)
			operations.LoadObjectFromMap(workloads.WorkloadsConfigurationFileKey, n.cattleConfig, workloadConfigs)

			workloadConfigs.DaemonSet.ObjectMeta.Namespace = n.namespace.Name
			workloadConfigs.DaemonSet.ObjectMeta.GenerateName = strings.ToLower(networkPolicyTest.name) + "-"

			logrus.Infof("Creating daemonset with name prefix: %s", workloadConfigs.DaemonSet.ObjectMeta.GenerateName)
			testDaemonset, err := daemonset.CreateDaemonSetFromConfig(n.downstreamClient, n.cluster.ID, workloadConfigs.DaemonSet)
			require.NoError(n.T(), err)

			logrus.Infof("Verifying daemonset %s is running", testDaemonset.Name)
			err = daemonset.VerifyDaemonset(n.client, n.cluster.ID, n.namespace.Name, testDaemonset.Name)
			require.NoError(n.T(), err)

			logrus.Infof("Verifying network policy by pinging pods from control plane node")
			err = networking.VerifyNetworkPolicy(n.client, n.cluster.ID, n.namespace.Name)
			require.NoError(n.T(), err)
		})
	}
}

func TestNetworkPolicyTestSuite(t *testing.T) {
	suite.Run(t, new(NetworkPolicyTestSuite))
}
