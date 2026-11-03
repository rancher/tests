package auth

import (
	"fmt"

	"github.com/rancher/shepherd/clients/cognito"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/auth/oidcprovider"
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
	cognitoPasswordClasses  = "A9!"
	cognitoGroupMemberCount = 2
	cognitoCallbackPath     = "/verify-auth"
	cognitoPoolPrefix       = "rancher-auth-automation"
	cognitoPasswordLength   = 12
)

var cognitoScopes = []string{"openid", "profile", "email"}

// NewCognitoClient constructs a Cognito administration client from the Cognito config key
func NewCognitoClient(testSession *session.Session) (*cognito.Client, error) {
	return cognito.NewClientFromConfigKey(oidcprovider.Cognito.ConfigKey, testSession)
}

// SetupCognito builds the user pool, the Rancher app client and the accounts and groups a run needs, and returns the administrator and the access mode input
func SetupCognito(client *rancher.Client, cognitoClient *cognito.Client) (User, *ExternalAuthConfig, error) {
	rancherAPIHost := rancherAPIHostFromConfig(client)
	if rancherAPIHost == "" {
		return User{}, nil, fmt.Errorf("the rancher config names no host, the identity provider redirects back to it so " +
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
		return User{}, nil, err
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
		return User{}, nil, err
	}

	appClient, err := cognitoClient.CreateUserPoolClient(userPoolID, poolName, []string{rancherURL},
		[]string{rancherAPIHost}, cognitoScopes)
	if err != nil {
		return User{}, nil, err
	}

	endpoints, err := cognitoClient.EndpointsFor(userPoolID, cognitoClient.HostedUIDomain(domainPrefix))
	if err != nil {
		return User{}, nil, err
	}

	providerConfig := client.Auth.Cognito.Config
	if providerConfig.Users == nil {
		providerConfig.Users = new(oidcprovider.Users)
	}

	authInput := new(ExternalAuthConfig)

	admin, err := cognitoAdminAccount(cognitoClient, userPoolID, providerConfig)
	if err != nil {
		return User{}, nil, err
	}

	logrus.Info("Settling the group and accounts the Cognito access mode tests sign in with")

	if err := setupCognitoAccounts(cognitoClient, userPoolID, providerConfig, authInput); err != nil {
		return User{}, nil, err
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
	providerConfig.Group = authInput.Group

	providerConfig.Users.Admin = &oidcprovider.User{
		Username: admin.Username,
		Password: admin.Password,
	}

	return admin, authInput, nil
}

// CognitoClaimValues returns the values Cognito puts in the given claim of the token it issues for a user
func CognitoClaimValues(client *rancher.Client, user User, claim string) ([]string, error) {
	claims, err := client.Auth.Cognito.IDTokenClaims(user.Username, user.Password)
	if err != nil {
		return nil, err
	}

	return oidcext.ClaimStrings(claims, claim), nil
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

func setupCognitoAccounts(cognitoClient *cognito.Client, userPoolID string, providerConfig *oidcprovider.Config,
	authInput *ExternalAuthConfig) error {
	group := providerConfig.Group
	if group == "" {
		group = namegenerator.AppendRandomString(cognitoGroupPrefix)
	}

	if err := cognitoClient.CreateGroup(userPoolID, group); err != nil {
		return err
	}

	authInput.Group = group

	members, err := cognitoGroupMembers(cognitoClient, userPoolID, group, providerConfig.Users.Members, cognitoMemberPrefix,
		cognitoGroupMemberCount)
	if err != nil {
		return err
	}

	authInput.Users = members

	outsiders, err := cognitoOutsiders(cognitoClient, userPoolID, providerConfig.Users.Outsiders)
	if err != nil {
		return err
	}

	authInput.ExcludedUsers = outsiders

	return nil
}

func cognitoAdminAccount(cognitoClient *cognito.Client, userPoolID string, providerConfig *oidcprovider.Config) (User, error) {
	admin := providerConfig.Users.Admin
	if admin != nil && admin.Username != "" && admin.Password != "" {
		logrus.Infof("Using the %s account named in the config to enable the provider", admin.Username)

		return cognitoUser(cognitoClient, userPoolID, admin.Username, admin.Password)
	}

	logrus.Info("Creating the Cognito account that enables the provider and becomes the Rancher administrator")

	return cognitoUser(cognitoClient, userPoolID, namegenerator.AppendRandomString(cognitoAdminPrefix), "")
}

func cognitoGroupMembers(cognitoClient *cognito.Client, userPoolID, group string, named []oidcprovider.User, prefix string,
	count int) ([]User, error) {
	members, err := cognitoAccounts(cognitoClient, userPoolID, named, prefix, count)
	if err != nil {
		return nil, err
	}

	for _, member := range members {
		if err := cognitoClient.AddUserToGroup(userPoolID, member.Username, group); err != nil {
			return nil, err
		}
	}

	return members, nil
}

func cognitoOutsiders(cognitoClient *cognito.Client, userPoolID string, named []oidcprovider.User) ([]User, error) {
	return cognitoAccounts(cognitoClient, userPoolID, named, cognitoOutsiderPrefix, 1)
}

func cognitoAccounts(cognitoClient *cognito.Client, userPoolID string, named []oidcprovider.User, prefix string, count int) ([]User, error) {
	if len(named) == 0 {
		named = make([]oidcprovider.User, count)
		for index := range named {
			named[index].Username = namegenerator.AppendRandomString(prefix)
		}
	}

	accounts := make([]User, 0, len(named))

	for _, user := range named {
		created, err := cognitoUser(cognitoClient, userPoolID, user.Username, user.Password)
		if err != nil {
			return nil, err
		}

		accounts = append(accounts, created)
	}

	return accounts, nil
}

func cognitoUser(cognitoClient *cognito.Client, userPoolID, username, password string) (User, error) {
	if password == "" {
		password = namegenerator.RandStringLower(cognitoPasswordLength) + cognitoPasswordClasses
	}

	email := fmt.Sprintf("%s@%s", username, cognitoClient.Config.UserEmailDomain)

	created, err := cognitoClient.CreateUser(userPoolID, username, email, password)
	if err != nil {
		return User{}, err
	}

	return User{Username: created.Username, Password: created.Password, PrincipalName: created.Sub}, nil
}
