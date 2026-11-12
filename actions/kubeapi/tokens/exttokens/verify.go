package exttokens

import (
	"fmt"
	"time"

	extapi "github.com/rancher/rancher/pkg/apis/ext.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	exttokenapi "github.com/rancher/shepherd/extensions/kubeapi/tokens"
	extuserapi "github.com/rancher/shepherd/extensions/kubeapi/users"
	"github.com/rancher/shepherd/pkg/session"
	settingsapi "github.com/rancher/tests/actions/kubeapi/settings"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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

// VerifySessionTokenCreateRejected returns an error unless both a real create and a dry-run create of an ext token of kind session are rejected with HTTP 400
func VerifySessionTokenCreateRejected(client *rancher.Client) error {
	_, err := exttokenapi.CreateExtToken(client, NewExtTokenObject("session", 0))
	if err == nil {
		return fmt.Errorf("creating an ext token of kind session should be rejected")
	}
	if !k8serrors.IsBadRequest(err) {
		return fmt.Errorf("expected HTTP 400 on create, got: %w", err)
	}

	_, err = CreateExtTokenDryRun(client, NewExtTokenObject("session", 0))
	if err == nil {
		return fmt.Errorf("dry-run create of an ext token of kind session should be rejected")
	}
	if !k8serrors.IsBadRequest(err) {
		return fmt.Errorf("expected HTTP 400 on dry-run, got: %w", err)
	}

	return nil
}

// VerifyTokenAuthenticates checks that the given bearer token can successfully authenticate with the Rancher API.
func VerifyTokenAuthenticates(bearerToken string) error {
	probeSession := session.NewSession()
	defer probeSession.Cleanup()

	testClient, err := rancher.NewClient(bearerToken, probeSession)
	if err != nil {
		return fmt.Errorf("token does not authenticate: %w", err)
	}

	if _, err := extuserapi.CreateSelfUserRequest(testClient); err != nil {
		return fmt.Errorf("token does not authenticate: %w", err)
	}

	return nil
}

// VerifyExpiresAtMatchesTTL verifies that the given expiresAt timestamp is within defaults.TwoMinuteTimeout of the current time plus the specified TTL.
func VerifyExpiresAtMatchesTTL(expiresAt string, ttl time.Duration) error {
	if expiresAt == "" {
		return fmt.Errorf("expected status.expiresAt to be populated")
	}

	parsed, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return fmt.Errorf("failed to parse status.expiresAt %q: %w", expiresAt, err)
	}

	diff := parsed.Sub(time.Now().Add(ttl))
	if diff < 0 {
		diff = -diff
	}
	if diff > defaults.TwoMinuteTimeout {
		return fmt.Errorf("expected expiresAt %q to be within %s of now+%s, got a drift of %s", expiresAt, defaults.TwoMinuteTimeout, ttl, diff)
	}
	return nil
}

// VerifyExpiresAtMatchesTTLMillis verifies that the given expiresAt timestamp is within defaults.TwoMinuteTimeout of the current time plus the specified TTL in milliseconds.
func VerifyExpiresAtMatchesTTLMillis(expiresAt string, ttlMillis int64) error {
	if expiresAt == "" {
		return fmt.Errorf("expected status.expiresAt to be populated")
	}

	parsed, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return fmt.Errorf("failed to parse status.expiresAt %q: %w", expiresAt, err)
	}

	expectedUnix := time.Now().Unix() + ttlMillis/settingsapi.MillisecondsPerSecond
	diffSeconds := parsed.Unix() - expectedUnix
	if diffSeconds < 0 {
		diffSeconds = -diffSeconds
	}
	if toleranceSeconds := int64(defaults.TwoMinuteTimeout / time.Second); diffSeconds > toleranceSeconds {
		return fmt.Errorf("expected expiresAt %q to be within %s of now+%dms, got a drift of %ds", expiresAt, defaults.TwoMinuteTimeout, ttlMillis, diffSeconds)
	}
	return nil
}

// VerifyNoExpiry returns an error unless expiresAt indicates the token/kubeconfig does not expire.
func VerifyNoExpiry(expiresAt string) error {
	if expiresAt != "" {
		return fmt.Errorf("expected status.expiresAt to be empty for a non-expiring token, got %q", expiresAt)
	}
	return nil
}
