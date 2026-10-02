package auth

import (
	"fmt"
	"net/url"

	"github.com/rancher/shepherd/clients/keycloak"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/auth/oidcprovider"
	oidcext "github.com/rancher/shepherd/extensions/auth/oidcprovider"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/sirupsen/logrus"
)

const (
	keycloakOIDCProtocol     = "openid-connect"
	keycloakOIDCClientPrefix = "rancher-oidc-automation"
	keycloakOIDCClientName   = "rancher-oidc-automation"

	keycloakOIDCGroupsMapperName      = "groups"
	keycloakOIDCGroupMembershipMapper = "oidc-group-membership-mapper"
	keycloakOIDCClaimNameSetting      = "claim.name"
	keycloakOIDCIDTokenClaimSetting   = "id.token.claim"
	keycloakOIDCAccessTokenSetting    = "access.token.claim"
	keycloakOIDCUserInfoClaimSetting  = "userinfo.token.claim"

	KeycloakOIDCFullGroupPathClaim = "full_group_path"
	KeycloakOIDCGroupsClaim        = "groups"

	keycloakRealmManagementClient = "realm-management"
	keycloakViewUsersRole         = "view-users"
	keycloakQueryGroupsRole       = "query-groups"

	keycloakOIDCAdminPrefix              = "rancher-oidc-admin"
	keycloakOIDCGroupPrefix              = "rancher-oidc-group"
	keycloakOIDCNestedGroupPrefix        = "rancher-oidc-nested-group"
	keycloakOIDCDoubleNestedGroupPrefix  = "rancher-oidc-double-nested-group"
	keycloakOIDCMemberPrefix             = "rancher-oidc-member"
	keycloakOIDCNestedMemberPrefix       = "rancher-oidc-nested-member"
	keycloakOIDCDoubleNestedMemberPrefix = "rancher-oidc-double-nested-member"
	keycloakOIDCOutsiderPrefix           = "rancher-oidc-outsider"
	keycloakOIDCUserSurname              = "oidc"
	keycloakOIDCUserPassword             = "OidcTestPassw0rd!"
	keycloakOIDCNestedMemberCount        = 1
	keycloakOIDCGroupMemberCount         = 2
)

// KeycloakOIDCFixture holds the accounts, groups and configuration a Keycloak OIDC run is built on
type KeycloakOIDCFixture struct {
	Admin            User
	AdminPrincipalID string
	AuthInput        *ExternalAuthConfig
	ClientID         string
}

// NewKeycloakOIDCClient constructs a Keycloak admin client from the Keycloak OIDC config key
func NewKeycloakOIDCClient(testSession *session.Session) (*keycloak.Client, error) {
	return keycloak.NewClientFromConfigKey(oidcprovider.KeycloakOIDC.ConfigKey, testSession)
}

// SetupKeycloakOIDC prepares the realm, the Rancher OpenID Connect client, and the accounts and groups a run needs
func SetupKeycloakOIDC(client *rancher.Client, keycloakClient *keycloak.Client) (*KeycloakOIDCFixture, error) {
	rancherAPIHost := rancherAPIHostFromConfig(client)
	if rancherAPIHost == "" {
		return nil, fmt.Errorf("the rancher config names no host, the identity provider redirects back to it so " +
			"it must be set")
	}

	created, err := keycloakClient.EnsureRealm()
	if err != nil {
		return nil, err
	}

	if created {
		logrus.Infof("Created the %s realm, which the Keycloak server did not have", keycloakClient.Realm())
	}

	discovery, err := keycloakClient.OIDCDiscovery()
	if err != nil {
		return nil, err
	}

	providerConfig := client.Auth.KeycloakOIDC.Config
	if providerConfig.Users == nil {
		providerConfig.Users = new(oidcprovider.Users)
	}

	rancherURL := rancherAPIHost + "/verify-auth"

	clientID, err := keycloakOIDCClientID(rancherAPIHost)
	if err != nil {
		return nil, err
	}

	logrus.Infof("Registering the Rancher OpenID Connect client %s in the %s realm", clientID,
		keycloakClient.Realm())

	registered, err := keycloakClient.ReplaceClient(newKeycloakOIDCClient(clientID, rancherAPIHost, rancherURL))
	if err != nil {
		return nil, err
	}

	clientSecret, err := keycloakClient.GetClientSecret(registered.ID)
	if err != nil {
		return nil, err
	}

	logrus.Infof("Granting the %s service account the realm rights a principal search is answered with",
		clientID)

	if err := grantKeycloakOIDCSearchRoles(keycloakClient, registered.ID); err != nil {
		return nil, err
	}

	fixture := &KeycloakOIDCFixture{AuthInput: new(ExternalAuthConfig), ClientID: clientID}

	fixture.Admin, err = keycloakOIDCAdminAccount(keycloakClient, providerConfig)
	if err != nil {
		return nil, err
	}

	fixture.AdminPrincipalID = GetUserPrincipalID(KeycloakOIDC, PrincipalNameOf(fixture.Admin), "", "")

	logrus.Info("Settling the group and accounts the Keycloak OIDC access mode tests sign in with")

	if err := setupKeycloakOIDCAccounts(keycloakClient, providerConfig, fixture.AuthInput); err != nil {
		return nil, err
	}

	providerConfig.ClientID = clientID
	providerConfig.ClientSecret = clientSecret
	providerConfig.Issuer = discovery.Issuer
	providerConfig.AuthEndpoint = discovery.AuthorizationEndpoint
	providerConfig.TokenEndpoint = discovery.TokenEndpoint
	providerConfig.UserInfoEndpoint = discovery.UserInfoEndpoint
	providerConfig.JWKSUrl = discovery.JWKSURI
	providerConfig.EndSessionEndpoint = discovery.EndSessionEndpoint
	providerConfig.RancherURL = rancherURL
	providerConfig.GroupsClaim = KeycloakOIDCGroupsClaim
	providerConfig.GroupSearchEnabled = keycloak.Pointer(true)
	providerConfig.ClientAuthenticatedSearch = true
	providerConfig.Group = fixture.AuthInput.Group
	providerConfig.NestedGroup = fixture.AuthInput.NestedGroup
	providerConfig.DoubleNestedGroup = fixture.AuthInput.DoubleNestedGroup

	providerConfig.Users.Admin = &oidcprovider.User{
		Username: fixture.Admin.Username,
		Password: fixture.Admin.Password,
	}

	switch {
	case providerConfig.Certificate == "" && providerConfig.PrivateKey == "":
		logrus.Infof("Reading the certificate Keycloak serves, so Rancher accepts the connection it makes to %s",
			discovery.TokenEndpoint)

		if err := client.Auth.KeycloakOIDC.TrustIdentityProvider(); err != nil {
			return nil, err
		}
	case providerConfig.Certificate == "" || providerConfig.PrivateKey == "":
		return nil, fmt.Errorf("only one of certificate and privateKey is set under the %s config key, Rancher "+
			"trusts the certificate only when a matching private key is set alongside it, so set both to pin your "+
			"own bundle or neither to have the certificate Keycloak serves read for you",
			oidcprovider.KeycloakOIDC.ConfigKey)
	}

	return fixture, nil
}

func enableKeycloakOIDC(client *rancher.Client) error {
	providerConfig := client.Auth.KeycloakOIDC.Config
	if providerConfig.Users == nil || providerConfig.Users.Admin == nil {
		return fmt.Errorf("no Keycloak account is available to enable the provider with, run SetupKeycloakOIDC "+
			"first or name one under users.admin in the %s config", oidcprovider.KeycloakOIDC.ConfigKey)
	}

	return client.Auth.KeycloakOIDC.EnableWithAdminLogin(
		providerConfig.Users.Admin.Username,
		providerConfig.Users.Admin.Password,
	)
}

// KeycloakOIDCClaimValues returns the values Keycloak puts in the given claim of the token it issues for a user
func KeycloakOIDCClaimValues(client *rancher.Client, user User, claim string) ([]string, error) {
	claims, err := client.Auth.KeycloakOIDC.IDTokenClaims(user.Username, user.Password)
	if err != nil {
		return nil, err
	}

	return oidcext.ClaimStrings(claims, claim), nil
}

// SetKeycloakOIDCGroupClaim rewrites the groups mapper to emit the given claim, by path or by name, and returns a restore func
func SetKeycloakOIDCGroupClaim(keycloakClient *keycloak.Client, clientID, claimName string, fullPath bool) (func() error, error) {
	oidcClient, err := keycloakClient.GetClient(clientID)
	if err != nil {
		return nil, err
	}

	if oidcClient == nil {
		return nil, fmt.Errorf("the %s realm holds no client registered as %s, so its group mapper cannot be "+
			"rewritten", keycloakClient.Realm(), clientID)
	}

	mapper, err := keycloakClient.GetProtocolMapper(oidcClient.ID, keycloakOIDCGroupsMapperName)
	if err != nil {
		return nil, err
	}

	if mapper == nil {
		return nil, fmt.Errorf("the %s client carries no %s mapper, so its tokens name no group at all",
			clientID, keycloakOIDCGroupsMapperName)
	}

	if mapper.Config == nil {
		mapper.Config = map[string]string{}
	}

	previousClaim := mapper.Config[keycloakOIDCClaimNameSetting]
	previousFullPath := mapper.Config[keycloakFullPathSetting]

	logrus.Infof("Setting the %s mapper to emit the %s claim with %s set to %v",
		keycloakOIDCGroupsMapperName, claimName, keycloakFullPathSetting, fullPath)

	if err := writeKeycloakOIDCGroupClaim(keycloakClient, oidcClient.ID, mapper, claimName, fullPath); err != nil {
		return nil, err
	}

	return func() error {
		return writeKeycloakOIDCGroupClaim(keycloakClient, oidcClient.ID, mapper, previousClaim,
			previousFullPath == "true")
	}, nil
}

func writeKeycloakOIDCGroupClaim(keycloakClient *keycloak.Client, clientUUID string,
	mapper *keycloak.ProtocolMapperRepresentation, claimName string, fullPath bool) error {
	mapper.Config[keycloakOIDCClaimNameSetting] = claimName
	mapper.Config[keycloakFullPathSetting] = fmt.Sprintf("%t", fullPath)

	return keycloakClient.ReplaceProtocolMapper(clientUUID, mapper)
}

func grantKeycloakOIDCSearchRoles(keycloakClient *keycloak.Client, clientUUID string) error {
	serviceAccount, err := keycloakClient.GetServiceAccountUser(clientUUID)
	if err != nil {
		return err
	}

	realmManagement, err := keycloakClient.GetClient(keycloakRealmManagementClient)
	if err != nil {
		return err
	}

	if realmManagement == nil {
		return fmt.Errorf("the %s realm holds no %s client, so the rights that let Rancher answer a principal "+
			"search cannot be granted", keycloakClient.Realm(), keycloakRealmManagementClient)
	}

	roles := make([]keycloak.RoleRepresentation, 0, 2)

	for _, name := range []string{keycloakViewUsersRole, keycloakQueryGroupsRole} {
		role, err := keycloakClient.GetClientRole(realmManagement.ID, name)
		if err != nil {
			return err
		}

		if role == nil {
			return fmt.Errorf("the %s client in realm %s defines no %s role, which a principal search needs",
				keycloakRealmManagementClient, keycloakClient.Realm(), name)
		}

		roles = append(roles, *role)
	}

	return keycloakClient.AddClientRolesToUser(serviceAccount.ID, realmManagement.ID, roles)
}

func newKeycloakOIDCClient(clientID, rancherAPIHost, rancherURL string) *keycloak.ClientRepresentation {
	return &keycloak.ClientRepresentation{
		ClientID:               clientID,
		Name:                   keycloakOIDCClientName,
		Protocol:               keycloakOIDCProtocol,
		Enabled:                keycloak.Pointer(true),
		PublicClient:           keycloak.Pointer(false),
		StandardFlowEnabled:    keycloak.Pointer(true),
		ServiceAccountsEnabled: keycloak.Pointer(true),
		RedirectURIs:           []string{rancherURL, rancherAPIHost + "/*"},
		WebOrigins:             []string{rancherAPIHost},
		RootURL:                rancherAPIHost,
		BaseURL:                rancherAPIHost,
		ProtocolMappers: []keycloak.ProtocolMapperRepresentation{
			{
				Name:           keycloakOIDCGroupsMapperName,
				Protocol:       keycloakOIDCProtocol,
				ProtocolMapper: keycloakOIDCGroupMembershipMapper,
				Config: map[string]string{
					keycloakOIDCClaimNameSetting:     KeycloakOIDCGroupsClaim,
					keycloakFullPathSetting:          "false",
					keycloakOIDCIDTokenClaimSetting:  "true",
					keycloakOIDCAccessTokenSetting:   "true",
					keycloakOIDCUserInfoClaimSetting: "true",
				},
			},
		},
	}
}

func keycloakOIDCAdminAccount(keycloakClient *keycloak.Client, providerConfig *oidcprovider.Config) (User, error) {
	admin := providerConfig.Users.Admin
	if admin != nil && admin.Username != "" && admin.Password != "" {
		logrus.Infof("Using the %s account named in the config to enable the provider", admin.Username)

		return keycloakOIDCUser(keycloakClient, admin.Username, admin.Password)
	}

	logrus.Info("Creating the Keycloak account that enables the provider and becomes the Rancher administrator")

	created, _, err := createKeycloakOIDCUser(keycloakClient, keycloakOIDCAdminPrefix)

	return created, err
}

func setupKeycloakOIDCAccounts(keycloakClient *keycloak.Client, providerConfig *oidcprovider.Config,
	authInput *ExternalAuthConfig) error {
	group, err := keycloakOIDCGroup(keycloakClient, providerConfig)
	if err != nil {
		return err
	}

	authInput.Group = group.Name

	authInput.Users, err = keycloakOIDCGroupMembers(keycloakClient, group, providerConfig.Users.Members,
		keycloakOIDCMemberPrefix, keycloakOIDCGroupMemberCount)
	if err != nil {
		return err
	}

	nestedGroup, err := keycloakChildGroup(keycloakClient, group, providerConfig.NestedGroup,
		keycloakOIDCNestedGroupPrefix)
	if err != nil {
		return err
	}

	authInput.NestedGroup = nestedGroup.Name

	authInput.NestedUsers, err = keycloakOIDCGroupMembers(keycloakClient, nestedGroup, providerConfig.Users.NestedMembers,
		keycloakOIDCNestedMemberPrefix, keycloakOIDCNestedMemberCount)
	if err != nil {
		return err
	}

	doubleNestedGroup, err := keycloakChildGroup(keycloakClient, nestedGroup, providerConfig.DoubleNestedGroup,
		keycloakOIDCDoubleNestedGroupPrefix)
	if err != nil {
		return err
	}

	authInput.DoubleNestedGroup = doubleNestedGroup.Name

	authInput.DoubleNestedUsers, err = keycloakOIDCGroupMembers(keycloakClient, doubleNestedGroup,
		providerConfig.Users.DoubleNestedMembers, keycloakOIDCDoubleNestedMemberPrefix, keycloakOIDCNestedMemberCount)
	if err != nil {
		return err
	}

	namedOutsiders := providerConfig.Users.Outsiders
	if len(namedOutsiders) > 0 {
		authInput.ExcludedUsers, err = keycloakOIDCNamedUsers(keycloakClient, namedOutsiders)
		if err != nil {
			return err
		}
	} else {
		outsider, _, err := createKeycloakOIDCUser(keycloakClient, keycloakOIDCOutsiderPrefix)
		if err != nil {
			return err
		}

		authInput.ExcludedUsers = append(authInput.ExcludedUsers, outsider)
	}

	tiers := []keycloakFixtureTier{
		{
			description:     "the allowed group",
			group:           group,
			groupFromConfig: providerConfig.Group != "",
			users:           authInput.Users,
			usersFromConfig: len(providerConfig.Users.Members) > 0,
		},
		{
			description:     "the group nested one deep",
			group:           nestedGroup,
			groupFromConfig: providerConfig.NestedGroup != "",
			users:           authInput.NestedUsers,
			usersFromConfig: len(providerConfig.Users.NestedMembers) > 0,
			forbidden:       []*keycloak.GroupRepresentation{group},
		},
		{
			description:     "the group nested two deep",
			group:           doubleNestedGroup,
			groupFromConfig: providerConfig.DoubleNestedGroup != "",
			users:           authInput.DoubleNestedUsers,
			usersFromConfig: len(providerConfig.Users.DoubleNestedMembers) > 0,
			forbidden:       []*keycloak.GroupRepresentation{group, nestedGroup},
		},
		{
			description:     "the accounts in no group",
			users:           authInput.ExcludedUsers,
			usersFromConfig: len(namedOutsiders) > 0,
			forbidden:       []*keycloak.GroupRepresentation{group, nestedGroup, doubleNestedGroup},
		},
	}

	if err := verifyKeycloakFixture(keycloakClient, tiers, oidcprovider.KeycloakOIDC.ConfigKey); err != nil {
		return err
	}

	logKeycloakFixture(tiers)

	return nil
}

func keycloakOIDCGroup(keycloakClient *keycloak.Client, providerConfig *oidcprovider.Config) (*keycloak.GroupRepresentation, error) {
	if providerConfig.Group == "" {
		return keycloakClient.CreateGroup(namegenerator.AppendRandomString(keycloakOIDCGroupPrefix))
	}

	group, err := keycloakClient.GetGroup(providerConfig.Group)
	if err != nil {
		return nil, err
	}

	if group == nil {
		return nil, fmt.Errorf("the %s realm holds no group named %s, name one it has under group in the %s "+
			"config or leave that out to have a group created",
			keycloakClient.Realm(), providerConfig.Group, oidcprovider.KeycloakOIDC.ConfigKey)
	}

	logrus.Infof("Using the %s group named in the config", group.Name)

	return group, nil
}

func keycloakOIDCGroupMembers(keycloakClient *keycloak.Client, group *keycloak.GroupRepresentation,
	named []oidcprovider.User, prefix string, count int) ([]User, error) {
	if len(named) > 0 {
		return keycloakOIDCNamedUsers(keycloakClient, named)
	}

	members := make([]User, 0, count)

	for range count {
		member, account, err := createKeycloakOIDCUser(keycloakClient, prefix)
		if err != nil {
			return nil, err
		}

		if err := keycloakClient.AddUserToGroup(account.ID, group.ID); err != nil {
			return nil, err
		}

		members = append(members, member)
	}

	return members, nil
}

func keycloakOIDCNamedUsers(keycloakClient *keycloak.Client, named []oidcprovider.User) ([]User, error) {
	users := make([]User, 0, len(named))

	for _, entry := range named {
		user, err := keycloakOIDCUser(keycloakClient, entry.Username, entry.Password)
		if err != nil {
			return nil, err
		}

		users = append(users, user)
	}

	return users, nil
}

func keycloakOIDCUser(keycloakClient *keycloak.Client, username, password string) (User, error) {
	if username == "" || password == "" {
		return User{}, fmt.Errorf("an account named in the %s config is missing its username or password, "+
			"both are needed to sign it in", oidcprovider.KeycloakOIDC.ConfigKey)
	}

	existing, err := keycloakClient.GetUser(username)
	if err != nil {
		return User{}, err
	}

	if existing == nil {
		return User{}, fmt.Errorf("the %s realm holds no account named %s, name one it has in the %s config "+
			"or leave the entry out to have accounts created",
			keycloakClient.Realm(), username, oidcprovider.KeycloakOIDC.ConfigKey)
	}

	return keycloakOIDCUserFrom(existing, password), nil
}

func keycloakOIDCUserFrom(account *keycloak.UserRepresentation, password string) User {
	return User{Username: account.Username, Password: password, PrincipalName: account.ID}
}

func createKeycloakOIDCUser(keycloakClient *keycloak.Client, prefix string) (User, *keycloak.UserRepresentation, error) {
	name := namegenerator.AppendRandomString(prefix)
	email := name + "@" + keycloakClient.Config.UserEmailDomain

	account, err := keycloakClient.CreateUser(&keycloak.UserRepresentation{
		Username:      email,
		Email:         email,
		FirstName:     name,
		LastName:      keycloakOIDCUserSurname,
		Enabled:       keycloak.Pointer(true),
		EmailVerified: keycloak.Pointer(true),
		Credentials: []keycloak.CredentialRepresentation{{
			Type:      "password",
			Value:     keycloakOIDCUserPassword,
			Temporary: keycloak.Pointer(false),
		}},
	})
	if err != nil {
		return User{}, nil, err
	}

	return keycloakOIDCUserFrom(account, keycloakOIDCUserPassword), account, nil
}

func keycloakOIDCClientID(rancherAPIHost string) (string, error) {
	parsed, err := url.Parse(rancherAPIHost)
	if err != nil {
		return "", fmt.Errorf("parsing the Rancher host %s to name the OpenID Connect client after it: %w", rancherAPIHost, err)
	}

	if parsed.Hostname() == "" {
		return "", fmt.Errorf("the Rancher host %s names no hostname to tie the OpenID Connect client to", rancherAPIHost)
	}

	return fmt.Sprintf("%s-%s", keycloakOIDCClientPrefix, parsed.Hostname()), nil
}
