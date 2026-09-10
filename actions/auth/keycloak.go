package auth

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/rancher/shepherd/clients/keycloak"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/auth/saml"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/sirupsen/logrus"
)

const (
	keycloakSAMLMetadataPathFormat = "%s/v1-saml/keycloak/saml/metadata"
	keycloakSAMLACSPathFormat      = "%s/v1-saml/keycloak/saml/acs"

	keycloakSAMLProtocol   = "saml"
	keycloakSAMLClientName = "rancher-saml-automation"

	keycloakGroupsMapperName = "groups"
	keycloakFullPathSetting  = "full.path"

	keycloakUserPropertyMapper    = "saml-user-property-mapper"
	keycloakRoleListMapper        = "saml-role-list-mapper"
	keycloakGroupMembershipMapper = "saml-group-membership-mapper"
	keycloakURIAttributeFormat    = "urn:oasis:names:tc:SAML:2.0:attrname-format:uri"
	keycloakBasicAttributeFormat  = "Basic"

	keycloakAdminPrefix              = "rancher-saml-admin"
	keycloakGroupPrefix              = "rancher-saml-group"
	keycloakNestedGroupPrefix        = "rancher-saml-nested-group"
	keycloakDoubleNestedGroupPrefix  = "rancher-saml-double-nested-group"
	keycloakMemberPrefix             = "rancher-saml-member"
	keycloakNestedMemberPrefix       = "rancher-saml-nested-member"
	keycloakDoubleNestedMemberPrefix = "rancher-saml-double-nested-member"
	keycloakOutsiderPrefix           = "rancher-saml-outsider"
	keycloakUserSurname              = "saml"
	keycloakNestedMemberCount        = 1
	keycloakGroupMemberCount         = 2
	keycloakUserPassword             = "SamlTestPassw0rd!"
)

type keycloakAttributeField struct {
	field    string
	property string
	x500     string
}

var keycloakAttributeFields = []keycloakAttributeField{
	{field: "email", property: "email", x500: "urn:oid:1.2.840.113549.1.9.1"},
	{field: "givenName", property: "firstName", x500: "urn:oid:2.5.4.42"},
	{field: "surname", property: "lastName", x500: "urn:oid:2.5.4.4"},
	{field: "sn", property: "lastName"},
	{field: "username", property: "username"},
	{field: "uid", property: "username"},
}

var keycloakPredefinedFields = []string{"email", "givenName", "surname"}

// KeycloakSAMLFixture holds the accounts, groups and configuration a Keycloak SAML run is built on
type KeycloakSAMLFixture struct {
	Admin            User
	AdminPrincipalID string
	AuthInput        *SAMLAuthConfig
	EntityID         string
	RancherAPIHost   string
}

// NewKeycloakClient constructs a Keycloak admin client from the Keycloak SAML config key
func NewKeycloakClient(testSession *session.Session) (*keycloak.Client, error) {
	return keycloak.NewClientFromConfigKey(saml.KeycloakSAML.ConfigKey, testSession)
}

// SetupKeycloakSAML prepares the realm, the Rancher SAML client, and the accounts and groups a run needs
func SetupKeycloakSAML(client *rancher.Client, keycloakClient *keycloak.Client) (*KeycloakSAMLFixture, error) {
	rancherAPIHost := rancherAPIHostFromConfig(client)
	if rancherAPIHost == "" {
		return nil, fmt.Errorf("the rancher config names no host, Keycloak issues assertions for it so it " +
			"must be set")
	}

	entityID := fmt.Sprintf(keycloakSAMLMetadataPathFormat, rancherAPIHost)
	acsURL := fmt.Sprintf(keycloakSAMLACSPathFormat, rancherAPIHost)

	created, err := keycloakClient.EnsureRealm()
	if err != nil {
		return nil, err
	}

	if created {
		logrus.Infof("Created the %s realm, which the Keycloak server did not have", keycloakClient.Realm())
	}

	providerConfig := client.Auth.KeycloakSAML.Config
	if providerConfig.Users == nil {
		providerConfig.Users = new(saml.Users)
	}

	logrus.Infof("Registering the Rancher SAML client %s in the %s realm", entityID, keycloakClient.Realm())
	samlClient, err := newKeycloakSAMLClient(entityID, rancherAPIHost, acsURL, providerConfig)
	if err != nil {
		return nil, err
	}

	if _, err := keycloakClient.ReplaceClient(samlClient); err != nil {
		return nil, err
	}

	fixture := &KeycloakSAMLFixture{
		AuthInput:      new(SAMLAuthConfig),
		EntityID:       entityID,
		RancherAPIHost: rancherAPIHost,
	}

	fixture.Admin, err = keycloakSAMLAdmin(keycloakClient, providerConfig)
	if err != nil {
		return nil, err
	}

	fixture.AdminPrincipalID = GetUserPrincipalID(KeycloakSAML, PrincipalNameOf(fixture.Admin), "", "")

	logrus.Infof("Settling the group and accounts the %s access mode tests sign in with", KeycloakSAML)
	if err := setupKeycloakSAMLAccounts(keycloakClient, providerConfig, fixture.AuthInput); err != nil {
		return nil, err
	}

	descriptor, err := keycloakClient.SAMLDescriptor()
	if err != nil {
		return nil, err
	}

	providerConfig.RancherAPIHost = rancherAPIHost
	providerConfig.EntityID = entityID
	providerConfig.IDPMetadataContent = descriptor
	providerConfig.Group = fixture.AuthInput.Group
	providerConfig.NestedGroup = fixture.AuthInput.NestedGroup
	providerConfig.DoubleNestedGroup = fixture.AuthInput.DoubleNestedGroup

	providerConfig.Users.Admin = &saml.User{
		Username: fixture.Admin.Username,
		Password: fixture.Admin.Password,
	}

	return fixture, nil
}

func enableKeycloakSAML(client *rancher.Client) error {
	providerConfig := client.Auth.KeycloakSAML.Config
	if providerConfig.Users == nil || providerConfig.Users.Admin == nil {
		return fmt.Errorf("no Keycloak account is available to enable the provider with, run SetupKeycloakSAML "+
			"first or name one under users.admin in the %s config", saml.KeycloakSAML.ConfigKey)
	}

	return client.Auth.KeycloakSAML.EnableWithAdminLogin(
		providerConfig.Users.Admin.Username,
		providerConfig.Users.Admin.Password,
	)
}

func keycloakSAMLAdmin(keycloakClient *keycloak.Client, providerConfig *saml.Config) (User, error) {
	admin := providerConfig.Users.Admin
	if admin != nil && admin.Username != "" && admin.Password != "" {
		logrus.Infof("Using the %s account named in the config to enable the provider", admin.Username)

		return keycloakSAMLUser(keycloakClient, admin.Username, admin.Password, providerConfig.UIDField)
	}

	logrus.Info("Creating the Keycloak account that enables the provider and becomes the Rancher administrator")

	created, _, err := createKeycloakSAMLUser(keycloakClient, keycloakAdminPrefix, providerConfig.UIDField)

	return created, err
}

func setupKeycloakSAMLAccounts(keycloakClient *keycloak.Client, providerConfig *saml.Config, authInput *SAMLAuthConfig) error {
	group, err := keycloakSAMLGroup(keycloakClient, providerConfig)
	if err != nil {
		return err
	}

	authInput.Group = group.Name

	authInput.Users, err = keycloakSAMLGroupMembers(keycloakClient, group, providerConfig.Users.Members,
		keycloakMemberPrefix, providerConfig.UIDField, keycloakGroupMemberCount)
	if err != nil {
		return err
	}

	nestedGroup, err := keycloakSAMLChildGroup(keycloakClient, group, providerConfig.NestedGroup, keycloakNestedGroupPrefix)
	if err != nil {
		return err
	}

	authInput.NestedGroup = nestedGroup.Name

	authInput.NestedUsers, err = keycloakSAMLGroupMembers(keycloakClient, nestedGroup, providerConfig.Users.NestedMembers,
		keycloakNestedMemberPrefix, providerConfig.UIDField, keycloakNestedMemberCount)
	if err != nil {
		return err
	}

	doubleNestedGroup, err := keycloakSAMLChildGroup(keycloakClient, nestedGroup, providerConfig.DoubleNestedGroup, keycloakDoubleNestedGroupPrefix)
	if err != nil {
		return err
	}

	authInput.DoubleNestedGroup = doubleNestedGroup.Name

	authInput.DoubleNestedUsers, err = keycloakSAMLGroupMembers(keycloakClient, doubleNestedGroup, providerConfig.Users.DoubleNestedMembers,
		keycloakDoubleNestedMemberPrefix, providerConfig.UIDField, keycloakNestedMemberCount)
	if err != nil {
		return err
	}

	namedOutsiders := providerConfig.Users.Outsiders
	if len(namedOutsiders) > 0 {
		authInput.ExcludedUsers, err = keycloakSAMLNamedUsers(keycloakClient, namedOutsiders, providerConfig.UIDField)
		if err != nil {
			return err
		}
	} else {
		outsider, _, err := createKeycloakSAMLUser(keycloakClient, keycloakOutsiderPrefix, providerConfig.UIDField)
		if err != nil {
			return err
		}

		authInput.ExcludedUsers = append(authInput.ExcludedUsers, outsider)
	}

	tiers := []keycloakSAMLTier{
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

	if err := verifyKeycloakSAMLFixture(keycloakClient, tiers); err != nil {
		return err
	}

	logKeycloakSAMLFixture(tiers)

	return nil
}

type keycloakSAMLTier struct {
	description     string
	group           *keycloak.GroupRepresentation
	groupFromConfig bool
	users           []User
	usersFromConfig bool
	forbidden       []*keycloak.GroupRepresentation
}

func verifyKeycloakSAMLFixture(keycloakClient *keycloak.Client, tiers []keycloakSAMLTier) error {
	for _, tier := range tiers {
		for _, user := range tier.users {
			account, err := keycloakClient.GetUser(user.Username)
			if err != nil {
				return err
			}

			if account == nil {
				return fmt.Errorf("the %s realm no longer holds the account %s that %s is made up of",
					keycloakClient.Realm(), user.Username, tier.description)
			}

			memberships, err := keycloakClient.GetUserGroups(account.ID)
			if err != nil {
				return err
			}

			if tier.group != nil && !keycloakGroupsContain(memberships, tier.group.ID) {
				return fmt.Errorf("account %s does not belong to group %s, which %s needs it to: it belongs to %v. "+
					"Name an account that is a member under the %s config, or leave the entry out to have one created "+
					"and joined for the run",
					user.Username, tier.group.Path, tier.description, keycloakGroupPaths(memberships),
					saml.KeycloakSAML.ConfigKey)
			}

			for _, forbidden := range tier.forbidden {
				if keycloakGroupsContain(memberships, forbidden.ID) {
					return fmt.Errorf("account %s belongs to group %s as well as to %v, and %s must stay out of it. "+
						"Keycloak reports every group an account joins directly, so a binding on %s would reach this "+
						"account and the test proving it does not would pass for the wrong reason",
						user.Username, forbidden.Path, keycloakGroupPaths(memberships), tier.description, forbidden.Path)
				}
			}
		}
	}

	return nil
}

func logKeycloakSAMLFixture(tiers []keycloakSAMLTier) {
	for _, tier := range tiers {
		usernames := make([]string, 0, len(tier.users))
		for _, user := range tier.users {
			usernames = append(usernames, user.Username)
		}

		if tier.group == nil {
			logrus.Infof("Using %s: %v, %s", tier.description, usernames, keycloakFixtureOrigin(tier.usersFromConfig))

			continue
		}

		logrus.Infof("Using %s: %s, %s, holding %v, %s",
			tier.description, tier.group.Path, keycloakFixtureOrigin(tier.groupFromConfig),
			usernames, keycloakFixtureOrigin(tier.usersFromConfig))
	}
}

func keycloakFixtureOrigin(fromConfig bool) string {
	if fromConfig {
		return "named in the config"
	}

	return "created for this run"
}

func keycloakGroupsContain(memberships []keycloak.GroupRepresentation, groupID string) bool {
	return slices.ContainsFunc(memberships, func(membership keycloak.GroupRepresentation) bool {
		return membership.ID == groupID
	})
}

func keycloakGroupPaths(memberships []keycloak.GroupRepresentation) []string {
	paths := make([]string, 0, len(memberships))
	for _, membership := range memberships {
		paths = append(paths, membership.Path)
	}

	if len(paths) == 0 {
		return []string{"no groups at all"}
	}

	return paths
}

// KeycloakSAMLAssertionGroups returns the groups Keycloak sends in the assertion for the given user
func KeycloakSAMLAssertionGroups(client *rancher.Client, user User) ([]string, error) {
	assertion, err := client.Auth.KeycloakSAML.CaptureAssertion(user.Username, user.Password)
	if err != nil {
		return nil, err
	}

	return assertion.Details.Attribute(client.Auth.KeycloakSAML.Config.GroupsField), nil
}

// SetKeycloakSAMLGroupPathMode switches the groups mapper between full path and name and returns a restore func
func SetKeycloakSAMLGroupPathMode(keycloakClient *keycloak.Client, entityID string, fullPath bool) (func() error, error) {
	samlClient, err := keycloakClient.GetClient(entityID)
	if err != nil {
		return nil, err
	}

	if samlClient == nil {
		return nil, fmt.Errorf("the %s realm holds no client registered as %s, so its group mapper cannot be "+
			"rewritten", keycloakClient.Realm(), entityID)
	}

	mapper, err := keycloakClient.GetProtocolMapper(samlClient.ID, keycloakGroupsMapperName)
	if err != nil {
		return nil, err
	}

	if mapper == nil {
		return nil, fmt.Errorf("the %s client carries no %s mapper, so the assertion reports no group memberships "+
			"at all", entityID, keycloakGroupsMapperName)
	}

	if mapper.Config == nil {
		mapper.Config = map[string]string{}
	}

	previous := mapper.Config[keycloakFullPathSetting]

	naming := "name"
	if fullPath {
		naming = "path"
	}

	logrus.Infof("Setting %s to %v on the %s mapper, so the assertion names groups by %s",
		keycloakFullPathSetting, fullPath, keycloakGroupsMapperName, naming)

	if err := writeKeycloakGroupPathMode(keycloakClient, samlClient.ID, mapper, strconv.FormatBool(fullPath)); err != nil {
		return nil, err
	}

	return func() error {
		return writeKeycloakGroupPathMode(keycloakClient, samlClient.ID, mapper, previous)
	}, nil
}

func writeKeycloakGroupPathMode(keycloakClient *keycloak.Client, clientUUID string,
	mapper *keycloak.ProtocolMapperRepresentation, value string) error {
	mapper.Config[keycloakFullPathSetting] = value

	return keycloakClient.ReplaceProtocolMapper(clientUUID, mapper)
}

func keycloakSAMLGroupMembers(keycloakClient *keycloak.Client, group *keycloak.GroupRepresentation,
	named []saml.User, prefix, uidField string, count int) ([]User, error) {
	if len(named) > 0 {
		return keycloakSAMLNamedUsers(keycloakClient, named, uidField)
	}

	members := make([]User, 0, count)

	for range count {
		member, account, err := createKeycloakSAMLUser(keycloakClient, prefix, uidField)
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

func keycloakSAMLChildGroup(keycloakClient *keycloak.Client, parent *keycloak.GroupRepresentation,
	name, prefix string) (*keycloak.GroupRepresentation, error) {
	if name == "" {
		return keycloakClient.CreateChildGroup(parent.ID, namegenerator.AppendRandomString(prefix))
	}

	group, err := keycloakClient.GetChildGroup(parent.ID, name)
	if err != nil {
		return nil, err
	}

	if group == nil {
		return nil, fmt.Errorf("the %s realm holds no group named %s beneath group %s, name one that sits there "+
			"or leave it out to have one created",
			keycloakClient.Realm(), name, parent.Name)
	}

	logrus.Infof("Using the %s group named in the config, nested beneath %s", group.Name, parent.Name)

	return group, nil
}

func keycloakSAMLGroup(keycloakClient *keycloak.Client, providerConfig *saml.Config) (*keycloak.GroupRepresentation, error) {
	if providerConfig.Group == "" {
		return keycloakClient.CreateGroup(namegenerator.AppendRandomString(keycloakGroupPrefix))
	}

	group, err := keycloakClient.GetGroup(providerConfig.Group)
	if err != nil {
		return nil, err
	}

	if group == nil {
		return nil, fmt.Errorf("the %s realm holds no group named %s, name one it has under group in the %s "+
			"config or leave that out to have a group created",
			keycloakClient.Realm(), providerConfig.Group, saml.KeycloakSAML.ConfigKey)
	}

	logrus.Infof("Using the %s group named in the config", group.Name)

	return group, nil
}

func keycloakSAMLNamedUsers(keycloakClient *keycloak.Client, named []saml.User, uidField string) ([]User, error) {
	users := make([]User, 0, len(named))

	for _, entry := range named {
		user, err := keycloakSAMLUser(keycloakClient, entry.Username, entry.Password, uidField)
		if err != nil {
			return nil, err
		}

		users = append(users, user)
	}

	return users, nil
}

func keycloakSAMLUser(keycloakClient *keycloak.Client, username, password, uidField string) (User, error) {
	if username == "" || password == "" {
		return User{}, fmt.Errorf("an account named in the %s config is missing its username or password, "+
			"both are needed to sign it in", saml.KeycloakSAML.ConfigKey)
	}

	existing, err := keycloakClient.GetUser(username)
	if err != nil {
		return User{}, err
	}

	if existing == nil {
		return User{}, fmt.Errorf("the %s realm holds no account named %s, name one it has in the %s config "+
			"or leave the entry out to have accounts created",
			keycloakClient.Realm(), username, saml.KeycloakSAML.ConfigKey)
	}

	return keycloakSAMLUserFrom(existing, password, uidField)
}

func keycloakSAMLUserFrom(account *keycloak.UserRepresentation, password, uidField string) (User, error) {
	principalName, err := keycloakPrincipalName(account, uidField)
	if err != nil {
		return User{}, err
	}

	return User{Username: account.Username, Password: password, PrincipalName: principalName}, nil
}

func createKeycloakSAMLUser(keycloakClient *keycloak.Client, prefix, uidField string) (User, *keycloak.UserRepresentation, error) {
	name := namegenerator.AppendRandomString(prefix)
	email := name + "@" + keycloakClient.Config.UserEmailDomain

	account, err := keycloakClient.CreateUser(&keycloak.UserRepresentation{
		Username:      email,
		Email:         email,
		FirstName:     name,
		LastName:      keycloakUserSurname,
		Enabled:       keycloak.Pointer(true),
		EmailVerified: keycloak.Pointer(true),
		Credentials: []keycloak.CredentialRepresentation{{
			Type:      "password",
			Value:     keycloakUserPassword,
			Temporary: keycloak.Pointer(false),
		}},
	})
	if err != nil {
		return User{}, nil, err
	}

	user, err := keycloakSAMLUserFrom(account, keycloakUserPassword, uidField)
	if err != nil {
		return User{}, nil, err
	}

	return user, account, nil
}

func keycloakPrincipalName(user *keycloak.UserRepresentation, uidField string) (string, error) {
	attribute, err := findKeycloakAttributeField(uidField)
	if err != nil {
		return "", err
	}

	var name string

	switch attribute.property {
	case "email":
		name = user.Email
	case "firstName":
		name = user.FirstName
	case "lastName":
		name = user.LastName
	case "username":
		name = user.Username
	}

	if name == "" {
		return "", fmt.Errorf("the %s account has no %s, which uidField %s asks Rancher to identify it by",
			user.Username, attribute.property, uidField)
	}

	return name, nil
}

func newKeycloakSAMLClient(clientID, rancherAPIHost, acsURL string, providerConfig *saml.Config) (*keycloak.ClientRepresentation, error) {
	mappers, err := newKeycloakSAMLMappers(providerConfig)
	if err != nil {
		return nil, err
	}

	return &keycloak.ClientRepresentation{
		ClientID:           clientID,
		Name:               keycloakSAMLClientName,
		Protocol:           keycloakSAMLProtocol,
		Enabled:            keycloak.Pointer(true),
		RedirectURIs:       []string{acsURL},
		BaseURL:            rancherAPIHost,
		AdminURL:           acsURL,
		FrontchannelLogout: keycloak.Pointer(true),
		Attributes: map[string]string{
			"saml.force.post.binding":  "false",
			"saml.authnstatement":      "false",
			"saml.server.signature":    "true",
			"saml.assertion.signature": "true",
			"saml.client.signature":    "false",
			"saml_name_id_format":      "username",
		},
		ProtocolMappers: mappers,
	}, nil
}

func newKeycloakSAMLMappers(providerConfig *saml.Config) ([]keycloak.ProtocolMapperRepresentation, error) {
	mappers := []keycloak.ProtocolMapperRepresentation{
		{
			Name:           "role list",
			Protocol:       keycloakSAMLProtocol,
			ProtocolMapper: keycloakRoleListMapper,
			Config: map[string]string{
				"attribute.nameformat": keycloakBasicAttributeFormat,
				"attribute.name":       "Role",
				"single":               "false",
			},
		},
		{
			Name:           keycloakGroupsMapperName,
			Protocol:       keycloakSAMLProtocol,
			ProtocolMapper: keycloakGroupMembershipMapper,
			Config: map[string]string{
				"attribute.nameformat":  keycloakBasicAttributeFormat,
				"attribute.name":        providerConfig.GroupsField,
				keycloakFullPathSetting: "false",
				"single":                "false",
			},
		},
	}

	requested := slices.Clone(keycloakPredefinedFields)
	requested = append(requested,
		providerConfig.DisplayNameField,
		providerConfig.UserNameField,
		providerConfig.UIDField,
	)

	registered := map[string]bool{}

	for _, field := range requested {
		if field == "" || registered[field] {
			continue
		}

		registered[field] = true

		mapper, err := newKeycloakAttributeMapper(field)
		if err != nil {
			return nil, err
		}

		mappers = append(mappers, mapper)
	}

	return mappers, nil
}

func newKeycloakAttributeMapper(field string) (keycloak.ProtocolMapperRepresentation, error) {
	attribute, err := findKeycloakAttributeField(field)
	if err != nil {
		return keycloak.ProtocolMapperRepresentation{}, err
	}

	if attribute.x500 == "" {
		return keycloak.ProtocolMapperRepresentation{
			Name:           field,
			Protocol:       keycloakSAMLProtocol,
			ProtocolMapper: keycloakUserPropertyMapper,
			Config: map[string]string{
				"attribute.nameformat": keycloakBasicAttributeFormat,
				"user.attribute":       attribute.property,
				"attribute.name":       field,
			},
		}, nil
	}

	return keycloak.ProtocolMapperRepresentation{
		Name:           "X500 " + field,
		Protocol:       keycloakSAMLProtocol,
		ProtocolMapper: keycloakUserPropertyMapper,
		Config: map[string]string{
			"attribute.nameformat": keycloakURIAttributeFormat,
			"user.attribute":       attribute.property,
			"friendly.name":        field,
			"attribute.name":       attribute.x500,
		},
	}, nil
}

func findKeycloakAttributeField(field string) (keycloakAttributeField, error) {
	names := make([]string, 0, len(keycloakAttributeFields))

	for _, known := range keycloakAttributeFields {
		if known.field == field {
			return known, nil
		}

		names = append(names, known.field)
	}

	return keycloakAttributeField{}, fmt.Errorf("no Keycloak account property answers the %q attribute field, "+
		"name one of %s in the %s config or add a mapper for it to the realm by hand",
		field, strings.Join(names, ", "), saml.KeycloakSAML.ConfigKey)
}

func rancherAPIHostFromConfig(client *rancher.Client) string {
	host := strings.TrimSuffix(client.RancherConfig.Host, "/")
	if host == "" {
		return ""
	}

	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return host
	}

	return "https://" + host
}
