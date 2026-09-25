package auth

import (
	"fmt"

	"github.com/rancher/shepherd/clients/keycloak"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/auth/saml"
	"github.com/sirupsen/logrus"
)

const (
	genericSAMLMetadataPathFormat = "%s/v1-saml/genericsaml/saml/metadata"
	genericSAMLACSPathFormat      = "%s/v1-saml/genericsaml/saml/acs"

	genericSAMLKeycloakAdminPrefix = "rancher-generic-saml-keycloak-admin"
)

// GenericSAMLKeycloakFixture holds the accounts, groups and configuration a Generic SAML run backed by Keycloak is built on
type GenericSAMLKeycloakFixture struct {
	Admin            User
	AdminPrincipalID string
	AuthInput        *SAMLAuthConfig
	EntityID         string
	RancherAPIHost   string
}

// SetupGenericSAMLKeycloak registers the Rancher generic SAML client in the Keycloak realm and settles the accounts and groups a run needs, reusing the same realm provisioning Keycloak SAML uses
func SetupGenericSAMLKeycloak(client *rancher.Client, keycloakClient *keycloak.Client) (*GenericSAMLKeycloakFixture, error) {
	rancherAPIHost := rancherAPIHostFromConfig(client)
	if rancherAPIHost == "" {
		return nil, fmt.Errorf("the rancher config names no host, the identity provider issues assertions for it so it " +
			"must be set")
	}

	entityID := fmt.Sprintf(genericSAMLMetadataPathFormat, rancherAPIHost)
	acsURL := fmt.Sprintf(genericSAMLACSPathFormat, rancherAPIHost)

	created, err := keycloakClient.EnsureRealm()
	if err != nil {
		return nil, err
	}

	if created {
		logrus.Infof("Created the %s realm, which the Keycloak server did not have", keycloakClient.Realm())
	}

	providerConfig := client.Auth.GenericSAML.Config
	if providerConfig.Users == nil {
		providerConfig.Users = new(saml.Users)
	}

	logrus.Infof("Registering the Rancher generic SAML client %s in the %s realm", entityID, keycloakClient.Realm())
	samlClient, err := newKeycloakSAMLClient(entityID, rancherAPIHost, acsURL, providerConfig, saml.GenericSAML.ConfigKey)
	if err != nil {
		return nil, err
	}

	if _, err := keycloakClient.ReplaceClient(samlClient); err != nil {
		return nil, err
	}

	fixture := &GenericSAMLKeycloakFixture{
		AuthInput:      new(SAMLAuthConfig),
		EntityID:       entityID,
		RancherAPIHost: rancherAPIHost,
	}

	fixture.Admin, err = genericSAMLKeycloakAdmin(keycloakClient, providerConfig, saml.GenericSAML.ConfigKey)
	if err != nil {
		return nil, err
	}

	fixture.AdminPrincipalID = GetUserPrincipalID(GenericSAML, PrincipalNameOf(fixture.Admin), "", "")

	logrus.Infof("Settling the group and accounts the %s access mode tests sign in with", GenericSAML)
	if err := setupKeycloakSAMLAccounts(keycloakClient, providerConfig, fixture.AuthInput, saml.GenericSAML.ConfigKey); err != nil {
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

func enableGenericSAML(client *rancher.Client) error {
	providerConfig := client.Auth.GenericSAML.Config
	if providerConfig.Users == nil || providerConfig.Users.Admin == nil {
		return fmt.Errorf("no identity provider account is available to enable the provider with, run a "+
			"SetupGenericSAML* helper first or name one under users.admin in the %s config", saml.GenericSAML.ConfigKey)
	}

	return client.Auth.GenericSAML.EnableWithAdminLogin(
		providerConfig.Users.Admin.Username,
		providerConfig.Users.Admin.Password,
	)
}

// ReconfigureGenericSAML disables the provider, applies the given mutation to its config, and re-enables it through an admin login
func ReconfigureGenericSAML(client *rancher.Client, mutate func(*saml.Config)) error {
	if err := client.Auth.GenericSAML.Disable(); err != nil {
		return fmt.Errorf("failed to disable generic SAML before reconfiguring it: %w", err)
	}

	if _, err := WaitForAuthProviderAnnotationUpdate(client, GenericSAML, AuthProvCleanupAnnotationValLocked); err != nil {
		return fmt.Errorf("failed waiting for generic SAML to disable before reconfiguring it: %w", err)
	}

	mutate(client.Auth.GenericSAML.Config)

	return EnsureAuthProviderEnabled(client, GenericSAML)
}

func genericSAMLKeycloakAdmin(keycloakClient *keycloak.Client, providerConfig *saml.Config, configKey string) (User, error) {
	admin := providerConfig.Users.Admin
	if admin != nil && admin.Username != "" && admin.Password != "" {
		logrus.Infof("Using the %s account named in the config to enable the provider", admin.Username)

		return keycloakSAMLUser(keycloakClient, admin.Username, admin.Password, providerConfig.UIDField, configKey)
	}

	logrus.Info("Creating the Keycloak account that enables the provider and becomes the Rancher administrator")

	created, _, err := createKeycloakSAMLUser(keycloakClient, genericSAMLKeycloakAdminPrefix, providerConfig.UIDField, configKey)

	return created, err
}

// GenericSAMLAssertionGroups returns the groups the identity provider sends in the assertion for the given user
func GenericSAMLAssertionGroups(client *rancher.Client, user User) ([]string, error) {
	assertion, err := client.Auth.GenericSAML.CaptureAssertion(user.Username, user.Password)
	if err != nil {
		return nil, err
	}

	return assertion.Details.Attribute(client.Auth.GenericSAML.Config.GroupsField), nil
}
