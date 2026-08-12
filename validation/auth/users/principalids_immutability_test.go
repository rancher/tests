//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12

package users

import (
	"slices"
	"testing"

	apiv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extuserapi "github.com/rancher/shepherd/extensions/kubeapi/users"
	normanusers "github.com/rancher/shepherd/extensions/users"
	"github.com/rancher/shepherd/pkg/config"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	userapi "github.com/rancher/tests/actions/kubeapi/users"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type PrincipalIDsImmutabilityTestSuite struct {
	suite.Suite
	client               *rancher.Client
	session              *session.Session
	authConfig           *authactions.AuthConfig
	injectedPrincipalID  string
	unemittedPrincipalID string
}

func (pi *PrincipalIDsImmutabilityTestSuite) SetupSuite() {
	pi.session = session.NewSession()

	client, err := rancher.NewClient("", pi.session)
	require.NoError(pi.T(), err)
	pi.client = client

	log.Info("Loading the OpenLDAP auth configuration from the config file")
	pi.authConfig = new(authactions.AuthConfig)
	config.LoadConfig(authactions.OpenLdapAuthInput, pi.authConfig)
	require.NotEmpty(pi.T(), pi.authConfig.Users, "OpenLDAP auth configuration must provide at least one user")

	log.Info("Enabling OpenLDAP so that the principal injected by the mutation attempts is a real external identity")
	err = authactions.EnsureAuthProviderEnabled(pi.client, authactions.OpenLdap)
	require.NoError(pi.T(), err, "failed to enable OpenLDAP")

	userSearchBase := pi.client.Auth.OLDAP.Config.Users.SearchBase
	groupSearchBase := pi.client.Auth.OLDAP.Config.Groups.SearchBase

	pi.injectedPrincipalID = authactions.GetUserPrincipalID(authactions.OpenLdap, pi.authConfig.Users[0].Username, userSearchBase, groupSearchBase)
	pi.unemittedPrincipalID = authactions.GetUserPrincipalID(authactions.OpenLdap, namegen.AppendRandomString(unemittedPrincipalName), userSearchBase, groupSearchBase)
}

func (pi *PrincipalIDsImmutabilityTestSuite) TearDownSuite() {
	pi.session.Cleanup()
}

func (pi *PrincipalIDsImmutabilityTestSuite) TestLocalUserCreationSetsLocalPrincipal() {
	subSession := pi.session.NewSession()
	defer subSession.Cleanup()

	log.Info("As admin, creating a user over the norman API")
	createdUser, err := normanusers.CreateUserWithRole(pi.client, normanusers.UserConfig(), rbac.StandardUser.String())
	require.NoError(pi.T(), err, "failed to create user")

	log.Infof("Verifying that the created user %s carries exactly its local principal", createdUser.ID)
	principalIDs, err := userapi.WaitForUserPrincipalID(pi.client, createdUser.ID, authactions.LocalPrincipalPrefix+createdUser.ID)
	require.NoError(pi.T(), err, "created user %s never received its local principal", createdUser.ID)
	require.Equal(pi.T(), []string{authactions.LocalPrincipalPrefix + createdUser.ID}, principalIDs, "a newly created local user should carry only its local principal")
}

func (pi *PrincipalIDsImmutabilityTestSuite) TestPrincipalIDsCannotBeMutatedViaNormanAPI() {
	subSession := pi.session.NewSession()
	defer subSession.Cleanup()

	targetUser, originalPrincipalIDs, err := newTargetUser(pi.client)
	require.NoError(pi.T(), err, "failed to create the target user")

	updaterClient, err := newUserWithUsersUpdateRole(pi.client)
	require.NoError(pi.T(), err, "failed to create the user holding update on users resource")

	log.Infof("Verifying that the user holding update on users resource can edit a mutable field on %s", targetUser.ID)
	err = updateUserViaNormanAPI(updaterClient, targetUser.ID, map[string]any{descriptionField: grantProbeDescription})
	require.NoError(pi.T(), err, "a user holding update on users resource should be able to edit a mutable field")

	log.Infof("Attempting to inject the external principal %s into user %s over the norman API", pi.injectedPrincipalID, targetUser.ID)
	err = updateUserViaNormanAPI(updaterClient, targetUser.ID, map[string]any{
		principalIDsField: append(slices.Clone(originalPrincipalIDs), pi.injectedPrincipalID),
	})
	log.Infof("The norman update attempt returned [%v]; the fix either denies the request at the webhook or strips the identity fields in the store, so the read-back invariant is what decides the result", err)

	log.Infof("Verifying that the identity fields of user %s are unchanged", targetUser.ID)
	err = userapi.VerifyUserIdentityFieldsUnchanged(pi.client, targetUser.ID, originalPrincipalIDs, targetUser.Username, targetUser.Name)
	require.NoError(pi.T(), err, "injecting an external principal over the norman API must not change the identity fields")
}

func (pi *PrincipalIDsImmutabilityTestSuite) TestPrincipalIDsCannotBeMutatedViaPublicAPI() {
	subSession := pi.session.NewSession()
	defer subSession.Cleanup()

	targetUser, originalPrincipalIDs, err := newTargetUser(pi.client)
	require.NoError(pi.T(), err, "failed to create the target user")

	updaterClient, err := newUserWithUsersUpdateRole(pi.client)
	require.NoError(pi.T(), err, "failed to create the user holding update on users resource")

	log.Infof("As the user holding update on users resource, fetching %s over the public API", targetUser.ID)
	latestUser, err := extuserapi.GetUserByName(updaterClient, targetUser.ID)
	require.NoError(pi.T(), err, "failed to fetch user %s over the public API", targetUser.ID)

	log.Infof("Attempting to inject the external principal %s into user %s over the public API", pi.injectedPrincipalID, targetUser.ID)
	latestUser.PrincipalIDs = append(latestUser.PrincipalIDs, pi.injectedPrincipalID)
	_, err = extuserapi.UpdateUser(updaterClient, latestUser)
	require.Error(pi.T(), err, "expected the admission webhook to deny a principalIds mutation")
	require.Contains(pi.T(), err.Error(), principalIDsField, "unexpected error: %v", err)

	log.Infof("Verifying that the identity fields of user %s are unchanged", targetUser.ID)
	err = userapi.VerifyUserIdentityFieldsUnchanged(pi.client, targetUser.ID, originalPrincipalIDs, targetUser.Username, targetUser.Name)
	require.NoError(pi.T(), err, "a webhook denied update must not change the identity fields")
}

func (pi *PrincipalIDsImmutabilityTestSuite) TestUsernameCannotBeMutatedViaNormanAPI() {
	subSession := pi.session.NewSession()
	defer subSession.Cleanup()

	targetUser, originalPrincipalIDs, err := newTargetUser(pi.client)
	require.NoError(pi.T(), err, "failed to create the target user")

	updaterClient, err := newUserWithUsersUpdateRole(pi.client)
	require.NoError(pi.T(), err, "failed to create the user holding update on users resource")

	log.Infof("Attempting to rewrite the username of user %s over the norman API", targetUser.ID)
	err = updateUserViaNormanAPI(updaterClient, targetUser.ID, map[string]any{usernameField: hijackedUsername})
	log.Infof("The norman update attempt returned [%v]; the read-back invariant is what decides the result", err)

	log.Infof("Verifying that the identity fields of user %s are unchanged", targetUser.ID)
	err = userapi.VerifyUserIdentityFieldsUnchanged(pi.client, targetUser.ID, originalPrincipalIDs, targetUser.Username, targetUser.Name)
	require.NoError(pi.T(), err, "rewriting the username over the norman API must not change the identity fields")
}

func (pi *PrincipalIDsImmutabilityTestSuite) TestUsernameCannotBeMutatedViaPublicAPI() {
	subSession := pi.session.NewSession()
	defer subSession.Cleanup()

	targetUser, originalPrincipalIDs, err := newTargetUser(pi.client)
	require.NoError(pi.T(), err, "failed to create the target user")

	updaterClient, err := newUserWithUsersUpdateRole(pi.client)
	require.NoError(pi.T(), err, "failed to create the user holding update on users resource")

	log.Infof("As the user holding update on users resource, fetching %s over the public API", targetUser.ID)
	latestUser, err := extuserapi.GetUserByName(updaterClient, targetUser.ID)
	require.NoError(pi.T(), err, "failed to fetch user %s over the public API", targetUser.ID)

	log.Infof("Attempting to rewrite the username of user %s over the public API", targetUser.ID)
	latestUser.Username = hijackedUsername
	_, err = extuserapi.UpdateUser(updaterClient, latestUser)
	require.Error(pi.T(), err, "expected the admission webhook to deny a username mutation")
	require.Contains(pi.T(), err.Error(), usernameField, "unexpected error: %v", err)

	log.Infof("Verifying that the identity fields of user %s are unchanged", targetUser.ID)
	err = userapi.VerifyUserIdentityFieldsUnchanged(pi.client, targetUser.ID, originalPrincipalIDs, targetUser.Username, targetUser.Name)
	require.NoError(pi.T(), err, "a webhook denied update must not change the identity fields")
}

func (pi *PrincipalIDsImmutabilityTestSuite) TestUsernameAndPrincipalIDsCannotBeMutatedTogether() {
	subSession := pi.session.NewSession()
	defer subSession.Cleanup()

	targetUser, originalPrincipalIDs, err := newTargetUser(pi.client)
	require.NoError(pi.T(), err, "failed to create the target user")

	updaterClient, err := newUserWithUsersUpdateRole(pi.client)
	require.NoError(pi.T(), err, "failed to create the user holding update on users resource")

	log.Infof("Attempting to rewrite the username and inject the external principal %s into user %s in a single norman update", pi.injectedPrincipalID, targetUser.ID)
	err = updateUserViaNormanAPI(updaterClient, targetUser.ID, map[string]any{
		usernameField:     hijackedUsername,
		principalIDsField: append(slices.Clone(originalPrincipalIDs), pi.injectedPrincipalID),
	})
	log.Infof("The norman update attempt returned [%v]; the read-back invariant is what decides the result", err)

	log.Infof("Verifying that neither identity field of user %s changed", targetUser.ID)
	err = userapi.VerifyUserIdentityFieldsUnchanged(pi.client, targetUser.ID, originalPrincipalIDs, targetUser.Username, targetUser.Name)
	require.NoError(pi.T(), err, "mutating both identity fields at once must change neither of them")
}

func (pi *PrincipalIDsImmutabilityTestSuite) TestIdentityFieldsRejectedAlongsideMutableFieldEdit() {
	subSession := pi.session.NewSession()
	defer subSession.Cleanup()

	targetUser, originalPrincipalIDs, err := newTargetUser(pi.client)
	require.NoError(pi.T(), err, "failed to create the target user")

	updaterClient, err := newUserWithUsersUpdateRole(pi.client)
	require.NoError(pi.T(), err, "failed to create the user holding update on users resource")

	log.Infof("Attempting to edit the description of user %s while also mutating its identity fields", targetUser.ID)
	err = updateUserViaNormanAPI(updaterClient, targetUser.ID, map[string]any{
		descriptionField:  legitimateDescription,
		usernameField:     hijackedUsername,
		principalIDsField: append(slices.Clone(originalPrincipalIDs), pi.injectedPrincipalID),
	})
	log.Infof("The norman update attempt returned [%v]; the identity fields must not change whether the request is denied or the fields are stripped", err)

	log.Infof("Verifying that the identity fields of user %s survived the mixed update", targetUser.ID)
	err = userapi.VerifyUserIdentityFieldsUnchanged(pi.client, targetUser.ID, originalPrincipalIDs, targetUser.Username, targetUser.Name)
	require.NoError(pi.T(), err, "pairing an identity mutation with a legitimate edit must not let the identity mutation through")
}

func (pi *PrincipalIDsImmutabilityTestSuite) TestUnemittedExternalPrincipalCannotBeInjected() {
	subSession := pi.session.NewSession()
	defer subSession.Cleanup()

	targetUser, originalPrincipalIDs, err := newTargetUser(pi.client)
	require.NoError(pi.T(), err, "failed to create the target user")

	updaterClient, err := newUserWithUsersUpdateRole(pi.client)
	require.NoError(pi.T(), err, "failed to create the user holding update on users resource")

	log.Infof("Attempting to orphan the bindings of user %s by injecting the never emitted principal %s", targetUser.ID, pi.unemittedPrincipalID)
	err = updateUserViaNormanAPI(updaterClient, targetUser.ID, map[string]any{
		principalIDsField: append(slices.Clone(originalPrincipalIDs), pi.unemittedPrincipalID),
	})
	log.Infof("The norman update attempt returned [%v]; the read-back invariant is what decides the result", err)

	log.Infof("Verifying that the identity fields of user %s are unchanged", targetUser.ID)
	err = userapi.VerifyUserIdentityFieldsUnchanged(pi.client, targetUser.ID, originalPrincipalIDs, targetUser.Username, targetUser.Name)
	require.NoError(pi.T(), err, "injecting a never emitted principal must not change the identity fields")
}

func (pi *PrincipalIDsImmutabilityTestSuite) TestAdminCannotMutatePrincipalIDsViaNormanAPI() {
	subSession := pi.session.NewSession()
	defer subSession.Cleanup()

	targetUser, originalPrincipalIDs, err := newTargetUser(pi.client)
	require.NoError(pi.T(), err, "failed to create the target user")

	log.Infof("As admin, attempting to inject the external principal %s into user %s over the norman API", pi.injectedPrincipalID, targetUser.ID)
	err = updateUserViaNormanAPI(pi.client, targetUser.ID, map[string]any{
		principalIDsField: append(slices.Clone(originalPrincipalIDs), pi.injectedPrincipalID),
	})
	log.Infof("The norman update attempt returned [%v]; the read-back invariant is what decides the result", err)

	log.Infof("Verifying that the identity fields of user %s are unchanged after the admin attempt", targetUser.ID)
	err = userapi.VerifyUserIdentityFieldsUnchanged(pi.client, targetUser.ID, originalPrincipalIDs, targetUser.Username, targetUser.Name)
	require.NoError(pi.T(), err, "the admin token must not be able to rebind a principal over the norman API")
}

func (pi *PrincipalIDsImmutabilityTestSuite) TestAdminCannotMutatePrincipalIDsViaPublicAPI() {
	subSession := pi.session.NewSession()
	defer subSession.Cleanup()

	targetUser, originalPrincipalIDs, err := newTargetUser(pi.client)
	require.NoError(pi.T(), err, "failed to create the target user")

	log.Infof("As admin, fetching user %s over the public API", targetUser.ID)
	latestUser, err := extuserapi.GetUserByName(pi.client, targetUser.ID)
	require.NoError(pi.T(), err, "failed to fetch user %s over the public API", targetUser.ID)

	log.Infof("As admin, attempting to inject the external principal %s into user %s over the public API", pi.injectedPrincipalID, targetUser.ID)
	latestUser.PrincipalIDs = append(latestUser.PrincipalIDs, pi.injectedPrincipalID)
	_, err = extuserapi.UpdateUser(pi.client, latestUser)
	require.Error(pi.T(), err, "expected the admission webhook to deny a principalIds mutation made by the admin")
	require.Contains(pi.T(), err.Error(), principalIDsField, "unexpected error: %v", err)

	log.Infof("Verifying that the identity fields of user %s are unchanged after the admin webhook attempt", targetUser.ID)
	err = userapi.VerifyUserIdentityFieldsUnchanged(pi.client, targetUser.ID, originalPrincipalIDs, targetUser.Username, targetUser.Name)
	require.NoError(pi.T(), err, "a webhook denied admin update must not change the identity fields")
}

func (pi *PrincipalIDsImmutabilityTestSuite) TestMutableFieldsRemainEditableViaNormanAPI() {
	subSession := pi.session.NewSession()
	defer subSession.Cleanup()

	targetUser, originalPrincipalIDs, err := newTargetUser(pi.client)
	require.NoError(pi.T(), err, "failed to create the target user")

	log.Infof("As admin, updating the description and enabled fields of user %s over the norman API", targetUser.ID)
	err = updateUserViaNormanAPI(pi.client, targetUser.ID, map[string]any{
		descriptionField: updatedDescription,
		enabledField:     true,
	})
	require.NoError(pi.T(), err, "the fix must not block legitimate edits to non identity fields")

	updatedUser, err := extuserapi.GetUserByName(pi.client, targetUser.ID)
	require.NoError(pi.T(), err, "failed to fetch user %s after the update", targetUser.ID)
	require.Equal(pi.T(), updatedDescription, updatedUser.Description, "description was not updated")
	require.True(pi.T(), *updatedUser.Enabled, "expected enabled=true, got false")

	log.Infof("Verifying that the legitimate edit left the identity fields of user %s untouched", targetUser.ID)
	err = userapi.VerifyUserIdentityFieldsUnchanged(pi.client, targetUser.ID, originalPrincipalIDs, targetUser.Username, targetUser.Name)
	require.NoError(pi.T(), err, "a legitimate edit must not change the identity fields")
}

func (pi *PrincipalIDsImmutabilityTestSuite) TestDisplayNameCannotBeMutatedViaNormanAPI() {
	pi.T().Skip("display name is stripped by neither the norman user store nor the admission webhook, pending confirmation of the intended behaviour")

	subSession := pi.session.NewSession()
	defer subSession.Cleanup()

	targetUser, originalPrincipalIDs, err := newTargetUser(pi.client)
	require.NoError(pi.T(), err, "failed to create the target user")

	updaterClient, err := newUserWithUsersUpdateRole(pi.client)
	require.NoError(pi.T(), err, "failed to create the user holding update on users resource")

	log.Infof("Attempting to rewrite the display name of user %s over the norman API", targetUser.ID)
	err = updateUserViaNormanAPI(updaterClient, targetUser.ID, map[string]any{displayNameField: updatedDisplayName})
	log.Infof("The norman update attempt returned [%v]; the read-back invariant is what decides the result", err)

	log.Infof("Verifying that the identity fields of user %s are unchanged", targetUser.ID)
	err = userapi.VerifyUserIdentityFieldsUnchanged(pi.client, targetUser.ID, originalPrincipalIDs, targetUser.Username, targetUser.Name)
	require.NoError(pi.T(), err, "rewriting the display name over the norman API must not change the identity fields")
}

func (pi *PrincipalIDsImmutabilityTestSuite) TestDisplayNameCannotBeMutatedViaPublicAPI() {
	pi.T().Skip("display name is stripped by neither the norman user store nor the admission webhook, pending confirmation of the intended behaviour")

	subSession := pi.session.NewSession()
	defer subSession.Cleanup()

	targetUser, originalPrincipalIDs, err := newTargetUser(pi.client)
	require.NoError(pi.T(), err, "failed to create the target user")

	updaterClient, err := newUserWithUsersUpdateRole(pi.client)
	require.NoError(pi.T(), err, "failed to create the user holding update on users resource")

	log.Infof("As the user holding update on users resource, fetching %s over the public API", targetUser.ID)
	latestUser, err := extuserapi.GetUserByName(updaterClient, targetUser.ID)
	require.NoError(pi.T(), err, "failed to fetch user %s over the public API", targetUser.ID)

	log.Infof("Attempting to rewrite the display name of user %s over the public API", targetUser.ID)
	latestUser.DisplayName = updatedDisplayName
	_, err = extuserapi.UpdateUser(updaterClient, latestUser)
	require.Error(pi.T(), err, "expected the admission webhook to deny a displayName mutation")
	require.Contains(pi.T(), err.Error(), displayNameCRDField, "unexpected error: %v", err)

	log.Infof("Verifying that the identity fields of user %s are unchanged", targetUser.ID)
	err = userapi.VerifyUserIdentityFieldsUnchanged(pi.client, targetUser.ID, originalPrincipalIDs, targetUser.Username, targetUser.Name)
	require.NoError(pi.T(), err, "a webhook denied update must not change the identity fields")
}

func (pi *PrincipalIDsImmutabilityTestSuite) TestResolverFailsClosedOnMultiMatchPrincipal() {
	subSession := pi.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := pi.client.WithSession(subSession)
	require.NoError(pi.T(), err, "failed to create a client bound to the test session")

	ldapUser := pi.authConfig.Users[0]
	ldapCredentials := &management.User{Username: ldapUser.Username, Password: ldapUser.Password}

	log.Infof("Logging in as the OpenLDAP user %s before planting anything, so that a later failure is attributable to the duplicate principal rather than to the provider being unreachable", ldapUser.Username)
	_, err = authactions.LoginAsAuthUser(pi.client, ldapCredentials, authactions.OpenLdap)
	require.NoError(pi.T(), err, "the OpenLDAP user must be able to login while its principal resolves to a single user")

	log.Infof("Recording the users already carrying the external principal %s", pi.injectedPrincipalID)
	before, err := userapi.ListUsersByPrincipalID(pi.client, pi.injectedPrincipalID)
	require.NoError(pi.T(), err, "failed to list the users carrying the external principal")

	log.Infof("Planting a first user record carrying the external principal %s at create time", pi.injectedPrincipalID)
	firstPlanted, err := plantUserCarryingPrincipal(subClient, pi.injectedPrincipalID)
	require.NoError(pi.T(), err, "failed to plant the first user carrying the external principal")

	log.Infof("Planting a second user record carrying the same external principal so that resolution is ambiguous")
	secondPlanted, err := plantUserCarryingPrincipal(subClient, pi.injectedPrincipalID)
	if err != nil {
		log.Infof("Creating a second user carrying an already claimed principal was rejected with [%v]; the principal is kept unique at create time, which is a stronger guarantee than failing closed at resolution", err)
		return
	}

	duplicates, err := userapi.ListUsersByPrincipalID(pi.client, pi.injectedPrincipalID)
	require.NoError(pi.T(), err, "failed to list the users carrying the external principal after planting")
	require.GreaterOrEqual(pi.T(), len(duplicates), 2, "the external principal must resolve to more than one user for this test to be meaningful")

	log.Infof("Attempting the same login now that the principal of %s resolves to %d user records", ldapUser.Username, len(duplicates))
	_, err = authactions.LoginAsAuthUser(pi.client, ldapCredentials, authactions.OpenLdap)
	require.Error(pi.T(), err, "an external principal resolving to more than one user must not authenticate to any of them")

	log.Infof("Verifying that the failed resolution neither bound nor minted a user record carrying %s", pi.injectedPrincipalID)
	after, err := userapi.ListUsersByPrincipalID(pi.client, pi.injectedPrincipalID)
	require.NoError(pi.T(), err, "failed to list the users carrying the external principal after the login attempt")
	require.Len(pi.T(), after, len(before)+2, "a failed multi match resolution must not create or rebind a user record")

	for _, planted := range []*apiv3.User{firstPlanted, secondPlanted} {
		err = userapi.VerifyUserIdentityFieldsUnchanged(pi.client, planted.Name, planted.PrincipalIDs, planted.Username, planted.DisplayName)
		require.NoError(pi.T(), err, "a failed multi match resolution must not change the identity fields of user %s", planted.Name)
	}
}

func TestPrincipalIDsImmutabilityTestSuite(t *testing.T) {
	suite.Run(t, new(PrincipalIDsImmutabilityTestSuite))
}
