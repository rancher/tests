//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10

package globalroles

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	password "github.com/rancher/shepherd/extensions/users/passwordgenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/users"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type StandardUserManageUsersTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (su *StandardUserManageUsersTestSuite) TearDownSuite() {
	su.session.Cleanup()
}

func (su *StandardUserManageUsersTestSuite) SetupSuite() {
	su.session = session.NewSession()

	client, err := rancher.NewClient("", su.session)
	require.NoError(su.T(), err)
	su.client = client

	log.Info("Getting cluster name from the config file and append cluster details in the struct.")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(su.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := extensionscluster.GetClusterIDByName(su.client, clusterName)
	require.NoError(su.T(), err, "Error getting cluster ID")
	su.cluster, err = su.client.Management.Cluster.ByID(clusterID)
	require.NoError(su.T(), err)
}

func (su *StandardUserManageUsersTestSuite) TestStandardUserWithoutManageUsersDelete() {
	subSession := su.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a standard user with delete custom global role")
	_, standardUser, err := createCustomGlobalRoleAndUser(su.client, &customGlobalRoleDelete)
	require.NoError(su.T(), err, "failed to create global role and user")

	standardUserClient, err := su.client.AsUser(standardUser)
	require.NoError(su.T(), err)

	adminUser, err := createUserWithBuiltinRole(su.client, rbac.Admin)
	require.NoError(su.T(), err)

	baseUser, err := createUserWithBuiltinRole(su.client, rbac.BaseUser)
	require.NoError(su.T(), err)

	log.Info("Verifying standard user with delete custom global role can delete a user with lower permissions")
	err = standardUserClient.Management.User.Delete(baseUser)
	require.NoError(su.T(), err)

	log.Info("Verifying standard user with delete custom global role cannot delete a user with higher permissions")
	err = standardUserClient.Management.User.Delete(adminUser)
	require.Error(su.T(), err)
	require.Contains(su.T(), err.Error(), "denied the request: request is attempting to modify user with more permissions than requester user")
}

func (su *StandardUserManageUsersTestSuite) TestStandardUserWithoutManageUsersEdit() {
	subSession := su.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a standard user with edit custom global role")
	_, standardUser, err := createCustomGlobalRoleAndUser(su.client, &customGlobalRoleEdit)
	require.NoError(su.T(), err, "failed to create global role and user")

	standardUserClient, err := su.client.AsUser(standardUser)
	require.NoError(su.T(), err)

	adminUser, err := createUserWithBuiltinRole(su.client, rbac.Admin)
	require.NoError(su.T(), err)

	baseUser, err := createUserWithBuiltinRole(su.client, rbac.BaseUser)
	require.NoError(su.T(), err)

	log.Info("Verifying standard user with edit custom global role can update password of a user with lower permissions")
	var testPass = password.GenerateUserPassword("testpass-")
	err = users.UpdateUserPassword(standardUserClient, baseUser, testPass)
	require.NoError(su.T(), err)

	log.Info("Verifying standard user with edit custom global role cannot update password of a user with higher permissions")
	err = users.UpdateUserPassword(standardUserClient, adminUser, testPass)
	require.Error(su.T(), err)
	require.Contains(su.T(), err.Error(), "denied the request: request is attempting to modify user with more permissions than requester user")
}

func (su *StandardUserManageUsersTestSuite) TestStandardUserWithManageUsersDelete() {
	subSession := su.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a standard user with manage-users verb on users resource")
	_, standardUser, err := createCustomGlobalRoleAndUser(su.client, &customGlobalRoleManageUsers)
	require.NoError(su.T(), err, "failed to create global role and user")

	standardUserClient, err := su.client.AsUser(standardUser)
	require.NoError(su.T(), err)

	adminUser, err := createUserWithBuiltinRole(su.client, rbac.Admin)
	require.NoError(su.T(), err)

	baseUser, err := createUserWithBuiltinRole(su.client, rbac.BaseUser)
	require.NoError(su.T(), err)

	log.Info("Verifying standard user with custom global role including manage-users verb can delete a user with lower permissions")
	err = standardUserClient.Management.User.Delete(baseUser)

	require.NoError(su.T(), err)

	log.Info("Verifying standard user with custom global role including manage-users verb can delete a user with higher permissions")
	err = standardUserClient.Management.User.Delete(adminUser)
	require.NoError(su.T(), err)
}

func (su *StandardUserManageUsersTestSuite) TestStandardUserWithManageUsersEdit() {
	subSession := su.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a standard user with manage-users verb on users resource")
	_, standardUser, err := createCustomGlobalRoleAndUser(su.client, &customGlobalRoleManageUsers)
	require.NoError(su.T(), err, "failed to create global role and user")

	standardUserClient, err := su.client.AsUser(standardUser)
	require.NoError(su.T(), err)

	adminUser, err := createUserWithBuiltinRole(su.client, rbac.Admin)
	require.NoError(su.T(), err)

	baseUser, err := createUserWithBuiltinRole(su.client, rbac.BaseUser)
	require.NoError(su.T(), err)

	log.Info("Verifying standard user with custom global role including manage-users verb can update password of a user with lower permissions")
	var testPass = password.GenerateUserPassword("testpass-")
	err = users.UpdateUserPassword(standardUserClient, baseUser, testPass)
	require.NoError(su.T(), err)

	log.Info("Verifying standard user with custom global role including manage-users verb can update password of a user with higher permissions")
	err = users.UpdateUserPassword(standardUserClient, adminUser, testPass)
	require.NoError(su.T(), err)
}

func TestStandardUserManageUsersTestSuite(t *testing.T) {
	suite.Run(t, new(StandardUserManageUsersTestSuite))
}
