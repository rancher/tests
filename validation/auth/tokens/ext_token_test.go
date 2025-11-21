//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11

package tokens

import (
	"strconv"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/settings"
	exttokenapi "github.com/rancher/tests/actions/tokens/exttokens"
	user "github.com/rancher/tests/actions/users"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ExtTokenTestSuite struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	cluster            *management.Cluster
	defaultExtTokenTTL int64
}

func (ext *ExtTokenTestSuite) TearDownSuite() {
	ext.session.Cleanup()
}

func (ext *ExtTokenTestSuite) SetupSuite() {
	ext.session = session.NewSession()

	client, err := rancher.NewClient("", ext.session)
	require.NoError(ext.T(), err)
	ext.client = client

	log.Info("Getting cluster name from the config file and append cluster details in the struct.")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(ext.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := extensionscluster.GetClusterIDByName(ext.client, clusterName)
	require.NoError(ext.T(), err, "Error getting cluster ID")
	ext.cluster, err = ext.client.Management.Cluster.ByID(clusterID)
	require.NoError(ext.T(), err)

	log.Info("Getting default TTL value to be used in tests")
	defaultTTLString, err := settings.GetGlobalSettingDefaultValue(ext.client, settings.AuthTokenMaxTTLMinutes)
	require.NoError(ext.T(), err)
	defaultTTLInt, err := strconv.Atoi(defaultTTLString)
	require.NoError(ext.T(), err)
	defaultTTL := int64(defaultTTLInt * 60000)
	ext.defaultExtTokenTTL = defaultTTL
}

func (ext *ExtTokenTestSuite) TestCreateExtTokenAsAdminUser() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext token as admin user")
	adminExtToken, err := exttokenapi.CreateExtToken(ext.client, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)
	adminUserID := ext.client.UserID

	log.Info("Verify ext token data")
	err = exttokenapi.VerifyExtTokenData(ext.client, adminExtToken, adminUserID, ext.defaultExtTokenTTL, true)
	require.NoError(ext.T(), err)
}

func (ext *ExtTokenTestSuite) TestCreateExtTokenAsStandardUser() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext token as standard user")
	standardUser, standardUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)
	standardUserExtToken, err := exttokenapi.CreateExtToken(standardUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("Verify ext token data")
	err = exttokenapi.VerifyExtTokenData(ext.client, standardUserExtToken, standardUser.ID, ext.defaultExtTokenTTL, true)
	require.NoError(ext.T(), err)
}

func (ext *ExtTokenTestSuite) TestCreateExtTokenAsBaseUser() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext token as base user")
	baseUser, baseUserClient, err := rbac.SetupUser(ext.client, rbac.BaseUser.String())
	require.NoError(ext.T(), err)
	baseUserExtToken, err := exttokenapi.CreateExtToken(baseUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("Verify ext token data")
	err = exttokenapi.VerifyExtTokenData(ext.client, baseUserExtToken, baseUser.ID, ext.defaultExtTokenTTL, true)
	require.NoError(ext.T(), err)
}

func (ext *ExtTokenTestSuite) TestCreateExtTokenWithTTLGreaterThanDefault() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	standardUser, standardUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)

	log.Info("Create ext token as standard user with TTL value > default TTL value")
	largerTTLValue := int64(9999999999)
	standardUserExtToken, err := exttokenapi.CreateExtToken(standardUserClient, largerTTLValue)
	require.NoError(ext.T(), err)

	log.Info("Verify ext token data has default TTL value instead of larger TTL value")
	err = exttokenapi.VerifyExtTokenData(ext.client, standardUserExtToken, standardUser.ID, ext.defaultExtTokenTTL, true)
	require.NoError(ext.T(), err)
}

func (ext *ExtTokenTestSuite) TestCreateExtTokenWithTTLSetToZeroUsesDefault() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	standardUser, standardUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)

	log.Info("Create ext token as standard user with TTL value set to 0")
	zeroTTLValue := int64(0)
	standardUserExtToken, err := exttokenapi.CreateExtToken(standardUserClient, zeroTTLValue)
	require.NoError(ext.T(), err)

	log.Info("Verify ext token data has default TTL value instead of zero")
	err = exttokenapi.VerifyExtTokenData(ext.client, standardUserExtToken, standardUser.ID, ext.defaultExtTokenTTL, true)
	require.NoError(ext.T(), err)
}

func (ext *ExtTokenTestSuite) TestUpdateExtTokenAsAdmin() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext token as admin user")
	adminExtToken, err := exttokenapi.CreateExtToken(ext.client, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("Update ext token as admin user")
	adminExtTokenToUpdate := adminExtToken.DeepCopy()
	adminExtTokenToUpdate.Labels["foo"] = "bar"
	_, err = exttokenapi.UpdateExtToken(ext.client, adminExtTokenToUpdate)
	require.NoError(ext.T(), err)

	log.Info("Verify admin ext token labels were updated")
	updatedAdminExtToken, err := exttokenapi.GetExtToken(ext.client, adminExtToken.Name)
	require.NoError(ext.T(), err)
	require.Equal(ext.T(), "bar", updatedAdminExtToken.Labels["foo"], "Expected admin ext token labels to be updated")
	require.Contains(ext.T(), "bar", updatedAdminExtToken.Labels["foo"], "Expected admin ext token labels to be updated")

	log.Info("Create ext token as standard user")
	_, standardUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)
	standardUserExtToken, err := exttokenapi.CreateExtToken(standardUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("Update standard user ext token as admin user")
	standardUserExtTokenToUpdate := standardUserExtToken.DeepCopy()
	standardUserExtTokenToUpdate.Labels["foo"] = "bar"
	_, err = exttokenapi.UpdateExtToken(ext.client, standardUserExtTokenToUpdate)
	require.NoError(ext.T(), err)

	log.Info("Verify standard user ext token labels were updated")
	updatedStandardUserExtToken, err := exttokenapi.GetExtToken(ext.client, adminExtToken.Name)
	require.NoError(ext.T(), err)
	require.Equal(ext.T(), "bar", updatedStandardUserExtToken.Labels["foo"], "Expected standard user ext token labels to be updated")
	require.Contains(ext.T(), "bar", updatedStandardUserExtToken.Labels["foo"], "Expected standard user ext token labels to be updated")

}

func (ext *ExtTokenTestSuite) TestUpdateExtTokenAsNonAdmin() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	firstUser, firstUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)
	log.Infof("Create ext token for the first standard user %s", firstUser.ID)
	firstUserExtToken, err := exttokenapi.CreateExtToken(firstUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	secondUser, secondUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)
	log.Infof("Create ext token for the second standard user %s", secondUser.ID)
	secondUserExtToken, err := exttokenapi.CreateExtToken(secondUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Infof("Update first standard user %s ext token as first standard user", firstUser.ID)
	firstUserExtTokenToUpdate := firstUserExtToken.DeepCopy()
	firstUserExtTokenToUpdate.Labels["foo"] = "bar"
	_, err = exttokenapi.UpdateExtToken(ext.client, firstUserExtTokenToUpdate)
	require.NoError(ext.T(), err)

	log.Infof("Verify first standard user %s ext token labels were updated", firstUser.ID)
	updatedStandardUserExtToken, err := exttokenapi.GetExtToken(ext.client, firstUserExtToken.Name)
	require.NoError(ext.T(), err)
	require.Equal(ext.T(), "bar", updatedStandardUserExtToken.Labels["foo"], "Expected standard user ext token labels to be updated")
	require.Contains(ext.T(), "bar", updatedStandardUserExtToken.Labels["foo"], "Expected standard user ext token labels to be updated")

	log.Infof("Attempt to update second standard user %s ext token as first standard user %s", secondUser.ID, firstUser.ID)
	secondUserExtTokenToUpdate := secondUserExtToken.DeepCopy()
	secondUserExtTokenToUpdate.Labels["foo"] = "bar"
	_, err = exttokenapi.UpdateExtToken(firstUserClient, secondUserExtTokenToUpdate)
	require.Error(ext.T(), err)
	require.True(ext.T(), k8serrors.IsNotFound(err))
}

func (ext *ExtTokenTestSuite) TestUpdateExtTokenTTL() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext token as admin user")
	adminExtToken, err := exttokenapi.CreateExtToken(ext.client, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("As admin user reduce the admin ext token TTL")
	adminExtTokenToUpdate := adminExtToken.DeepCopy()
	updatedTTL := int64(100000)
	adminExtTokenToUpdate.Spec.TTL = updatedTTL
	updatedAdminExtToken, err := exttokenapi.UpdateExtToken(ext.client, adminExtTokenToUpdate)
	require.NoError(ext.T(), err)
	require.Equal(ext.T(), updatedTTL, updatedAdminExtToken.Spec.TTL, "Expected TTL to equal updated value")
}

func (ext *ExtTokenTestSuite) TestListExtTokenAsAdmin() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext token as the admin user")
	adminUserExtToken, err := exttokenapi.CreateExtToken(ext.client, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("Create ext token for the first standard user")
	_, firstUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)
	firstUserExtToken, err := exttokenapi.CreateExtToken(firstUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("Create ext token for the second standard user")
	_, secondUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)
	secondUserExtToken, err := exttokenapi.CreateExtToken(secondUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("As the admin user list all the ext tokens")
	adminTokenList, err := exttokenapi.ListExtToken(ext.client)
	require.NoError(ext.T(), err)
	require.GreaterOrEqual(ext.T(), len(adminTokenList.Items), 3, "Expected admin list to contain atleast 3 ext tokens")
	require.True(ext.T(), exttokenapi.VerifyExtTokenExistsInList(adminTokenList.Items, adminUserExtToken.Name))
	require.True(ext.T(), exttokenapi.VerifyExtTokenExistsInList(adminTokenList.Items, firstUserExtToken.Name))
	require.True(ext.T(), exttokenapi.VerifyExtTokenExistsInList(adminTokenList.Items, secondUserExtToken.Name))
}

func (ext *ExtTokenTestSuite) TestListExtTokenAsNonAdmin() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext token for the first standard user")
	_, firstUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)
	firstUserExtToken, err := exttokenapi.CreateExtToken(firstUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("Create ext token for the second standard user")
	_, secondUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)
	secondUserExtToken, err := exttokenapi.CreateExtToken(secondUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("List ext tokens as first standard user")
	firstUserExtTokenList, err := exttokenapi.ListExtToken(firstUserClient)
	require.NoError(ext.T(), err)
	require.Equal(ext.T(), len(firstUserExtTokenList.Items), 1, "Standard user should only be able to list the token they own")
	require.True(ext.T(), exttokenapi.VerifyExtTokenExistsInList(firstUserExtTokenList.Items, firstUserExtToken.Name))
	require.False(ext.T(), exttokenapi.VerifyExtTokenExistsInList(firstUserExtTokenList.Items, secondUserExtToken.Name))
}

func (ext *ExtTokenTestSuite) TestDeleteExtTokenAsAdmin() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext token as the admin user")
	adminUserExtToken, err := exttokenapi.CreateExtToken(ext.client, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("Create ext token for the first standard user")
	_, firstUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)
	firstUserExtToken, err := exttokenapi.CreateExtToken(firstUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("Create ext token for the second standard user")
	_, secondUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)
	secondUserExtToken, err := exttokenapi.CreateExtToken(secondUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("As the admin user delete the admin user token")
	err = exttokenapi.DeleteExtToken(ext.client, adminUserExtToken.Name)
	require.NoError(ext.T(), err)
	log.Info("As the admin user delete the first users token")
	err = exttokenapi.DeleteExtToken(ext.client, firstUserExtToken.Name)
	require.NoError(ext.T(), err)
	log.Info("As the admin user delete the second users token")
	err = exttokenapi.DeleteExtToken(ext.client, secondUserExtToken.Name)
	require.NoError(ext.T(), err)
}

func (ext *ExtTokenTestSuite) TestDeleteExtTokenAsNonAdmin() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext token for the first standard user")
	_, firstUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)
	firstUserExtToken, err := exttokenapi.CreateExtToken(firstUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("Create ext token for the second standard user")
	_, secondUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)
	secondUserExtToken, err := exttokenapi.CreateExtToken(secondUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("As the first user delete the first users ext token")
	err = exttokenapi.DeleteExtToken(firstUserClient, firstUserExtToken.Name)
	require.NoError(ext.T(), err)

	log.Info("As the first user attempt to delete non-owned ext token")
	err = exttokenapi.DeleteExtToken(firstUserClient, secondUserExtToken.Name)
	require.Error(ext.T(), err)
	require.True(ext.T(), k8serrors.IsNotFound(err))
}

func (ext *ExtTokenTestSuite) TestExtTokenIsDeletedUponUserDeletion() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext token for the first standard user")
	firstUser, firstUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)
	firstUserExtToken, err := exttokenapi.CreateExtToken(firstUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("As the admin user delete standard user")
	err = ext.client.WranglerContext.Mgmt.User().Delete(firstUser.ID, &metav1.DeleteOptions{})
	require.NoError(ext.T(), err)
	err = user.WaitForUserDeletion(ext.client, firstUser.ID)
	require.NoError(ext.T(), err)

	log.Info("Verify the standard users ext token is deleted upon user deletion")
	adminTokenList, err := exttokenapi.ListExtToken(ext.client)
	require.NoError(ext.T(), err)
	require.False(ext.T(), exttokenapi.VerifyExtTokenExistsInList(adminTokenList.Items, firstUserExtToken.Name))
}

func (ext *ExtTokenTestSuite) TestExtTokenExpired() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext token as standard user with TTL value set to 10 seconds")
	_, standardUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)

	zeroTTLValue := int64(1000)
	standardUserExtToken, err := exttokenapi.CreateExtToken(standardUserClient, zeroTTLValue)
	require.NoError(ext.T(), err)

	log.Info("Wait for ext token TTL to expire and token.Status to reach expired")
	tokenWithExpiredTTL, err := exttokenapi.WaitForExtTokenStatusExpired(standardUserClient, standardUserExtToken.Name, exttokenapi.ExtTokenStatusExpiredValue)
	require.NoError(ext.T(), err)
	require.Equal(ext.T(), tokenWithExpiredTTL.Status.Expired, exttokenapi.ExtTokenStatusExpiredValue, "Expected token status to be expired")
}

func (ext *ExtTokenTestSuite) TestExtTokenExtendingTTLRejected() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext token as standard user")
	_, standardUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)
	standardUserExtToken, err := exttokenapi.CreateExtToken(standardUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("Attempt to extend the ext token TTL value")
	extendedTTLValue := int64(9999999999)
	standardUserExtToken.Spec.TTL = extendedTTLValue
	updatedStandardUserExtToken, err := exttokenapi.UpdateExtToken(standardUserClient, standardUserExtToken)
	require.NoError(ext.T(), err)
	require.Equal(ext.T(), ext.defaultExtTokenTTL, updatedStandardUserExtToken.Spec.TTL, "Expected ext token TTL to be default value")
}

func (ext *ExtTokenTestSuite) TestAuthenticateWithExtToken() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext token as standard user")
	_, standardUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)
	standardUserExtToken, err := exttokenapi.CreateExtToken(standardUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("Create an R_SESS cookie containing the ext token value and use it to authenticate a request")
	tokenValue := standardUserExtToken.Status.Value
	baseURL := "https://" + standardUserClient.RancherConfig.Host
	extTokenAPIPath := "/v1/ext.cattle.io.tokens"
	err = exttokenapi.AuthenticateWithExtToken(baseURL, standardUserExtToken.Name, tokenValue, extTokenAPIPath)
	require.NoError(ext.T(), err)
}

func (ext *ExtTokenTestSuite) TestAuthenticateWithDisabledExtToken() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext token as standard user")
	_, standardUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)
	standardUserExtToken, err := exttokenapi.CreateExtToken(standardUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("As the standard user disable the ext token")
	disabled := false
	standardUserExtToken.Spec.Enabled = &disabled
	disabledExtToken, err := exttokenapi.UpdateExtToken(standardUserClient, standardUserExtToken)
	require.NoError(ext.T(), err)

	log.Info("Create an R_SESS cookie containing the disabled ext token value and attempt to authenticate a request")
	tokenValue := standardUserExtToken.Status.Value
	baseURL := "https://" + standardUserClient.RancherConfig.Host
	extTokenAPIPath := "/v1/ext.cattle.io.tokens"
	err = exttokenapi.AuthenticateWithExtToken(baseURL, disabledExtToken.Name, tokenValue, extTokenAPIPath)
	require.Error(ext.T(), err, "Expected request to be rejected due to disabled ext token")
}

func (ext *ExtTokenTestSuite) TestExtTokenIsDisabledUponUserDisablement() {
	subSession := ext.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext token as standard user")
	standardUser, standardUserClient, err := rbac.SetupUser(ext.client, rbac.StandardUser.String())
	require.NoError(ext.T(), err)
	standardUserExtToken, err := exttokenapi.CreateExtToken(standardUserClient, ext.defaultExtTokenTTL)
	require.NoError(ext.T(), err)

	log.Info("As the admin user disable the standard user")
	disabled := false
	standardUser.Enabled = &disabled
	updatedUserPayload := &management.User{
		Enabled: &disabled,
	}
	disabledStandardUser, err := ext.client.Management.User.Update(standardUser, updatedUserPayload)
	require.NoError(ext.T(), err)
	require.False(ext.T(), *disabledStandardUser.Enabled, "Expected user to be disabled")

	log.Info("Verify the ext token is disabled upon user being disabled")
	disabledExtToken, err := exttokenapi.WaitForExtTokenToDisable(ext.client, standardUserExtToken.Name, false)
	require.NoError(ext.T(), err, "Polling for ext token to be disabled failed")
	require.False(ext.T(), *disabledExtToken.Spec.Enabled, "Expected ext token to be disabled")
}

func TestExtTokenTestSuite(ext *testing.T) {
	suite.Run(ext, new(ExtTokenTestSuite))
}
