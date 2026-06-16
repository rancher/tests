package tokens

import (
	"fmt"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	"k8s.io/apimachinery/pkg/types"
)

// PatchToken patches a token using wrangler context. Supported patch operations: add, replace, remove.
func PatchToken(client *rancher.Client, tokenName, patchOp, patchPath, patchData string) (*v3.Token, error) {
	patchJSON := fmt.Sprintf(`[{"op": "%s", "path": "%s", "value": "%s"}]`, patchOp, patchPath, patchData)

	return client.WranglerContext.Mgmt.Token().Patch(tokenName, types.JSONPatchType, []byte(patchJSON))
}
