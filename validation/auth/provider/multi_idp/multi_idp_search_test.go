//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.10 && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package multiidp

import (
	"net/url"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	extfeaturesapi "github.com/rancher/shepherd/extensions/kubeapi/features"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type MultiIdpSearchSuite struct {
	suite.Suite
	session               *session.Session
	client                *rancher.Client
	instances             []authactions.NamedOpenLdapInstance
	featureEnabledBySuite bool
}

func (s *MultiIdpSearchSuite) SetupSuite() {
	s.session = session.NewSession()

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err, "Failed to create Rancher client")
	s.client = client

	if _, err := client.Management.Feature.ByID(authactions.MultiAuthProviderCrossFeature); err != nil {
		s.T().Skipf("the %s feature is not available, it is Prime only: %v", authactions.MultiAuthProviderCrossFeature, err)
	}

	featureEnabled, err := extfeaturesapi.IsFeatureEnabled(s.client, authactions.MultiAuthProviderCrossFeature)
	require.NoError(s.T(), err, "Failed to check if the feature flag is enabled")
	if !featureEnabled {
		log.Infof("Enabling the feature flag %s", authactions.MultiAuthProviderCrossFeature)
		err := extfeaturesapi.EnableFeatureFlag(s.client, authactions.MultiAuthProviderCrossFeature)
		require.NoError(s.T(), err, "Failed to enable the feature flag")
		s.featureEnabledBySuite = true
	}

	s.instances, err = LoadInstances()
	require.NoError(s.T(), err, "Failed to load the multi IdP instances")
}

func (s *MultiIdpSearchSuite) TearDownSuite() {
	s.session.Cleanup()

	if s.client != nil {
		for _, err := range TearDownInstances(s.client, s.instances) {
			require.NoError(s.T(), err, "Failed to remove an auth config in teardown")
		}
	}

	if s.featureEnabledBySuite {
		log.Infof("Disabling the feature flag %s", authactions.MultiAuthProviderCrossFeature)
		err := extfeaturesapi.DisableFeatureFlag(s.client, authactions.MultiAuthProviderCrossFeature)
		if err != nil {
			log.Warnf("Failed to disable the feature flag during teardown: %v", err)
		}
	}
}


// knownUser signs the first user of the instance in once, so Rancher knows it, and returns the display name Rancher searches by
func (s *MultiIdpSearchSuite) knownUser(authAdmin *rancher.Client, instance *authactions.NamedOpenLdapInstance) string {
	require.NoError(s.T(), authactions.EnsureNamedOpenLdapEnabled(s.client, instance), "Failed to enable %s", instance.ConfigName)
	require.NotEmpty(s.T(), instance.Directory.Users, "%s needs users to sign in as", instance.ConfigName)

	userInfo := instance.Directory.Users[0]
	userClient, err := LoginNamedUser(authAdmin, instance, userInfo)
	require.NoError(s.T(), err, "Failed to login user [%v]", userInfo.Username)

	user, err := s.client.Management.User.ByID(userClient.UserID)
	require.NoError(s.T(), err, "Failed to retrieve user [%v]", userInfo.Username)

	principalID := ""
	for _, id := range user.PrincipalIDs {
		if strings.HasPrefix(id, instance.PrincipalPrefix()) {
			principalID = id
			break
		}
	}
	require.NotEmpty(s.T(), principalID, "%v has no principal with prefix %q", userInfo.Username, instance.PrincipalPrefix())

	principal, err := s.client.Management.Principal.ByID(url.PathEscape(principalID))
	require.NoError(s.T(), err, "Failed to retrieve principal [%v]", principalID)
	require.NotEmpty(s.T(), principal.Name, "Principal search matches display names, so %v needs one", principalID)

	return principal.Name
}

func (s *MultiIdpSearchSuite) TestCrossSearchFlagTogglesResults() {
	searching, other := &s.instances[0], &s.instances[1]

	subSession, authAdmin, err := authactions.SetupNamedOpenLdapSession(s.client, s.session, searching)
	require.NoError(s.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	displayName := s.knownUser(authAdmin, other)
	
	err = extfeaturesapi.UpdateFeatureFlag(s.client, authactions.MultiAuthProviderCrossFeature, false)
	require.NoError(s.T(), err, "Failed to set the feature flag to %v", false)
	require.NoError(s.T(), authactions.WaitForPrincipalSearchPrefix(authAdmin, displayName, other.PrincipalPrefix(), false),
		"With the flag off a search through %s must not return users of %s", searching.ConfigName, other.ConfigName)

	err = extfeaturesapi.UpdateFeatureFlag(s.client, authactions.MultiAuthProviderCrossFeature, true)
	require.NoError(s.T(), err, "Failed to set the feature flag to %v", true)
	require.NoError(s.T(), authactions.WaitForPrincipalSearchPrefix(authAdmin, displayName, other.PrincipalPrefix(), true),
		"With the flag on a search through %s should return users of %s", searching.ConfigName, other.ConfigName)
}

func (s *MultiIdpSearchSuite) TestCrossSearchFindsEveryConfigDynamic() {
	searching := &s.instances[0]

	subSession, authAdmin, err := authactions.SetupNamedOpenLdapSession(s.client, s.session, searching)
	require.NoError(s.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	err = extfeaturesapi.UpdateFeatureFlag(s.client, authactions.MultiAuthProviderCrossFeature, true)
	require.NoError(s.T(), err, "Failed to set the feature flag to %v", true)

	for i := 1; i < len(s.instances); i++ {
		other := &s.instances[i]
		displayName := s.knownUser(authAdmin, other)

		require.NoError(s.T(), authactions.WaitForPrincipalSearchPrefix(authAdmin, displayName, other.PrincipalPrefix(), true),
			"A cross search should return users of %s", other.ConfigName)
	}
}

func TestMultiIdpSearchSuite(t *testing.T) {
	suite.Run(t, new(MultiIdpSearchSuite))
}
