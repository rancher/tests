//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package scim

import (
	"net/http"

	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extsecretapi "github.com/rancher/shepherd/extensions/kubeapi/secrets"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	authactions "github.com/rancher/tests/actions/auth"
	scimactions "github.com/rancher/tests/actions/kubeapi/scim"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

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

func (s *SCIMSecurityTestSuite) TestSCIMInvalidTokenIsRefusedOnEveryOperation() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying every SCIM operation refuses an invalid token with 401 before it reads the provider's state")

	defer func() {
		require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap))
	}()

	badClient := scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, authactions.OpenLdap, scimInvalidToken)

	states := []struct {
		name          string
		config        map[string]string
		settledStatus int
	}{
		{"enabled", scimactions.BaselineSCIMConfigMap(), http.StatusOK},
		{"disabled", map[string]string{scimactions.SCIMConfigEnabledKey: scimactions.SCIMConfigFalse}, http.StatusNotFound},
		{"paused", map[string]string{scimactions.SCIMConfigEnabledKey: scimactions.SCIMConfigTrue, scimactions.SCIMConfigPausedKey: scimactions.SCIMConfigTrue}, http.StatusServiceUnavailable},
	}

	for _, state := range states {
		require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWait(s.client, s.scimClient, authactions.OpenLdap, state.config, state.settledStatus))

		statuses, err := scimactions.ProbeSCIMOperationStatuses(badClient, subSession, s.scimClient)
		require.NoError(s.T(), err)

		for operation, status := range statuses {
			require.Equal(s.T(), http.StatusUnauthorized, status,
				"%s should answer 401 for an invalid token while SCIM is %s, not the %d the provider state would give", operation, state.name, state.settledStatus)
		}
	}

	logrus.Infof("Verifying every SCIM operation refuses an invalid token with 401 for a provider that does not exist")

	unknownClient := scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, scimUnknownProvider, scimInvalidToken)

	unknownStatuses, err := scimactions.ProbeSCIMOperationStatuses(unknownClient, subSession, s.scimClient)
	require.NoError(s.T(), err)

	for operation, status := range unknownStatuses {
		require.Equal(s.T(), http.StatusUnauthorized, status,
			"%s should answer 401 for an invalid token on a provider that does not exist, not 404", operation)
	}
}
