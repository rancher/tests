//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14

package scim

import (
	"maps"
	"net/http"
	"slices"

	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	authactions "github.com/rancher/tests/actions/auth"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	scimactions "github.com/rancher/tests/actions/kubeapi/scim"
	usersapi "github.com/rancher/tests/actions/kubeapi/users"
	"github.com/rancher/tests/actions/rbac"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
)

func (s *SCIMLoginAttributesTestSuite) TestSCIMUserStoredAttributesStayOutOfDownstreamRequests() {
	clusterID, err := clusters.GetClusterIDByName(s.client, s.client.RancherConfig.ClusterName)
	require.NoError(s.T(), err, "Failed to find the downstream cluster [%v]", s.client.RancherConfig.ClusterName)

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	restoreACE, err := authactions.EnsureACEEnabled(s.client, clusterID)
	if restoreACE != nil {
		subSession.RegisterCleanupFunc(restoreACE)
	}
	require.NoError(s.T(), err, "Failed to enable the authorized cluster endpoint on cluster [%v]", clusterID)

	ldapUser := s.authUsers[0]
	distinguishedName := authactions.AuthUserDistinguishedName(authactions.OpenLdap, ldapUser.Username, s.userSearchBase, s.groupSearchBase)
	externalID := namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix)
	email := namegen.AppendRandomString("scim-mail") + "@example.com"

	logrus.Infof("Provisioning OpenLDAP user %s through SCIM with an externalId and an email", ldapUser.Username)
	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, distinguishedName, externalID, email)
	require.NoError(s.T(), err)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateClusterRoleTemplateBinding(subClient, clusterID, userID, rbac.ClusterMember.String())
	require.NoError(s.T(), err, "Failed to grant the SCIM user access to cluster [%v]", clusterID)

	authUser, err := authactions.LoginAsAuthUser(subClient, &v3.User{Username: ldapUser.Username, Password: ldapUser.Password}, authactions.OpenLdap)
	require.NoError(s.T(), err, "OpenLDAP user %s should be able to login", ldapUser.Username)
	require.Equal(s.T(), userID, authUser.UserID, "The login should land on the SCIM provisioned user, not a second one")

	generated, err := authactions.SyncedACEKubeconfig(s.client, authUser, clusterID)
	require.NoError(s.T(), err, "Failed to generate the SCIM user's kubeconfig for cluster [%v]", clusterID)

	replacedExternalID := namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix)
	patchResp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "externalId", replacedExternalID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH /Users/{id} should write the externalId back after login"))

	storedExternalID, err := usersapi.GetUserExtraAttribute(s.client, userID, authactions.OpenLdap, scimactions.UserAttributeExternalIDKey)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{replacedExternalID}, storedExternalID, "The user attribute should carry the externalId, so there is an attribute beyond principalid and username to leave out")

	proxyConfig, err := authactions.ProxyRestConfig(generated)
	require.NoError(s.T(), err)

	proxyClientset, err := kubernetes.NewForConfig(proxyConfig)
	require.NoError(s.T(), err)

	requiredExtras := []string{scimactions.UserAttributePrincipalIDKey, scimactions.UserAttributeUserNameKey}
	requestExtras := append(slices.Clone(requiredExtras), scimactions.UserExtraRequestTokenIDKey, scimactions.UserExtraRequestHostKey)

	require.NoError(s.T(), authactions.VerifySelfSubjectReviewExtras(proxyClientset, userID, requiredExtras, requestExtras),
		"The Rancher proxy on cluster [%v] should send the SCIM user with only the defined extras, not its stored attributes", clusterID)

	attribute, err := authactions.WaitForClusterUserAttributeExtras(s.client, clusterID, userID)
	require.NoError(s.T(), err)

	require.Contains(s.T(), attribute.ExtraByProvider, authactions.OpenLdap, "The cluster user attribute should hold the OpenLDAP extras")
	require.ElementsMatch(s.T(), requiredExtras, slices.Collect(maps.Keys(attribute.ExtraByProvider[authactions.OpenLdap])),
		"The cluster user attribute on cluster [%v] should hold exactly principalid and username for OpenLDAP, not the user's stored attributes", clusterID)
	for provider, extra := range attribute.ExtraByProvider {
		require.Subset(s.T(), requiredExtras, slices.Collect(maps.Keys(extra)),
			"The cluster user attribute on cluster [%v] should hold nothing beyond principalid and username for %v", clusterID, provider)
	}

	aceClientsets, err := authactions.ClientsetsFromKubeconfig(generated)
	require.NoError(s.T(), err)

	require.NotEmpty(s.T(), aceClientsets, "The kubeconfig should name an authorized cluster endpoint context")

	for name, clientset := range aceClientsets {
		require.NoError(s.T(), authactions.VerifySelfSubjectReviewExtras(clientset, userID, nil, requestExtras),
			"The authorized cluster endpoint context [%v] should resolve to the SCIM user with only the defined extras, not its stored attributes", name)
	}
}
