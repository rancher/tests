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

// VerifyProviderDisabled waits for the auth config to report the provider disabled with its cleanup annotation locked
func VerifyProviderDisabled(client *rancher.Client, providerName string) error {
	authConfig, err := WaitForAuthProviderAnnotationUpdate(client, providerName, AuthProvCleanupAnnotationValLocked)
	if err != nil {
		return fmt.Errorf("timed out waiting for %s to report its cleanup annotation locked: %w", providerName, err)
	}

	if authConfig.Enabled {
		return fmt.Errorf("auth config %s still reports enabled after being disabled", providerName)
	}

	return nil
}

// VerifyProviderSessionRejected waits until a client authenticated through the provider can no longer reach the Rancher API
func VerifyProviderSessionRejected(authClient *rancher.Client) error {
	var lastErr error

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, false, func(context.Context) (bool, error) {
		_, listErr := authClient.Management.User.List(&types.ListOpts{})
		if listErr == nil {
			lastErr = fmt.Errorf("a session established through the provider still reaches the Rancher API after the provider was disabled")
			return false, nil
		}

		var apiError *clientbase.APIError
		if errors.As(listErr, &apiError) && (apiError.StatusCode == http.StatusUnauthorized || apiError.StatusCode == http.StatusForbidden) {
			return true, nil
		}

		lastErr = fmt.Errorf("listing users through the provider session failed for an unrelated reason: %w", listErr)

		return false, nil
	})

	if err != nil {
		return fmt.Errorf("timed out waiting for the provider session to be rejected: %w", lastErr)
	}

	return nil
}

// VerifyKubeAPIAuthLogged reports whether any kube-api-auth pod has gained every given reason since the baseline it was last read at
func VerifyKubeAPIAuthLogged(client *rancher.Client, clusterID string, baseline map[string]string, reasons ...string) error {
	current, err := KubeAPIAuthLogs(client, clusterID)
	if err != nil {
		return err
	}

	appended := strings.Builder{}

	for pod, logs := range current {
		appended.WriteString(logsSince(baseline[pod], logs))
		appended.WriteString("\n")
	}

	for _, reason := range reasons {
		if !strings.Contains(appended.String(), reason) {
			return fmt.Errorf("no %s pod gained an entry naming %q, which is where a request refused with an empty body says why", KubeAPIAuthDaemonSet, reason)
		}
	}

	return nil
}

// VerifyKubeAPIAuthRefused polls until kube-api-auth logs the refusal of the access key and returns an error unless it is exactly one info line with the reason and no error line
func VerifyKubeAPIAuthRefused(client *rancher.Client, clusterID, accessKey, reason string) error {
	var refusals []string

	var errorLines []string

	var lastErr error

	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true,
		func(context.Context) (bool, error) {
			current, err := KubeAPIAuthLogs(client, clusterID)
			if err != nil {
				lastErr = err

				return false, nil
			}

			refusals, errorLines = kubeAPIAuthRefusalLines(current, accessKey)

			return len(refusals) > 0 || len(errorLines) > 0, nil
		})
	if pollErr != nil {
		return fmt.Errorf("%s never logged the refusal of token %s (last read error: %v): %w", KubeAPIAuthDaemonSet, accessKey, lastErr, pollErr)
	}

	if len(errorLines) > 0 {
		return fmt.Errorf("%s logged %d error lines for the refused token %s, the first being %q", KubeAPIAuthDaemonSet, len(errorLines), accessKey, errorLines[0])
	}

	if len(refusals) != 1 {
		return fmt.Errorf("%s logged the refusal of token %s %d times where one info line was expected", KubeAPIAuthDaemonSet, accessKey, len(refusals))
	}

	if !strings.Contains(refusals[0], reason) {
		return fmt.Errorf("%s logged the refusal of token %s without the reason %q: %q", KubeAPIAuthDaemonSet, accessKey, reason, refusals[0])
	}

	return nil
}

func kubeAPIAuthRefusalLines(logsByPod map[string]string, accessKey string) ([]string, []string) {
	refusals := []string{}
	errorLines := []string{}

	for _, logs := range logsByPod {
		for _, line := range strings.Split(logs, "\n") {
			if !strings.Contains(line, accessKey) {
				continue
			}

			switch {
			case strings.Contains(line, "level=error"):
				errorLines = append(errorLines, line)
			case strings.Contains(line, "level=info") && strings.Contains(line, "Authentication refused for "+accessKey+":"):
				refusals = append(refusals, line)
			}
		}
	}

	return refusals, errorLines
}

// VerifyTokenReviewRefused returns an error unless kube-api-auth refuses the token under both apiVersions with a JSON TokenReview that names the reason and omits the secret
func VerifyTokenReviewRefused(client *rancher.Client, clusterID, token, reason string) error {
	_, secret, found := strings.Cut(token, ":")
	if !found || secret == "" {
		return fmt.Errorf("the token should look like accessKey:secret so the refusal can be checked for the secret")
	}

	for _, apiVersion := range []string{TokenReviewAPIV1, TokenReviewAPIV1Beta} {
		refusal, err := waitForTokenReviewRefusal(client, clusterID, apiVersion, token, reason)
		if err != nil {
			return err
		}

		if refusal.StatusCode != http.StatusOK {
			return fmt.Errorf("kube-api-auth answered the refusal under %s with status %d rather than a TokenReview", apiVersion, refusal.StatusCode)
		}

		if refusal.Kind != TokenReviewKind {
			return fmt.Errorf("the refusal under %s carried kind %q where %q was expected", apiVersion, refusal.Kind, TokenReviewKind)
		}

		if refusal.APIVersion != apiVersion {
			return fmt.Errorf("the refusal carried apiVersion %q where %q was requested", refusal.APIVersion, apiVersion)
		}

		if !strings.Contains(refusal.Error, reason) {
			return fmt.Errorf("the refusal under %s says %q where %q was expected", apiVersion, refusal.Error, reason)
		}

		if strings.Contains(refusal.Body, secret) {
			return fmt.Errorf("the refusal under %s repeats the token secret", apiVersion)
		}

		if !strings.Contains(refusal.ContentType, TokenReviewContentType) {
			return fmt.Errorf("the refusal under %s was answered as %q rather than JSON", apiVersion, refusal.ContentType)
		}
	}

	return nil
}

func logsSince(baseline, current string) string {
	trimmed := strings.TrimRight(baseline, "\n")
	if trimmed == "" {
		return current
	}

	lines := strings.Split(trimmed, "\n")
	last := lines[len(lines)-1]

	if marker := strings.LastIndex(current, last); marker >= 0 {
		return current[marker+len(last):]
	}

	return current
}
