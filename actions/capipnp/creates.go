package capipnp

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/kubeconfig"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/sirupsen/logrus"
	yamlv3 "gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"
)

const (
	localCluster = "local"
)

// LoadSecretManifest is a helper function that loads a secret manifest from the provided path
// and unmarshals it into a corev1.Secret object.
func LoadSecretManifest(path string) (*corev1.Secret, error) {
	b, err := loadManifest(path)
	if err != nil {
		return nil, err
	}

	return unmarshalSecret(b, path)
}

// LoadManifestContentsFromPaths is a helper function that loads the contents of multiple manifest
// files from the provided paths.
func LoadManifestContentsFromPaths(manifestPaths []string) ([]ManifestContent, error) {
	manifests := make([]ManifestContent, 0, len(manifestPaths))
	for _, path := range manifestPaths {
		content, err := loadManifest(path)
		if err != nil {
			return nil, err
		}

		manifests = append(manifests, ManifestContent{
			Path:    path,
			Content: content,
		})
	}

	return manifests, nil
}

// CreateManifestsWithKubectl is a helper function that creates the provided manifests in the cluster using kubectl.
func CreateManifestsWithKubectl(client *rancher.Client, manifests []ManifestContent, yamlPath string) error {
	kubeconfigPath, cleanup, err := writeKubeconfigToTempFile(client, localCluster)
	if err != nil {
		return err
	}
	defer cleanup()

	for _, manifest := range manifests {
		if isManifestAlreadyApplied(kubeconfigPath, manifest.Content) {
			continue
		}

		createCommand := kubectlCreateCommand(kubeconfigPath)
		output, cmdErr := runLocalKubectl(createCommand, manifest.Content)
		if cmdErr != nil {
			if strings.Contains(output, "AlreadyExists") || strings.Contains(output, "already exists") {
				continue
			}

			return fmt.Errorf("kubectl create failed for manifest %q: %w: %s", manifest.Path, cmdErr, strings.TrimSpace(output))
		}
	}

	return nil
}

// CreateCAPICluster is a helper function that creates a single manifest in the cluster using kubectl.
func CreateCAPICluster(client *rancher.Client, manifestName string, content []byte) error {
	return CreateManifestsWithKubectl(client, []ManifestContent{{Path: manifestName, Content: content}}, manifestName)
}

// CreateManifestFromPath is a helper function that loads the contents of a single manifest file from the provided path
// and creates it in the cluster using kubectl.
func CreateManifestFromPath(client *rancher.Client, yamlPath string) error {
	manifests, err := LoadManifestContentsFromPaths([]string{yamlPath})
	if err != nil {
		return err
	}

	return CreateManifestsWithKubectl(client, manifests, yamlPath)
}

// CreateProviderManifestFromPath renders provider-specific template parameters and creates the manifest in the cluster.
func CreateProviderManifestFromPath(client *rancher.Client, config *Config, yamlPath string) error {
	content, err := RenderProviderManifestFromPath(config, yamlPath)
	if err != nil {
		return err
	}

	return CreateCAPICluster(client, yamlPath, content)
}

// RenderProviderManifestFromPath renders a provider template manifest with values from config.
func RenderProviderManifestFromPath(config *Config, yamlPath string) ([]byte, error) {
	templateBytes, err := loadManifest(yamlPath)
	if err != nil {
		return nil, err
	}

	documents, err := decodeManifestDocuments(templateBytes)
	if err != nil {
		return nil, err
	}

	provider := CreateCAPIProvider(config.Provider)
	if provider.RenderProviderManifestFunc == nil {
		return nil, fmt.Errorf("provider %q does not implement provider manifest rendering", config.Provider)
	}

	if err := provider.RenderProviderManifestFunc(documents, config); err != nil {
		return nil, err
	}

	return encodeManifestDocuments(documents)
}

// WaitProviderPrerequisitesReady waits for provider resources to become active before dependent manifests are applied.
func WaitProviderPrerequisitesReady(client *rancher.Client, config *Config) error {
	provider := CreateCAPIProvider(config.Provider)
	if provider.WaitProviderPrerequisitesFunc == nil {
		return fmt.Errorf("provider %q does not implement prerequisite readiness checks", config.Provider)
	}

	return provider.WaitProviderPrerequisitesFunc(client)
}

// CreateClusterManifestFromDefaults is a helper function that creates a cluster manifest from the provided defaults and machine pools.
func CreateClusterManifestFromDefaults(config *Config, machinePools []provisioninginput.MachinePools, clusterName string) ([]byte, error) {
	templateBytes, err := loadManifest(config.ClusterYAML)
	if err != nil {
		return nil, err
	}

	documents, err := decodeManifestDocuments(templateBytes)
	if err != nil {
		return nil, err
	}

	provider := CreateCAPIProvider(config.Provider)
	if provider.RenderClusterManifestFunc == nil {
		return nil, fmt.Errorf("provider %q does not implement cluster manifest rendering", config.Provider)
	}

	if err := provider.RenderClusterManifestFunc(documents, config, machinePools, clusterName); err != nil {
		return nil, err
	}

	return encodeManifestDocuments(documents)
}

func unmarshalSecret(data []byte, path string) (*corev1.Secret, error) {
	var secret corev1.Secret

	if err := yaml.UnmarshalStrict(data, &secret); err != nil {
		return nil, fmt.Errorf("unmarshal secret manifest %q: %w", path, err)
	}

	return &secret, nil
}

func loadManifest(path string) ([]byte, error) {
	resolvedPath := resolveManifestPath(path)
	file, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest %q: %w", path, err)
	}

	return file, nil
}

func resolveManifestPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}

	if _, err := os.Stat(path); err == nil {
		return path
	}

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		return path
	}

	packageRelativePath := filepath.Join(filepath.Dir(sourceFile), path)
	if _, err := os.Stat(packageRelativePath); err == nil {
		return packageRelativePath
	}

	return path
}

func kubectlCreateCommand(kubeconfigPath string) []string {
	return []string{"kubectl", "--kubeconfig", kubeconfigPath, "create", "-f", "-"}
}

func kubectlGetCommand(kubeconfigPath string) []string {
	return []string{"kubectl", "--kubeconfig", kubeconfigPath, "get", "-f", "-"}
}

func runLocalKubectl(args []string, input []byte) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	if len(input) > 0 {
		cmd.Stdin = bytes.NewReader(input)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("failed to run kubectl command %q: %w", strings.Join(args, " "), err)
	}

	return string(out), err
}

func isManifestAlreadyApplied(kubeconfigPath string, manifest []byte) bool {
	output, err := runLocalKubectl(kubectlGetCommand(kubeconfigPath), manifest)
	if err != nil {
		return false
	}

	if strings.Contains(output, "NotFound") || strings.Contains(output, "not found") {
		logrus.Debug("Manifest not found in cluster, will apply it.")

		return false
	}

	logrus.Debug("Manifest already exists in cluster, skipping apply.")

	return true
}

func decodeManifestDocuments(content []byte) ([]map[string]any, error) {
	decoder := yamlv3.NewDecoder(bytes.NewReader(content))
	documents := []map[string]any{}

	for {
		var document map[string]any

		err := decoder.Decode(&document)
		if err == io.EOF {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("decode manifest documents: %w", err)
		}

		if len(document) == 0 {
			continue
		}

		documents = append(documents, document)
	}

	return documents, nil
}

func encodeManifestDocuments(documents []map[string]any) ([]byte, error) {
	buffer := new(bytes.Buffer)
	encoder := yamlv3.NewEncoder(buffer)
	defer encoder.Close()

	for _, document := range documents {
		if err := encoder.Encode(document); err != nil {
			return nil, fmt.Errorf("encode manifest documents: %w", err)
		}
	}

	return buffer.Bytes(), nil
}

func writeKubeconfigToTempFile(client *rancher.Client, clusterID string) (string, func(), error) {
	cfg, err := kubeconfig.GetKubeconfig(client, clusterID)
	if err != nil {
		return "", nil, fmt.Errorf("generate kubeconfig for cluster %q: %w", clusterID, err)
	}

	rawCfg, err := (*cfg).RawConfig()
	if err != nil {
		return "", nil, fmt.Errorf("read raw kubeconfig for cluster %q: %w", clusterID, err)
	}

	kcfgBytes, err := clientcmd.Write(rawCfg)
	if err != nil {
		return "", nil, fmt.Errorf("marshal kubeconfig for cluster %q: %w", clusterID, err)
	}

	tmpFile, err := os.CreateTemp("", "capipnp-kubeconfig-*.yaml")
	if err != nil {
		return "", nil, fmt.Errorf("create temp kubeconfig file: %w", err)
	}

	if _, err := tmpFile.Write(kcfgBytes); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())

		return "", nil, fmt.Errorf("write temp kubeconfig file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("close temp kubeconfig file: %w", err)
	}

	cleanup := func() {
		os.Remove(tmpFile.Name())
	}

	return tmpFile.Name(), cleanup, nil
}
