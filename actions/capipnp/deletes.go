package capipnp

import (
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
)

// DeleteManifestsWithKubectl is a helper function that deletes the provided manifests in the cluster using kubectl.
func DeleteManifestsWithKubectl(client *rancher.Client, manifests []ManifestContent, yamlPath string) error {
	kubeconfigPath, cleanup, err := writeKubeconfigToTempFile(client, localCluster)
	if err != nil {
		return err
	}
	defer cleanup()

	for _, manifest := range manifests {
		deleteCommand := kubectlDeleteCommand(kubeconfigPath)
		output, cmdErr := runLocalKubectl(deleteCommand, manifest.Content)
		if cmdErr != nil {
			if strings.Contains(output, "NotFound") || strings.Contains(output, "not found") {
				continue
			}

			return fmt.Errorf("kubectl delete failed for manifest %q: %w: %s", manifest.Path, cmdErr, strings.TrimSpace(output))
		}
	}

	return nil
}

// DeleteCAPICluster is a helper function that deletes a single manifest in the cluster using kubectl.
func DeleteCAPICluster(client *rancher.Client, manifests string, content []byte) error {
	return DeleteManifestsWithKubectl(client, []ManifestContent{{Path: manifests, Content: content}}, manifests)
}

// DeleteManifestFromPath is a helper function that loads and deletes a single manifest file from the provided path.
func DeleteManifestFromPath(client *rancher.Client, yamlPath string) error {
	manifests, err := LoadManifestContentsFromPaths([]string{yamlPath})
	if err != nil {
		return err
	}

	return DeleteManifestsWithKubectl(client, manifests, yamlPath)
}

// DeleteProviderManifestFromPath renders provider-specific template parameters and deletes the manifest in the cluster.
func DeleteProviderManifestFromPath(client *rancher.Client, config *Config, yamlPath string) error {
	content, err := RenderProviderManifestFromPath(config, yamlPath)
	if err != nil {
		return err
	}

	return DeleteCAPICluster(client, yamlPath, content)
}

func kubectlDeleteCommand(kubeconfigPath string) []string {
	return []string{"kubectl", "--kubeconfig", kubeconfigPath, "delete", "-f", "-"}
}
