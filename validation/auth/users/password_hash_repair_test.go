//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.11 && !2.12 && !2.13 && !2.14

package users

import (
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extsecretapi "github.com/rancher/shepherd/extensions/kubeapi/secrets"
	extuserapi "github.com/rancher/shepherd/extensions/kubeapi/users"
	password "github.com/rancher/shepherd/extensions/users/passwordgenerator"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	userapi "github.com/rancher/tests/actions/kubeapi/users"
	webhookactions "github.com/rancher/tests/actions/kubeapi/webhook"
	"github.com/rancher/tests/actions/rbac"
	usersactions "github.com/rancher/tests/actions/users"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PasswordHashRepairTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
}

func (pr *PasswordHashRepairTestSuite) SetupSuite() {
	pr.session = session.NewSession()

	client, err := rancher.NewClient("", pr.session)
	require.NoError(pr.T(), err)
	pr.client = client
}

func (pr *PasswordHashRepairTestSuite) TearDownSuite() {
	pr.session.Cleanup()
}

func (pr *PasswordHashRepairTestSuite) setupUnhashedPasswordSecret() (*v3.User, string) {
	mutatingWebhookBackup, validatingWebhookBackup, err := webhookactions.SimulateRancherWebhookDown(pr.client, extclusterapi.LocalCluster)
	require.NoError(pr.T(), err, "failed to simulate rancher-webhook being down")

	testUser, err := userapi.CreateUser(pr.client)
	require.NoError(pr.T(), err, "failed to create user")

	_, oldPassword, err := extuserapi.CreateUserPassword(pr.client, testUser.Username, 15)
	require.NoError(pr.T(), err, "failed to create password secret for user %s", testUser.Username)

	unhashedSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "failed to get password secret for user %s", testUser.Username)
	_, ok := unhashedSecret.Annotations[extuserapi.PasswordHashAnnotation]
	require.False(pr.T(), ok, "password-hash annotation should not be present on an unhashed secret")

	_, err = pr.client.AsPublicAPIUser(testUser, oldPassword)
	require.Error(pr.T(), err, "expected login to fail before the secret is repaired")
	require.Contains(pr.T(), err.Error(), "401", "expected 401 Unauthorized error, got: %v", err)

	err = webhookactions.RestoreRancherWebhook(pr.client, extclusterapi.LocalCluster, mutatingWebhookBackup, validatingWebhookBackup)
	require.NoError(pr.T(), err, "failed to restore rancher-webhook")

	_, err = pr.client.AsPublicAPIUser(testUser, oldPassword)
	require.Error(pr.T(), err, "expected login to fail before the secret is repaired")
	require.Contains(pr.T(), err.Error(), "401", "expected 401 Unauthorized error, got: %v", err)

	return testUser, oldPassword
}

func (pr *PasswordHashRepairTestSuite) TestAdminRepairsNoAnnotationSecretViaPasswordChangeRequest() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an admin can repair a password secret with no password-hash annotation via PasswordChangeRequest and that the user can then log in")

	testUser, oldPassword := pr.setupUnhashedPasswordSecret()

	newPassword, err := extuserapi.ChangePasswordForUser(pr.client, testUser.Username, "", 15)
	require.NoError(pr.T(), err, "failed to repair password via PasswordChangeRequest")

	repairedSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "failed to get repaired password secret for user %s", testUser.Username)

	hash, ok := repairedSecret.Annotations[extuserapi.PasswordHashAnnotation]
	require.True(pr.T(), ok, "password-hash annotation not found after repair")
	require.Equal(pr.T(), extuserapi.PasswordHash, hash, "password-hash value mismatch after repair")
	require.NotEmpty(pr.T(), repairedSecret.Data["salt"], "salt should be present after repair")
	require.NotEqual(pr.T(), []byte(oldPassword), repairedSecret.Data["password"], "password data should be hashed, not the plaintext")
	require.Len(pr.T(), repairedSecret.OwnerReferences, 1, "secret should have one ownerReference after repair")
	require.Equal(pr.T(), testUser.Username, repairedSecret.OwnerReferences[0].Name, "ownerReference does not match user after repair")

	_, err = pr.client.AsPublicAPIUser(testUser, newPassword)
	require.NoError(pr.T(), err, "user %s should be able to login with the new password", testUser.Username)

	_, err = pr.client.AsPublicAPIUser(testUser, oldPassword)
	require.Error(pr.T(), err, "expected login to fail with the old plaintext password")
	require.Contains(pr.T(), err.Error(), "401", "expected 401 Unauthorized error, got: %v", err)
}

func (pr *PasswordHashRepairTestSuite) TestAdminRepairsNoAnnotationSecretViaSetPasswordAction() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an admin can repair a password secret with no password-hash annotation via the Norman setpassword action and that the user can then log in")

	testUser, oldPassword := pr.setupUnhashedPasswordSecret()

	mgmtUser, err := pr.client.Management.User.ByID(testUser.Name)
	require.NoError(pr.T(), err, "failed to fetch Norman user %s", testUser.Name)

	newPassword := password.GenerateUserPassword("RepairedPass-")
	err = usersactions.UpdateUserPassword(pr.client, mgmtUser, newPassword)
	require.NoError(pr.T(), err, "failed to repair password via setpassword action")

	repairedSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "failed to get repaired password secret for user %s", testUser.Username)

	hash, ok := repairedSecret.Annotations[extuserapi.PasswordHashAnnotation]
	require.True(pr.T(), ok, "password-hash annotation not found after repair")
	require.Equal(pr.T(), extuserapi.PasswordHash, hash, "password-hash value mismatch after repair")
	require.NotEmpty(pr.T(), repairedSecret.Data["salt"], "salt should be present after repair")
	require.NotEqual(pr.T(), []byte(oldPassword), repairedSecret.Data["password"], "password data should be hashed, not the plaintext")
	require.Len(pr.T(), repairedSecret.OwnerReferences, 1, "secret should have one ownerReference after repair")
	require.Equal(pr.T(), testUser.Username, repairedSecret.OwnerReferences[0].Name, "ownerReference does not match user after repair")

	_, err = pr.client.AsPublicAPIUser(testUser, newPassword)
	require.NoError(pr.T(), err, "user %s should be able to login with the new password", testUser.Username)

	_, err = pr.client.AsPublicAPIUser(testUser, oldPassword)
	require.Error(pr.T(), err, "expected login to fail with the old plaintext password")
	require.Contains(pr.T(), err.Error(), "401", "expected 401 Unauthorized error, got: %v", err)
}

func (pr *PasswordHashRepairTestSuite) TestAdminRepairsNoAnnotationSecretWhileWebhookStillDown() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an admin can repair a password secret with no password-hash annotation via PasswordChangeRequest while rancher-webhook is still down")

	mutatingWebhookBackup, validatingWebhookBackup, err := webhookactions.SimulateRancherWebhookDown(pr.client, extclusterapi.LocalCluster)
	require.NoError(pr.T(), err, "failed to simulate rancher-webhook being down")

	testUser, err := userapi.CreateUser(pr.client)
	require.NoError(pr.T(), err, "failed to create user")

	_, oldPassword, err := extuserapi.CreateUserPassword(pr.client, testUser.Username, 15)
	require.NoError(pr.T(), err, "failed to create password secret for user %s", testUser.Username)

	unhashedSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "failed to get password secret for user %s", testUser.Username)
	_, ok := unhashedSecret.Annotations[extuserapi.PasswordHashAnnotation]
	require.False(pr.T(), ok, "password-hash annotation should not be present on an unhashed secret")

	_, err = pr.client.AsPublicAPIUser(testUser, oldPassword)
	require.Error(pr.T(), err, "expected login to fail before the secret is repaired")
	require.Contains(pr.T(), err.Error(), "401", "expected 401 Unauthorized error, got: %v", err)

	newPassword, err := extuserapi.ChangePasswordForUser(pr.client, testUser.Username, "", 15)
	require.NoError(pr.T(), err, "failed to repair password via PasswordChangeRequest while rancher-webhook is still down")

	repairedSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "failed to get repaired password secret for user %s", testUser.Username)

	hash, ok := repairedSecret.Annotations[extuserapi.PasswordHashAnnotation]
	require.True(pr.T(), ok, "password-hash annotation not found after repair")
	require.Equal(pr.T(), extuserapi.PasswordHash, hash, "password-hash value mismatch after repair")
	require.NotEmpty(pr.T(), repairedSecret.Data["salt"], "salt should be present after repair")
	require.NotEqual(pr.T(), []byte(oldPassword), repairedSecret.Data["password"], "password data should be hashed, not the plaintext")
	require.Len(pr.T(), repairedSecret.OwnerReferences, 1, "secret should have one ownerReference after repair")
	require.Equal(pr.T(), testUser.Username, repairedSecret.OwnerReferences[0].Name, "ownerReference does not match user after repair")

	_, err = pr.client.AsPublicAPIUser(testUser, newPassword)
	require.NoError(pr.T(), err, "user %s should be able to login with the new password while rancher-webhook is still down", testUser.Username)

	_, err = pr.client.AsPublicAPIUser(testUser, oldPassword)
	require.Error(pr.T(), err, "expected login to fail with the old plaintext password")
	require.Contains(pr.T(), err.Error(), "401", "expected 401 Unauthorized error, got: %v", err)

	err = webhookactions.RestoreRancherWebhook(pr.client, extclusterapi.LocalCluster, mutatingWebhookBackup, validatingWebhookBackup)
	require.NoError(pr.T(), err, "failed to restore rancher-webhook")
}

func (pr *PasswordHashRepairTestSuite) TestAdminRepairsEmptyAnnotationSecretViaPasswordChangeRequest() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an admin can repair a password secret whose hash annotation is present but empty via PasswordChangeRequest and that the user can then log in")

	mutatingWebhookBackup, validatingWebhookBackup, err := webhookactions.SimulateRancherWebhookDown(pr.client, extclusterapi.LocalCluster)
	require.NoError(pr.T(), err, "failed to simulate rancher-webhook being down")

	testUser, err := userapi.CreateUser(pr.client)
	require.NoError(pr.T(), err, "failed to create user")

	oldPassword := password.GenerateUserPassword("OldPlain-")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testUser.Username,
			Namespace: extuserapi.UserPasswordSecretNamespace,
			Annotations: map[string]string{
				extuserapi.PasswordHashAnnotation: "",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"password": oldPassword,
		},
	}
	_, err = pr.client.WranglerContext.Core.Secret().Create(secret)
	require.NoError(pr.T(), err, "failed to create password secret with an empty hash annotation for user %s", testUser.Username)

	emptyAnnotationSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "failed to get password secret for user %s", testUser.Username)
	hash, ok := emptyAnnotationSecret.Annotations[extuserapi.PasswordHashAnnotation]
	require.True(pr.T(), ok, "password-hash annotation key should be present, even though its value is empty")
	require.Empty(pr.T(), hash, "password-hash annotation value should be empty")

	_, err = pr.client.AsPublicAPIUser(testUser, oldPassword)
	require.Error(pr.T(), err, "expected login to fail before the secret is repaired")
	require.Contains(pr.T(), err.Error(), "401", "expected 401 Unauthorized error, got: %v", err)

	err = webhookactions.RestoreRancherWebhook(pr.client, extclusterapi.LocalCluster, mutatingWebhookBackup, validatingWebhookBackup)
	require.NoError(pr.T(), err, "failed to restore rancher-webhook")

	newPassword, err := extuserapi.ChangePasswordForUser(pr.client, testUser.Username, "", 15)
	require.NoError(pr.T(), err, "failed to repair password via PasswordChangeRequest")

	repairedSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "failed to get repaired password secret for user %s", testUser.Username)

	hash, ok = repairedSecret.Annotations[extuserapi.PasswordHashAnnotation]
	require.True(pr.T(), ok, "password-hash annotation not found after repair")
	require.Equal(pr.T(), extuserapi.PasswordHash, hash, "password-hash value mismatch after repair")
	require.NotEmpty(pr.T(), repairedSecret.Data["salt"], "salt should be present after repair")
	require.NotEqual(pr.T(), []byte(oldPassword), repairedSecret.Data["password"], "password data should be hashed, not the plaintext")
	require.Len(pr.T(), repairedSecret.OwnerReferences, 1, "secret should have one ownerReference after repair")
	require.Equal(pr.T(), testUser.Username, repairedSecret.OwnerReferences[0].Name, "ownerReference does not match user after repair")

	_, err = pr.client.AsPublicAPIUser(testUser, newPassword)
	require.NoError(pr.T(), err, "user %s should be able to login with the new password", testUser.Username)

	_, err = pr.client.AsPublicAPIUser(testUser, oldPassword)
	require.Error(pr.T(), err, "expected login to fail with the old plaintext password")
	require.Contains(pr.T(), err.Error(), "401", "expected 401 Unauthorized error, got: %v", err)
}

func (pr *PasswordHashRepairTestSuite) TestAdminSetsPasswordForUserWithNoSecretViaPasswordChangeRequest() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an admin can set a password via PasswordChangeRequest for a user with no password secret at all, creating the secret, and that the user can then log in")

	mutatingWebhookBackup, validatingWebhookBackup, err := webhookactions.SimulateRancherWebhookDown(pr.client, extclusterapi.LocalCluster)
	require.NoError(pr.T(), err, "failed to simulate rancher-webhook being down")

	testUser, err := userapi.CreateUser(pr.client)
	require.NoError(pr.T(), err, "failed to create user")

	_, err = extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.Error(pr.T(), err, "expected no password secret to exist for user %s", testUser.Username)
	require.True(pr.T(), apierrors.IsNotFound(err), "expected NotFound error, got: %v", err)

	err = webhookactions.RestoreRancherWebhook(pr.client, extclusterapi.LocalCluster, mutatingWebhookBackup, validatingWebhookBackup)
	require.NoError(pr.T(), err, "failed to restore rancher-webhook")

	newPassword, err := extuserapi.ChangePasswordForUser(pr.client, testUser.Username, "", 15)
	require.NoError(pr.T(), err, "failed to set password via PasswordChangeRequest")

	createdSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "failed to get password secret for user %s", testUser.Username)

	hash, ok := createdSecret.Annotations[extuserapi.PasswordHashAnnotation]
	require.True(pr.T(), ok, "password-hash annotation not found on created secret")
	require.Equal(pr.T(), extuserapi.PasswordHash, hash, "password-hash value mismatch")
	require.NotEmpty(pr.T(), createdSecret.Data["salt"], "salt should be present on the created secret")
	require.NotEmpty(pr.T(), createdSecret.Data["password"], "password data should be present on the created secret")
	require.Len(pr.T(), createdSecret.OwnerReferences, 1, "secret should have one ownerReference")
	require.Equal(pr.T(), testUser.Username, createdSecret.OwnerReferences[0].Name, "ownerReference does not match user")

	_, err = pr.client.AsPublicAPIUser(testUser, newPassword)
	require.NoError(pr.T(), err, "user %s should be able to login with the new password", testUser.Username)

	_, err = pr.client.AsPublicAPIUser(testUser, "wrong-password")
	require.Error(pr.T(), err, "expected login to fail with an incorrect password")
	require.Contains(pr.T(), err.Error(), "401", "expected 401 Unauthorized error, got: %v", err)
}

func (pr *PasswordHashRepairTestSuite) TestAdminSetsPasswordForUserWithNoSecretViaSetPasswordAction() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an admin can set a password via the Norman setpassword action for a user with no password secret at all, creating the secret, and that the user can then log in")

	mutatingWebhookBackup, validatingWebhookBackup, err := webhookactions.SimulateRancherWebhookDown(pr.client, extclusterapi.LocalCluster)
	require.NoError(pr.T(), err, "failed to simulate rancher-webhook being down")

	testUser, err := userapi.CreateUser(pr.client)
	require.NoError(pr.T(), err, "failed to create user")

	_, err = extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.Error(pr.T(), err, "expected no password secret to exist for user %s", testUser.Username)
	require.True(pr.T(), apierrors.IsNotFound(err), "expected NotFound error, got: %v", err)

	err = webhookactions.RestoreRancherWebhook(pr.client, extclusterapi.LocalCluster, mutatingWebhookBackup, validatingWebhookBackup)
	require.NoError(pr.T(), err, "failed to restore rancher-webhook")

	mgmtUser, err := pr.client.Management.User.ByID(testUser.Name)
	require.NoError(pr.T(), err, "failed to fetch Norman user %s", testUser.Name)

	newPassword := password.GenerateUserPassword("FreshPass-")
	err = usersactions.UpdateUserPassword(pr.client, mgmtUser, newPassword)
	require.NoError(pr.T(), err, "failed to set password via setpassword action")

	createdSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "failed to get password secret for user %s", testUser.Username)

	hash, ok := createdSecret.Annotations[extuserapi.PasswordHashAnnotation]
	require.True(pr.T(), ok, "password-hash annotation not found on created secret")
	require.Equal(pr.T(), extuserapi.PasswordHash, hash, "password-hash value mismatch")
	require.NotEmpty(pr.T(), createdSecret.Data["salt"], "salt should be present on the created secret")
	require.NotEmpty(pr.T(), createdSecret.Data["password"], "password data should be present on the created secret")
	require.Len(pr.T(), createdSecret.OwnerReferences, 1, "secret should have one ownerReference")
	require.Equal(pr.T(), testUser.Username, createdSecret.OwnerReferences[0].Name, "ownerReference does not match user")

	_, err = pr.client.AsPublicAPIUser(testUser, newPassword)
	require.NoError(pr.T(), err, "user %s should be able to login with the new password", testUser.Username)
}

func (pr *PasswordHashRepairTestSuite) TestDeletingUserRemovesRepairedPasswordSecret() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that deleting a user removes a password secret that was repaired by an admin path")

	testUser, _ := pr.setupUnhashedPasswordSecret()

	_, err := extuserapi.ChangePasswordForUser(pr.client, testUser.Username, "", 15)
	require.NoError(pr.T(), err, "failed to repair password via PasswordChangeRequest")

	repairedSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "failed to get repaired password secret for user %s", testUser.Username)
	require.Len(pr.T(), repairedSecret.OwnerReferences, 1, "secret should have one ownerReference after repair")

	err = extuserapi.DeleteUser(pr.client, testUser.Username, true)
	require.NoError(pr.T(), err, "failed to delete user %s", testUser.Username)

	err = userapi.WaitForBackingSecretDeletion(pr.client, testUser.Username)
	require.NoError(pr.T(), err, "timed out waiting for backing secret deletion for user %s", testUser.Username)
}

func (pr *PasswordHashRepairTestSuite) TestPasswordChangeRequestRejectsUserWithNoUsername() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that PasswordChangeRequest rejects a user with no username and creates no password secret")

	name := namegen.AppendRandomString("no-username-user")
	noUsernameUser := &v3.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		DisplayName:  "External Auth Simulation",
		PrincipalIDs: []string{"github_user://12345"},
	}

	_, err := pr.client.WranglerContext.Mgmt.User().Create(noUsernameUser)
	require.NoError(pr.T(), err, "failed to create user with no username")

	_, err = extuserapi.ChangePasswordForUser(pr.client, name, "", 15)
	require.Error(pr.T(), err, "expected PasswordChangeRequest to be rejected for a user with no username")
	require.True(pr.T(), apierrors.IsBadRequest(err), "expected a 400 Bad Request error, got: %v", err)
	require.Contains(pr.T(), err.Error(), "has no username and cannot log in locally", "unexpected error message: %v", err)

	_, err = extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, name)
	require.Error(pr.T(), err, "expected no password secret to exist for %s", name)
	require.True(pr.T(), apierrors.IsNotFound(err), "expected NotFound error, got: %v", err)
}

func (pr *PasswordHashRepairTestSuite) TestAdminRepairPreservesOtherAnnotations() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that repairing a password secret preserves any other pre-existing annotations and only adds the password-hash annotation")

	mutatingWebhookBackup, validatingWebhookBackup, err := webhookactions.SimulateRancherWebhookDown(pr.client, extclusterapi.LocalCluster)
	require.NoError(pr.T(), err, "failed to simulate rancher-webhook being down")

	testUser, err := userapi.CreateUser(pr.client)
	require.NoError(pr.T(), err, "failed to create user")

	const customAnnotationKey = "example.io/keep-me"
	const customAnnotationValue = "do-not-remove"

	oldPassword := password.GenerateUserPassword("OldPlain-")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testUser.Username,
			Namespace: extuserapi.UserPasswordSecretNamespace,
			Annotations: map[string]string{
				customAnnotationKey: customAnnotationValue,
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"password": oldPassword,
		},
	}
	_, err = pr.client.WranglerContext.Core.Secret().Create(secret)
	require.NoError(pr.T(), err, "failed to create password secret with a custom annotation for user %s", testUser.Username)

	unhashedSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "failed to get password secret for user %s", testUser.Username)
	require.Equal(pr.T(), customAnnotationValue, unhashedSecret.Annotations[customAnnotationKey], "custom annotation should be present before repair")
	_, ok := unhashedSecret.Annotations[extuserapi.PasswordHashAnnotation]
	require.False(pr.T(), ok, "password-hash annotation should not be present on an unhashed secret")

	err = webhookactions.RestoreRancherWebhook(pr.client, extclusterapi.LocalCluster, mutatingWebhookBackup, validatingWebhookBackup)
	require.NoError(pr.T(), err, "failed to restore rancher-webhook")

	newPassword, err := extuserapi.ChangePasswordForUser(pr.client, testUser.Username, "", 15)
	require.NoError(pr.T(), err, "failed to repair password via PasswordChangeRequest")

	repairedSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "failed to get repaired password secret for user %s", testUser.Username)

	require.Equal(pr.T(), customAnnotationValue, repairedSecret.Annotations[customAnnotationKey], "custom annotation should be preserved after repair")
	hash, ok := repairedSecret.Annotations[extuserapi.PasswordHashAnnotation]
	require.True(pr.T(), ok, "password-hash annotation not found after repair")
	require.Equal(pr.T(), extuserapi.PasswordHash, hash, "password-hash value mismatch after repair")
	require.NotEmpty(pr.T(), repairedSecret.Data["salt"], "salt should be present after repair")
	require.NotEqual(pr.T(), []byte(oldPassword), repairedSecret.Data["password"], "password data should be hashed, not the plaintext")
	require.Len(pr.T(), repairedSecret.OwnerReferences, 1, "secret should have one ownerReference after repair")
	require.Equal(pr.T(), testUser.Username, repairedSecret.OwnerReferences[0].Name, "ownerReference does not match user after repair")

	_, err = pr.client.AsPublicAPIUser(testUser, newPassword)
	require.NoError(pr.T(), err, "user %s should be able to login with the new password", testUser.Username)
}

func (pr *PasswordHashRepairTestSuite) TestAdminChangePasswordOnAlreadyHashedSecretDoesNotDuplicateOwnerReference() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an admin password change on an already-hashed, already-owned secret keeps working and does not duplicate the ownerReference")

	testUser, firstPassword, err := userapi.CreateUserWithPasswordSecret(pr.client, 15)
	require.NoError(pr.T(), err, "failed to create user with password secret")

	initialSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "failed to get initial password secret for user %s", testUser.Username)
	hash, ok := initialSecret.Annotations[extuserapi.PasswordHashAnnotation]
	require.True(pr.T(), ok, "password-hash annotation not found on initial secret")
	require.Equal(pr.T(), extuserapi.PasswordHash, hash, "password-hash value mismatch on initial secret")
	require.Len(pr.T(), initialSecret.OwnerReferences, 1, "initial secret should have exactly one ownerReference")

	newPassword, err := extuserapi.ChangePasswordForUser(pr.client, testUser.Username, "", 15)
	require.NoError(pr.T(), err, "failed to change password via PasswordChangeRequest")

	changedSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "failed to get password secret for user %s after change", testUser.Username)

	hash, ok = changedSecret.Annotations[extuserapi.PasswordHashAnnotation]
	require.True(pr.T(), ok, "password-hash annotation not found after change")
	require.Equal(pr.T(), extuserapi.PasswordHash, hash, "password-hash value mismatch after change")
	require.NotEmpty(pr.T(), changedSecret.Data["salt"], "salt should be present after change")
	require.Len(pr.T(), changedSecret.OwnerReferences, 1, "secret should still have exactly one ownerReference after change, not duplicated")
	require.Equal(pr.T(), testUser.Username, changedSecret.OwnerReferences[0].Name, "ownerReference does not match user after change")

	_, err = pr.client.AsPublicAPIUser(testUser, newPassword)
	require.NoError(pr.T(), err, "user %s should be able to login with the new password", testUser.Username)

	_, err = pr.client.AsPublicAPIUser(testUser, firstPassword)
	require.Error(pr.T(), err, "expected login to fail with the old password")
	require.Contains(pr.T(), err.Error(), "401", "expected 401 Unauthorized error, got: %v", err)
}

func (pr *PasswordHashRepairTestSuite) TestUserChangesOwnPasswordWithCurrentPassword() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a user can change their own password by providing the correct current password")

	testUser, initialPassword, err := userapi.CreateUserWithRoles(pr.client, rbac.StandardUser.String())
	require.NoError(pr.T(), err, "failed to create user")

	userClient, err := pr.client.AsPublicAPIUser(testUser, initialPassword)
	require.NoError(pr.T(), err, "failed to login as user %s", testUser.Username)

	newPassword, err := extuserapi.ChangePasswordForUser(userClient, testUser.Username, initialPassword, 15)
	require.NoError(pr.T(), err, "failed to change own password with the current password")

	_, err = pr.client.AsPublicAPIUser(testUser, newPassword)
	require.NoError(pr.T(), err, "user %s should be able to login with the new password", testUser.Username)

	_, err = pr.client.AsPublicAPIUser(testUser, initialPassword)
	require.Error(pr.T(), err, "expected login to fail with the old password")
	require.Contains(pr.T(), err.Error(), "401", "expected 401 Unauthorized error, got: %v", err)
}

func (pr *PasswordHashRepairTestSuite) TestDeletingUserLeavesUnrepairedSecretOrphaned() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that deleting a user does not remove a password secret that was never repaired, since it has no ownerReference to the user")

	testUser, _ := pr.setupUnhashedPasswordSecret()

	unrepairedSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "failed to get password secret for user %s", testUser.Username)
	require.Empty(pr.T(), unrepairedSecret.OwnerReferences, "unrepaired secret should have no ownerReference")

	err = extuserapi.DeleteUser(pr.client, testUser.Username, true)
	require.NoError(pr.T(), err, "failed to delete user %s", testUser.Username)

	log.Infof("Verifying the unrepaired password secret for %s is left behind, since it was never linked to the user by an ownerReference", testUser.Username)
	orphanedSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "expected the unrepaired secret to still exist after the user is deleted")
	require.Equal(pr.T(), testUser.Username, orphanedSecret.Name, "unexpected secret returned")

	log.Infof("Cleaning up the orphaned secret for %s, since it is not removed automatically", testUser.Username)
	err = pr.client.WranglerContext.Core.Secret().Delete(extuserapi.UserPasswordSecretNamespace, testUser.Username, &metav1.DeleteOptions{})
	require.NoError(pr.T(), err, "failed to clean up orphaned secret for %s", testUser.Username)
}

func (pr *PasswordHashRepairTestSuite) TestAdminChangeRejectsUnsupportedHashingAlgorithm() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an admin password change fails for a secret with an unrecognized hashing algorithm annotation")

	// rancher-webhook treats any password-hash annotation other than the known algorithms as
	// unhashed and re-hashes it on admission, so it must be down while the secret is created,
	// otherwise the unsupported annotation would be silently overwritten before the change is attempted.
	mutatingWebhookBackup, validatingWebhookBackup, err := webhookactions.SimulateRancherWebhookDown(pr.client, extclusterapi.LocalCluster)
	require.NoError(pr.T(), err, "failed to simulate rancher-webhook being down")

	testUser, err := userapi.CreateUser(pr.client)
	require.NoError(pr.T(), err, "failed to create user")

	const placeholderData = "placeholder-not-a-real-hash"
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testUser.Username,
			Namespace: extuserapi.UserPasswordSecretNamespace,
			Annotations: map[string]string{
				extuserapi.PasswordHashAnnotation: "argon2",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"password": placeholderData,
		},
	}
	_, err = pr.client.WranglerContext.Core.Secret().Create(secret)
	require.NoError(pr.T(), err, "failed to create password secret with an unsupported hashing algorithm for user %s", testUser.Username)

	unhashedSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "failed to get password secret for user %s", testUser.Username)
	require.Equal(pr.T(), "argon2", unhashedSecret.Annotations[extuserapi.PasswordHashAnnotation], "password-hash annotation should still be the unsupported algorithm before the webhook is restored")

	err = webhookactions.RestoreRancherWebhook(pr.client, extclusterapi.LocalCluster, mutatingWebhookBackup, validatingWebhookBackup)
	require.NoError(pr.T(), err, "failed to restore rancher-webhook")

	_, err = extuserapi.ChangePasswordForUser(pr.client, testUser.Username, "", 15)
	require.Error(pr.T(), err, "expected the password change to be rejected for an unsupported hashing algorithm")
	require.Contains(pr.T(), err.Error(), `unsupported hashing algorithm "argon2"`, "unexpected error message: %v", err)

	log.Infof("Confirming the password secret for %s is unchanged after the failed attempt", testUser.Username)
	unchangedSecret, err := extsecretapi.GetSecretByName(pr.client, extclusterapi.LocalCluster, extuserapi.UserPasswordSecretNamespace, testUser.Username)
	require.NoError(pr.T(), err, "failed to get password secret for user %s", testUser.Username)
	require.Equal(pr.T(), "argon2", unchangedSecret.Annotations[extuserapi.PasswordHashAnnotation], "password-hash annotation should be unchanged after the failed attempt")
	require.Equal(pr.T(), []byte(placeholderData), unchangedSecret.Data["password"], "password data should be unchanged after the failed attempt")
}

func TestPasswordHashRepairTestSuite(t *testing.T) {
	suite.Run(t, new(PasswordHashRepairTestSuite))
}
