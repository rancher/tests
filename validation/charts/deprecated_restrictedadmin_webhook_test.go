//go:build (2.8 || 2.9 || 2.10) && validation

package charts

import (
	"testing"

	"github.com/rancher/shepherd/extensions/users"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	restrictedAdmin = "restricted-admin"
)

func (w *WebhookTestSuite) TestWebhookEscalationCheck() {
	w.Run("Verify escalation check", func() {
		newUser, err := users.CreateUserWithRole(w.client, users.UserConfig(), restrictedAdmin)
		require.NoError(w.T(), err)
		w.T().Logf("Created user: %v", newUser.Name)

		restrictedAdminClient, err := w.client.AsUser(newUser)
		require.NoError(w.T(), err)

		getAdminRole, err := restrictedAdminClient.Management.GlobalRole.ByID(admin)
		require.NoError(w.T(), err)
		updatedAdminRole := *getAdminRole
		updatedAdminRole.NewUserDefault = true

		_, err = restrictedAdminClient.Management.GlobalRole.Update(getAdminRole, updatedAdminRole)
		require.Error(w.T(), err)
		errMessage := "admission webhook \"rancher.cattle.io.globalroles.management.cattle.io\" denied the request"
		assert.Contains(w.T(), err.Error(), errMessage)
	})
}

func TestWebhookRATestSuite(t *testing.T) {
	suite.Run(t, new(WebhookTestSuite))
}
