package auth

import (
	"context"
	"fmt"

	"github.com/rancher/norman/types"
	"github.com/rancher/shepherd/clients/rancher"
	shepherdauth "github.com/rancher/shepherd/clients/rancher/auth"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// searchPrincipals runs the /v3/principals search for the given name. The principal collection is
// listed first so the search collection action link is populated before invoking it.
func searchPrincipals(client *rancher.Client, name string) ([]v3.Principal, error) {
	collection, err := client.Management.Principal.List(&types.ListOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to list principals: %w", err)
	}
	result, err := client.Management.Principal.CollectionActionSearch(collection, &v3.SearchPrincipalsInput{Name: name})
	if err != nil {
		return nil, fmt.Errorf("failed to search principals for %q: %w", name, err)
	}
	return result.Data, nil
}

// VerifyPrincipalNotLocal polls the /v3/principals search for the given name and returns an error if
// any returned principal is from the local provider. Externally-provisioned users and groups (SCIM,
// SAML, OAuth) have no local login and must not be surfaced as local principals
func VerifyPrincipalNotLocal(client *rancher.Client, name string) error {
	var localErr, callErr error
	_ = kwait.PollUntilContextTimeout(
		context.Background(),
		defaults.FiveSecondTimeout,
		defaults.TenSecondTimeout,
		true,
		func(ctx context.Context) (bool, error) {
			principals, err := searchPrincipals(client, name)
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

// VerifyPrincipalIsLocal runs the /v3/principals search for the given name and returns an error if no
// local principal is returned. A genuine local user must still surface as local after external-user dedup.
func VerifyPrincipalIsLocal(client *rancher.Client, name string) error {
	principals, err := searchPrincipals(client, name)
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
