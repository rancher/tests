package auth

import (
	"fmt"

	managementv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	authconfigsapi "github.com/rancher/tests/actions/kubeapi/authconfigs"
)

const activeDirectoryConfigSchema = "activeDirectoryConfigs"

// ActiveDirectoryTestAndApply submits the configured Active Directory settings with the given principal ID attributes to testAndApply
func ActiveDirectoryTestAndApply(client *rancher.Client, userIDAttribute, groupIDAttribute string) error {
	cfg := client.Auth.ActiveDirectory.Config

	input := managementv3.ActiveDirectoryTestAndApplyInput{
		ActiveDirectoryConfig: managementv3.ActiveDirectoryConfig{
			AuthConfig: managementv3.AuthConfig{
				AccessMode: cfg.AccessMode,
			},
			Servers:                      []string{AuthProviderServer(cfg.Hostname, cfg.IP)},
			Port:                         int64(cfg.Port),
			TLS:                          cfg.TLS,
			StartTLS:                     cfg.StartTLS,
			ServiceAccountUsername:       cfg.ServiceAccount.DistinguishedName,
			ServiceAccountPassword:       cfg.ServiceAccount.Password,
			UserSearchBase:               cfg.Users.SearchBase,
			UserObjectClass:              cfg.Users.ObjectClass,
			UserNameAttribute:            cfg.Users.UsernameAttribute,
			UserLoginAttribute:           cfg.Users.LoginAttribute,
			UserSearchAttribute:          cfg.Users.SearchAttribute,
			UserSearchFilter:             cfg.Users.SearchFilter,
			UserEnabledAttribute:         cfg.Users.EnabledAttribute,
			UserDisabledBitMask:          cfg.Users.DisabledBitMask,
			GroupSearchBase:              cfg.Groups.SearchBase,
			GroupObjectClass:             cfg.Groups.ObjectClass,
			GroupNameAttribute:           cfg.Groups.NameAttribute,
			GroupSearchAttribute:         cfg.Groups.SearchAttribute,
			GroupSearchFilter:            cfg.Groups.SearchFilter,
			GroupMemberUserAttribute:     cfg.Groups.MemberUserAttribute,
			GroupMemberMappingAttribute:  cfg.Groups.MemberMappingAttribute,
			GroupDNAttribute:             cfg.Groups.DNAttribute,
			NestedGroupMembershipEnabled: &cfg.Groups.NestedGroupMembershipEnabled,
			UserIDAttribute:              userIDAttribute,
			GroupIDAttribute:             groupIDAttribute,
		},
		Username: cfg.Users.Admin.Username,
		Password: cfg.Users.Admin.Password,
		Enabled:  true,
	}

	if err := submitTestAndApply(client, activeDirectoryConfigSchema, ActiveDirectory, input); err != nil {
		return fmt.Errorf("submitting Active Directory testAndApply with userIDAttribute %q and groupIDAttribute %q: %w", userIDAttribute, groupIDAttribute, err)
	}

	return nil
}

// EnableActiveDirectoryWithIDAttributes enables Active Directory with the given principal ID attributes and waits for Rancher to unlock its auth config
func EnableActiveDirectoryWithIDAttributes(client *rancher.Client, userIDAttribute, groupIDAttribute string) error {
	if err := EnsureAuthConfigPresent(client, ActiveDirectory); err != nil {
		return err
	}

	if err := ActiveDirectoryTestAndApply(client, userIDAttribute, groupIDAttribute); err != nil {
		return err
	}

	_, err := WaitForAuthProviderAnnotationUpdate(client, ActiveDirectory, AuthProvCleanupAnnotationValUnlocked)

	return err
}

// CaptureActiveDirectoryRestore records whether Active Directory is enabled and the attributes and access mode it runs with, and returns a function that puts them back
func CaptureActiveDirectoryRestore(client *rancher.Client) (func() error, error) {
	enabled, err := authconfigsapi.AuthConfigEnabled(client, ActiveDirectory)
	if err != nil {
		return nil, err
	}

	if !enabled {
		return func() error { return DisableAuthProviderAndWait(client, ActiveDirectory) }, nil
	}

	userIDAttribute, groupIDAttribute, err := authconfigsapi.FindAuthConfigIDAttributes(client, ActiveDirectory)
	if err != nil {
		return nil, err
	}

	accessMode, _, err := authconfigsapi.FindAuthConfigField(client, ActiveDirectory, authconfigsapi.AccessModeField)
	if err != nil {
		return nil, err
	}

	return func() error {
		if err := DisableAuthProviderAndWait(client, ActiveDirectory); err != nil {
			return err
		}

		if err := EnableActiveDirectoryWithIDAttributes(client, userIDAttribute, groupIDAttribute); err != nil {
			return err
		}

		return authconfigsapi.PatchAuthConfig(client, ActiveDirectory, map[string]any{authconfigsapi.AccessModeField: accessMode})
	}, nil
}
