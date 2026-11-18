//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package keycloakoidc

import (
	"fmt"
	"slices"
	"strings"

	managementv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	authactions "github.com/rancher/tests/actions/auth"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	userapi "github.com/rancher/tests/actions/kubeapi/users"
	"github.com/rancher/tests/actions/rbac"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCAdminLogin() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	authenticatedUser, err := authAdmin.Management.User.ByID(authAdmin.UserID)
	require.NoError(k.T(), err, "Failed to retrieve the user the OpenID Connect session authenticated as")
	require.Contains(k.T(), authenticatedUser.PrincipalIDs, k.adminPrincipalID, "Session should resolve to the Keycloak principal")

	var localPrincipals []string
	for _, principalID := range authenticatedUser.PrincipalIDs {
		if strings.HasPrefix(principalID, authactions.LocalPrincipalPrefix) {
			localPrincipals = append(localPrincipals, principalID)
		}
	}
	require.NotEmpty(k.T(), localPrincipals, "Enabling should have attached the Keycloak identity to the existing Rancher administrator, so the same user should still hold its local principal")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCUnrestrictedAccessMode() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)
	require.NoError(k.T(), err, "Failed to update access mode")
	require.Equal(k.T(), authactions.AccessModeUnrestricted, newAuthConfig.AccessMode, "Access mode should be unrestricted")

	allUsers := slices.Concat(k.authConfig.Users, k.authConfig.ExcludedUsers)
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, allUsers, authactions.AccessModeUnrestricted+" access mode", true)
	require.NoError(k.T(), err, "Every Keycloak user should be able to login")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCRestrictedAccessModeAuthorizedUsersCanLogin() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	principalIDs, err := authactions.SetupExternalRequiredAccessModePrincipals(authAdmin, k.cluster.ID, k.authConfig, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup restricted access mode test")

	principalIDs = append(principalIDs, k.adminPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeRestricted, principalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	subSession.RegisterCleanupFunc(func() error {
		_, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)
		return err
	})
	require.Equal(k.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, k.authConfig.Users, authactions.AccessModeRestricted+" access mode", true)
	require.NoError(k.T(), err, "Members of the allowed group should be able to login")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCRestrictedAccessModeUnauthorizedLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(k.T(), k.authConfig.ExcludedUsers, "Keycloak OIDC auth input must list users outside the allowed group to prove they are turned away")

	principalIDs, err := authactions.SetupExternalRequiredAccessModePrincipals(authAdmin, k.cluster.ID, k.authConfig, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup restricted access mode test")

	principalIDs = append(principalIDs, k.adminPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeRestricted, principalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	subSession.RegisterCleanupFunc(func() error {
		_, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)
		return err
	})
	require.Equal(k.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, k.authConfig.ExcludedUsers, authactions.AccessModeRestricted+" access mode", false)
	require.NoError(k.T(), err, "Users outside the allowed group should NOT be able to login")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCRequiredAccessModeAuthorizedUsersCanLogin() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	principalIDs, err := authactions.SetupExternalRequiredAccessModePrincipals(authAdmin, k.cluster.ID, k.authConfig, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup required access mode test")

	principalIDs = append(principalIDs, k.adminPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeRequired, principalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	subSession.RegisterCleanupFunc(func() error {
		_, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)
		return err
	})
	require.Equal(k.T(), authactions.AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, k.authConfig.Users, authactions.AccessModeRequired+" access mode", true)
	require.NoError(k.T(), err, "Authorized users should be able to login")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCRequiredAccessModeUnauthorizedLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(k.T(), k.authConfig.ExcludedUsers, "Keycloak OIDC auth input must list users outside the allowed group to prove they are turned away")

	principalIDs, err := authactions.SetupExternalRequiredAccessModePrincipals(authAdmin, k.cluster.ID, k.authConfig, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup required access mode test")

	principalIDs = append(principalIDs, k.adminPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeRequired, principalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	subSession.RegisterCleanupFunc(func() error {
		_, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)
		return err
	})
	require.Equal(k.T(), authactions.AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, k.authConfig.ExcludedUsers, authactions.AccessModeRequired+" access mode", false)
	require.NoError(k.T(), err, "Unauthorized users should NOT be able to login")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCGroupClusterAccess() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.Group)

	logrus.Infof("Granting Keycloak group [%v] the %v role on cluster [%v]", k.authConfig.Group, rbac.ClusterOwner, k.cluster.ID)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, groupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(k.T(), err, "Failed to create cluster role template binding")

	for _, userInfo := range k.authConfig.Users {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakOIDC)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		rbac.VerifyUserCanListCluster(k.T(), k.client, userClient, k.cluster.ID, rbac.ClusterOwner)
	}

	foundCRTB, err := rbacapi.GetClusterRoleTemplateBindingsForGroup(k.client, groupPrincipalID, k.cluster.ID)
	require.NoError(k.T(), err, "Failed to get group CRTB")
	require.NotNil(k.T(), foundCRTB, "Cluster role binding should exist for group")

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(k.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCGroupProjectAccess() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	projectResp, _, err := projectapi.CreateProjectAndNamespace(authAdmin, k.cluster.ID)
	require.NoError(k.T(), err, "Failed to create project and namespace")

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.Group)

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}

	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	logrus.Infof("Granting Keycloak group [%v] the %v role on project [%v]", k.authConfig.Group, rbac.ProjectOwner, projectName)
	groupPRTBResp, err := rbacapi.CreateGroupProjectRoleTemplateBinding(authAdmin, projectName, prtbNamespace, groupPrincipalID, rbac.ProjectOwner.String())
	require.NoError(k.T(), err, "Failed to create PRTB")
	require.NotNil(k.T(), groupPRTBResp, "PRTB should be created")

	for _, userInfo := range k.authConfig.Users {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakOIDC)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.WranglerContext.Mgmt.Project().Get(projectResp.Namespace, projectResp.Name, metav1.GetOptions{})
		require.NoError(k.T(), err, "User [%v] should be able to get project %s because the token places them in group [%v]", userInfo.Username, projectResp.Name, k.authConfig.Group)
	}

	err = authAdmin.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Delete(groupPRTBResp.Namespace, groupPRTBResp.Name, &metav1.DeleteOptions{})
	require.NoError(k.T(), err, "Failed to delete PRTB: %s/%s", groupPRTBResp.Namespace, groupPRTBResp.Name)
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCNonMemberClusterAccessDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(k.T(), k.authConfig.ExcludedUsers, "Keycloak OIDC auth input must list users outside the allowed group to prove the group binding does not reach them")

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.Group)

	logrus.Infof("Granting Keycloak group [%v] the %v role on cluster [%v]", k.authConfig.Group, rbac.ClusterOwner, k.cluster.ID)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, groupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(k.T(), err, "Failed to create group cluster role template binding")

	for _, userInfo := range k.authConfig.ExcludedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakOIDC)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
		require.NotNil(k.T(), err, "User [%v] should NOT list clusters", userInfo.Username)
		require.Contains(k.T(), err.Error(), "Resource type [provisioning.cattle.io.cluster] has no method GET", "Should indicate insufficient permissions")
	}
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCRestrictedModeBindings() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.Group)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, groupPrincipalID, rbac.ClusterMember.String())
	require.NoError(k.T(), err, "Failed to create cluster role template binding")

	projectResp, _, err := projectapi.CreateProjectAndNamespace(authAdmin, k.cluster.ID)
	require.NoError(k.T(), err, "Failed to create project")

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}

	err = authactions.WaitForNamespaceReady(authAdmin, prtbNamespace)
	require.NoError(k.T(), err, "Namespace should be ready")

	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	for _, userInfo := range k.authConfig.Users {
		userPrincipalID := authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, userInfo)
		userPRTB := &managementv3.ProjectRoleTemplateBinding{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    prtbNamespace,
				GenerateName: "prtb-",
			},
			ProjectName:       projectName,
			UserPrincipalName: userPrincipalID,
			RoleTemplateName:  rbac.ProjectOwner.String(),
		}

		userPRTBResp, err := authAdmin.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Create(userPRTB)
		require.NoError(k.T(), err, "Failed to create PRTB for user [%v]", userInfo.Username)
		require.NotNil(k.T(), userPRTBResp, "PRTB should be created for user [%v]", userInfo.Username)
	}

	subSession.RegisterCleanupFunc(func() error {
		_, rollbackErr := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)
		return rollbackErr
	})

	allowedPrincipalIDs := []string{groupPrincipalID, k.adminPrincipalID}

	logrus.Infof("Restricting Keycloak OIDC to group [%v] and the administrator", k.authConfig.Group)
	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeRestricted, allowedPrincipalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	require.Equal(k.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, k.authConfig.Users, "restricted access mode holding cluster and project bindings", true)
	require.NoError(k.T(), err, "Members bound to the cluster and to the project should still be able to login once the provider is restricted to their group")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCAllowClusterAndProjectMembersAccessMode() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(k.T(), k.authConfig.ExcludedUsers, "Keycloak OIDC auth input must list a user outside the allowed group, since this test admits one on a project binding alone")

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.Group)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, groupPrincipalID, rbac.ClusterMember.String())
	require.NoError(k.T(), err, "Failed to create group cluster role template binding")

	projectResp, _, err := projectapi.CreateProjectAndNamespace(authAdmin, k.cluster.ID)
	require.NoError(k.T(), err, "Failed to create project")

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}

	err = authactions.WaitForNamespaceReady(authAdmin, prtbNamespace)
	require.NoError(k.T(), err, "Namespace should be ready")

	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	outsider := k.authConfig.ExcludedUsers[0]
	outsiderPrincipalID := authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, outsider)

	logrus.Infof("Signing user [%v] in while access is still unrestricted, so that a Rancher user record exists for them", outsider.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, []authactions.User{outsider}, "unrestricted access mode", true)
	require.NoError(k.T(), err, "User [%v] should be able to login before the provider is restricted; restricted access mode reads a user's bindings off their Rancher user record, which only a login creates", outsider.Username)

	_, err = userapi.WaitForUserByPrincipalID(k.client, outsiderPrincipalID)
	require.NoError(k.T(), err, "Login should have created a Rancher user record carrying principal [%v]", outsiderPrincipalID)

	logrus.Infof("Granting user [%v], who is outside group [%v], the %v role on project [%v]", outsider.Username, k.authConfig.Group, rbac.ProjectOwner, projectName)
	outsiderPRTB := &managementv3.ProjectRoleTemplateBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    prtbNamespace,
			GenerateName: "prtb-",
		},
		ProjectName:       projectName,
		UserPrincipalName: outsiderPrincipalID,
		RoleTemplateName:  rbac.ProjectOwner.String(),
	}
	outsiderPRTBResp, err := authAdmin.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Create(outsiderPRTB)
	require.NoError(k.T(), err, "Failed to create PRTB for user [%v]", outsider.Username)
	require.NotNil(k.T(), outsiderPRTBResp, "PRTB should be created for user [%v]", outsider.Username)

	subSession.RegisterCleanupFunc(func() error {
		_, rollbackErr := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)
		return rollbackErr
	})

	allowedPrincipalIDs := []string{groupPrincipalID, k.adminPrincipalID}

	logrus.Infof("Restricting Keycloak OIDC to group [%v] and the administrator, leaving user [%v] listed nowhere", k.authConfig.Group, outsider.Username)
	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeRestricted, allowedPrincipalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	require.Equal(k.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")
	require.ElementsMatch(k.T(), allowedPrincipalIDs, newAuthConfig.AllowedPrincipalIDs, "Allowed principals should be persisted exactly as sent")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, k.authConfig.Users, "restricted access mode as members of an allowed group", true)
	require.NoError(k.T(), err, "Members of the allowed group should be able to login")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, []authactions.User{outsider}, "restricted access mode as a project member only", true)
	require.NoError(k.T(), err, "Restricted access mode should admit user [%v] on their project binding alone, even though no principal of theirs is in the allow list", outsider.Username)
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCRequiredModeRevokedPrincipalLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	subSession.RegisterCleanupFunc(func() error {
		_, rollbackErr := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)
		return rollbackErr
	})

	grantedPrincipalIDs := []string{
		authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.Group),
		k.adminPrincipalID,
	}
	for _, user := range k.authConfig.Users {
		grantedPrincipalIDs = append(grantedPrincipalIDs, authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, user))
	}

	logrus.Infof("Granting Keycloak group [%v] and its members access in required mode", k.authConfig.Group)
	grantedConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeRequired, grantedPrincipalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	require.Equal(k.T(), authactions.AccessModeRequired, grantedConfig.AccessMode, "Access mode should be required")
	require.ElementsMatch(k.T(), grantedPrincipalIDs, grantedConfig.AllowedPrincipalIDs, "Granted principals should be persisted exactly as sent")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, k.authConfig.Users, "required access mode with principals granted", true)
	require.NoError(k.T(), err, "Granted users should be able to login")

	revokedPrincipalIDs := []string{k.adminPrincipalID}

	logrus.Infof("Revoking Keycloak group [%v] and its members, leaving only the administrator allowed", k.authConfig.Group)
	revokedConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeRequired, revokedPrincipalIDs)
	require.NoError(k.T(), err, "Failed to revoke principals")
	require.Equal(k.T(), authactions.AccessModeRequired, revokedConfig.AccessMode, "Access mode should remain required")
	require.ElementsMatch(k.T(), revokedPrincipalIDs, revokedConfig.AllowedPrincipalIDs, "Revoked principals should no longer appear in the allow list")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, k.authConfig.Users, "required access mode after principals revoked", false)
	require.NoError(k.T(), err, "Revoked users should NOT be able to login")

	stillAllowed := []authactions.User{{Username: k.adminUser.Username, Password: k.adminUser.Password}}
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, stillAllowed, "required access mode after principals revoked", true)
	require.NoError(k.T(), err, "The administrator, whose principal was left in the allow list, should still be able to login")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCPrincipalSearchFindsLocalUserByDisplayName() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	logrus.Info("Creating a local user whose display name differs from its username")
	localUser, err := userapi.CreateUser(authAdmin)
	require.NoError(k.T(), err, "Failed to create local user")
	require.NotEqual(k.T(), localUser.Username, localUser.DisplayName, "Local user display name must differ from its username for this search to be meaningful")

	logrus.Infof("Searching principals for local user by username [%v]", localUser.Username)
	err = authactions.VerifyPrincipalIsLocal(k.client, localUser.Username)
	require.NoError(k.T(), err, "Local user [%v] should be findable by username while Keycloak OIDC is enabled", localUser.Username)

	logrus.Infof("Searching principals for local user by display name [%v]", localUser.DisplayName)
	err = authactions.VerifyPrincipalIsLocal(k.client, localUser.DisplayName)
	require.NoError(k.T(), err, "Local user [%v] should be findable by display name [%v]", localUser.Username, localUser.DisplayName)
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCLoginAttachesExternalPrincipal() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	keycloakUser := k.authConfig.Users[0]
	userPrincipalID := authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, keycloakUser)

	logrus.Infof("Logging in as Keycloak user %s so that the login flow attaches its external principal", keycloakUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, []authactions.User{keycloakUser}, "external principal attachment", true)
	require.NoError(k.T(), err, "Keycloak user should be able to login")

	logrus.Infof("Verifying that a user record carrying the external principal %s exists", userPrincipalID)
	attachedUser, err := userapi.WaitForUserByPrincipalID(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Login should attach the external principal to a user record")
	require.Contains(k.T(), attachedUser.PrincipalIDs, userPrincipalID, "User record should carry the external principal")
	require.Contains(k.T(), attachedUser.PrincipalIDs, authactions.LocalPrincipalPrefix+attachedUser.Name, "User record should also carry its local principal")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCDisableRemovesExternalPrincipals() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	keycloakUser := k.authConfig.Users[0]
	userPrincipalID := authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, keycloakUser)

	logrus.Infof("Logging in as Keycloak user %s so that a user record carrying its external principal exists", keycloakUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, []authactions.User{keycloakUser}, "external principal attachment", true)
	require.NoError(k.T(), err, "Keycloak user should be able to login")

	attachedUser, err := userapi.WaitForUserByPrincipalID(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Login should attach the external principal to a user record")
	require.ElementsMatch(k.T(), []string{userPrincipalID, authactions.LocalPrincipalPrefix + attachedUser.Name}, attachedUser.PrincipalIDs,
		"User %s should carry only the Keycloak principal and the local principal added alongside it, so that disabling the provider leaves it with no external identity", attachedUser.Name)

	logrus.Info("Disabling Keycloak OIDC so that the cleanup service reconciles principals it emitted")
	err = k.client.Auth.KeycloakOIDC.Disable()
	require.NoError(k.T(), err, "Failed to disable Keycloak OIDC")

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling Keycloak OIDC so that a failure here does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakOIDC)
	})

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(k.client, authactions.KeycloakOIDC, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(k.T(), err, "Failed waiting for annotation update")

	logrus.Infof("Verifying that no user record still carries the external principal %s", userPrincipalID)
	err = userapi.WaitForUserByPrincipalIDDeletion(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Disabling the provider should leave no user carrying a Keycloak principal")

	logrus.Infof("Verifying that user %s was removed rather than left behind holding only its local principal", attachedUser.Name)
	err = userapi.WaitForUserDeletion(k.client, attachedUser.Name)
	require.NoError(k.T(), err, "Disabling the provider should remove a user whose only external identity that provider emitted, leaving no record that could later be rebound")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCParentGroupBindingDoesNotReachNestedMembers() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.Group)

	logrus.Infof("Granting Keycloak group [%v] the %v role on cluster [%v]", k.authConfig.Group, rbac.ClusterOwner, k.cluster.ID)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, groupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(k.T(), err, "Failed to create cluster role template binding")

	directMember := &v3.User{
		Username: k.authConfig.Users[0].Username,
		Password: k.authConfig.Users[0].Password,
	}
	directClient, err := authactions.LoginAsAuthUser(authAdmin, directMember, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to login user [%v]", directMember.Username)

	rbac.VerifyUserCanListCluster(k.T(), k.client, directClient, k.cluster.ID, rbac.ClusterOwner)

	nestedMembers := slices.Concat(k.authConfig.NestedUsers, k.authConfig.DoubleNestedUsers)

	for _, userInfo := range nestedMembers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakOIDC)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
		require.Error(k.T(), err, "User [%v] sits beneath group [%v] rather than in it, and the groups claim names only their direct group, so the binding on [%v] must not reach them", user.Username, k.authConfig.Group, groupPrincipalID)
		require.Contains(k.T(), err.Error(), "Resource type [provisioning.cattle.io.cluster] has no method GET", "Should indicate insufficient permissions")
	}

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(k.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCNestedGroupBindingGrantsItsOwnMembers() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	nestedPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.NestedGroup)

	logrus.Infof("Granting nested Keycloak group [%v] the %v role on cluster [%v]", k.authConfig.NestedGroup, rbac.ClusterOwner, k.cluster.ID)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, nestedPrincipalID, rbac.ClusterOwner.String())
	require.NoError(k.T(), err, "A nested group should be bindable by its own name, since Rancher takes group names off the token and never asks Keycloak where they sit")

	for _, userInfo := range k.authConfig.NestedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakOIDC)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		rbac.VerifyUserCanListCluster(k.T(), k.client, userClient, k.cluster.ID, rbac.ClusterOwner)
	}

	for _, userInfo := range k.authConfig.DoubleNestedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakOIDC)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
		require.Error(k.T(), err, "User [%v] belongs to group [%v] beneath [%v], and a nested group is as flat to Rancher as any other, so the binding must not descend to them either", user.Username, k.authConfig.DoubleNestedGroup, k.authConfig.NestedGroup)
		require.Contains(k.T(), err.Error(), "Resource type [provisioning.cattle.io.cluster] has no method GET", "Should indicate insufficient permissions")
	}

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(k.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCFullGroupPathClaimGrantsAncestors() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	logrus.Infof("Asking Keycloak to name each group by its full path in the %v claim", authactions.KeycloakOIDCFullGroupPathClaim)
	restoreGroupClaim, err := authactions.SetKeycloakOIDCGroupClaim(k.client, k.keycloak, k.clientID, authactions.KeycloakOIDCFullGroupPathClaim, true)
	subSession.RegisterCleanupFunc(restoreGroupClaim)
	require.NoError(k.T(), err, "Failed to switch the Keycloak groups mapper to full paths")

	nestedUser := k.authConfig.NestedUsers[0]

	claimedPaths, err := authactions.KeycloakOIDCClaimValues(k.client, nestedUser, authactions.KeycloakOIDCFullGroupPathClaim)
	require.NoError(k.T(), err, "Failed to capture the token Keycloak issues for user [%v]", nestedUser.Username)

	nestedPath := fmt.Sprintf("/%s/%s", k.authConfig.Group, k.authConfig.NestedGroup)
	require.Contains(k.T(), claimedPaths, nestedPath, "Keycloak should name group [%v] by the path [%v] once the mapper emits full paths", k.authConfig.NestedGroup, nestedPath)

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.Group)

	logrus.Infof("Granting the parent Keycloak group [%v] the %v role on cluster [%v]", k.authConfig.Group, rbac.ClusterOwner, k.cluster.ID)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, groupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(k.T(), err, "Failed to create cluster role template binding")

	nestedMember := &v3.User{
		Username: nestedUser.Username,
		Password: nestedUser.Password,
	}
	nestedClient, err := authactions.LoginAsAuthUser(authAdmin, nestedMember, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to login user [%v]", nestedUser.Username)

	rbac.VerifyUserCanListCluster(k.T(), k.client, nestedClient, k.cluster.ID, rbac.ClusterOwner)

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(k.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}
