//go:build (validation || infra.any || cluster.any || extended) && !stress && !sanity && (2.8 || 2.9 || 2.10)

package globalroles

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/settings"
	password "github.com/rancher/shepherd/extensions/users/passwordgenerator"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/machinepools"
	rbac "github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/users"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RestrictedAdminTestSuite struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	cluster            *management.Cluster
	clusterConfig      *clusters.ClusterConfig
}

const (
	restrictedAdmin rbac.Role = "restricted-admin"
	manageUsers     rbac.Role = "users-manage"
)

func (ra *RestrictedAdminTestSuite) TearDownSuite() {
	ra.session.Cleanup()
}

func (ra *RestrictedAdminTestSuite) SetupSuite() {
	ra.session = session.NewSession()

	client, err := rancher.NewClient("", ra.session)
	require.NoError(ra.T(), err)

	ra.client = client

	ra.clusterConfig = new(clusters.ClusterConfig)
	config.LoadConfig(defaults.ClusterConfigKey, ra.clusterConfig)

	log.Info("Getting cluster name from the config file and append cluster details in the struct.")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(ra.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := extensionscluster.GetClusterIDByName(ra.client, clusterName)
	require.NoError(ra.T(), err, "Error getting cluster ID")
	ra.cluster, err = ra.client.Management.Cluster.ByID(clusterID)
	require.NoError(ra.T(), err)
}

func (ra *RestrictedAdminTestSuite) createRestrictedAdminAndAdminUser(addManageUsersRole bool) (*management.User, *management.User, *rancher.Client) {
	var restrictedAdminUser *management.User
	var restrictedAdminClient *rancher.Client
	var err error

	if addManageUsersRole {
		log.Info("Creating the restricted admin user with manage users")
		restrictedAdminUser, restrictedAdminClient, err = rbac.SetupUser(ra.client, restrictedAdmin.String(), manageUsers.String())
	} else {
		log.Info("Creating the restricted admin user")
		restrictedAdminUser, restrictedAdminClient, err = rbac.SetupUser(ra.client, restrictedAdmin.String())
	}
	require.NoError(ra.T(), err)

	log.Info("Creating the admin user")
	adminUser, err := createUserWithBuiltinRole(ra.client, rbac.Admin)
	require.NoError(ra.T(), err)

	return restrictedAdminUser, adminUser, restrictedAdminClient
}

func (ra *RestrictedAdminTestSuite) TestRestrictedAdminCreateK3sCluster() {
	subSession := ra.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating the restricted admin global role")
	_, restrictedAdminClient, err := rbac.SetupUser(ra.client, restrictedAdmin.String())
	require.NoError(ra.T(), err)

	ra.T().Logf("Verifying restricted admin can create a downstream cluster")
	nodeRolesAll := []provisioninginput.MachinePools{provisioninginput.AllRolesMachinePool}
	ra.clusterConfig.MachinePools = nodeRolesAll

	log.Info("Setting up cluster config and provider for downstream k3s cluster")
	provider := provisioning.CreateProvider(ra.clusterConfig.Provider)
	credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
	machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))
	clusterObject, err := provisioning.CreateProvisioningCluster(restrictedAdminClient, provider, credentialSpec, ra.clusterConfig, machineConfigSpec, nil)
	require.NoError(ra.T(), err)

	provisioning.VerifyCluster(ra.T(), ra.client, ra.clusterConfig, clusterObject)
}

func (ra *RestrictedAdminTestSuite) TestRestrictedAdminGlobalSettings() {
	subSession := ra.session.NewSession()
	defer subSession.Cleanup()

	_, restrictedAdminClient, err := rbac.SetupUser(ra.client, restrictedAdmin.String())
	require.NoError(ra.T(), err)
	ra.T().Log("Validating restricted Admin can list global settings")
	steveRestrictedAdminclient := restrictedAdminClient.Steve
	steveAdminClient := ra.client.Steve

	adminListSettings, err := steveAdminClient.SteveType(settings.ManagementSetting).List(nil)
	require.NoError(ra.T(), err)
	adminSettings := adminListSettings.Names()

	resAdminListSettings, err := steveRestrictedAdminclient.SteveType(settings.ManagementSetting).List(nil)
	require.NoError(ra.T(), err)
	resAdminSettings := resAdminListSettings.Names()

	assert.Equal(ra.T(), len(adminSettings), len(resAdminSettings))
	assert.Equal(ra.T(), adminSettings, resAdminSettings)
}

func (ra *RestrictedAdminTestSuite) TestRestrictedAdminCantUpdateGlobalSettings() {
	subSession := ra.session.NewSession()
	defer subSession.Cleanup()

	ra.T().Logf("Validating restrictedAdmin cannot edit global settings")

	_, restrictedAdminClient, err := rbac.SetupUser(ra.client, restrictedAdmin.String())
	require.NoError(ra.T(), err)

	steveRestrictedAdminclient := restrictedAdminClient.Steve
	steveAdminClient := ra.client.Steve

	kubeConfigTokenSetting, err := steveAdminClient.SteveType(settings.ManagementSetting).ByID(settings.KubeConfigToken)
	require.NoError(ra.T(), err)

	_, err = settings.UpdateGlobalSettings(steveRestrictedAdminclient, kubeConfigTokenSetting, "3")
	require.Error(ra.T(), err)
	assert.Contains(ra.T(), err.Error(), "Resource type [management.cattle.io.setting] is not updatable")
}

func (ra *RestrictedAdminTestSuite) TestRestrictedAdminCantUpdateAdminPassword() {
	subSession := ra.session.NewSession()
	defer subSession.Cleanup()

	_, adminUser, restrictedAdminClient := ra.createRestrictedAdminAndAdminUser(false)

	log.Info("Verifying user with restricted admin replacement role cannot update admin user password")
	testPass := password.GenerateUserPassword("testpass-")
	err := users.UpdateUserPassword(restrictedAdminClient, adminUser, testPass)
	require.Error(ra.T(), err)
	require.Contains(ra.T(), err.Error(), "denied the request: request is attempting to modify user with more permissions than requester user")
}

func (ra *RestrictedAdminTestSuite) TestRestrictedAdminCantDeleteAdmin() {
	subSession := ra.session.NewSession()
	defer subSession.Cleanup()

	_, adminUser, restrictedAdminClient := ra.createRestrictedAdminAndAdminUser(false)

	log.Info("Verifying restricted admin cannot delete admin user")
	err := restrictedAdminClient.Management.User.Delete(adminUser)
	require.Error(ra.T(), err)
	require.Contains(ra.T(), err.Error(), "denied the request: request is attempting to modify user with more permissions than requester user")
}

func (ra *RestrictedAdminTestSuite) TestRestrictedAdminWithManageUsersCanUpdateAdminPassword() {
	subSession := ra.session.NewSession()
	defer subSession.Cleanup()

	_, adminUser, restrictedAdminClient := ra.createRestrictedAdminAndAdminUser(true)

	log.Info("Verifying restricted admin with manage users can update admin user password")
	testPass := password.GenerateUserPassword("testpass-")
	err := users.UpdateUserPassword(restrictedAdminClient, adminUser, testPass)
	require.NoError(ra.T(), err)
}

func (ra *RestrictedAdminTestSuite) TestRestrictedAdminWithManageUsersCanDeleteAdmin() {
	subSession := ra.session.NewSession()
	defer subSession.Cleanup()

	_, adminUser, restrictedAdminClient := ra.createRestrictedAdminAndAdminUser(true)
	log.Info("Verifying restricted admin with manage users can delete admin user")
	err := restrictedAdminClient.Management.User.Delete(adminUser)
	require.NoError(ra.T(), err)
}

func TestRestrictedAdminTestSuite(t *testing.T) {
	suite.Run(t, new(RestrictedAdminTestSuite))
}
