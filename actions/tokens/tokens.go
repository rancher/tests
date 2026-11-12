package tokens

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// NewV3TokenObject returns a v3 token request, using norman API, with the given TTL, in milliseconds.
func NewV3TokenObject(ttlMillis int64) *management.Token {
	return &management.Token{
		Description: namegen.AppendRandomString("test-v3token"),
		TTLMillis:   ttlMillis,
	}
}

// CreateV3Token creates a v3 (norman) token, with the given TTL, in milliseconds.
func CreateV3Token(client *rancher.Client, ttlMillis int64) (*management.Token, error) {
	createdToken, err := client.Management.Token.Create(NewV3TokenObject(ttlMillis))
	if err != nil {
		return nil, fmt.Errorf("failed to create v3 token: %w", err)
	}

	return createdToken, nil
}

// GetLoginV3Token returns the most recently created login v3 token belonging to the given user.
func GetLoginV3Token(client *rancher.Client, userID string) (*management.Token, error) {
	tokenCollection, err := client.Management.Token.List(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list tokens for user %s: %w", userID, err)
	}

	var loginToken *management.Token
	for i := range tokenCollection.Data {
		token := &tokenCollection.Data[i]
		if token.UserID != userID || token.IsDerived {
			continue
		}
		if loginToken == nil || token.Created > loginToken.Created {
			loginToken = token
		}
	}
	if loginToken == nil {
		return nil, fmt.Errorf("no login token found for user %s", userID)
	}

	return loginToken, nil
}

// WaitForV3TokenExpiresAt waits for the expiresAt field of a v3 token to be populated and returns the updated token.
func WaitForV3TokenExpiresAt(client *rancher.Client, tokenID string) (*management.Token, error) {
	var token *management.Token
	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(context.Context) (bool, error) {
		var pollErr error
		token, pollErr = client.Management.Token.ByID(tokenID)
		if pollErr != nil {
			return false, pollErr
		}
		return token.ExpiresAt != "", nil
	})

	if err != nil {
		return nil, fmt.Errorf("timed out waiting for v3 token %s expiresAt to be set: %w", tokenID, err)
	}

	return token, nil
}

// VerifyExpiresAtMatchesTTLFromCreated checks that the expiresAt timestamp is within a reasonable drift of created + ttl.
func VerifyExpiresAtMatchesTTLFromCreated(created, expiresAt string, ttl time.Duration) error {
	if created == "" {
		return fmt.Errorf("expected created to be populated")
	}

	if expiresAt == "" {
		return fmt.Errorf("expected expiresAt to be populated")
	}

	createdTime, err := time.Parse(time.RFC3339, created)
	if err != nil {
		return fmt.Errorf("failed to parse created %q: %w", created, err)
	}

	expiresAtTime, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return fmt.Errorf("failed to parse expiresAt %q: %w", expiresAt, err)
	}

	diff := expiresAtTime.Sub(createdTime.Add(ttl))
	if diff < 0 {
		diff = -diff
	}

	if diff > defaults.TwoMinuteTimeout {
		return fmt.Errorf("expected expiresAt %q to be within %s of created(%q)+%s, got a drift of %s", expiresAt, defaults.TwoMinuteTimeout, created, ttl, diff)
	}

	return nil
}
