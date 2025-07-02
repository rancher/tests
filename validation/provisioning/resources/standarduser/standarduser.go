package standarduser

import (
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/users"
	password "github.com/rancher/shepherd/extensions/users/passwordgenerator"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
)

// CreateStandardUser is a helper function that creates a standard user in the Rancher cluster.
func CreateStandardUser(client *rancher.Client) (*rancher.Client, error) {
	enabled := true
	testuser := namegen.AppendRandomString("testuser-")
	testpassword := password.GenerateUserPassword("testpass-")

	user := &management.User{
		Username: testuser,
		Password: testpassword,
		Name:     testuser,
		Enabled:  &enabled,
	}

	newUser, err := users.CreateUserWithRole(client, user, "user")
	if err != nil {
		return nil, err
	}

	newUser.Password = user.Password

	standardUserClient, err := client.AsUser(newUser)
	if err != nil {
		return nil, err
	}

	return standardUserClient, nil
}
