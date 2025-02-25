package globalroles

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	extensionsettings "github.com/rancher/shepherd/extensions/settings"
	"github.com/rancher/shepherd/pkg/config"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioning/permutations"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/settings"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RestrictedAdminReplacementTestSuite struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	cluster            *management.Cluster
	provisioningConfig *provisioninginput.Config
}

func (ra *RestrictedAdminReplacementTestSuite) TearDownSuite() {
	ra.session.Cleanup()
}

func (ra *RestrictedAdminReplacementTestSuite) SetupSuite() {
	ra.session = session.NewSession()

	ra.provisioningConfig = new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, ra.provisioningConfig)

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

func (ra *RestrictedAdminReplacementTestSuite) TestRestrictedAdminReplacementCreateCluster() {
	subSession := ra.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating the replacement restricted admin global role")
	restrictedAdminReplacementRoleName := namegen.AppendRandomString("restricted-admin-replacement")
	restrictedAdminReplacementRole := newRestrictedAdminReplacementTemplate(restrictedAdminReplacementRoleName)
	createdRaReplacementRole, createdRaReplacementUser, err := createCustomGlobalRoleAndUser(ra.client, &restrictedAdminReplacementRole)
	require.NoError(ra.T(), err, "failed to create global role and user")

	createdRAReplacementUserClient, err := ra.client.AsUser(createdRaReplacementUser)
	require.NoError(ra.T(), err)

	ra.T().Logf("Verifying user %s with role %s can create a downstream cluster", createdRaReplacementUser.Name, createdRaReplacementRole.Name)
	nodeRolesAll := []provisioninginput.MachinePools{provisioninginput.AllRolesMachinePool}
	provisioningConfig := *ra.provisioningConfig
	provisioningConfig.MachinePools = nodeRolesAll

	log.Info("Setting up cluster config and provider for downstream k3s cluster")
	clusterConfig := clusters.ConvertConfigToClusterConfig(&provisioningConfig)
	clusterConfig.KubernetesVersion = ra.provisioningConfig.K3SKubernetesVersions[0]
	k3sprovider, _, _, _ := permutations.GetClusterProvider(permutations.K3SProvisionCluster, (*clusterConfig.Providers)[0], &provisioningConfig)
	clusterObject, err := provisioning.CreateProvisioningCluster(createdRAReplacementUserClient, *k3sprovider, clusterConfig, nil)
	require.NoError(ra.T(), err)

	provisioning.VerifyCluster(ra.T(), ra.client, clusterConfig, clusterObject)
}

func (ra *RestrictedAdminReplacementTestSuite) TestRestrictedAdminReplacementListGlobalSettings() {
	subSession := ra.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating the replacement restricted admin global role")
	restrictedAdminReplacementRoleName := namegen.AppendRandomString("restricted-admin-replacement")
	restrictedAdminReplacementRole := newRestrictedAdminReplacementTemplate(restrictedAdminReplacementRoleName)
	createdRaReplacementRole, createdRaReplacementUser, err := createCustomGlobalRoleAndUser(ra.client, &restrictedAdminReplacementRole)
	require.NoError(ra.T(), err, "failed to create global role and user")

	createdRAReplacementUserClient, err := ra.client.AsUser(createdRaReplacementUser)
	require.NoError(ra.T(), err)

	log.Infof("Verifying user %s with role %s can list global settings", createdRaReplacementUser.Name, createdRaReplacementRole.Name)
	raReplacementUserSettingsList, err := settings.GetGlobalSettingNames(createdRAReplacementUserClient, ra.cluster.ID)
	require.NoError(ra.T(), err)
	adminGlobalSettingsList, err := settings.GetGlobalSettingNames(ra.client, ra.cluster.ID)
	require.NoError(ra.T(), err)

	require.Equal(ra.T(), adminGlobalSettingsList, raReplacementUserSettingsList)
	require.Equal(ra.T(), len(adminGlobalSettingsList), len(raReplacementUserSettingsList))
}

func (ra *RestrictedAdminReplacementTestSuite) TestRestrictedAdminReplacementCantUpdateGlobalSettings() {
	subSession := ra.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating the replacement restricted admin global role")
	restrictedAdminReplacementRoleName := namegen.AppendRandomString("restricted-admin-replacement")
	restrictedAdminReplacementRole := newRestrictedAdminReplacementTemplate(restrictedAdminReplacementRoleName)
	_, createdRaReplacementUser, err := createCustomGlobalRoleAndUser(ra.client, &restrictedAdminReplacementRole)
	require.NoError(ra.T(), err, "failed to create global role and user")

	createdRAReplacementUserClient, err := ra.client.AsUser(createdRaReplacementUser)
	require.NoError(ra.T(), err)

	steveRAReplacementClient := createdRAReplacementUserClient.Steve
	steveAdminClient := ra.client.Steve

	log.Info("Getting kubeconfig-default-token-ttl-minutes setting")
	kubeConfigTokenSetting, err := steveAdminClient.SteveType(extensionsettings.ManagementSetting).ByID(extensionsettings.KubeConfigToken)
	require.NoError(ra.T(), err)

	log.Infof("Verifying user %s with the replacement restricted admin global role cannot update setting", createdRaReplacementUser.Name)
	_, err = extensionsettings.UpdateGlobalSettings(steveRAReplacementClient, kubeConfigTokenSetting, "3")
	require.Error(ra.T(), err)
	require.Contains(ra.T(), err.Error(), "Resource type [management.cattle.io.setting] is not updatable")
}

func TestRestrictedAdminReplacementTestSuite(t *testing.T) {
	suite.Run(t, new(RestrictedAdminReplacementTestSuite))
}
