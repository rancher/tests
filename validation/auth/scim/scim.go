//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package scim

import (
	"context"
	"net/http"

	"github.com/rancher/shepherd/clients/rancher"
	scimclient "github.com/rancher/shepherd/clients/rancher/auth/scim"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/session"
	scimactions "github.com/rancher/tests/actions/scim"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// SCIMBaseSuite holds the state and helper methods shared by every per-provider
// SCIM test suite. Concrete provider suites (OpenLDAP, AzureAD, ...) embed this
// type and populate the fields in their own SetupSuite, then add any
// provider-specific fields alongside.
type SCIMBaseSuite struct {
	suite.Suite
	session  *session.Session
	client   *rancher.Client
	cluster  *v3.Cluster
	scim     *scimclient.Client
	provider string
}

func (b *SCIMBaseSuite) applyConfigMap(data map[string]string, expectedStatus int) {
	require.NoError(b.T(), scimactions.SetSCIMConfigMap(b.client, b.provider, data))
	require.NoError(b.T(), scimactions.WaitForSCIMEndpointStatus(b.scim, expectedStatus))
}

func (b *SCIMBaseSuite) restoreBaseline() {
	require.NoError(b.T(), scimactions.RestoreSCIMBaseline(b.client, b.scim, b.provider))
}

func (b *SCIMBaseSuite) observeSCIMThrottle(maxAttempts int) bool {
	return b.firstThrottledResponse(maxAttempts) != nil
}

func (b *SCIMBaseSuite) firstThrottledResponse(maxAttempts int) *scimclient.Response {
	for i := 0; i < maxAttempts; i++ {
		resp, err := b.scim.Users().List(nil)
		require.NoError(b.T(), err)
		if resp.StatusCode == http.StatusTooManyRequests {
			return resp
		}
	}
	return nil
}

func (b *SCIMBaseSuite) createUserWithCleanup(externalID string, active bool) (string, string) {
	userName, userID, err := scimactions.CreateSCIMUser(b.scim, externalID, active)
	require.NoError(b.T(), err)
	b.T().Cleanup(func() {
		_, _ = b.scim.Users().Delete(userID)
	})
	return userName, userID
}

func (b *SCIMBaseSuite) createGroupWithCleanup(externalID string) (string, string) {
	groupName, groupID, err := scimactions.CreateSCIMGroup(b.scim, externalID)
	require.NoError(b.T(), err)
	b.T().Cleanup(func() {
		_, _ = b.scim.Groups().Delete(groupID)
	})
	return groupName, groupID
}

func (b *SCIMBaseSuite) decodeJSONBody(resp *scimclient.Response) map[string]interface{} {
	var body map[string]interface{}
	require.NoError(b.T(), resp.DecodeJSON(&body))
	return body
}

func (b *SCIMBaseSuite) patchUser(userID, op, path string, value interface{}) *scimclient.Response {
	resp, err := b.scim.Users().Patch(userID, scimclient.PatchOp{
		Schemas: []string{scimclient.SCIMSchemaPatchOp},
		Operations: []scimclient.Operation{
			{Op: op, Path: path, Value: value},
		},
	})
	require.NoError(b.T(), err)
	return resp
}

func (b *SCIMBaseSuite) patchGroup(groupID, op, path string, value interface{}) *scimclient.Response {
	resp, err := b.scim.Groups().Patch(groupID, scimclient.PatchOp{
		Schemas: []string{scimclient.SCIMSchemaPatchOp},
		Operations: []scimclient.Operation{
			{Op: op, Path: path, Value: value},
		},
	})
	require.NoError(b.T(), err)
	return resp
}

func (b *SCIMBaseSuite) waitForRancherUserName(userID, expectedName string) error {
	return kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		// User.ByID may transiently 404 while the SCIM-created user propagates
		// to the management API; treat all errors as "not yet" and let the timeout decide.
		mgmtUser, getErr := b.client.Management.User.ByID(userID)
		if getErr != nil {
			return false, nil
		}
		return mgmtUser.Name == expectedName, nil
	})
}

// countSCIMCodes counts 200 and 429 responses in a SCIM result slice. Used by
// rate-limit tests to assert mixed-outcome bursts.
func countSCIMCodes(results []int) (ok, throttled int) {
	for _, code := range results {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusTooManyRequests:
			throttled++
		}
	}
	return ok, throttled
}