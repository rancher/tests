package settings

import (
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	extsettingsapi "github.com/rancher/shepherd/extensions/kubeapi/settings"
)

// VerifyGlobalSettingValue checks that a global setting currently holds the expected value.
func VerifyGlobalSettingValue(client *rancher.Client, settingName, expectedValue string) error {
	value, err := extsettingsapi.GetGlobalSettingValue(client, settingName)
	if err != nil {
		return err
	}
	if value != expectedValue {
		return fmt.Errorf("setting %s mismatch: got %q, want %q", settingName, value, expectedValue)
	}
	return nil
}

// VerifyGlobalSettingDefaultValue checks that a setting's installation default matches the expected value.
func VerifyGlobalSettingDefaultValue(client *rancher.Client, settingName, expectedDefault string) error {
	defaultValue, err := extsettingsapi.GetGlobalSettingDefaultValue(client, settingName)
	if err != nil {
		return err
	}

	if defaultValue != expectedDefault {
		return fmt.Errorf("setting %s default mismatch: got %q, want %q", settingName, defaultValue, expectedDefault)
	}

	return nil
}

// VerifyGlobalSettingAtDefault checks that a setting's current value matches its installation default.
func VerifyGlobalSettingAtDefault(client *rancher.Client, settingName string) error {
	value, err := extsettingsapi.GetGlobalSettingValue(client, settingName)
	if err != nil {
		return err
	}

	defaultValue, err := extsettingsapi.GetGlobalSettingDefaultValue(client, settingName)
	if err != nil {
		return err
	}

	if value != defaultValue {
		return fmt.Errorf("setting %s is not at its installation default: got %q, want %q", settingName, value, defaultValue)
	}

	return nil
}
