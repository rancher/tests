//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package scim

import (
	"net/http"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	scimclient "github.com/rancher/shepherd/clients/rancher/auth/scim"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extfeatures "github.com/rancher/shepherd/extensions/kubeapi/features"
	extdeployments "github.com/rancher/shepherd/extensions/kubeapi/workloads/deployments"
	"github.com/rancher/shepherd/extensions/users"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	rancherserverapi "github.com/rancher/tests/actions/kubeapi/rancherserver"
	scimactions "github.com/rancher/tests/actions/kubeapi/scim"
	deploymentsapi "github.com/rancher/tests/actions/kubeapi/workloads/deployments"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	primeReferenceFeature    = "cluster-autoscaling"
	nonPrimeReferenceFeature = "fleet"
	cattleFeaturesEnv        = "CATTLE_FEATURES"
	cattleFeaturesSCIMOn     = "scim=true"

	primeBuildOnly     = "This test covers a Rancher Prime build and this server reports a community build"
	communityBuildOnly = "This test covers a community Rancher build and this server reports a Prime build"
)

type SCIMPrimeFeatureTestSuite struct {
	suite.Suite
	session    *session.Session
	client     *rancher.Client
	scimClient *scimclient.Client
	prime      bool
}

func (s *SCIMPrimeFeatureTestSuite) SetupSuite() {
	s.session = session.NewSession()

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err, "Failed to create Rancher client")
	s.client = client

	logrus.Info("Setting up SCIM client for OpenLDAP provider")
	scimClient, err := scimactions.SetupSCIMClient(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to setup SCIM client")
	s.scimClient = scimClient

	logrus.Info("Ensuring baseline SCIM ConfigMap (enabled=true) for OpenLDAP provider")
	require.NoError(s.T(), scimactions.SetSCIMConfigMap(s.client, authactions.OpenLdap, scimactions.BaselineSCIMConfigMap()))

	require.NoError(s.T(), scimactions.VerifyExpectedBuildType(s.client), "The Rancher build type should match the one the config expects")

	s.prime, err = rancherserverapi.IsRancherPrimeBuild(s.client)
	require.NoError(s.T(), err, "Failed to read the Rancher build type")
	logrus.Infof("The Rancher server reports a Prime build: %t", s.prime)
}

func (s *SCIMPrimeFeatureTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *SCIMPrimeFeatureTestSuite) TestSCIMFeatureIsRegisteredAsPrime() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the scim feature reports itself as a Prime feature")

	feature, err := s.client.WranglerContext.Mgmt.Feature().Get(scimactions.SCIMFeatureFlag, metav1.GetOptions{})
	require.NoError(s.T(), err, "Should be able to get the %s feature", scimactions.SCIMFeatureFlag)

	require.True(s.T(), feature.Status.Prime, "The %s feature should report status.prime=true", scimactions.SCIMFeatureFlag)
	require.False(s.T(), feature.Status.Default, "The %s feature should stay off by default", scimactions.SCIMFeatureFlag)
	require.False(s.T(), feature.Status.Dynamic, "Changing the %s feature should require a Rancher restart", scimactions.SCIMFeatureFlag)
	require.Equal(s.T(), scimactions.SCIMFeatureDescription, feature.Status.Description, "The %s feature description should match the registered one", scimactions.SCIMFeatureFlag)
}

func (s *SCIMPrimeFeatureTestSuite) TestSCIMFeatureMatchesPrimeReferenceFeature() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying the scim feature reports prime like %s and unlike %s", primeReferenceFeature, nonPrimeReferenceFeature)

	scimFeature, err := s.client.WranglerContext.Mgmt.Feature().Get(scimactions.SCIMFeatureFlag, metav1.GetOptions{})
	require.NoError(s.T(), err, "Should be able to get the %s feature", scimactions.SCIMFeatureFlag)

	primeFeature, err := s.client.WranglerContext.Mgmt.Feature().Get(primeReferenceFeature, metav1.GetOptions{})
	require.NoError(s.T(), err, "Should be able to get the %s feature", primeReferenceFeature)
	require.True(s.T(), primeFeature.Status.Prime, "%s is the Prime reference feature and should report status.prime=true", primeReferenceFeature)
	require.Equal(s.T(), primeFeature.Status.Prime, scimFeature.Status.Prime, "The %s feature should report prime like %s", scimactions.SCIMFeatureFlag, primeReferenceFeature)

	nonPrimeFeature, err := s.client.WranglerContext.Mgmt.Feature().Get(nonPrimeReferenceFeature, metav1.GetOptions{})
	require.NoError(s.T(), err, "Should be able to get the %s feature", nonPrimeReferenceFeature)
	require.False(s.T(), nonPrimeFeature.Status.Prime, "%s is not a Prime feature and should report status.prime=false", nonPrimeReferenceFeature)
}

func (s *SCIMPrimeFeatureTestSuite) TestSCIMFeatureValueResolvesToBuildType() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the feature API resolves the scim value against the build type")

	enabled, err := extfeatures.IsFeatureEnabled(s.client, scimactions.SCIMFeatureFlag)
	require.NoError(s.T(), err, "Should be able to read the %s feature value", scimactions.SCIMFeatureFlag)
	require.Equal(s.T(), s.prime, enabled, "The %s feature should resolve to enabled only on a Prime build", scimactions.SCIMFeatureFlag)

	feature, err := s.client.Management.Feature.ByID(scimactions.SCIMFeatureFlag)
	require.NoError(s.T(), err, "Should be able to get the %s feature through the management API", scimactions.SCIMFeatureFlag)
	require.NotNil(s.T(), feature.Value, "The management API should report a value for the %s feature", scimactions.SCIMFeatureFlag)

	require.Equal(s.T(), s.prime, *feature.Value, "The management API should report the %s feature as enabled only on a Prime build", scimactions.SCIMFeatureFlag)
}

func (s *SCIMPrimeFeatureTestSuite) TestSCIMEndpointFollowsBuildType() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the SCIM endpoint is served only on a Prime build")

	resp, err := s.scimClient.Discovery().ServiceProviderConfig()
	require.NoError(s.T(), err)

	if s.prime {
		require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "GET /ServiceProviderConfig should return 200 on a Prime build with the scim feature enabled"))

		return
	}

	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusNotFound, "GET /ServiceProviderConfig should return 404 on a non-Prime build even with the scim feature enabled"))
}

func (s *SCIMPrimeFeatureTestSuite) TestSCIMProvisioningRejectedOnCommunityBuild() {
	if s.prime {
		s.T().Skip(communityBuildOnly)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying a community build provisions no SCIM user even with a valid token and ConfigMap")

	userName := namegen.AppendRandomString("scim-user")

	createResp, err := s.scimClient.Users().Create(scimclient.User{
		Schemas:  []string{scimclient.SCIMSchemaUser},
		UserName: userName,
		Active:   scimclient.BoolPtr(true),
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(createResp, http.StatusNotFound, "POST /Users should return 404 on a community build"))

	listResp, err := s.scimClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(listResp, http.StatusNotFound, "GET /Users should return 404 on a community build"))

	userID, err := users.GetUserIDByName(s.client, userName)
	require.NoError(s.T(), err)
	require.Empty(s.T(), userID, "A community build should create no Rancher user for a rejected SCIM request")
}

func (s *SCIMPrimeFeatureTestSuite) TestSCIMFeatureToggleDoesNotRestartCommunityBuild() {
	if s.prime {
		s.T().Skip(communityBuildOnly)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying a scim feature change restarts nothing on a community build")

	before, err := rancherserverapi.FetchRancherServerRestartCounts(s.client)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), before, "Should find at least one Rancher server pod")

	require.NoError(s.T(), extfeatures.UpdateFeatureFlag(s.client, scimactions.SCIMFeatureFlag, false))
	subSession.RegisterCleanupFunc(func() error {
		return extfeatures.UpdateFeatureFlag(s.client, scimactions.SCIMFeatureFlag, true)
	})

	logrus.Infof("Watching %s for a restart that should not happen", defaults.TwoMinuteTimeout)
	stable, err := rancherserverapi.VerifyRancherServerStaysStable(s.client, before, defaults.TwoMinuteTimeout)
	require.NoError(s.T(), err)
	require.True(s.T(), stable, "A Prime feature change should not restart Rancher on a community build")

	resp, err := s.scimClient.Discovery().ServiceProviderConfig()
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusNotFound, "GET /ServiceProviderConfig should stay 404 on a community build"))
}

func (s *SCIMPrimeFeatureTestSuite) TestSCIMFeatureCannotBeForcedByCattleFeatures() {
	if s.prime {
		s.T().Skip(communityBuildOnly)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying %s=%s cannot serve SCIM on a community build", cattleFeaturesEnv, cattleFeaturesSCIMOn)

	require.NoError(s.T(), deploymentsapi.UpdateOrRemoveEnvVarForDeployment(s.client, extclusterapi.LocalCluster, extdeployments.RancherDeploymentNamespace, extdeployments.RancherDeploymentName, cattleFeaturesEnv, cattleFeaturesSCIMOn))
	subSession.RegisterCleanupFunc(func() error {
		return deploymentsapi.UpdateOrRemoveEnvVarForDeployment(s.client, extclusterapi.LocalCluster, extdeployments.RancherDeploymentNamespace, extdeployments.RancherDeploymentName, cattleFeaturesEnv, "")
	})

	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(s.scimClient, http.StatusNotFound), "GET /Users should stay 404 when the feature is forced through %s", cattleFeaturesEnv)

	require.NoError(s.T(), scimactions.WaitForSCIMDiscoveryStatus(s.scimClient, http.StatusNotFound), "GET /ServiceProviderConfig should stay 404 when the feature is forced through %s", cattleFeaturesEnv)

	feature, err := s.client.Management.Feature.ByID(scimactions.SCIMFeatureFlag)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), feature.Value)
	require.False(s.T(), *feature.Value, "The management API should still report the %s feature as disabled", scimactions.SCIMFeatureFlag)
}

func (s *SCIMPrimeFeatureTestSuite) TestSCIMNoFeatureErrorsInRancherLogs() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the Rancher server logs no error about the scim feature")

	require.NoError(s.T(), rancherserverapi.VerifyNoRancherServerErrors(s.client, scimactions.SCIMFeatureFlag), "The Rancher server should log no error about %s", scimactions.SCIMFeatureFlag)
}

func (s *SCIMPrimeFeatureTestSuite) TestSCIMZFeatureDisableAndEnableOnPrimeBuild() {
	if !s.prime {
		s.T().Skip(primeBuildOnly)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying provisioning works, survives a disable and works again after a re-enable")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)
	_, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	getResp, err := s.scimClient.Groups().ByID(groupID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(getResp, http.StatusOK, "GET /Groups/{id} should return 200 while the feature is enabled"))

	before, err := rancherserverapi.FetchRancherServerRestartCounts(s.client)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), before, "Should find at least one Rancher server pod")

	logrus.Info("Disabling the SCIM feature flag")
	require.NoError(s.T(), extfeatures.UpdateFeatureFlag(s.client, scimactions.SCIMFeatureFlag, false))
	subSession.RegisterCleanupFunc(func() error {
		return extfeatures.UpdateFeatureFlag(s.client, scimactions.SCIMFeatureFlag, true)
	})

	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(s.scimClient, http.StatusNotFound), "GET /Users should return 404 once the feature is disabled")

	require.NoError(s.T(), scimactions.WaitForSCIMDiscoveryStatus(s.scimClient, http.StatusNotFound), "GET /ServiceProviderConfig should return 404 once the feature is disabled")

	restarted, err := rancherserverapi.VerifyRancherServerRestarted(s.client, before)
	require.NoError(s.T(), err)
	require.True(s.T(), restarted, "A non-dynamic feature change should restart Rancher on a Prime build")

	keptUser, err := s.client.Management.User.ByID(userID)
	require.NoError(s.T(), err, "The SCIM provisioned user should survive the feature being disabled")
	require.Equal(s.T(), userID, keptUser.ID)

	logrus.Info("Re-enabling the SCIM feature flag")
	require.NoError(s.T(), extfeatures.UpdateFeatureFlag(s.client, scimactions.SCIMFeatureFlag, true))
	require.NoError(s.T(), scimactions.WaitForRancherRestart(s.client, s.scimClient), "Rancher should be fully operational after the feature flag is re-enabled")

	_, reprovisionedID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err, "Provisioning should work again after the feature is re-enabled")

	reprovisionedResp, err := s.scimClient.Users().ByID(reprovisionedID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(reprovisionedResp, http.StatusOK, "GET /Users/{id} should return 200 after the feature is re-enabled"))

	_, newGroupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err, "Group provisioning should work again after the feature is re-enabled")

	newGroupResp, err := s.scimClient.Groups().ByID(newGroupID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(newGroupResp, http.StatusOK, "GET /Groups/{id} should return 200 after the feature is re-enabled"))
}

func TestSCIMPrimeFeatureSuite(t *testing.T) {
	suite.Run(t, new(SCIMPrimeFeatureTestSuite))
}
