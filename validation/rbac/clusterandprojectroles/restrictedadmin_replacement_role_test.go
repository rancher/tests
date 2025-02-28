package clusterandprojectroles

import (
	"fmt"
	"testing"

	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RestrictedAdminReplacementTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (ra *RestrictedAdminReplacementTestSuite) TearDownSuite() {
	ra.session.Cleanup()
}

func (ra *RestrictedAdminReplacementTestSuite) SetupSuite() {
	ra.session = session.NewSession()

	client, err := rancher.NewClient("", ra.session)
	require.NoError(ra.T(), err)
	ra.client = client

	log.Info("Getting cluster name from the config file and append cluster details in the struct.")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(ra.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := extensionscluster.GetClusterIDByName(ra.client, clusterName)
	require.NoError(ra.T(), err, "Error getting cluster ID")
	ra.cluster, err = ra.client.Management.Cluster.ByID(clusterID)
	require.NoError(ra.T(), err)
}

func (ra *RestrictedAdminReplacementTestSuite) updateGlobalSetting(client *rancher.Client, settingName string, settingValue string) error {
	setting, err := ra.client.WranglerContext.Mgmt.Setting().Get(settingName, v1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get setting %s: %w", settingName, err)
	}

	setting.Value = settingValue
	updatedSetting, err := client.WranglerContext.Mgmt.Setting().Update(setting)
	if err != nil {
		return fmt.Errorf("failed to update setting %s: %w", updatedSetting.Name, err)
	}
	return nil
}

func (ra *RestrictedAdminReplacementTestSuite) TestRestrictedAdminReplacementCreateCluster() {
	subSession := ra.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating the replacement restricted admin global role")
	createdRAReplacementRole, createdRaReplacementUser, err := createRestrictedAdminReplacementGlobalRoleAndUser(ra.client)
	require.NoError(ra.T(), err, "failed to create global role and user")

	createdRAReplacementUserClient, err := ra.client.AsUser(createdRaReplacementUser)
	require.NoError(ra.T(), err)

	ra.T().Logf("Verifying user %s with role %s can create a downstream cluster", createdRaReplacementUser.Name, createdRAReplacementRole.Name)
	userConfig := new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, userConfig)
	nodeProviders := userConfig.NodeProviders[0]
	nodeAndRoles := []provisioninginput.NodePools{
		provisioninginput.AllRolesNodePool,
	}
	externalNodeProvider := provisioning.ExternalNodeProviderSetup(nodeProviders)
	clusterConfig := clusters.ConvertConfigToClusterConfig(userConfig)
	clusterConfig.NodePools = nodeAndRoles
	kubernetesVersion, err := kubernetesversions.Default(createdRAReplacementUserClient, extensionscluster.RKE1ClusterType.String(), []string{})
	require.NoError(ra.T(), err)

	clusterConfig.KubernetesVersion = kubernetesVersion[0]
	clusterConfig.CNI = userConfig.CNIs[0]
	clusterObject, _, err := provisioning.CreateProvisioningRKE1CustomCluster(createdRAReplacementUserClient, &externalNodeProvider, clusterConfig)
	require.NoError(ra.T(), err)
	provisioning.VerifyRKE1Cluster(ra.T(), createdRAReplacementUserClient, clusterConfig, clusterObject)
}

func (ra *RestrictedAdminReplacementTestSuite) TestRestrictedAdminReplacementGlobalSettings() {
	subSession := ra.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating the replacement restricted admin global role")
	createdRaReplacementRole, createdRaReplacementUser, err := createRestrictedAdminReplacementGlobalRoleAndUser(ra.client)
	require.NoError(ra.T(), err, "failed to create global role and user")

	createdRAReplacementUserClient, err := ra.client.AsUser(createdRaReplacementUser)
	require.NoError(ra.T(), err)

	log.Infof("Verifying user %s  with role %s can list global settings", createdRaReplacementUser.Name, createdRaReplacementRole.Name)
	raReplacementUserSettingsList, err := getGlobalSettings(createdRAReplacementUserClient, ra.cluster.ID)
	require.NoError(ra.T(), err)
	adminGlobalSettingsList, err := getGlobalSettings(ra.client, ra.cluster.ID)
	require.NoError(ra.T(), err)

	require.Equal(ra.T(), adminGlobalSettingsList, raReplacementUserSettingsList)
	require.Equal(ra.T(), len(adminGlobalSettingsList), len(raReplacementUserSettingsList))
}

func (ra *RestrictedAdminReplacementTestSuite) TestRestrictedAdminReplacementCantUpdateGlobalSettings() {
	subSession := ra.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating the replacement restricted admin global role")
	createdRAReplacementRole, createdRaReplacementUser, err := createRestrictedAdminReplacementGlobalRoleAndUser(ra.client)
	require.NoError(ra.T(), err, "failed to create global role and user")

	createdRAReplacementUserClient, err := ra.client.AsUser(createdRaReplacementUser)
	require.NoError(ra.T(), err)

	kubeConfigTokenSetting, err := ra.client.WranglerContext.Mgmt.Setting().Get(kubeconfigSetting, v1.GetOptions{})
	require.NoError(ra.T(), err)

	ra.T().Logf("Verifying user %s with role %s cannot update global settings", createdRaReplacementUser.Name, createdRAReplacementRole.Name)
	err = ra.updateGlobalSetting(createdRAReplacementUserClient, kubeConfigTokenSetting.Name, updateValue)
	require.Error(ra.T(), err)
	require.Contains(ra.T(), err.Error(), "failed to update setting :  (put settings.meta.k8s.io kubeconfig-default-token-ttl-minutes)")
}

func TestRestrictedAdminReplacementTestSuite(t *testing.T) {
	suite.Run(t, new(RestrictedAdminReplacementTestSuite))
}
