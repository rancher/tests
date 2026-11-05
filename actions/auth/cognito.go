package auth

import (
	"fmt"

	"github.com/rancher/shepherd/clients/cognito"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/auth/oidcprovider"
	authext "github.com/rancher/shepherd/extensions/auth"
	oidcext "github.com/rancher/shepherd/extensions/auth/oidcprovider"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/sirupsen/logrus"
)

const (
	CognitoGroupsClaim = "cognito:groups"

	cognitoAdminPrefix      = "rancher-cognito-admin"
	cognitoGroupPrefix      = "rancher-cognito-group"
	cognitoMemberPrefix     = "rancher-cognito-member"
	cognitoOutsiderPrefix   = "rancher-cognito-outsider"
	cognitoUserPassword     = "CognitoTestPassw0rd!"
	cognitoGroupMemberCount = 2
	cognitoCallbackPath     = "/verify-auth"
	cognitoPoolPrefix       = "rancher-auth-automation"
	cognitoPasswordLength   = 12
)

var cognitoScopes = []string{"openid", "profile", "email"}

// CognitoFixture holds the user pool, accounts and groups an AWS Cognito run is built on
type CognitoFixture struct {
	Admin            User
	AdminPrincipalID string
	AuthInput        *ExternalAuthConfig
	ClientID         string
	UserPoolID       string
}

// NewCognitoClient constructs a Cognito administration client from the Cognito config key
func NewCognitoClient(testSession *session.Session) (*cognito.Client, error) {
	return cognito.NewClientFromConfigKey(oidcprovider.Cognito.ConfigKey, testSession)
}

// SetupCognito builds the user pool, the Rancher app client, and the accounts and groups a run needs
func SetupCognito(client *rancher.Client, cognitoClient *cognito.Client) (*CognitoFixture, error) {
	rancherAPIHost := rancherAPIHostFromConfig(client)
	if rancherAPIHost == "" {
		return nil, fmt.Errorf("the rancher config names no host, the identity provider redirects back to it so " +
			"it must be set")
	}

	rancherURL := rancherAPIHost + cognitoCallbackPath

	poolName := cognitoClient.Config.UserPoolName
	generatedPoolName := ""

	if poolName == "" {
		generatedPoolName = namegenerator.AppendRandomString(cognitoPoolPrefix)
		poolName = generatedPoolName
	}

	logrus.Infof("Creating the Cognito user pool %s Rancher signs in through", poolName)

	userPoolID, err := cognitoClient.CreateUserPool(poolName, &cognito.PasswordPolicy{
		MinimumLength:    cognitoPasswordLength,
		RequireLowercase: true,
		RequireNumbers:   true,
		RequireSymbols:   true,
		RequireUppercase: true,
	})
	if err != nil {
		return nil, err
	}

	domainPrefix := cognitoClient.Config.DomainPrefix
	if domainPrefix == "" {
		domainPrefix = generatedPoolName
	}

	if domainPrefix == "" {
		domainPrefix = namegenerator.AppendRandomString(cognitoPoolPrefix)
	}

	logrus.Infof("Creating the hosted UI domain %s the sign-in form is served from", domainPrefix)

	if err := cognitoClient.CreateUserPoolDomain(userPoolID, domainPrefix); err != nil {
		return nil, err
	}

	appClient, err := cognitoClient.CreateUserPoolClient(userPoolID, poolName, []string{rancherURL},
		[]string{rancherAPIHost}, cognitoScopes)
	if err != nil {
		return nil, err
	}

	endpoints, err := cognitoClient.EndpointsFor(userPoolID, cognitoClient.HostedUIDomain(domainPrefix))
	if err != nil {
		return nil, err
	}

	providerConfig := client.Auth.Cognito.Config
	if providerConfig.Users == nil {
		providerConfig.Users = new(oidcprovider.Users)
	}

	fixture := &CognitoFixture{
		AuthInput:  new(ExternalAuthConfig),
		ClientID:   appClient.ID,
		UserPoolID: userPoolID,
	}

	fixture.Admin, err = cognitoAdminAccount(cognitoClient, providerConfig)
	if err != nil {
		return nil, err
	}

	fixture.AdminPrincipalID = GetUserPrincipalID(Cognito, PrincipalNameOf(fixture.Admin), "", "")

	logrus.Info("Settling the group and accounts the Cognito access mode tests sign in with")

	if err := setupCognitoAccounts(cognitoClient, providerConfig, fixture.AuthInput); err != nil {
		return nil, err
	}

	providerConfig.ClientID = appClient.ID
	providerConfig.ClientSecret = appClient.Secret
	providerConfig.Issuer = endpoints.Issuer
	providerConfig.AuthEndpoint = endpoints.AuthEndpoint
	providerConfig.TokenEndpoint = endpoints.TokenEndpoint
	providerConfig.UserInfoEndpoint = endpoints.UserInfoEndpoint
	providerConfig.JWKSUrl = endpoints.JWKSUrl
	providerConfig.EndSessionEndpoint = endpoints.EndSessionEndpoint
	providerConfig.RancherURL = rancherURL
	providerConfig.GroupsClaim = CognitoGroupsClaim
	providerConfig.Group = fixture.AuthInput.Group

	providerConfig.Users.Admin = &oidcprovider.User{
		Username: fixture.Admin.Username,
		Password: fixture.Admin.Password,
	}

	return fixture, nil
}

// CognitoClaimValues returns the values Cognito puts in the given claim of the token it issues for a user
func CognitoClaimValues(client *rancher.Client, user User, claim string) ([]string, error) {
	claims, err := client.Auth.Cognito.IDTokenClaims(user.Username, user.Password)
	if err != nil {
		return nil, err
	}

	return oidcext.ClaimStrings(claims, claim), nil
}

// LogoutAll ends the session at the provider's identity provider and returns the address the browser is sent to
func LogoutAll(client *rancher.Client, providerName string) (string, error) {
	rancherAPIHost := rancherAPIHostFromConfig(client)
	if rancherAPIHost == "" {
		return "", fmt.Errorf("the rancher config names no host, the identity provider sends the browser back "+
			"to it after it ends the %s session, so it must be set", providerName)
	}

	return authext.LogoutAll(client, rancherAPIHost)
}

func enableCognito(client *rancher.Client) error {
	providerConfig := client.Auth.Cognito.Config
	if providerConfig.Users == nil || providerConfig.Users.Admin == nil {
		return fmt.Errorf("no Cognito account is available to enable the provider with, run SetupCognito first "+
			"or name one under users.admin in the %s config", oidcprovider.Cognito.ConfigKey)
	}

	return client.Auth.Cognito.EnableWithAdminLogin(
		providerConfig.Users.Admin.Username,
		providerConfig.Users.Admin.Password,
	)
}

func setupCognitoAccounts(cognitoClient *cognito.Client, providerConfig *oidcprovider.Config,
	authInput *ExternalAuthConfig) error {
	group := providerConfig.Group
	if group == "" {
		group = namegenerator.AppendRandomString(cognitoGroupPrefix)
	}

	if err := cognitoClient.CreateGroup(group); err != nil {
		return err
	}

	authInput.Group = group

	members, err := cognitoGroupMembers(cognitoClient, group, providerConfig.Users.Members, cognitoMemberPrefix,
		cognitoGroupMemberCount)
	if err != nil {
		return err
	}

	authInput.Users = members

	outsiders, err := cognitoOutsiders(cognitoClient, providerConfig.Users.Outsiders)
	if err != nil {
		return err
	}

	authInput.ExcludedUsers = outsiders

	return nil
}

func cognitoAdminAccount(cognitoClient *cognito.Client, providerConfig *oidcprovider.Config) (User, error) {
	admin := providerConfig.Users.Admin
	if admin != nil && admin.Username != "" && admin.Password != "" {
		logrus.Infof("Using the %s account named in the config to enable the provider", admin.Username)

		return cognitoUser(cognitoClient, admin.Username, admin.Password)
	}

	logrus.Info("Creating the Cognito account that enables the provider and becomes the Rancher administrator")

	return cognitoUser(cognitoClient, namegenerator.AppendRandomString(cognitoAdminPrefix), cognitoUserPassword)
}

func cognitoGroupMembers(cognitoClient *cognito.Client, group string, named []oidcprovider.User, prefix string,
	count int) ([]User, error) {
	if len(named) > 0 {
		members := make([]User, 0, len(named))

		for _, user := range named {
			created, err := cognitoUser(cognitoClient, user.Username, user.Password)
			if err != nil {
				return nil, err
			}

			if err := cognitoClient.AddUserToGroup(created.Username, group); err != nil {
				return nil, err
			}

			members = append(members, created)
		}

		return members, nil
	}

	members := make([]User, 0, count)

	for range count {
		created, err := cognitoUser(cognitoClient, namegenerator.AppendRandomString(prefix), cognitoUserPassword)
		if err != nil {
			return nil, err
		}

		if err := cognitoClient.AddUserToGroup(created.Username, group); err != nil {
			return nil, err
		}

		members = append(members, created)
	}

	return members, nil
}

func cognitoOutsiders(cognitoClient *cognito.Client, named []oidcprovider.User) ([]User, error) {
	if len(named) > 0 {
		outsiders := make([]User, 0, len(named))

		for _, user := range named {
			created, err := cognitoUser(cognitoClient, user.Username, user.Password)
			if err != nil {
				return nil, err
			}

			outsiders = append(outsiders, created)
		}

		return outsiders, nil
	}

	created, err := cognitoUser(cognitoClient, namegenerator.AppendRandomString(cognitoOutsiderPrefix),
		cognitoUserPassword)
	if err != nil {
		return nil, err
	}

	return []User{created}, nil
}

func cognitoUser(cognitoClient *cognito.Client, username, password string) (User, error) {
	if password == "" {
		password = cognitoUserPassword
	}

	existing, err := cognitoClient.GetUser(username)
	if err != nil {
		return User{}, err
	}

	if existing != nil {
		return User{Username: existing.Username, Password: password, PrincipalName: existing.Sub}, nil
	}

	email := fmt.Sprintf("%s@%s", username, cognitoClient.Config.UserEmailDomain)

	created, err := cognitoClient.CreateUser(username, email, password)
	if err != nil {
		return User{}, err
	}

	return User{Username: created.Username, Password: created.Password, PrincipalName: created.Sub}, nil
}
