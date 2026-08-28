package exttokens

import (
	"fmt"

	extapi "github.com/rancher/rancher/pkg/apis/ext.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
)

// VerifyExtTokenData validates ext token data against expected values
func VerifyExtTokenData(client *rancher.Client, exttoken *extapi.Token, expectedUserID string, expectedDefaultTTL int64, onCreation bool) error {
	userID, ok := exttoken.Labels[UserIDLabel]
	if !ok {
		return fmt.Errorf("expected label cattle.io/user-id to exist on token")
	}
	if userID != expectedUserID {
		return fmt.Errorf("label mismatch for user ID: got '%s', want '%s'", userID, expectedUserID)
	}

	if exttoken.Spec.Enabled == nil {
		return fmt.Errorf("Spec.Enabled should not be nil")
	}
	if !*exttoken.Spec.Enabled {
		return fmt.Errorf("expected token to be enabled, but it was disabled")
	}
	if exttoken.Spec.TTL != expectedDefaultTTL {
		return fmt.Errorf("TTL mismatch: got %d, want %d", exttoken.Spec.TTL, expectedDefaultTTL)
	}

	if exttoken.Status.Expired != ExtTokenStatusCurrentValue {
		return fmt.Errorf("status.Current mismatch: got '%v', want '%v'", exttoken.Status.Current, FalseConditionStatus)
	}

	if exttoken.Status.Expired != ExtTokenStatusExpiredValue {
		return fmt.Errorf("status.Expired mismatch: got '%v', want '%v'", exttoken.Status.Expired, ExtTokenStatusExpiredValue)
	}

	if onCreation {
		if exttoken.Status.Value == "" {
			return fmt.Errorf("expected Status.Value to be populated, but it was empty")
		}
		if exttoken.Status.Hash != "" {
			return fmt.Errorf("expected Status.Hash to be empty on creation, but it was: '%s'", exttoken.Status.Hash)
		}
	} else {
		if exttoken.Status.Value != "" {
			return fmt.Errorf("expected Status.Value to be empty after creation, but it was populated")
		}
		if exttoken.Status.Hash == "" {
			return fmt.Errorf("expected Status.Hash to be populated after creation, but it was empty")
		}
	}
	return nil
}

// VerifyExtSessionTokenData validates that a session ext token has the expected properties
func VerifyExtSessionTokenData(exttoken *extapi.Token, expectedUserID string) error {
	userID, ok := exttoken.Labels[UserIDLabel]
	if !ok {
		return fmt.Errorf("expected label cattle.io/user-id to exist on token")
	}
	if userID != expectedUserID {
		return fmt.Errorf("label mismatch for user ID: got '%s', want '%s'", userID, expectedUserID)
	}
	if exttoken.Spec.Kind != "session" {
		return fmt.Errorf("expected Spec.Kind to be 'session', got '%s'", exttoken.Spec.Kind)
	}
	if exttoken.Spec.Enabled == nil {
		return fmt.Errorf("Spec.Enabled should not be nil")
	}
	if !*exttoken.Spec.Enabled {
		return fmt.Errorf("expected session token to be enabled, but it was disabled")
	}
	if exttoken.Status.Value == "" {
		return fmt.Errorf("expected Status.Value to be populated, but it was empty")
	}
	if exttoken.Status.Hash != "" {
		return fmt.Errorf("expected Status.Hash to be empty on creation, but it was: '%s'", exttoken.Status.Hash)
	}
	return nil
}

// VerifyExtTokenExistsInList returns true if a token with the given name exists in the provided list
func VerifyExtTokenExistsInList(tokens []extapi.Token, name string) bool {
	for _, token := range tokens {
		if token.Name == name {
			return true
		}
	}
	return false
}
