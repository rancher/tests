package auth

import (
	"fmt"

	managementv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	authconfigsapi "github.com/rancher/tests/actions/kubeapi/authconfigs"
)

const (
	openLDAPConfigSchema    = "openLdapConfigs"
	userLoginAttributeField = "userLoginAttribute"
)

// OpenLDAPTestAndApply submits the configured OpenLDAP settings with the given login and principal ID attributes to testAndApply, where an empty login attribute keeps Rancher's default
func OpenLDAPTestAndApply(client *rancher.Client, userLoginAttribute, userIDAttribute, groupIDAttribute string) error {
	cfg := client.Auth.OLDAP.Config

	input := managementv3.OpenLdapTestAndApplyInput{
		LdapTestAndApplyInput: managementv3.LdapTestAndApplyInput{
			LdapConfig: managementv3.LdapConfig{
				AuthConfig: managementv3.AuthConfig{
					Type:       v3.OpenLdapConfigType,
					Enabled:    true,
					AccessMode: cfg.AccessMode,
				},
				LdapFields: managementv3.LdapFields{
					Servers:                         []string{AuthProviderServer(cfg.Hostname, cfg.IP)},
					UserSearchBase:                  cfg.Users.SearchBase,
					GroupSearchBase:                 cfg.Groups.SearchBase,
					ServiceAccountDistinguishedName: cfg.ServiceAccount.DistinguishedName,
					ServiceAccountPassword:          cfg.ServiceAccount.Password,
					GroupMemberUserAttribute:        cfg.Groups.MemberMappingAttribute,
					NestedGroupMembershipEnabled:    cfg.Groups.NestedGroupMembershipEnabled,
					GroupObjectClass:                cfg.Groups.ObjectClass,
					UserLoginAttribute:              userLoginAttribute,
					UserIDAttribute:                 userIDAttribute,
					GroupIDAttribute:                groupIDAttribute,
				},
			},
			Username: cfg.Users.Admin.Username,
			Password: cfg.Users.Admin.Password,
		},
	}

	if err := submitTestAndApply(client, openLDAPConfigSchema, OpenLdap, input); err != nil {
		return fmt.Errorf("submitting OpenLDAP testAndApply with userIDAttribute %q and groupIDAttribute %q: %w", userIDAttribute, groupIDAttribute, err)
	}

	return nil
}

// EnableOpenLDAPWithIDAttributes enables OpenLDAP with the given login and principal ID attributes and waits for Rancher to unlock its auth config
func EnableOpenLDAPWithIDAttributes(client *rancher.Client, userLoginAttribute, userIDAttribute, groupIDAttribute string) error {
	if err := EnsureAuthConfigPresent(client, OpenLdap); err != nil {
		return err
	}

	if err := OpenLDAPTestAndApply(client, userLoginAttribute, userIDAttribute, groupIDAttribute); err != nil {
		return err
	}

	_, err := WaitForAuthProviderAnnotationUpdate(client, OpenLdap, AuthProvCleanupAnnotationValUnlocked)

	return err
}

// ReenableOpenLDAPWithIDAttributes disables OpenLDAP and enables it again with the given login and principal ID attributes
func ReenableOpenLDAPWithIDAttributes(client *rancher.Client, userLoginAttribute, userIDAttribute, groupIDAttribute string) error {
	if err := DisableAuthProviderAndWait(client, OpenLdap); err != nil {
		return err
	}

	return EnableOpenLDAPWithIDAttributes(client, userLoginAttribute, userIDAttribute, groupIDAttribute)
}

// CaptureOpenLDAPRestore records whether OpenLDAP is enabled and the attributes and access mode it runs with, and returns a function that puts them back
func CaptureOpenLDAPRestore(client *rancher.Client) (func() error, error) {
	enabled, err := authconfigsapi.AuthConfigEnabled(client, OpenLdap)
	if err != nil {
		return nil, err
	}

	if !enabled {
		return func() error { return DisableAuthProviderAndWait(client, OpenLdap) }, nil
	}

	userIDAttribute, groupIDAttribute, err := authconfigsapi.FindAuthConfigIDAttributes(client, OpenLdap)
	if err != nil {
		return nil, err
	}

	userLoginAttribute, _, err := authconfigsapi.FindAuthConfigField(client, OpenLdap, userLoginAttributeField)
	if err != nil {
		return nil, err
	}

	accessMode, _, err := authconfigsapi.FindAuthConfigField(client, OpenLdap, authconfigsapi.AccessModeField)
	if err != nil {
		return nil, err
	}

	return func() error {
		if err := ReenableOpenLDAPWithIDAttributes(client, userLoginAttribute, userIDAttribute, groupIDAttribute); err != nil {
			return err
		}

		return authconfigsapi.PatchAuthConfig(client, OpenLdap, map[string]any{authconfigsapi.AccessModeField: accessMode})
	}, nil
}
