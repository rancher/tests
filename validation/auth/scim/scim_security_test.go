//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package scim

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	scimclient "github.com/rancher/shepherd/clients/rancher/auth/scim"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	extauthz "github.com/rancher/shepherd/extensions/kubeapi/authorization"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extfeatures "github.com/rancher/shepherd/extensions/kubeapi/features"
	"github.com/rancher/shepherd/extensions/kubeapi/namespaces"
	extsecretapi "github.com/rancher/shepherd/extensions/kubeapi/secrets"
	"github.com/rancher/shepherd/extensions/users"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	rancherserverapi "github.com/rancher/tests/actions/kubeapi/rancherserver"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	scimactions "github.com/rancher/tests/actions/kubeapi/scim"
	usersapi "github.com/rancher/tests/actions/kubeapi/users"
	"github.com/rancher/tests/actions/rbac"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	scimStandardUserTokenIssue   = "Known issue: https://github.com/rancher/rancher-security/issues/2570"
	scimCrossProviderMemberIssue = "Known issue: https://github.com/rancher/rancher-security/issues/2573"
	scimProviderOwnershipIssue   = "Known issue: https://github.com/rancher/rancher-security/issues/2574"

	scimUnknownProvider       = "no-such-provider"
	scimInvalidToken          = "invalid-token"
	scimShortTokenExpiry      = "10s"
	scimSecretsResource       = "secrets"
	scimCreateVerb            = "create"
	scimGetVerb               = "get"
	scimLegacyTokenLogMessage = "Deleted SCIM token secret"
	scimListVerb              = "list"
	scimHijackedUserPrefix    = "scim-hijacked-user"
	scimHijackedGroupPrefix   = "scim-hijacked-group"
	scimForeignProvider       = "azuread"
)

type SCIMSecurityTestSuite struct {
	suite.Suite
	session           *session.Session
	client            *rancher.Client
	cluster           *v3.Cluster
	scimClient        *scimclient.Client
	tokenNamespaceOn  bool
	providerScopingOn bool
}

func (s *SCIMSecurityTestSuite) SetupSuite() {
	s.session = session.NewSession()

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err, "Failed to create Rancher client")
	s.client = client

	logrus.Info("Getting cluster name from the config file")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(s.T(), clusterName, "Cluster name should be set")

	clusterID, err := clusters.GetClusterIDByName(s.client, clusterName)
	require.NoError(s.T(), err, "Error getting cluster ID for cluster: %s", clusterName)

	s.cluster, err = s.client.Management.Cluster.ByID(clusterID)
	require.NoError(s.T(), err, "Failed to retrieve cluster by ID: %s", clusterID)

	logrus.Info("Setting up SCIM client for OpenLDAP provider")
	scimClient, err := scimactions.SetupSCIMClient(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to setup SCIM client")
	s.scimClient = scimClient

	require.NoError(s.T(), scimactions.VerifyExpectedBuildType(s.client), "The Rancher build type should match the one the config expects")

	servable, reason, err := scimactions.VerifySCIMServable(s.client)
	require.NoError(s.T(), err, "Failed to read whether this Rancher server serves SCIM")
	if !servable {
		s.T().Skip(reason)
	}

	logrus.Info("Ensuring baseline SCIM ConfigMap (enabled=true) for OpenLDAP provider")
	require.NoError(s.T(), scimactions.SetSCIMConfigMap(s.client, authactions.OpenLdap, scimactions.BaselineSCIMConfigMap()))
	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(s.scimClient, http.StatusOK))

	logrus.Infof("Checking whether this Rancher server reads SCIM tokens only from %s", scimactions.SCIMTokenSecretNamespace)
	s.tokenNamespaceOn, err = scimactions.VerifySCIMTokenNamespaceEnforced(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to read which namespace this Rancher server accepts SCIM tokens from")
	logrus.Infof("The Rancher server reads SCIM tokens only from %s: %t", scimactions.SCIMTokenSecretNamespace, s.tokenNamespaceOn)

	logrus.Info("Checking whether this Rancher server limits SCIM requests to the provider's own users and groups")
	s.providerScopingOn, err = scimactions.VerifySCIMProviderScopingEnforced(s.client, s.scimClient, authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to read whether this Rancher server scopes SCIM requests to the provider")
	logrus.Infof("The Rancher server limits SCIM requests to the provider that owns the object: %t", s.providerScopingOn)
}

func (s *SCIMSecurityTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *SCIMSecurityTestSuite) TestSCIMTokenInGlobalDataNamespaceIsRejected() {
	if !s.tokenNamespaceOn {
		s.T().Skip(scimStandardUserTokenIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying a SCIM token secret in %s no longer authenticates", scimactions.SCIMSecretNamespace)

	secretName := namegen.AppendRandomString("scim-legacy-token")
	_, token, err := scimactions.CreateSCIMTokenSecretInNamespace(s.client, authactions.OpenLdap, scimactions.SCIMSecretNamespace, secretName)
	require.NoError(s.T(), err)
	subSession.RegisterCleanupFunc(func() error {
		return extsecretapi.DeleteSecret(s.client, extclusterapi.LocalCluster, scimactions.SCIMSecretNamespace, secretName, false)
	})

	legacyClient := scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, authactions.OpenLdap, token)
	resp, err := legacyClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusUnauthorized, fmt.Sprintf("A token secret in %s should not authenticate", scimactions.SCIMSecretNamespace)))
}

func (s *SCIMSecurityTestSuite) TestSCIMTokenInTokenNamespaceAuthenticates() {
	if !s.tokenNamespaceOn {
		s.T().Skip(scimStandardUserTokenIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying a SCIM token secret in %s authenticates", scimactions.SCIMTokenSecretNamespace)

	secretName := namegen.AppendRandomString("scim-token")
	_, token, err := scimactions.CreateSCIMTokenSecretInNamespace(s.client, authactions.OpenLdap, scimactions.SCIMTokenSecretNamespace, secretName)
	require.NoError(s.T(), err)
	subSession.RegisterCleanupFunc(func() error {
		return extsecretapi.DeleteSecret(s.client, extclusterapi.LocalCluster, scimactions.SCIMTokenSecretNamespace, secretName, false)
	})

	tokenClient := scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, authactions.OpenLdap, token)
	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(tokenClient, http.StatusOK), "A token secret in %s should authenticate", scimactions.SCIMTokenSecretNamespace)
}

func (s *SCIMSecurityTestSuite) TestSCIMStandardUserCannotCreateTokenSecret() {
	if !s.tokenNamespaceOn {
		s.T().Skip(scimStandardUserTokenIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying a standard user cannot create a secret in %s", scimactions.SCIMTokenSecretNamespace)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	standardUser, err := users.CreateUserWithRole(subClient, users.UserConfig(), string(rbac.StandardUser))
	require.NoError(s.T(), err, "Should be able to create a standard user")

	standardUserClient, err := subClient.AsUser(standardUser)
	require.NoError(s.T(), err, "Standard user %s should be able to login", standardUser.Name)

	allowed, err := extauthz.IsAllowed(standardUserClient, extclusterapi.LocalCluster, &authzv1.ResourceAttributes{
		Namespace: scimactions.SCIMTokenSecretNamespace,
		Verb:      scimCreateVerb,
		Resource:  scimSecretsResource,
	})
	require.NoError(s.T(), err, "Should be able to ask whether the standard user may create secrets in %s", scimactions.SCIMTokenSecretNamespace)
	require.False(s.T(), allowed, "A standard user should not be allowed to create secrets in %s", scimactions.SCIMTokenSecretNamespace)

	secretName := namegen.AppendRandomString("scim-token")
	_, _, err = scimactions.CreateSCIMTokenSecretInNamespace(standardUserClient, authactions.OpenLdap, scimactions.SCIMTokenSecretNamespace, secretName)
	require.Error(s.T(), err, "A standard user should not be able to create a SCIM token secret in %s", scimactions.SCIMTokenSecretNamespace)
}

func (s *SCIMSecurityTestSuite) TestSCIMStandardUserTokenIsRejected() {
	if !s.tokenNamespaceOn {
		s.T().Skip(scimStandardUserTokenIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying a SCIM token a standard user plants in %s does not authenticate", scimactions.SCIMSecretNamespace)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	standardUser, err := users.CreateUserWithRole(subClient, users.UserConfig(), string(rbac.StandardUser))
	require.NoError(s.T(), err, "Should be able to create a standard user")

	standardUserClient, err := subClient.AsUser(standardUser)
	require.NoError(s.T(), err, "Standard user %s should be able to login", standardUser.Name)

	secretName := namegen.AppendRandomString("scim-planted-token")
	_, token, err := scimactions.CreateSCIMTokenSecretInNamespace(standardUserClient, authactions.OpenLdap, scimactions.SCIMSecretNamespace, secretName)
	require.NoError(s.T(), err, "The %s global role still grants create on secrets in %s", rbac.StandardUser, scimactions.SCIMSecretNamespace)
	subSession.RegisterCleanupFunc(func() error {
		return extsecretapi.DeleteSecret(s.client, extclusterapi.LocalCluster, scimactions.SCIMSecretNamespace, secretName, false)
	})

	plantedClient := scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, authactions.OpenLdap, token)
	resp, err := plantedClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusUnauthorized, "A token a standard user planted should not reach the SCIM endpoints"))
}

func (s *SCIMSecurityTestSuite) TestSCIMStandardUserCannotReadTokenSecrets() {
	if !s.tokenNamespaceOn {
		s.T().Skip(scimStandardUserTokenIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying a standard user cannot read the secrets in %s", scimactions.SCIMTokenSecretNamespace)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	standardUser, err := users.CreateUserWithRole(subClient, users.UserConfig(), string(rbac.StandardUser))
	require.NoError(s.T(), err, "Should be able to create a standard user")

	standardUserClient, err := subClient.AsUser(standardUser)
	require.NoError(s.T(), err, "Standard user %s should be able to login", standardUser.Name)

	for _, verb := range []string{scimGetVerb, scimListVerb} {
		allowed, err := extauthz.IsAllowed(standardUserClient, extclusterapi.LocalCluster, &authzv1.ResourceAttributes{
			Namespace: scimactions.SCIMTokenSecretNamespace,
			Verb:      verb,
			Resource:  scimSecretsResource,
		})
		require.NoError(s.T(), err, "Should be able to ask whether the standard user may %s secrets in %s", verb, scimactions.SCIMTokenSecretNamespace)
		require.False(s.T(), allowed, "A standard user should not be allowed to %s secrets in %s", verb, scimactions.SCIMTokenSecretNamespace)
	}
}

func (s *SCIMSecurityTestSuite) TestSCIMTokenInOtherNamespaceIsRejected() {
	if !s.tokenNamespaceOn {
		s.T().Skip(scimStandardUserTokenIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying a correctly labelled SCIM token outside %s does not authenticate", scimactions.SCIMTokenSecretNamespace)

	namespaceName := namegen.AppendRandomString("scim-token-ns")
	_, err := namespaces.CreateNamespace(s.client, extclusterapi.LocalCluster, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName}})
	require.NoError(s.T(), err, "Should be able to create namespace %s", namespaceName)
	subSession.RegisterCleanupFunc(func() error {
		return s.client.WranglerContext.Core.Namespace().Delete(namespaceName, &metav1.DeleteOptions{})
	})

	secretName := namegen.AppendRandomString("scim-token")
	_, token, err := scimactions.CreateSCIMTokenSecretInNamespace(s.client, authactions.OpenLdap, namespaceName, secretName)
	require.NoError(s.T(), err)

	strayClient := scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, authactions.OpenLdap, token)
	resp, err := strayClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusUnauthorized, fmt.Sprintf("A token secret outside %s should not authenticate", scimactions.SCIMTokenSecretNamespace)))
}

func (s *SCIMSecurityTestSuite) TestSCIMTokenRotationInTokenNamespace() {
	if !s.tokenNamespaceOn {
		s.T().Skip(scimStandardUserTokenIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying two SCIM tokens in %s authenticate and that deleting one leaves the other working", scimactions.SCIMTokenSecretNamespace)

	secondName := namegen.AppendRandomString("scim-token-rotated")
	_, secondToken, err := scimactions.CreateSCIMTokenSecretInNamespace(s.client, authactions.OpenLdap, scimactions.SCIMTokenSecretNamespace, secondName)
	require.NoError(s.T(), err)
	subSession.RegisterCleanupFunc(func() error {
		return extsecretapi.DeleteSecret(s.client, extclusterapi.LocalCluster, scimactions.SCIMTokenSecretNamespace, secondName, false)
	})

	rotatedClient := scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, authactions.OpenLdap, secondToken)
	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(rotatedClient, http.StatusOK), "The rotated token should authenticate")
	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(s.scimClient, http.StatusOK), "The original token should keep working while both exist")

	require.NoError(s.T(), extsecretapi.DeleteSecret(s.client, extclusterapi.LocalCluster, scimactions.SCIMTokenSecretNamespace, secondName, false))
	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(rotatedClient, http.StatusUnauthorized), "The deleted token should stop authenticating")
}

func (s *SCIMSecurityTestSuite) TestSCIMZExpiredTokenIsRejectedAndRemoved() {
	if !s.tokenNamespaceOn {
		s.T().Skip(scimStandardUserTokenIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying an expired SCIM token in %s stops authenticating and is removed", scimactions.SCIMTokenSecretNamespace)

	secretName := namegen.AppendRandomString("scim-expiring-token")
	_, token, err := scimactions.CreateSCIMTokenSecretInNamespace(s.client, authactions.OpenLdap, scimactions.SCIMTokenSecretNamespace, secretName)
	require.NoError(s.T(), err)
	subSession.RegisterCleanupFunc(func() error {
		return extsecretapi.DeleteSecret(s.client, extclusterapi.LocalCluster, scimactions.SCIMTokenSecretNamespace, secretName, false)
	})

	expiringClient := scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, authactions.OpenLdap, token)
	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(expiringClient, http.StatusOK), "The new token should authenticate before it expires")

	restoreExpiry, err := scimactions.SetSCIMTokenExpiry(s.client, scimShortTokenExpiry)
	require.NoError(s.T(), err)
	subSession.RegisterCleanupFunc(func() error {
		if err := restoreExpiry(); err != nil {
			return err
		}

		scimClient, err := scimactions.SetupSCIMClient(s.client, authactions.OpenLdap)
		if err != nil {
			return err
		}
		s.scimClient = scimClient

		return nil
	})

	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(expiringClient, http.StatusUnauthorized), "An expired token should stop authenticating")
	require.NoError(s.T(), extsecretapi.WaitForSecretDeletion(s.client, extclusterapi.LocalCluster, scimactions.SCIMTokenSecretNamespace, secretName), "An expired token secret should be removed")
}

func (s *SCIMSecurityTestSuite) TestSCIMZProviderDisableRemovesTokenFromTokenNamespace() {
	if !s.tokenNamespaceOn {
		s.T().Skip(scimStandardUserTokenIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying disabling the auth provider removes its SCIM tokens from %s", scimactions.SCIMTokenSecretNamespace)

	secrets, err := scimactions.ListSCIMTokenSecrets(s.client, authactions.OpenLdap, scimactions.SCIMTokenSecretNamespace)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), secrets, "The provider should hold at least one token secret in %s before it is disabled", scimactions.SCIMTokenSecretNamespace)

	subSession.RegisterCleanupFunc(func() error {
		scimClient, err := scimactions.SetupSCIMClient(s.client, authactions.OpenLdap)
		if err != nil {
			return err
		}
		s.scimClient = scimClient

		return scimactions.WaitForSCIMEndpointStatus(s.scimClient, http.StatusOK)
	})

	logrus.Info("Disabling the OpenLDAP auth provider")
	require.NoError(s.T(), s.client.Auth.OLDAP.Disable())

	for _, secret := range secrets {
		require.NoError(s.T(), extsecretapi.WaitForSecretDeletion(s.client, extclusterapi.LocalCluster, scimactions.SCIMTokenSecretNamespace, secret.Name), "Provider cleanup should remove the token secret %s/%s", scimactions.SCIMTokenSecretNamespace, secret.Name)
	}
}

func (s *SCIMSecurityTestSuite) TestSCIMGroupRejectsMemberWithoutProviderPrincipal() {
	if !s.providerScopingOn {
		s.T().Skip(scimCrossProviderMemberIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying a SCIM group does not accept a local user as a member")

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	localUser, err := users.CreateUserWithRole(subClient, users.UserConfig(), string(rbac.StandardUser))
	require.NoError(s.T(), err, "Should be able to create a local user")

	_, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	patchResp, err := scimactions.PatchGroup(s.scimClient, groupID, "add", "members", []scimclient.Member{{Value: localUser.ID}})
	require.NoError(s.T(), err)
	require.NotEqual(s.T(), http.StatusOK, patchResp.StatusCode, "Adding a user with no %s principal should be rejected, got: %s", authactions.OpenLdap, string(patchResp.Body))
	require.GreaterOrEqual(s.T(), patchResp.StatusCode, http.StatusBadRequest, "Adding a user with no %s principal should answer a client error", authactions.OpenLdap)

	principals, err := usersapi.GetUserGroupPrincipals(s.client, localUser.ID, authactions.OpenLdap)
	require.NoError(s.T(), err)
	require.Empty(s.T(), principals, "A local user should hold no %s group principals", authactions.OpenLdap)
}

func (s *SCIMSecurityTestSuite) TestSCIMGroupUpdateRejectsMemberWithoutProviderPrincipal() {
	if !s.providerScopingOn {
		s.T().Skip(scimCrossProviderMemberIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying PUT /Groups refuses a member with no %s principal and writes nothing", authactions.OpenLdap)

	groupName, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	localUser, err := users.CreateUserWithRole(subClient, users.UserConfig(), string(rbac.StandardUser))
	require.NoError(s.T(), err, "Should be able to create a local user")

	putResp, err := s.scimClient.Groups().Update(groupID, scimclient.Group{
		Schemas:     []string{scimclient.SCIMSchemaGroup},
		ID:          groupID,
		DisplayName: groupName,
		Members:     []scimclient.Member{{Value: localUser.ID}},
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(putResp, http.StatusNotFound,
		fmt.Sprintf("PUT /Groups with a member holding no %s principal should answer 404", authactions.OpenLdap)))

	require.NoError(s.T(), scimactions.WaitForGroupMemberCount(s.scimClient, groupID, 0),
		"The refused PUT should have written no member to the group")

	principals, err := usersapi.GetUserGroupPrincipals(s.client, localUser.ID, authactions.OpenLdap)
	require.NoError(s.T(), err)
	require.Empty(s.T(), principals, "A local user should hold no %s group principals after a refused PUT", authactions.OpenLdap)
}

func (s *SCIMSecurityTestSuite) TestSCIMGroupCreateRejectsMemberWithoutProviderPrincipal() {
	if !s.providerScopingOn {
		s.T().Skip(scimCrossProviderMemberIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying POST /Groups refuses an inline member with no %s principal", authactions.OpenLdap)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	localUser, err := users.CreateUserWithRole(subClient, users.UserConfig(), string(rbac.StandardUser))
	require.NoError(s.T(), err, "Should be able to create a local user")

	createResp, err := s.scimClient.Groups().Create(scimclient.Group{
		Schemas:     []string{scimclient.SCIMSchemaGroup},
		DisplayName: namegen.AppendRandomString("scim-group"),
		Members:     []scimclient.Member{{Value: localUser.ID}},
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(createResp, http.StatusNotFound,
		fmt.Sprintf("POST /Groups with an inline member holding no %s principal should answer 404", authactions.OpenLdap)))

	principals, err := usersapi.GetUserGroupPrincipals(s.client, localUser.ID, authactions.OpenLdap)
	require.NoError(s.T(), err)
	require.Empty(s.T(), principals, "A local user should hold no %s group principals after a refused create", authactions.OpenLdap)
}

func (s *SCIMSecurityTestSuite) TestSCIMGroupMembershipDoesNotGrantLocalUserProviderRoles() {
	if !s.providerScopingOn {
		s.T().Skip(scimCrossProviderMemberIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying a SCIM group membership cannot hand a local user the roles bound to that group")

	groupName, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	groupPrincipal := fmt.Sprintf("%s_%s://%s", authactions.OpenLdap, authactions.PrincipalTypeGroup, groupName)
	logrus.Infof("Binding %s to the cluster as %s", groupPrincipal, rbac.ClusterMember)

	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(s.client, s.cluster.ID, groupPrincipal, string(rbac.ClusterMember))
	require.NoError(s.T(), err, "Should be able to create the group CRTB")
	require.NotEmpty(s.T(), crtb.Name)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	localUser, err := users.CreateUserWithRole(subClient, users.UserConfig(), string(rbac.StandardUser))
	require.NoError(s.T(), err, "Should be able to create a local user")

	patchResp, err := scimactions.PatchGroup(s.scimClient, groupID, "add", "members", []scimclient.Member{{Value: localUser.ID}})
	require.NoError(s.T(), err)
	require.NotEqual(s.T(), http.StatusOK, patchResp.StatusCode, "Adding a local user to a SCIM group should be rejected, got: %s", string(patchResp.Body))

	localUserClient, err := subClient.AsUser(localUser)
	require.NoError(s.T(), err, "Local user %s should be able to login", localUser.Name)

	_, err = localUserClient.Management.Cluster.ByID(s.cluster.ID)
	require.Error(s.T(), err, "A local user added to a SCIM group should not reach the cluster bound to that group")
}

func (s *SCIMSecurityTestSuite) TestSCIMLoginIgnoresGroupPrincipalsOfAnotherProvider() {
	if !s.providerScopingOn {
		s.T().Skip(scimCrossProviderMemberIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying a local login does not pick up the %s group principals stored on the user", authactions.OpenLdap)

	groupName, _, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	groupPrincipal := fmt.Sprintf("%s_%s://%s", authactions.OpenLdap, authactions.PrincipalTypeGroup, groupName)
	logrus.Infof("Binding %s to the cluster as %s", groupPrincipal, rbac.ClusterMember)

	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(s.client, s.cluster.ID, groupPrincipal, string(rbac.ClusterMember))
	require.NoError(s.T(), err, "Should be able to create the group CRTB")
	require.NotEmpty(s.T(), crtb.Name)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	localUser, err := users.CreateUserWithRole(subClient, users.UserConfig(), string(rbac.StandardUser))
	require.NoError(s.T(), err, "Should be able to create a local user")

	require.NoError(s.T(), usersapi.SetUserGroupPrincipals(s.client, localUser.ID, authactions.OpenLdap, []string{groupPrincipal}),
		"Should be able to store a %s group principal on the local user, as a SCIM write would", authactions.OpenLdap)

	stored, err := usersapi.GetUserGroupPrincipals(s.client, localUser.ID, authactions.OpenLdap)
	require.NoError(s.T(), err)
	require.Contains(s.T(), stored, groupPrincipal, "The %s group principal should be stored before the login", authactions.OpenLdap)

	localUserClient, err := subClient.AsUser(localUser)
	require.NoError(s.T(), err, "Local user %s should be able to login", localUser.Name)

	_, err = localUserClient.Management.Cluster.ByID(s.cluster.ID)
	require.Error(s.T(), err, "A password login should not carry the %s group principals stored on the user, so it should not reach the cluster bound to %s", authactions.OpenLdap, groupPrincipal)
}

func (s *SCIMSecurityTestSuite) TestSCIMUserByIDRejectsNonProviderUser() {
	if !s.providerScopingOn {
		s.T().Skip(scimProviderOwnershipIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying every by-ID SCIM user request answers 404 for a local user the %s provider does not own", authactions.OpenLdap)

	ownedName, ownedID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err, "Should be able to create a SCIM user the %s provider owns", authactions.OpenLdap)

	ownedResp, err := s.scimClient.Users().ByID(ownedID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(ownedResp, http.StatusOK,
		fmt.Sprintf("The %s provider should reach its own user %s by id, so a later 404 means scoping and not a broken client", authactions.OpenLdap, ownedName)))

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	localUser, err := users.CreateUserWithRole(subClient, users.UserConfig(), string(rbac.StandardUser))
	require.NoError(s.T(), err, "Should be able to create a local user")

	results, err := scimactions.FetchSCIMUserByIDResults(s.scimClient, localUser.ID, namegen.AppendRandomString(scimHijackedUserPrefix))
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.VerifyByIDResultsStatus(results, http.StatusNotFound),
		"A local user carries no %s principal, so every by-ID request should answer 404", authactions.OpenLdap)

	survivor, err := s.client.Management.User.ByID(localUser.ID)
	require.NoError(s.T(), err, "The local user should survive the refused by-ID requests")
	require.Equal(s.T(), localUser.Username, survivor.Username, "The refused by-ID requests should not have renamed the local user")
	require.NotNil(s.T(), survivor.Enabled, "The local user should report an enabled state")
	require.True(s.T(), *survivor.Enabled, "The local user should still be enabled after the refused PATCH")
}

func (s *SCIMSecurityTestSuite) TestSCIMUserListExcludesNonProviderUser() {
	if !s.providerScopingOn {
		s.T().Skip(scimProviderOwnershipIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying the %s user list carries the users it owns and no local user", authactions.OpenLdap)

	_, ownedID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err, "Should be able to create a SCIM user the %s provider owns", authactions.OpenLdap)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	localUser, err := users.CreateUserWithRole(subClient, users.UserConfig(), string(rbac.StandardUser))
	require.NoError(s.T(), err, "Should be able to create a local user")

	listedIDs, err := scimactions.FetchSCIMUserIDs(s.scimClient)
	require.NoError(s.T(), err)
	require.Contains(s.T(), listedIDs, ownedID, "The %s provider should list the user it owns", authactions.OpenLdap)
	require.NotContains(s.T(), listedIDs, localUser.ID, "The %s provider should not list a local user", authactions.OpenLdap)
}

func (s *SCIMSecurityTestSuite) TestSCIMGroupByIDRejectsNonProviderGroup() {
	if !s.providerScopingOn {
		s.T().Skip(scimProviderOwnershipIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying every by-ID SCIM group request answers 404 for a group another provider owns, and leaves it in place")

	foreignGroup, err := scimactions.CreateGroupOwnedByProvider(s.client, subSession, scimForeignProvider)
	require.NoError(s.T(), err, "Should be able to create a group owned by the %s provider", scimForeignProvider)

	results, err := scimactions.FetchSCIMGroupByIDResults(s.scimClient, foreignGroup.Name, namegen.AppendRandomString(scimHijackedGroupPrefix))
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.VerifyByIDResultsStatus(results, http.StatusNotFound),
		"A group the %s provider owns should answer 404 to every by-ID request from the %s provider", scimForeignProvider, authactions.OpenLdap)

	require.NoError(s.T(), scimactions.VerifyGroupExists(s.client, foreignGroup.Name),
		"The %s group should survive the refused by-ID requests, including the DELETE", scimForeignProvider)
}

func (s *SCIMSecurityTestSuite) TestSCIMGroupListExcludesNonProviderGroup() {
	if !s.providerScopingOn {
		s.T().Skip(scimProviderOwnershipIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying the %s group list carries the groups it owns and no group of another provider", authactions.OpenLdap)

	_, ownedID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	foreignGroup, err := scimactions.CreateGroupOwnedByProvider(s.client, subSession, scimForeignProvider)
	require.NoError(s.T(), err, "Should be able to create a group owned by the %s provider", scimForeignProvider)

	resp, err := s.scimClient.Groups().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "GET /Groups should return 200"))

	body, err := scimactions.DecodeJSONBody(resp)
	require.NoError(s.T(), err)

	listed := []string{}
	resources, _ := body["Resources"].([]interface{})
	for _, resource := range resources {
		entry, _ := resource.(map[string]interface{})
		if id, ok := entry["id"].(string); ok {
			listed = append(listed, id)
		}
	}

	require.NotContains(s.T(), listed, foreignGroup.Name, "The %s provider should not list a group owned by %s", authactions.OpenLdap, scimForeignProvider)
	require.Contains(s.T(), listed, ownedID, "The %s provider should list the group it owns", authactions.OpenLdap)
}

func (s *SCIMSecurityTestSuite) TestSCIMRateLimitLeavesUnauthenticatedRequestsToTheAuthCheck() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the per provider rate limit does not throttle requests that carry no valid token, so a caller without a token cannot spend the provider's budget")

	defer func() {
		require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap))
	}()
	require.NoError(s.T(), scimactions.SetSCIMConfigMap(s.client, authactions.OpenLdap, scimactions.RateLimitSCIMConfigMap(1, 1)))

	badClient := scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, authactions.OpenLdap, scimInvalidToken)

	results, err := scimactions.BurstSCIMRequests(badClient, 20)
	require.NoError(s.T(), err)

	_, throttled := scimactions.CountSCIMCodes(results)
	require.Zero(s.T(), throttled, "Unauthenticated requests should be refused by the auth check, not throttled against the provider's budget, got: %v", results)
}

func (s *SCIMSecurityTestSuite) TestSCIMUnauthenticatedAnswerHidesProviderState() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying an unauthenticated caller cannot tell which providers have SCIM enabled, disabled or paused")

	defer func() {
		require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap))
	}()

	badClient := scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, authactions.OpenLdap, scimInvalidToken)

	enabledResp, err := badClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(enabledResp, http.StatusUnauthorized, "An invalid token should return 401 while SCIM is enabled"))

	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWait(s.client, s.scimClient, authactions.OpenLdap, map[string]string{scimactions.SCIMConfigEnabledKey: scimactions.SCIMConfigFalse}, http.StatusNotFound))

	disabledResp, err := badClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(disabledResp, http.StatusUnauthorized, "An invalid token should return 401, not 404, while SCIM is disabled"))

	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWait(s.client, s.scimClient, authactions.OpenLdap, map[string]string{scimactions.SCIMConfigEnabledKey: scimactions.SCIMConfigTrue, scimactions.SCIMConfigPausedKey: scimactions.SCIMConfigTrue}, http.StatusServiceUnavailable))

	pausedResp, err := badClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(pausedResp, http.StatusUnauthorized, "An invalid token should return 401, not 503, while SCIM is paused"))

	unknownClient := scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, scimUnknownProvider, scimInvalidToken)
	unknownResp, err := unknownClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(unknownResp, http.StatusUnauthorized, "An invalid token should return 401, not 404, for a provider that does not exist"))
}

func (s *SCIMSecurityTestSuite) TestSCIMZExpiredTokenIsDeletedWithoutRequest() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying expired SCIM token secrets are deleted by a controller, with no request on the endpoint")

	secretName := namegen.AppendRandomString("scim-expiring-token")
	_, _, err := scimactions.CreateSCIMTokenSecretInNamespace(s.client, authactions.OpenLdap, scimactions.SCIMTokenSecretNamespace, secretName)
	require.NoError(s.T(), err)
	subSession.RegisterCleanupFunc(func() error {
		return extsecretapi.DeleteSecret(s.client, extclusterapi.LocalCluster, scimactions.SCIMTokenSecretNamespace, secretName, false)
	})

	restoreExpiry, err := scimactions.SetSCIMTokenExpiry(s.client, scimShortTokenExpiry)
	require.NoError(s.T(), err)
	subSession.RegisterCleanupFunc(func() error {
		if err := restoreExpiry(); err != nil {
			return err
		}

		scimClient, err := scimactions.SetupSCIMClient(s.client, authactions.OpenLdap)
		if err != nil {
			return err
		}
		s.scimClient = scimClient

		return nil
	})

	require.NoError(s.T(), extsecretapi.WaitForSecretDeletion(s.client, extclusterapi.LocalCluster, scimactions.SCIMTokenSecretNamespace, secretName), "An expired token secret should be deleted without any SCIM request")
}

func (s *SCIMSecurityTestSuite) TestSCIMZZLegacyTokenSecretRemovedOnRestart() {
	if !s.tokenNamespaceOn {
		s.T().Skip(scimStandardUserTokenIssue)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying Rancher removes and reports a SCIM token secret left in %s on startup", scimactions.SCIMSecretNamespace)

	secretName := namegen.AppendRandomString("scim-legacy-token")
	_, _, err := scimactions.CreateSCIMTokenSecretInNamespace(s.client, authactions.OpenLdap, scimactions.SCIMSecretNamespace, secretName)
	require.NoError(s.T(), err)
	subSession.RegisterCleanupFunc(func() error {
		return extsecretapi.DeleteSecret(s.client, extclusterapi.LocalCluster, scimactions.SCIMSecretNamespace, secretName, false)
	})

	logrus.Info("Restarting Rancher by toggling the SCIM feature flag")
	require.NoError(s.T(), extfeatures.UpdateFeatureFlag(s.client, scimactions.SCIMFeatureFlag, false))
	subSession.RegisterCleanupFunc(func() error {
		return extfeatures.UpdateFeatureFlag(s.client, scimactions.SCIMFeatureFlag, true)
	})

	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(s.scimClient, http.StatusNotFound))
	require.NoError(s.T(), extfeatures.UpdateFeatureFlag(s.client, scimactions.SCIMFeatureFlag, true))
	require.NoError(s.T(), scimactions.WaitForRancherRestart(s.client, s.scimClient), "Rancher should be fully operational after the feature flag toggle")

	require.NoError(s.T(), extsecretapi.WaitForSecretDeletion(s.client, extclusterapi.LocalCluster, scimactions.SCIMSecretNamespace, secretName), "Rancher should delete the token secret left in %s", scimactions.SCIMSecretNamespace)

	warnings, err := rancherserverapi.FindRancherServerLogs(s.client, secretName)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), warnings, "Rancher should log a warning naming the token secret left in %s", scimactions.SCIMSecretNamespace)

	deletionLogged := false
	for _, warning := range warnings {
		if strings.Contains(warning, scimLegacyTokenLogMessage) {
			deletionLogged = true
			break
		}
	}
	require.True(s.T(), deletionLogged, "The warning naming %s should report the deletion, got: %v", secretName, warnings)
}

func TestSCIMSecuritySuite(t *testing.T) {
	suite.Run(t, new(SCIMSecurityTestSuite))
}
