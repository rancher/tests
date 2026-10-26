package auth

import (
	"fmt"

	"github.com/rancher/shepherd/clients/keycloak"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/auth/saml"
)

const (
	genericSAMLMetadataPathFormat  = "%s/v1-saml/genericsaml/saml/metadata"
	genericSAMLACSPathFormat       = "%s/v1-saml/genericsaml/saml/acs"
	genericSAMLKeycloakAdminPrefix = "rancher-generic-saml-keycloak-admin"
)

// GenericSAMLKeycloakFixture holds the accounts, groups and configuration a Generic SAML run backed by Keycloak is built on
type GenericSAMLKeycloakFixture struct {
	Admin            User
	AdminPrincipalID string
	AuthInput        *ExternalAuthConfig
	EntityID         string
	RancherAPIHost   string
}

// SetupGenericSAMLKeycloak registers the Rancher generic SAML client in the Keycloak realm and settles the accounts and groups a run needs, reusing the same realm provisioning Keycloak SAML uses
func SetupGenericSAMLKeycloak(client *rancher.Client, keycloakClient *keycloak.Client) (*GenericSAMLKeycloakFixture, error) {
	fixture, err := setupSAMLKeycloakFixture(client, keycloakClient, samlKeycloakFixtureSpec{
		providerName:   GenericSAML,
		configKey:      saml.GenericSAML.ConfigKey,
		metadataFormat: genericSAMLMetadataPathFormat,
		acsFormat:      genericSAMLACSPathFormat,
		adminPrefix:    genericSAMLKeycloakAdminPrefix,
		config:         client.Auth.GenericSAML.Config,
	})
	if err != nil {
		return nil, err
	}

	return &GenericSAMLKeycloakFixture{
		Admin:            fixture.Admin,
		AdminPrincipalID: fixture.AdminPrincipalID,
		AuthInput:        fixture.AuthInput,
		EntityID:         fixture.EntityID,
		RancherAPIHost:   fixture.RancherAPIHost,
	}, nil
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

// GenericSAMLAssertionGroups returns the groups the identity provider sends in the assertion for the given user
func GenericSAMLAssertionGroups(client *rancher.Client, user User) ([]string, error) {
	assertion, err := client.Auth.GenericSAML.CaptureAssertion(user.Username, user.Password)
	if err != nil {
		return nil, err
	}

	return assertion.Details.Attribute(client.Auth.GenericSAML.Config.GroupsField), nil
}
