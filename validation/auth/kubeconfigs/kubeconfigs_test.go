//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !(2.8 || 2.9 || 2.10 || 2.11)

package kubeconfigs

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	kubeconfigapi "github.com/rancher/tests/actions/kubeconfigs"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type KubeconfigTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (kc *KubeconfigTestSuite) SetupSuite() {
	err := os.Setenv("DISABLE_PROTOBUF", "true")
	require.NoError(kc.T(), err)

	kc.session = session.NewSession()

	client, err := rancher.NewClient("", kc.session)
	require.NoError(kc.T(), err)
	kc.client = client

	log.Info("Getting cluster name from the config file and append cluster details in rbos")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(kc.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(kc.client, clusterName)
	require.NoError(kc.T(), err, "Error getting cluster ID")
	kc.cluster, err = kc.client.Management.Cluster.ByID(clusterID)
	require.NoError(kc.T(), err)
}

func (kc *KubeconfigTestSuite) TearDownSuite() {
	kc.session.Cleanup()
}

func (kc *KubeconfigTestSuite) TestCreateKubeconfig() {
	log.Infof("Creating a kubeconfig for cluster: %s", kc.cluster.ID)
	createdKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotNil(kc.T(), createdKubeconfig)
	require.NotEmpty(kc.T(), createdKubeconfig.Status.Value)

	log.Infof("GET the kubeconfig and validate the fields")
	kubeconfigObj, err := kc.client.WranglerContext.Ext.Kubeconfig().Get(createdKubeconfig.Name, metav1.GetOptions{})
	require.NoError(kc.T(), err)
	require.NotNil(kc.T(), kubeconfigObj)
	require.Equal(kc.T(), "Complete", kubeconfigObj.Status.Summary, "Kubeconfig status summary should be Complete")
	require.GreaterOrEqual(kc.T(), len(kubeconfigObj.Status.Tokens), 1, "Expected one token in status.tokens")
}

func TestKubeconfigTestSuite(t *testing.T) {
	suite.Run(t, new(KubeconfigTestSuite))
}
