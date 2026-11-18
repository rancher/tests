package tokens

import (
	"fmt"
	"time"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// PatchToken patches a token using wrangler context. Supported patch operations: add, replace, remove.
func PatchToken(client *rancher.Client, tokenName, patchOp, patchPath, patchData string) (*v3.Token, error) {
	patchJSON := fmt.Sprintf(`[{"op": "%s", "path": "%s", "value": "%s"}]`, patchOp, patchPath, patchData)

	return client.WranglerContext.Mgmt.Token().Patch(tokenName, types.JSONPatchType, []byte(patchJSON))
}

// GetV3TokenLastUsedAt reads the lastUsedAt of the named v3 token, or nil when it is unset
func GetV3TokenLastUsedAt(client *rancher.Client, name string) (*time.Time, error) {
	token, err := client.WranglerContext.Mgmt.Token().Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("reading token %s: %w", name, err)
	}

	if token.LastUsedAt == nil {
		return nil, nil
	}

	return &token.LastUsedAt.Time, nil
}

// SetV3TokenLastUsedAt overwrites the lastUsedAt of the named v3 token
func SetV3TokenLastUsedAt(client *rancher.Client, name string, at time.Time) error {
	if _, err := PatchToken(client, name, "add", "/lastUsedAt", at.UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("setting lastUsedAt of token %s: %w", name, err)
	}

	return nil
}
