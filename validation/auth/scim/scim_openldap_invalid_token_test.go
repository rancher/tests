//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package scim

import (
	"net/http"

	authactions "github.com/rancher/tests/actions/auth"
	scimactions "github.com/rancher/tests/actions/kubeapi/scim"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func (s *SCIMOpenLDAPTestSuite) TestSCIMCMInvalidTokenReturns401WithoutConfigMap() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying an invalid bearer token returns 401 when the ConfigMap is absent, so the answer does not report the provider state")

	defer func() {
		require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap))
	}()
	require.NoError(s.T(), scimactions.DeleteSCIMConfigMap(s.client, authactions.OpenLdap))
	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(s.scimClient, http.StatusNotFound))

	badClient := scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, authactions.OpenLdap, "invalid-token")
	resp, err := badClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusUnauthorized, "An invalid token should return 401 whether or not the ConfigMap is present"))
}
