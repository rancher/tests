//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12

package users

import (
	"fmt"
	"testing"
	"time"

	extapi "github.com/rancher/rancher/pkg/apis/ext.cattle.io/v1"
	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/settings"
	userapi "github.com/rancher/tests/actions/users"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type PapiUsersTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
}

func (pu *PapiUsersTestSuite) SetupSuite() {
	pu.session = session.NewSession()

	client, err := rancher.NewClient("", pu.session)
	require.NoError(pu.T(), err)
	pu.client = client
}

func (pu *PapiUsersTestSuite) TearDownSuite() {
	pu.session.Cleanup()
}

func (pu *PapiUsersTestSuite) TestCreateUserAsAdmin() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user and a secret for password")
	stdUser, stdUserPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user with password secret")

	log.Infof("Verifying annotations and ownerReferences for the user %s's password secret", stdUser.Username)
	passwordSecret, err := pu.client.WranglerContext.Core.Secret().Get(userapi.UserPasswordSecretNamespace, stdUser.Username, metav1.GetOptions{})
	require.NoError(pu.T(), err, "failed to get password secret")

	hash, ok := passwordSecret.Annotations[userapi.PasswordHashAnnotation]
	require.True(pu.T(), ok, "password-hash annotation not found")
	require.Equal(pu.T(), userapi.PasswordHash, hash, "password-hash value mismatch")
	require.Len(pu.T(), passwordSecret.OwnerReferences, 1, "secret should have one ownerReference")
	require.Equal(pu.T(), stdUser.Username, passwordSecret.OwnerReferences[0].Name, "ownerReference does not match user")

	log.Infof("Verifying that the user %s can login", stdUser.Username)
	_, err = pu.client.AsPublicAPIUser(stdUser, stdUserPassword)
	require.NoError(pu.T(), err, "failed to login as user")

	_, err = pu.client.AsPublicAPIUser(stdUser, userapi.DummyPassword)
	require.Error(pu.T(), err, "should fail to login as user")
	require.Contains(pu.T(), err.Error(), "401 Unauthorized", "expected 401 Unauthorized error, got: %v", err)
}

func (pu *PapiUsersTestSuite) TestCreateUserWithManageUsersRole() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user with 'Manage Users' permission and a secret for password")
	mUser, mUserPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String(), rbac.ManageUsers.String())
	require.NoError(pu.T(), err, "failed to create user with roles and password secret")

	log.Infof("Verifying that the user %s can login", mUser.Username)
	manageUserClient, err := pu.client.AsPublicAPIUser(mUser, mUserPassword)
	require.NoError(pu.T(), err, "failed to login as user")

	log.Infof("As %s, creating a new standard user and a secret for password", mUser.Username)
	stdUser, stdUserPassword, err := userapi.CreateUserWithRoles(manageUserClient, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user and password secret")

	log.Infof("Verifying annotations and ownerReferences for the user %s's password secret", stdUser.Username)
	passwordSecret, err := pu.client.WranglerContext.Core.Secret().Get(userapi.UserPasswordSecretNamespace, stdUser.Username, metav1.GetOptions{})
	require.NoError(pu.T(), err, "failed to get password secret")

	hash, ok := passwordSecret.Annotations[userapi.PasswordHashAnnotation]
	require.True(pu.T(), ok, "password-hash annotation not found")
	require.Equal(pu.T(), userapi.PasswordHash, hash, "password-hash value mismatch")
	require.Len(pu.T(), passwordSecret.OwnerReferences, 1, "secret should have one ownerReference")
	require.Equal(pu.T(), stdUser.Username, passwordSecret.OwnerReferences[0].Name, "ownerReference does not match user")

	log.Infof("Verifying that the user %s can login", stdUser.Username)
	_, err = pu.client.AsPublicAPIUser(stdUser, stdUserPassword)
	require.NoError(pu.T(), err, "failed to login as user")
}

func (pu *PapiUsersTestSuite) TestStandardUserCannotCreateUser() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user and a secret for password")
	stdUser, stdUserPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user with password secret")

	log.Infof("Verifying that the user %s cannot create another user", stdUser.Username)
	stdUserClient, err := pu.client.AsPublicAPIUser(stdUser, stdUserPassword)
	require.NoError(pu.T(), err, "failed to login as user")

	_, err = userapi.CreateUser(stdUserClient)
	require.Error(pu.T(), err)
	require.True(pu.T(), apierrors.IsForbidden(err), "expected 403 Forbidden error, got: %v", err)
}

func (pu *PapiUsersTestSuite) TestDuplicateUsernamesNotAllowed() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, verifying that attempting to create a new user with username 'admin' fails")
	adminUser := &v3.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "admin",
		},
		Username: "admin",
	}

	_, err := pu.client.WranglerContext.Mgmt.User().Create(adminUser)
	require.Error(pu.T(), err, "expected error when creating duplicate user 'admin'")
	require.Contains(pu.T(), err.Error(), "username already exists", "unexpected error: %v", err)

	log.Infof("As admin, creating a user and a secret for password")
	stdUser, err := userapi.CreateUser(pu.client)
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("As admin, verifying that attempting to create a duplicate user with username %s fails", stdUser.Username)
	duplicateUser := &v3.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: stdUser.Username,
		},
		Username: stdUser.Username,
	}
	_, err = pu.client.WranglerContext.Mgmt.User().Create(duplicateUser)
	require.Error(pu.T(), err, "expected error when creating duplicate user")
	require.Contains(pu.T(), err.Error(), "username already exists", "unexpected error: %v", err)
}

func (pu *PapiUsersTestSuite) TestCreatingPasswordSecretBeforeUserIsBlocked() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, attempting to create a password secret for non-existent user %q", userapi.DummyUserName)
	_, _, err := userapi.CreateUserPassword(pu.client, userapi.DummyUserName, 15)
	require.Error(pu.T(), err, "expected error when creating password secret before user creation")

	expectedErr := fmt.Sprintf(`admission webhook "rancher.cattle.io.secrets" denied the request: user %s does not exist. User must be created before the secret`, userapi.DummyUserName)
	require.Contains(pu.T(), err.Error(), expectedErr, "unexpected error: %v", err)
}

func (pu *PapiUsersTestSuite) TestUpdateUserAsAdmin() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, updating the description for the admin user")
	adminUser, err := userapi.GetUserByUsername(pu.client, rbac.Admin.String())
	require.NoError(pu.T(), err, "failed to fetch admin user")
	adminUser.Description = "Edited by self"
	updatedAdminUser, err := userapi.UpdateUser(pu.client, adminUser)
	require.NoError(pu.T(), err, "failed to update admin user description")
	require.Equal(pu.T(), "Edited by self", updatedAdminUser.Description, "admin user description was not updated")

	log.Infof("As admin, creating a user")
	testUser, err := userapi.CreateUser(pu.client)
	require.NoError(pu.T(), err, "failed to create user")
	log.Infof("Created user %s", testUser.Username)

	log.Infof("As admin, updating the description for the user %s", testUser.Username)
	testUser.Description = "Edited by admin"
	updatedTestUser, err := userapi.UpdateUser(pu.client, testUser)
	require.NoError(pu.T(), err, "failed to update description")
	require.Equal(pu.T(), "Edited by admin", updatedTestUser.Description, "user description was not updated")
}

func (pu *PapiUsersTestSuite) TestUpdateUserWithManageUsersRole() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user with 'Manage Users' permission and a secret for password")
	mUser, mUserPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String(), rbac.ManageUsers.String())
	require.NoError(pu.T(), err, "failed to create user with roles and password secret")

	manageUserClient, err := pu.client.AsPublicAPIUser(mUser, mUserPassword)
	require.NoError(pu.T(), err, "failed to login as user")

	log.Infof("As %s, updating its own description", mUser.Username)
	mUser, err = userapi.GetUserByUsername(manageUserClient, mUser.Username)
	require.NoError(pu.T(), err, "failed to fetch user")
	mUser.Description = "Edited by self"
	updatedTestUser, err := userapi.UpdateUser(manageUserClient, mUser)
	require.NoError(pu.T(), err, "failed to update description")
	require.Equal(pu.T(), "Edited by self", updatedTestUser.Description, "user description was not updated")

	log.Infof("As admin, creating a new user")
	testUser, err := userapi.CreateUser(pu.client)
	require.NoError(pu.T(), err, "failed to create user")
	log.Infof("Created user %s", testUser.Username)

	log.Infof("As %s, updating the description for the user %s", mUser.Username, testUser.Username)
	testUser.Description = "Edited by user with manage users role"
	updatedTestUser, err = userapi.UpdateUser(manageUserClient, testUser)
	require.NoError(pu.T(), err, "failed to update description")
	require.Equal(pu.T(), "Edited by user with manage users role", updatedTestUser.Description, "user description was not updated")
}

func (pu *PapiUsersTestSuite) TestStandardUserCannotUpdateUser() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user and a secret for password")
	stdUser, stdUserPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user with password secret")

	log.Infof("Verifying that the user %s cannot update itself", stdUser.Username)
	stdUserClient, err := pu.client.AsPublicAPIUser(stdUser, stdUserPassword)
	require.NoError(pu.T(), err, "failed to login as user")
	stdUser, err = userapi.GetUserByUsername(pu.client, stdUser.Username)
	require.NoError(pu.T(), err, "failed to fetch user")
	stdUser.Description = "Edited by self"
	_, err = userapi.UpdateUser(stdUserClient, stdUser)
	require.Error(pu.T(), err)
	require.True(pu.T(), apierrors.IsForbidden(err), "expected 403 Forbidden error, got: %v", err)

	log.Infof("Verifying that the user %s cannot update another user", stdUser.Username)
	newUser, err := userapi.CreateUser(pu.client)
	require.NoError(pu.T(), err, "failed to create user")
	newUser.Description = "Edited by another user"
	_, err = userapi.UpdateUser(stdUserClient, newUser)
	require.Error(pu.T(), err)
	require.True(pu.T(), apierrors.IsForbidden(err), "expected 403 Forbidden error, got: %v", err)
}

func (pu *PapiUsersTestSuite) TestCreateUserWithoutUsernameAndUpdate() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a new user without specifying username")
	username := namegen.AppendRandomString("testuser")
	newUser := &v3.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: username,
		},
		Username: "",
	}
	_, err := pu.client.WranglerContext.Mgmt.User().Create(newUser)
	require.NoError(pu.T(), err, "failed to create user without username")
	createdUser, err := userapi.WaitForUserCreation(pu.client, username)
	require.NoError(pu.T(), err, "timed out waiting for user to exist")

	log.Infof("Updating the username for user %s", createdUser.Name)
	createdUser.Username = username
	updatedUser, err := userapi.UpdateUser(pu.client, createdUser)
	require.NoError(pu.T(), err, "failed to update username")
	require.Equal(pu.T(), username, updatedUser.Username, "username was not updated successfully")

	log.Infof("Creating password secret for user '%s'", updatedUser.Username)
	passwordSecret, stdUserPassword, err := userapi.CreateUserPassword(pu.client, updatedUser.Username, 15)
	require.NoError(pu.T(), err, "failed to create password secret")

	hash, ok := passwordSecret.Annotations[userapi.PasswordHashAnnotation]
	require.True(pu.T(), ok, "password-hash annotation not found")
	require.Equal(pu.T(), userapi.PasswordHash, hash, "password-hash value mismatch")
	require.Len(pu.T(), passwordSecret.OwnerReferences, 1, "secret should have one ownerReference")
	require.Equal(pu.T(), updatedUser.Username, passwordSecret.OwnerReferences[0].Name, "ownerReference does not match user")

	log.Infof("Verifying that the user '%s' can login using the password", updatedUser.Username)
	_, err = pu.client.AsPublicAPIUser(updatedUser, stdUserPassword)
	require.NoError(pu.T(), err, "failed to login as user")
}

func (pu *PapiUsersTestSuite) TestUsernameIsImmutable() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user and a secret for password")
	createdUser, _, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user with password secret")

	log.Infof("As admin, attempting to update username of user %s", createdUser.Username)
	latestUser, err := userapi.GetUserByName(pu.client, createdUser.Name)
	require.NoError(pu.T(), err, "failed to fetch latest user object")

	latestUser.Username = namegen.AppendRandomString("newtestuser")
	_, err = userapi.UpdateUser(pu.client, latestUser)
	require.Error(pu.T(), err, "expected error when updating immutable username")
	require.Contains(pu.T(), err.Error(), "field is immutable", "unexpected error: %v", err)

	log.Infof("Attempting to patch username of user %s", createdUser.Username)
	patch := `[{"op": "replace", "path": "/username", "value": "test"}]`
	_, err = pu.client.WranglerContext.Mgmt.User().Patch(createdUser.Name, types.JSONPatchType, []byte(patch))
	require.Error(pu.T(), err, "expected error when patching immutable username")
	require.Contains(pu.T(), err.Error(), "field is immutable", "unexpected error: %v", err)
}

func (pu *PapiUsersTestSuite) TestCannotUpdateUsernameToExistingAdmin() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a new user without specifying username")
	username := namegen.AppendRandomString("testuser")
	newUser := &v3.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: username,
		},
		Username: "",
	}
	createdUser, err := pu.client.WranglerContext.Mgmt.User().Create(newUser)
	require.NoError(pu.T(), err, "failed to create user without username")

	log.Infof("As admin, attempting to patch username of '%s' to %s", createdUser.Name, rbac.Admin.String())
	patch := fmt.Sprintf(`[{"op":"replace","path":"/username","value":"%s"}]`, rbac.Admin.String())
	_, err = pu.client.WranglerContext.Mgmt.User().Patch(createdUser.Name, types.JSONPatchType, []byte(patch))
	require.Error(pu.T(), err, "expected error when patching username to existing admin")
	require.Contains(pu.T(), err.Error(), "username already exists", "unexpected error: %v", err)
}

func (pu *PapiUsersTestSuite) TestAdminCanGetAndListUsers() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating standard users")
	testUser1, _, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")
	testUser2, _, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("As %s, GET %s by name", rbac.Admin.String(), testUser1.Name)
	getUser, err := userapi.GetUserByName(pu.client, testUser1.Name)
	require.NoError(pu.T(), err, "%s should be able to get %s by name", rbac.Admin.String(), testUser1.Name)
	require.Equal(pu.T(), testUser1.Name, getUser.Name, "fetched user does not match expected")

	log.Infof("As %s, LIST users", rbac.Admin.String())
	userList, err := userapi.ListUsers(pu.client)
	require.NoError(pu.T(), err, "%s should be able to list all users", rbac.Admin.String())

	usernames := make([]string, 0, len(userList.Items))
	for _, u := range userList.Items {
		usernames = append(usernames, u.Username)
	}
	require.Contains(pu.T(), usernames, rbac.Admin.String(), "%s user should be in the list", rbac.Admin.String())
	require.Contains(pu.T(), usernames, testUser1.Username, "%s should be in the list", testUser1.Username)
	require.Contains(pu.T(), usernames, testUser2.Username, "%s should be in the list", testUser2.Username)
}

func (pu *PapiUsersTestSuite) TestManageUsersCanGetAndListUsers() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user with 'Manage Users' permission and a secret for password")
	mUser, mUserPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String(), rbac.ManageUsers.String())
	require.NoError(pu.T(), err, "failed to create user with roles and password secret")

	log.Infof("As admin, creating additional standard users")
	testUser1, _, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")
	testUser2, _, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("As %s, GET %s by name", mUser.Username, testUser1.Name)
	mUserClient, err := pu.client.AsPublicAPIUser(mUser, mUserPassword)
	require.NoError(pu.T(), err, "failed to login as %s", mUser.Username)
	getUser, err := userapi.GetUserByName(mUserClient, testUser1.Name)
	require.NoError(pu.T(), err, "%s should be able to get %s by name", mUser.Username, testUser1.Name)
	require.Equal(pu.T(), testUser1.Name, getUser.Name, "fetched user does not match expected")

	log.Infof("As %s, LIST users", mUser.Username)
	userList, err := userapi.ListUsers(mUserClient)
	require.NoError(pu.T(), err, "%s should be able to list all users", mUser.Username)

	usernames := make([]string, 0, len(userList.Items))
	for _, u := range userList.Items {
		usernames = append(usernames, u.Username)
	}
	require.Contains(pu.T(), usernames, rbac.Admin.String(), "%s user should be in the list", rbac.Admin.String())
	require.Contains(pu.T(), usernames, mUser.Username, "%s should be in the list", mUser.Username)
	require.Contains(pu.T(), usernames, testUser1.Username, "%s should be in the list", testUser1.Username)
	require.Contains(pu.T(), usernames, testUser2.Username, "%s should be in the list", testUser2.Username)
}

func (pu *PapiUsersTestSuite) TestStandardUserCanGetSelf() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user")
	testUser1, testUser1Password, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("As %s, GET its own user object", testUser1.Name)
	testUser1Client, err := pu.client.AsPublicAPIUser(testUser1, testUser1Password)
	require.NoError(pu.T(), err, "failed to login as %s", testUser1.Name)
	gotUser, err := userapi.GetUserByName(testUser1Client, testUser1.Name)
	require.NoError(pu.T(), err, "%s should be able to get itself", testUser1.Name)
	require.Equal(pu.T(), testUser1.Name, gotUser.Name, "fetched user does not match %s", testUser1.Name)
}

func (pu *PapiUsersTestSuite) TestStandardUserCannotListAllUsers() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user")
	testUser1, testUser1Password, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("As admin, creating additional standard users")
	_, _, err = userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")
	_, _, err = userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("As %s, attempting to LIST all users", testUser1.Name)
	testUser1Client, err := pu.client.AsPublicAPIUser(testUser1, testUser1Password)
	require.NoError(pu.T(), err, "failed to login as %s", testUser1.Name)
	_, err = userapi.ListUsers(testUser1Client)
	require.Error(pu.T(), err, "expected error when listing all users as standard user")
	require.True(pu.T(), apierrors.IsForbidden(err), "expected forbidden error, got: %v", err)
}

func (pu *PapiUsersTestSuite) TestAdminCanDeleteUser() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user")
	testUser1, _, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("As admin, deleting user %s", testUser1.Name)
	err = userapi.DeleteUser(pu.client, testUser1.Name)
	require.NoError(pu.T(), err, "failed to delete user %s", testUser1.Name)

	_, err = userapi.GetUserByName(pu.client, testUser1.Name)
	require.Error(pu.T(), err, "expected error when fetching deleted user %s", testUser1.Name)
	require.True(pu.T(), apierrors.IsNotFound(err), "expected NotFound error, got: %v", err)
}

func (pu *PapiUsersTestSuite) TestAdminCannotDeleteSelf() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, attempting to delete itself")
	adminUser, err := userapi.GetUserByUsername(pu.client, rbac.Admin.String())
	require.NoError(pu.T(), err, "failed to fetch admin user")
	err = userapi.DeleteUser(pu.client, adminUser.Name)
	require.Error(pu.T(), err, "expected error when admin attempts to delete itself")
	require.Contains(pu.T(), err.Error(), "can't delete yourself", "unexpected error: %v", err)
}

func (pu *PapiUsersTestSuite) TestManageUsersCanDeleteUser() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a user with 'Manage Users' permission")
	mUser, mUserPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String(), rbac.ManageUsers.String())
	require.NoError(pu.T(), err, "failed to create user with Manage Users role")

	log.Infof("As admin, creating a standard user to be deleted")
	testUser1, _, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("As %s, deleting user %s", mUser.Username, testUser1.Name)
	mUserClient, err := pu.client.AsPublicAPIUser(mUser, mUserPassword)
	require.NoError(pu.T(), err, "failed to login as %s", mUser.Username)
	err = userapi.DeleteUser(mUserClient, testUser1.Name)
	require.NoError(pu.T(), err, "failed to delete user %s", testUser1.Name)

	_, err = userapi.GetUserByName(pu.client, testUser1.Name)
	require.Error(pu.T(), err, "expected error when fetching deleted user %s", testUser1.Name)
	require.True(pu.T(), apierrors.IsNotFound(err), "expected NotFound error, got: %v", err)
}

func (pu *PapiUsersTestSuite) TestManageUsersCannotDeleteSelf() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a user with 'Manage Users' permission")
	mUser, mUserPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String(), rbac.ManageUsers.String())
	require.NoError(pu.T(), err, "failed to create user with Manage Users role")

	log.Infof("As %s, attempting to delete itself", mUser.Username)
	mUserClient, err := pu.client.AsPublicAPIUser(mUser, mUserPassword)
	require.NoError(pu.T(), err, "failed to login as %s", mUser.Username)
	err = userapi.DeleteUser(mUserClient, mUser.Name)
	require.Error(pu.T(), err, "expected error when user with Manage Users role attempts to delete itself")
	require.Contains(pu.T(), err.Error(), "can't delete yourself", "unexpected error: %v", err)
}

func (pu *PapiUsersTestSuite) TestStandardUserCannotDeleteOtherUsers() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating two standard users")
	user1, user1Password, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")
	user2, _, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("As %s, attempting to delete %s", user1.Username, user2.Name)
	user1Client, err := pu.client.AsPublicAPIUser(user1, user1Password)
	require.NoError(pu.T(), err, "failed to login as %s", user1.Username)
	err = userapi.DeleteUser(user1Client, user2.Name)
	require.Error(pu.T(), err, "expected error when standard user attempts to delete another user")
	require.True(pu.T(), apierrors.IsForbidden(err), "expected 403 Forbidden error, got: %v", err)

	_, err = userapi.GetUserByName(pu.client, user2.Name)
	require.NoError(pu.T(), err, "%s should still exist", user2.Name)
}

func (pu *PapiUsersTestSuite) TestUserDeletionRemovesBackingSecret() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user")
	testUser1, _, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("As admin, deleting user %s", testUser1.Name)
	err = userapi.DeleteUser(pu.client, testUser1.Name)
	require.NoError(pu.T(), err, "failed to delete user %s", testUser1.Name)

	log.Infof("Verifying backing secret is deleted for user %s", testUser1.Name)
	err = userapi.WaitForBackingSecretDeletion(pu.client, testUser1.Name)
	require.NoError(pu.T(), err, "timed out waiting for backing secret deletion")
}

func (pu *PapiUsersTestSuite) TestDeletingBackingSecretBlocksUserAccess() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user")
	testUser, testPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	_, err = pu.client.AsPublicAPIUser(testUser, testPassword)
	require.NoError(pu.T(), err, "user %s should be able to login before secret deletion", testUser.Username)

	log.Infof("As admin, deleting backing secret for %s", testUser.Name)
	err = pu.client.WranglerContext.Core.Secret().Delete(userapi.UserPasswordSecretNamespace, testUser.Name, &metav1.DeleteOptions{})
	require.NoError(pu.T(), err, "failed to delete backing secret for user %s", testUser.Name)

	log.Infof("Verifying that %s can no longer login", testUser.Name)
	_, err = pu.client.AsPublicAPIUser(testUser, testPassword)
	require.Error(pu.T(), err, "expected login to fail after backing secret deletion")
	require.Contains(pu.T(), err.Error(), "401", "expected 401 Unauthorized error, got: %v", err)

	log.Infof("Recreating password secret for %s", testUser.Name)
	secret, newTestPassword, err := userapi.CreateUserPassword(pu.client, testUser.Username, 15)
	require.NoError(pu.T(), err, "failed to recreate password secret for user %s", testUser.Name)

	hash, ok := secret.Annotations[userapi.PasswordHashAnnotation]
	require.True(pu.T(), ok, "password-hash annotation not found")
	require.Equal(pu.T(), userapi.PasswordHash, hash, "password-hash value mismatch")
	require.Len(pu.T(), secret.OwnerReferences, 1, "secret should have one ownerReference")
	require.Equal(pu.T(), testUser.Username, secret.OwnerReferences[0].Name, "ownerReference does not match user")

	log.Infof("Verifying that %s can login after secret recreation", testUser.Name)
	_, err = pu.client.AsPublicAPIUser(testUser, newTestPassword)
	require.NoError(pu.T(), err, "user %s should be able to login after secret recreation", testUser.Username)
}

func (pu *PapiUsersTestSuite) TestDeactivateAndReactivateUserAsAdmin() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user")
	testUser, testPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	_, err = pu.client.AsPublicAPIUser(testUser, testPassword)
	require.NoError(pu.T(), err, "user %s should be able to login before deactivation", testUser.Username)

	log.Infof("As admin, deactivating user %s", testUser.Name)
	patch := []byte(`{"enabled":false}`)
	_, err = pu.client.WranglerContext.Mgmt.User().Patch(testUser.Name, types.MergePatchType, patch)
	require.NoError(pu.T(), err, "failed to deactivate user %s", testUser.Name)

	log.Infof("Verifying that %s is deactivated", testUser.Name)
	updatedUser, err := userapi.GetUserByName(pu.client, testUser.Name)
	require.NoError(pu.T(), err, "failed to get user %s after deactivation", testUser.Name)
	require.False(pu.T(), *updatedUser.Enabled, "expected enabled=false, got true")

	log.Infof("Verifying that %s can no longer login", testUser.Name)
	_, err = pu.client.AsPublicAPIUser(testUser, testPassword)
	require.Error(pu.T(), err, "expected login to fail after deactivation")
	require.Contains(pu.T(), err.Error(), "403 Forbidden", "expected 403 Forbidden error, got: %v", err)

	log.Infof("As admin, reactivating user %s", testUser.Name)
	reactivatePatch := []byte(`{"enabled":true}`)
	_, err = pu.client.WranglerContext.Mgmt.User().Patch(testUser.Name, types.MergePatchType, reactivatePatch)
	require.NoError(pu.T(), err, "failed to reactivate user %s", testUser.Name)

	reactivatedUser, err := userapi.GetUserByName(pu.client, testUser.Name)
	require.NoError(pu.T(), err, "failed to get user %s after reactivation", testUser.Name)
	log.Infof("User %s enabled=%v after reactivation", testUser.Name, *reactivatedUser.Enabled)
	require.True(pu.T(), *reactivatedUser.Enabled, "expected enabled=true, got false")

	log.Infof("Verifying that %s can login after reactivation", testUser.Name)
	_, err = pu.client.AsPublicAPIUser(testUser, testPassword)
	require.NoError(pu.T(), err, "user %s should be able to login after reactivation", testUser.Username)
}

func (pu *PapiUsersTestSuite) TestAdminCannotDeactivateSelf() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, attempting to deactivate itself")
	adminUser, err := userapi.GetUserByUsername(pu.client, rbac.Admin.String())
	require.NoError(pu.T(), err, "failed to fetch admin user")
	deactivatePatch := []byte(`{"enabled":false}`)
	_, err = pu.client.WranglerContext.Mgmt.User().Patch(adminUser.Name, types.MergePatchType, deactivatePatch)
	require.Error(pu.T(), err, "expected error when admin attempts to deactivate itself")
	require.Contains(pu.T(), err.Error(), "can't deactivate yourself", "unexpected error: %v", err)
}

func (pu *PapiUsersTestSuite) TestManageUsersCannotDeactivateSelf() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user with 'Manage Users' role")
	mUser, mUserPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String(), rbac.ManageUsers.String())
	require.NoError(pu.T(), err, "failed to create user with Manage Users role")

	log.Infof("Attempting to deactivate itself as %s", mUser.Username)
	mUserClient, err := pu.client.AsPublicAPIUser(mUser, mUserPassword)
	require.NoError(pu.T(), err, "failed to login as %s", mUser.Username)
	deactivatePatch := []byte(`{"enabled":false}`)
	_, err = mUserClient.WranglerContext.Mgmt.User().Patch(mUser.Name, types.MergePatchType, deactivatePatch)
	require.Error(pu.T(), err, "expected error when %s attempts to deactivate itself", mUser.Username)
	require.Contains(pu.T(), err.Error(), "can't deactivate yourself", "unexpected error: %v", err)
}

func (pu *PapiUsersTestSuite) TestDeactivateAndReactivateUserAsManageUsers() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user with 'Manage Users' permission")
	mUser, mUserPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String(), rbac.ManageUsers.String())
	require.NoError(pu.T(), err, "failed to create user with Manage Users role")

	log.Infof("As admin, creating a standard user to be deactivated/reactivated")
	targetUser, targetPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create target user")

	log.Infof("As %s, deactivating user %s", mUser.Username, targetUser.Name)
	mUserClient, err := pu.client.AsPublicAPIUser(mUser, mUserPassword)
	require.NoError(pu.T(), err, "failed to login as %s", mUser.Username)
	deactivatePatch := []byte(`{"enabled":false}`)
	_, err = mUserClient.WranglerContext.Mgmt.User().Patch(targetUser.Name, types.MergePatchType, deactivatePatch)
	require.NoError(pu.T(), err, "%s failed to deactivate user %s", mUser.Username, targetUser.Name)

	updatedUser, err := userapi.GetUserByName(mUserClient, targetUser.Name)
	require.NoError(pu.T(), err, "failed to get user %s after deactivation", targetUser.Name)
	require.False(pu.T(), *updatedUser.Enabled, "expected enabled=false, got true")

	log.Infof("Verifying that %s can no longer login", targetUser.Name)
	_, err = pu.client.AsPublicAPIUser(targetUser, targetPassword)
	require.Error(pu.T(), err, "expected login to fail after deactivation")
	require.Contains(pu.T(), err.Error(), "403 Forbidden", "expected 403 Forbidden error, got: %v", err)

	log.Infof("As %s, reactivating user %s", mUser.Username, targetUser.Name)
	reactivatePatch := []byte(`{"enabled":true}`)
	_, err = mUserClient.WranglerContext.Mgmt.User().Patch(targetUser.Name, types.MergePatchType, reactivatePatch)
	require.NoError(pu.T(), err, "%s failed to reactivate user %s", mUser.Username, targetUser.Name)

	reactivatedUser, err := userapi.GetUserByName(mUserClient, targetUser.Name)
	require.NoError(pu.T(), err, "failed to get user %s after reactivation", targetUser.Name)
	require.True(pu.T(), *reactivatedUser.Enabled, "expected enabled=true, got false")

	log.Infof("Verifying that %s can login after reactivation", targetUser.Name)
	_, err = pu.client.AsPublicAPIUser(targetUser, targetPassword)
	require.NoError(pu.T(), err, "user %s should be able to login after reactivation", targetUser.Username)
}

func (pu *PapiUsersTestSuite) TestStandardUserCannotDeactivateOtherUser() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating two standard users")
	user1, user1Password, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")
	user2, user2Password, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("As %s, attempting to deactivate %s", user1.Username, user2.Username)
	user1Client, err := pu.client.AsPublicAPIUser(user1, user1Password)
	require.NoError(pu.T(), err, "failed to login as %s", user1.Username)
	deactivatePatch := []byte(`{"enabled":false}`)
	_, err = user1Client.WranglerContext.Mgmt.User().Patch(user2.Name, types.MergePatchType, deactivatePatch)
	require.Error(pu.T(), err, "expected deactivation attempt by %s to fail", user1.Username)
	require.Contains(pu.T(), err.Error(), "forbidden", "expected forbidden error, got: %v", err)

	log.Infof("Verifying that %s is still active", user2.Username)
	_, err = pu.client.AsPublicAPIUser(user2, user2Password)
	require.NoError(pu.T(), err, "user %s should be able to login after reactivation", user2.Username)
}

func (pu *PapiUsersTestSuite) TestAdminChangeUserPassword() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user")
	testUser1, initialPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("Verifying that %s can login with the initial password", testUser1.Username)
	_, err = pu.client.AsPublicAPIUser(testUser1, initialPassword)
	require.NoError(pu.T(), err, "user %s should be able to login with initial password", testUser1.Username)

	log.Infof("As admin, changing password for user %s", testUser1.Username)
	newPassword, err := userapi.PasswordChangeRequest(pu.client, testUser1.Username, initialPassword, 15)
	require.NoError(pu.T(), err, "failed to change password for user %s", testUser1.Username)

	log.Infof("Verifying that %s cannot login with the initial password", testUser1.Username)
	_, err = pu.client.AsPublicAPIUser(testUser1, initialPassword)
	require.Error(pu.T(), err, "expected login to fail with old password after password change")
	require.Contains(pu.T(), err.Error(), "401", "expected 401 Unauthorized error, got: %v", err)

	log.Infof("Verifying that %s can login with the new password", testUser1.Username)
	_, err = pu.client.AsPublicAPIUser(testUser1, newPassword)
	require.NoError(pu.T(), err, "user %s should be able to login with new password", testUser1.Username)
}

func (pu *PapiUsersTestSuite) TestAdminChangeUserPasswordWithoutCurrentPassword() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user")
	testUser1, initialPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("Verifying that %s can login with the initial password", testUser1.Username)
	_, err = pu.client.AsPublicAPIUser(testUser1, initialPassword)
	require.NoError(pu.T(), err, "user %s should be able to login with initial password", testUser1.Username)

	log.Infof("As admin, changing password for user %s", testUser1.Username)
	newPassword, err := userapi.PasswordChangeRequest(pu.client, testUser1.Username, "", 15)
	require.NoError(pu.T(), err, "failed to change password for user %s", testUser1.Username)

	log.Infof("Verifying that %s cannot login with the initial password", testUser1.Username)
	_, err = pu.client.AsPublicAPIUser(testUser1, initialPassword)
	require.Error(pu.T(), err, "expected login to fail with old password after password change")
	require.Contains(pu.T(), err.Error(), "401", "expected 401 Unauthorized error, got: %v", err)

	log.Infof("Verifying that %s can login with the new password", testUser1.Username)
	_, err = pu.client.AsPublicAPIUser(testUser1, newPassword)
	require.NoError(pu.T(), err, "user %s should be able to login with new password", testUser1.Username)
}

func (pu *PapiUsersTestSuite) TestChangeUserPasswordWithInvalidUserID() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user")
	testUser1, initialPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("Verifying that %s can login with the initial password", testUser1.Username)
	_, err = pu.client.AsPublicAPIUser(testUser1, initialPassword)
	require.NoError(pu.T(), err, "user %s should be able to login with initial password", testUser1.Username)

	log.Infof("As admin, changing password for user %s", testUser1.Username)
	dummyUsername := namegen.AppendRandomString("dummyuser")
	_, err = userapi.PasswordChangeRequest(pu.client, dummyUsername, initialPassword, 15)
	require.Error(pu.T(), err, "expected error when changing password for non-existent user")
	require.Contains(pu.T(), err.Error(), "not found", "expected not found error, got: %v", err)
}

func (pu *PapiUsersTestSuite) TestCreatePasswordWithShortPassword() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user")
	testUser1, err := userapi.CreateUser(pu.client)
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("As admin, creating a password for user %s with shorter length", testUser1.Username)
	passwordLength, err := settings.GetGlobalSettingDefaultValue(pu.client, settings.UserPasswordMinLength)
	require.NoError(pu.T(), err, "failed to get password minimum length")
	_, _, err = userapi.CreateUserPassword(pu.client, testUser1.Username, 5)
	require.Error(pu.T(), err, "expected error when creating password with shorter length")
	require.Contains(pu.T(), err.Error(), fmt.Sprintf("password must be at least %s characters", passwordLength), "unexpected error: %v", err)
}

func (pu *PapiUsersTestSuite) TestChangePasswordWithShortPassword() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user")
	testUser1, initialPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("As admin, changing password for user %s with shorter length", testUser1.Username)
	passwordLength, err := settings.GetGlobalSettingDefaultValue(pu.client, settings.UserPasswordMinLength)
	require.NoError(pu.T(), err, "failed to get password minimum length")
	_, err = userapi.PasswordChangeRequest(pu.client, testUser1.Username, initialPassword, 5)
	require.Error(pu.T(), err, "expected error when creating password with shorter length")
	require.Contains(pu.T(), err.Error(), fmt.Sprintf("password must be at least %s characters", passwordLength), "unexpected error: %v", err)
	log.Infof("As admin, updating password for user %s with empty newPassword", testUser1.Username)
	_, err = userapi.PasswordChangeRequest(pu.client, testUser1.Username, initialPassword, 0)
	require.Error(pu.T(), err, "expected error when newPassword is empty")
	require.Contains(pu.T(), err.Error(), fmt.Sprintf("password must be at least %s characters", passwordLength), "unexpected error: %v", err)
}

func (pu *PapiUsersTestSuite) TestPasswordCannotBeSameAsUsernameOnCreate() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user")
	username := namegen.AppendRandomString("standardtestuser")
	testUser1 := &v3.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: username,
		},
		Username: username,
	}
	_, err := pu.client.WranglerContext.Mgmt.User().Create(testUser1)
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("As admin, creating a password for user %s that is the same as the username", testUser1.Username)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      username,
			Namespace: userapi.UserPasswordSecretNamespace,
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"password": username,
		},
	}
	_, err = pu.client.WranglerContext.Core.Secret().Create(secret)
	require.Error(pu.T(), err, "expected error when creating password same as username")
	require.Contains(pu.T(), err.Error(), "password cannot be the same as username", "unexpected error: %v", err)
}

func (pu *PapiUsersTestSuite) TestPasswordCannotBeSameAsUsernameOnUpdate() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user")
	testUser1, currentPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("As admin, changing password for user %s to be the same as the username", testUser1.Username)
	newPassword := testUser1.Username
	name := fmt.Sprintf("%s-passwd-change", testUser1.Username)
	passwordChangeReq := &extapi.PasswordChangeRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: extapi.PasswordChangeRequestSpec{
			CurrentPassword: currentPassword,
			NewPassword:     newPassword,
			UserID:          testUser1.Username,
		},
	}
	_, err = pu.client.WranglerContext.Ext.PasswordChangeRequest().Create(passwordChangeReq)
	require.Error(pu.T(), err, "expected error when updating password same as username")
	require.Contains(pu.T(), err.Error(), "password cannot be the same as the username", "unexpected error: %v", err)
}

func (pu *PapiUsersTestSuite) TestDeprecatedPasswordFieldIgnored() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user with the deprecated 'password' field")
	username := namegen.AppendRandomString("deprecatedpassuser")
	deprecatedPassword := "DeprecatedPass123!"

	userWithDeprecatedPassword := &v3.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: username,
		},
		Username: username,
		Password: deprecatedPassword,
	}

	testUser1, err := pu.client.WranglerContext.Mgmt.User().Create(userWithDeprecatedPassword)
	require.NoError(pu.T(), err)

	log.Infof("Verifying that the deprecated 'password' field is not honored for user %s", testUser1.Username)
	_, err = pu.client.AsPublicAPIUser(testUser1, deprecatedPassword)
	require.Error(pu.T(), err, "expected login to fail with deprecated password")
	require.Contains(pu.T(), err.Error(), "401", "expected 401 Unauthorized error, got: %v", err)

	log.Infof("As admin, creating a new standard user")
	testUser2, err := userapi.CreateUser(pu.client)
	require.NoError(pu.T(), err, "failed to create user")

	log.Infof("As admin, attempting to update user resource with the deprecated 'password' field")
	testUser2.Password = deprecatedPassword
	_, err = pu.client.WranglerContext.Mgmt.User().Update(testUser2)
	require.NoError(pu.T(), err)

	log.Infof("Verifying that the deprecated 'password' field is not honored for user %s", testUser2.Username)
	_, err = pu.client.AsPublicAPIUser(testUser2, deprecatedPassword)
	require.Error(pu.T(), err, "expected login to fail with deprecated password")
	require.Contains(pu.T(), err.Error(), "401", "expected 401 Unauthorized error, got: %v", err)
}

func (pu *PapiUsersTestSuite) TestSelfUserAsAdmin() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user and a secret for password")
	_, _, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user with password secret")

	log.Infof("Verifying that the self user request returns the admin user ID")
	adminUser, err := userapi.GetUserByUsername(pu.client, rbac.Admin.String())
	require.NoError(pu.T(), err, "failed to fetch admin user")

	selfUserID, err := userapi.CreateSelfUserRequest(pu.client)
	require.NoError(pu.T(), err, "failed to create self user request as admin")

	require.Equal(pu.T(), adminUser.Name, selfUserID, "self user ID does not match admin user ID")
}

func (pu *PapiUsersTestSuite) TestSelfUserAsStandardUser() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a standard user and a secret for password")
	stdUser, stdUserPassword, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user with password secret")

	log.Infof("Verifying that the self user request returns the standard user's ID")
	stdUserClient, err := pu.client.AsPublicAPIUser(stdUser, stdUserPassword)
	require.NoError(pu.T(), err, "failed to login as %s", stdUser.Username)

	selfUserID, err := userapi.CreateSelfUserRequest(stdUserClient)
	require.NoError(pu.T(), err, "failed to create self user request as admin")
	require.Equal(pu.T(), stdUser.Name, selfUserID, "self user ID does not match standard user ID")
}

func (pu *PapiUsersTestSuite) TestGroupMembershipRefreshSingleUser() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating multiple users for testing single-user refresh")
	testUser1, testUser1Password, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")
	testUser2, testUser2Password, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	_, err = pu.client.AsPublicAPIUser(testUser1, testUser1Password)
	require.NoError(pu.T(), err)
	_, err = pu.client.AsPublicAPIUser(testUser2, testUser2Password)
	require.NoError(pu.T(), err)

	log.Infof("Fetching current LastRefresh timestamps for all users")
	testUser1AttrsBefore, err := pu.client.WranglerContext.Mgmt.UserAttribute().Get(testUser1.Name, metav1.GetOptions{})
	require.NoError(pu.T(), err)
	testUser2AttrsBefore, err := pu.client.WranglerContext.Mgmt.UserAttribute().Get(testUser2.Name, metav1.GetOptions{})
	require.NoError(pu.T(), err)

	beforeTime1, err := time.Parse(time.RFC3339, testUser1AttrsBefore.LastRefresh)
	require.NoError(pu.T(), err)
	beforeTime2, err := time.Parse(time.RFC3339, testUser2AttrsBefore.LastRefresh)
	require.NoError(pu.T(), err)

	log.Infof("Triggering group membership refresh for a single user: %s", testUser1.Name)
	err = userapi.CreateGroupMembershipRefreshRequest(pu.client, testUser1.Name)
	require.NoError(pu.T(), err, "failed to trigger group membership refresh for user %s", testUser1.Name)

	log.Infof("Fetching LastRefresh timestamps after refresh")
	afterTime1, err := userapi.WaitForUserLastRefreshUpdate(pu.client, testUser1.Name, beforeTime1)
	require.NoError(pu.T(), err, "LastRefresh for %s did not update after refresh request", testUser1.Name)
	testUser2AttrsAfter, _ := pu.client.WranglerContext.Mgmt.UserAttribute().Get(testUser2.Name, metav1.GetOptions{})
	afterTime2, _ := time.Parse(time.RFC3339, testUser2AttrsAfter.LastRefresh)

	log.Infof("%s LastRefresh before: %s, after: %s", testUser1.Name, beforeTime1, afterTime1)
	require.True(pu.T(), afterTime1.After(beforeTime1), "expected LastRefresh for %s to be updated", testUser1.Name)
	require.Equal(pu.T(), beforeTime2, afterTime2, "expected LastRefresh for %s to be unchanged", testUser2.Name)
}

func (pu *PapiUsersTestSuite) TestGroupMembershipRefreshAllUsers() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating multiple users for testing single-user refresh")
	testUser1, testUser1Password, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")
	testUser2, testUser2Password, err := userapi.CreateUserWithRoles(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err, "failed to create user")

	_, err = pu.client.AsPublicAPIUser(testUser1, testUser1Password)
	require.NoError(pu.T(), err)
	_, err = pu.client.AsPublicAPIUser(testUser2, testUser2Password)
	require.NoError(pu.T(), err)

	log.Infof("Fetching current LastRefresh timestamps for all users")
	testUser1AttrsBefore, err := pu.client.WranglerContext.Mgmt.UserAttribute().Get(testUser1.Name, metav1.GetOptions{})
	require.NoError(pu.T(), err)
	testUser2AttrsBefore, err := pu.client.WranglerContext.Mgmt.UserAttribute().Get(testUser2.Name, metav1.GetOptions{})
	require.NoError(pu.T(), err)

	beforeTime1, err := time.Parse(time.RFC3339, testUser1AttrsBefore.LastRefresh)
	require.NoError(pu.T(), err)
	beforeTime2, err := time.Parse(time.RFC3339, testUser2AttrsBefore.LastRefresh)
	require.NoError(pu.T(), err)

	log.Infof("Triggering group membership refresh for all users")
	err = userapi.CreateGroupMembershipRefreshRequest(pu.client, "*")
	require.NoError(pu.T(), err, "failed to trigger group membership refresh for all users")

	log.Infof("Fetching LastRefresh timestamps after refresh")
	afterTime1, err := userapi.WaitForUserLastRefreshUpdate(pu.client, testUser1.Name, beforeTime1)
	require.NoError(pu.T(), err, "LastRefresh for %s did not update after refresh request", testUser1.Name)
	afterTime2, err := userapi.WaitForUserLastRefreshUpdate(pu.client, testUser2.Name, beforeTime2)
	require.NoError(pu.T(), err, "LastRefresh for %s did not update after refresh request", testUser2.Name)

	log.Infof("%s LastRefresh before: %s, after: %s", testUser1.Name, beforeTime1, afterTime1)
	require.True(pu.T(), afterTime1.After(beforeTime1), "expected LastRefresh for %s to be updated", testUser1.Name)
	log.Infof("%s LastRefresh before: %s, after: %s", testUser2.Name, beforeTime2, afterTime2)
	require.True(pu.T(), afterTime2.After(beforeTime2), "expected LastRefresh for %s to be updated", testUser2.Name)
}

func TestPapiUsersTestSuite(t *testing.T) {
	suite.Run(t, new(PapiUsersTestSuite))
}
