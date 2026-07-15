//go:build (validation || infra.any || cluster.any || sanity) && !stress

package tokens

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	fv3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/pkg/session"
	tokenapi "github.com/rancher/tests/actions/kubeapi/tokens"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	initialTokenDesc = "my-token"
	updatedTokenDesc = "changed-token"
)

type TokenTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (t *TokenTestSuite) TearDownSuite() {
	t.session.Cleanup()
}

func (t *TokenTestSuite) SetupSuite() {
	testSession := session.NewSession()
	t.session = testSession

	client, err := rancher.NewClient("", t.session)
	require.NoError(t.T(), err)

	t.client = client
}

func (t *TokenTestSuite) TestPatchToken() {
	tokenToCreate := &fv3.Token{Description: initialTokenDesc}
	createdToken, err := t.client.Management.Token.Create(tokenToCreate)
	require.NoError(t.T(), err)

	assert.Equal(t.T(), initialTokenDesc, createdToken.Description)

	patchedToken, err := tokenapi.PatchToken(t.client, createdToken.Name, "replace", "/description", updatedTokenDesc)
	require.NoError(t.T(), err)

	assert.Equal(t.T(), updatedTokenDesc, patchedToken.Description)

	if len(patchedToken.GroupPrincipals) > 0 {
		assert.NotEmpty(t.T(), patchedToken.GroupPrincipals)
	}
}

func TestTokenTestSuite(t *testing.T) {
	suite.Run(t, new(TokenTestSuite))
}
