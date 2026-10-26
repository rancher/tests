//go:build !pit.daily && !pit.weekly && !pit.event && !pit.harvester.daily && !pit.elemental && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package counts

import (
	"fmt"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	exttokens "github.com/rancher/shepherd/extensions/kubeapi/tokens"
	"github.com/rancher/shepherd/pkg/session"
	tokenactions "github.com/rancher/tests/actions/kubeapi/tokens/exttokens"
	"github.com/rancher/tests/actions/rbac"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const tokenSchemaID = "ext.cattle.io.token"

type ownedToken struct {
	client *rancher.Client
	name   string
}

type TokenCountTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
}

func (t *TokenCountTestSuite) SetupSuite() {
	t.session = session.NewSession()

	client, err := rancher.NewClient("", t.session)
	require.NoError(t.T(), err)
	t.client = client
}

func (t *TokenCountTestSuite) TearDownSuite() {
	t.session.Cleanup()
}

func (t *TokenCountTestSuite) TestTokenCountsRespectOwnership() {
	testSession := t.session.NewSession()
	defer testSession.Cleanup()

	adminClient, err := t.client.WithSession(testSession)
	require.NoError(t.T(), err)

	_, additionalAdminClient, err := rbac.SetupUser(adminClient, rbac.Admin.String())
	require.NoError(t.T(), err, "Failed to create an additional global admin")

	_, standardUserClient, err := rbac.SetupUser(adminClient, rbac.StandardUser.String())
	require.NoError(t.T(), err, "Failed to create a Standard User")

	_, baseUserClient, err := rbac.SetupUser(adminClient, rbac.BaseUser.String())
	require.NoError(t.T(), err, "Failed to create a User-Base user")

	_, standardUserWithoutTokensClient, err := rbac.SetupUser(adminClient, rbac.StandardUser.String())
	require.NoError(t.T(), err, "Failed to create a Standard User without tokens")

	_, baseUserWithoutTokensClient, err := rbac.SetupUser(adminClient, rbac.BaseUser.String())
	require.NoError(t.T(), err, "Failed to create a User-Base user without tokens")

	createdTokens := []ownedToken{}
	defer func() {
		for index := len(createdTokens) - 1; index >= 0; index-- {
			fixture := createdTokens[index]
			err := fixture.client.WranglerContext.Ext.Token().Delete(fixture.name, &metav1.DeleteOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				t.T().Errorf("Failed to clean up token %q: %v", fixture.name, err)
			}
		}
	}()

	createToken := func(client *rancher.Client) string {
		created, err := tokenactions.CreateExtToken(client, 0)
		require.NoError(t.T(), err, "Failed to create token as user %q", client.UserID)
		require.NotEmpty(t.T(), created.Name)

		createdTokens = append(createdTokens, ownedToken{
			client: client,
			name:   created.Name,
		})
		require.Equal(t.T(), client.UserID, created.Labels[tokenactions.UserIDLabel])
		return created.Name
	}

	adminTokenNames := []string{
		createToken(adminClient),
		createToken(additionalAdminClient),
	}
	standardUserTokenNames := []string{
		createToken(standardUserClient),
		createToken(standardUserClient),
	}
	baseUserTokenNames := []string{
		createToken(baseUserClient),
	}

	allFixtureNames := combineNames(
		adminTokenNames,
		standardUserTokenNames,
		baseUserTokenNames,
	)

	initialCases := []resourceCountTestCase{
		{
			name:          "configured admin sees every fixture",
			client:        adminClient,
			includedNames: allFixtureNames,
			minimumCount:  len(allFixtureNames),
		},
		{
			name:          "additional global admin sees every fixture",
			client:        additionalAdminClient,
			includedNames: allFixtureNames,
			minimumCount:  len(allFixtureNames),
		},
		{
			name:          "Standard User sees two owned tokens only",
			client:        standardUserClient,
			exactNames:    standardUserTokenNames,
			excludedNames: namesExcept(allFixtureNames, standardUserTokenNames),
		},
		{
			name:          "User-Base sees one owned token only",
			client:        baseUserClient,
			exactNames:    baseUserTokenNames,
			excludedNames: namesExcept(allFixtureNames, baseUserTokenNames),
		},
		{
			name:          "Standard User without owned tokens sees zero",
			client:        standardUserWithoutTokensClient,
			exactNames:    []string{},
			excludedNames: allFixtureNames,
		},
		{
			name:          "User-Base without owned tokens sees zero",
			client:        baseUserWithoutTokensClient,
			exactNames:    []string{},
			excludedNames: allFixtureNames,
		},
	}

	t.runTokenCountCases("after creation", initialCases)

	deletedName := standardUserTokenNames[0]
	err = exttokens.DeleteExtToken(standardUserClient, deletedName, true)
	require.NoError(t.T(), err, "Failed to delete owned token %q", deletedName)

	remainingStandardUserNames := standardUserTokenNames[1:]
	remainingFixtureNames := namesExcept(allFixtureNames, []string{deletedName})
	afterDeletionCases := []resourceCountTestCase{
		{
			name:          "configured admin list and count reflect deletion",
			client:        adminClient,
			includedNames: remainingFixtureNames,
			excludedNames: []string{deletedName},
			minimumCount:  len(remainingFixtureNames),
		},
		{
			name:          "additional global admin list and count reflect deletion",
			client:        additionalAdminClient,
			includedNames: remainingFixtureNames,
			excludedNames: []string{deletedName},
			minimumCount:  len(remainingFixtureNames),
		},
		{
			name:          "Standard User count decrements after deleting one of two",
			client:        standardUserClient,
			exactNames:    remainingStandardUserNames,
			excludedNames: combineNames(namesExcept(remainingFixtureNames, remainingStandardUserNames), []string{deletedName}),
		},
		{
			name:          "User-Base count remains isolated",
			client:        baseUserClient,
			exactNames:    baseUserTokenNames,
			excludedNames: combineNames(namesExcept(remainingFixtureNames, baseUserTokenNames), []string{deletedName}),
		},
		{
			name:          "Standard User without tokens remains at zero",
			client:        standardUserWithoutTokensClient,
			exactNames:    []string{},
			excludedNames: allFixtureNames,
		},
		{
			name:          "User-Base without tokens remains at zero",
			client:        baseUserWithoutTokensClient,
			exactNames:    []string{},
			excludedNames: allFixtureNames,
		},
	}

	t.runTokenCountCases("after deletion", afterDeletionCases)
}

func (t *TokenCountTestSuite) runTokenCountCases(phase string, testCases []resourceCountTestCase) {
	var convergedObservations []resourceCountObservation
	converged := assert.EventuallyWithT(t.T(), func(collect *assert.CollectT) {
		observations := make([]resourceCountObservation, len(testCases))
		for index, testCase := range testCases {
			observation, err := observeResourceCount(testCase.client, tokenSchemaID, "tokens")
			if !assert.NoError(collect, err, "%s: %s", phase, testCase.name) {
				continue
			}
			observations[index] = observation
			assertResourceCountObservation(collect, phase, "token", testCase, observation)
		}
		convergedObservations = observations
	}, defaults.OneMinuteTimeout, defaults.FiveSecondTimeout, "%s token counts did not converge", phase)
	if !converged {
		return
	}

	for index, testCase := range testCases {
		testCase := testCase
		observation := convergedObservations[index]
		t.Run(fmt.Sprintf("%s/%s", phase, testCase.name), func() {
			assertResourceCountObservation(t.T(), phase, "token", testCase, observation)
		})
	}
}

func TestTokenCountTestSuite(t *testing.T) {
	suite.Run(t, new(TokenCountTestSuite))
}
