//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package kubeapiauth

import (
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	authactions "github.com/rancher/tests/actions/auth"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func (k *KubeAPIAuthProviderSuite) TestACEAuthenticatesAKeycloakOIDCGroupMember() {
	keycloakClient, err := authactions.NewKeycloakOIDCClient(k.session)
	require.NoError(k.T(), err, "Failed to create the Keycloak admin client")

	logrus.Info("Building the Keycloak realm the OpenID Connect provider authenticates against")
	keycloakOIDCFixture, err := authactions.SetupKeycloakOIDC(k.client, keycloakClient)
	require.NoError(k.T(), err, "Failed to set up the Keycloak realm for Keycloak OIDC")
	require.NotEmpty(k.T(), keycloakOIDCFixture.AuthInput.Group, "Keycloak OIDC setup should have settled on the group the cluster role is bound to")
	require.NotEmpty(k.T(), keycloakOIDCFixture.AuthInput.Users, "Keycloak OIDC setup should have settled on the users that belong to the group")

	admin := &management.User{Username: keycloakOIDCFixture.Admin.Username, Password: keycloakOIDCFixture.Admin.Password}
	principalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, keycloakOIDCFixture.AuthInput.Group)

	k.reachClusterAsGroupMember(authactions.KeycloakOIDC, admin, keycloakOIDCFixture.AuthInput.Group, principalID, keycloakOIDCFixture.AuthInput.Users[0])
}
