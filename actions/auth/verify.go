package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/rancher/norman/types"
	"github.com/rancher/shepherd/clients/rancher"
	shepherdauth "github.com/rancher/shepherd/clients/rancher/auth"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/clientbase"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// searchPrincipals runs the /v3/principals search for the given name, restricted to principalType when it is not empty.
func searchPrincipals(client *rancher.Client, name, principalType string) ([]v3.Principal, error) {
	collection, err := client.Management.Principal.List(&types.ListOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to list principals: %w", err)
	}
	result, err := client.Management.Principal.CollectionActionSearch(collection, &v3.SearchPrincipalsInput{
		Name:          name,
		PrincipalType: principalType,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to search principals for %q: %w", name, err)
	}
	return result.Data, nil
}

// VerifyPrincipalNotLocal polls the /v3/principals search for the given name and returns an error if any returned principal is from the local provider.
func VerifyPrincipalNotLocal(client *rancher.Client, name string) error {
	var localErr, callErr error
	_ = kwait.PollUntilContextTimeout(
		context.Background(),
		defaults.FiveSecondTimeout,
		defaults.TenSecondTimeout,
		true,
		func(ctx context.Context) (bool, error) {
			principals, err := searchPrincipals(client, name, "")
			if err != nil {
				callErr = err
				return false, err
			}
			for i := range principals {
				if principals[i].Provider == shepherdauth.LocalAuth.String() {
					localErr = fmt.Errorf("principal search for %q returned a local principal (id=%s, type=%s); externally-provisioned principals must not appear as local", name, principals[i].ID, principals[i].PrincipalType)
					return false, localErr
				}
			}
			return len(principals) > 0, nil
		},
	)
	if localErr != nil {
		return localErr
	}
	return callErr
}

// VerifyPrincipalSearchReturnsID runs the /v3/principals search for the given name and returns an error if none of the returned principals carry the expected principal ID.
func VerifyPrincipalSearchReturnsID(client *rancher.Client, name, expectedPrincipalID string) error {
	principals, err := searchPrincipals(client, name, "")
	if err != nil {
		return err
	}

	returnedIDs := make([]string, 0, len(principals))
	for i := range principals {
		if strings.EqualFold(principals[i].ID, expectedPrincipalID) {
			return nil
		}
		returnedIDs = append(returnedIDs, principals[i].ID)
	}

	return fmt.Errorf("principal search for %q did not return %q; returned %v", name, expectedPrincipalID, returnedIDs)
}

// VerifyPrincipalSearchByTypeReturnsOnly runs the /v3/principals search restricted to principalType and returns an error if the expected principal ID is missing or any returned principal has a different type.
func VerifyPrincipalSearchByTypeReturnsOnly(client *rancher.Client, name, principalType, expectedPrincipalID string) error {
	principals, err := searchPrincipals(client, name, principalType)
	if err != nil {
		return err
	}

	found := false
	for i := range principals {
		if principals[i].PrincipalType != principalType {
			return fmt.Errorf("principal search for %q restricted to type %q returned %q of type %q", name, principalType, principals[i].ID, principals[i].PrincipalType)
		}
		if strings.EqualFold(principals[i].ID, expectedPrincipalID) {
			found = true
		}
	}

	if !found {
		return fmt.Errorf("principal search for %q restricted to type %q did not return %q", name, principalType, expectedPrincipalID)
	}

	return nil
}

// VerifyPrincipalSearchExcludesProvider runs the /v3/principals search for the given name and returns an error if any returned principal belongs to the given provider.
func VerifyPrincipalSearchExcludesProvider(client *rancher.Client, name, provider string) error {
	principals, err := searchPrincipals(client, name, "")
	if err != nil {
		return err
	}

	for i := range principals {
		if principals[i].Provider == provider {
			return fmt.Errorf("principal search for %q returned principal %q while provider %q should contribute no results", name, principals[i].ID, provider)
		}
	}

	return nil
}

// VerifyPrincipalByID resolves a principal through /v3/principals/<id> and returns an error if the lookup fails or the resolved principal does not match the expected provider and type.
func VerifyPrincipalByID(client *rancher.Client, principalID, expectedProvider, expectedPrincipalType string) error {
	principal, err := client.Management.Principal.ByID(url.PathEscape(principalID))
	if err != nil {
		return fmt.Errorf("failed to resolve principal %q: %w", principalID, err)
	}

	if principal.Provider != expectedProvider {
		return fmt.Errorf("principal %q resolved to provider %q, expected %q", principalID, principal.Provider, expectedProvider)
	}

	if principal.PrincipalType != expectedPrincipalType {
		return fmt.Errorf("principal %q resolved to type %q, expected %q", principalID, principal.PrincipalType, expectedPrincipalType)
	}

	if principal.Name == "" {
		return fmt.Errorf("principal %q resolved with an empty display name", principalID)
	}

	return nil
}

// VerifyProviderSessionRejected polls until the given client's session stops being accepted
func VerifyProviderSessionRejected(client *rancher.Client) error {
	acceptedErr := fmt.Errorf("the session is still accepted, so the provider that issued it has not had its tokens deleted")

	var lastErr error
	pollErr := kwait.PollUntilContextTimeout(
		context.Background(),
		defaults.FiveSecondTimeout,
		defaults.TwoMinuteTimeout,
		true,
		func(ctx context.Context) (bool, error) {
			_, err := client.Management.Principal.List(&types.ListOpts{})
			if err == nil {
				lastErr = nil
				return false, nil
			}

			var apiErr *clientbase.APIError
			if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden) {
				return true, nil
			}

			lastErr = err

			return false, nil
		},
	)
	if pollErr != nil {
		if lastErr != nil {
			return fmt.Errorf("the session was never rejected on authentication grounds, the last request failed with: %w", lastErr)
		}

		return fmt.Errorf("%w: %w", acceptedErr, pollErr)
	}

	return nil
}

// VerifyPrincipalIsLocal runs the /v3/principals search for the given name and returns an error if no local principal is returned.
func VerifyPrincipalIsLocal(client *rancher.Client, name string) error {
	principals, err := searchPrincipals(client, name, "")
	if err != nil {
		return err
	}
	for i := range principals {
		if principals[i].Provider == shepherdauth.LocalAuth.String() {
			return nil
		}
	}
	return fmt.Errorf("principal search for %q returned no local principal; a local user must still surface as local", name)
}
