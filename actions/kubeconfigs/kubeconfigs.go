package kubeconfigs

import (
	"context"
	"fmt"
	"os"

	extapi "github.com/rancher/rancher/pkg/apis/ext.cattle.io/v1"
	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/clusters"
	configDefaults "github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	clientcmd "k8s.io/client-go/tools/clientcmd"
)

const (
	TokenKind                                           = "Token"
	StatusConditionType                                 = "TokenCreated"
	UserIDLabel                                         = "cattle.io/user-id"
	KubeconfigIDLabel                                   = "authn.management.cattle.io/kubeconfig-id"
	KubeconfigConfigmapNamespace                        = "cattle-tokens"
	KubeconfigFile                                      = "kc_kubeconfig.yaml"
	DummyFinalizer                                      = "cleanup.rancher.io/dummy"
	StatusCompletedSummary                              = "Complete"
	TrueConditionStatus          metav1.ConditionStatus = "True"
	FalseConditionStatus         metav1.ConditionStatus = "False"
	AceClusterType                                      = "ace"
	NonAceClusterType                                   = "non-ace"
	RancherContext                                      = "rancher"
)

// CreateKubeconfig creates a kubeconfig using public API
func CreateKubeconfig(client *rancher.Client, clusters []string, currentContext string, ttl *int64) (*extapi.Kubeconfig, error) {
	name := namegen.AppendRandomString("testkubeconfig")
	kubeconfig := &extapi.Kubeconfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: extapi.KubeconfigSpec{
			Clusters: clusters,
		},
	}

	if currentContext != "" {
		kubeconfig.Spec.CurrentContext = currentContext
	}
	if ttl != nil {
		kubeconfig.Spec.TTL = *ttl
	}

	createdKubeconfig, err := client.WranglerContext.Ext.Kubeconfig().Create(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubeconfig: %w", err)
	}

	return createdKubeconfig, nil
}

// GetKubeconfig retrieves a kubeconfig by name using the GET API
func GetKubeconfig(client *rancher.Client, name string) (*extapi.Kubeconfig, error) {
	kubeconfig, err := client.WranglerContext.Ext.Kubeconfig().Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig %s: %w", name, err)
	}

	return kubeconfig, nil
}

// ListKubeconfig retrieves kubeconfig using the LIST API
func ListKubeconfigs(client *rancher.Client) (*extapi.KubeconfigList, error) {
	kubeconfigs, err := client.WranglerContext.Ext.Kubeconfig().List(metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list kubeconfig: %w", err)
	}

	return kubeconfigs, nil
}

// UpdateKubeconfig updates an existing kubeconfig using public API
func UpdateKubeconfig(client *rancher.Client, kcObj *extapi.Kubeconfig) (*extapi.Kubeconfig, error) {
	if kcObj == nil {
		return nil, fmt.Errorf("kubeconfig object is nil")
	}

	updated, err := client.WranglerContext.Ext.Kubeconfig().Update(kcObj)
	if err != nil {
		return nil, fmt.Errorf("failed to update kubeconfig %s: %w", kcObj.Name, err)
	}
	return updated, nil
}

// DeleteKubeconfig deletes a kubeconfig by name using public API
func DeleteKubeconfig(client *rancher.Client, name string) error {
	err := client.WranglerContext.Ext.Kubeconfig().Delete(name, &metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete kubeconfig %s: %w", name, err)
	}

	err = WaitForKubeconfigDeletion(client, name)
	if err != nil {
		return fmt.Errorf("timed out waiting for kubeconfig %s to be deleted: %w", name, err)
	}

	return nil
}

// GetBackingTokensForKubeconfigName returns all the backing tokens created for a specific kubeconfig name
func GetBackingTokensForKubeconfigName(client *rancher.Client, kubeconfigName string) ([]management.Token, error) {
	if kubeconfigName == "" {
		return nil, fmt.Errorf("kubeconfig name cannot be empty")
	}

	tokenCollection, err := client.Management.Token.List(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list tokens: %w", err)
	}

	var filteredTokens []management.Token
	for _, token := range tokenCollection.Data {
		if val, ok := token.Labels[KubeconfigIDLabel]; ok && val == kubeconfigName {
			filteredTokens = append(filteredTokens, token)
		}
	}

	return filteredTokens, nil
}

// CreateDownstreamCluster creates a ACE enabled or disabled downstream cluster
func CreateDownstreamCluster(client *rancher.Client, isACE bool) (*v1.SteveAPIObject, *clusters.ClusterConfig, error) {
	cattleConfig := config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))
	cattleConfig, err := configDefaults.SetK8sDefault(client, configDefaults.K3S, cattleConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to set k8s default to k3s: %w", err)
	}

	nodeRolesAll := []provisioninginput.MachinePools{provisioninginput.AllRolesMachinePool}

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(configDefaults.ClusterConfigKey, cattleConfig, clusterConfig)

	if isACE {
		networking := provisioninginput.Networking{
			LocalClusterAuthEndpoint: &rkev1.LocalClusterAuthEndpoint{
				Enabled: true,
			},
		}
		clusterConfig.Networking = &networking
	}

	clusterConfig.MachinePools = nodeRolesAll

	provider := provisioning.CreateProvider(clusterConfig.Provider)
	credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
	machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

	clusterObject, err := provisioning.CreateProvisioningCluster(client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
	if err != nil {
		if isACE {
			return nil, nil, fmt.Errorf("failed to create ACE enabled cluster: %w", err)
		}
		return nil, nil, fmt.Errorf("failed to create non-ACE cluster: %w", err)
	}

	return clusterObject, clusterConfig, nil
}

// GetMapClusterNameToID maps cluster names to their IDs from expectedClusterIDs.
func GetMapClusterNameToID(client *rancher.Client, expectedClusterIDs []string) (map[string]string, string, error) {
	clusterNameToID := make(map[string]string, len(expectedClusterIDs))
	var mainClusterName string

	for _, id := range expectedClusterIDs {
		testCluster, err := client.Management.Cluster.ByID(id)
		if err != nil {
			return nil, "", fmt.Errorf("failed to get cluster by ID %s: %w", id, err)
		}
		clusterNameToID[testCluster.Name] = id
		if mainClusterName == "" {
			mainClusterName = testCluster.Name
		}
	}

	return clusterNameToID, mainClusterName, nil
}

// GetCurrentContext retrieves the current context from the kubeconfig file
func GetCurrentContext(kubeconfigFile string) (string, error) {
	config, err := clientcmd.LoadFromFile(kubeconfigFile)
	if err != nil {
		return "", fmt.Errorf("failed to load kubeconfig file %q: %w", kubeconfigFile, err)
	}

	if config.CurrentContext == "" {
		return "", fmt.Errorf("kubeconfig file %q has no current-context set", kubeconfigFile)
	}

	return config.CurrentContext, nil
}

// WaitForKubeconfigDeletion polls until the kubeconfig with the given name is deleted or the timeout is reached.
func WaitForKubeconfigDeletion(client *rancher.Client, name string) error {
	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		_, err := GetKubeconfig(client, name)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})
}

// WaitForBackingConfigMapDeletion polls until the backing ConfigMap with the given name is deleted or the timeout is reached.
func WaitForBackingConfigMapDeletion(client *rancher.Client, name string) error {
	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		_, err := client.WranglerContext.Core.ConfigMap().Get(KubeconfigConfigmapNamespace, name, metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})
}

// WaitForBackingTokenDeletion polls until the backing Token with the given name is deleted or the timeout is reached.
func WaitForBackingTokenDeletion(client *rancher.Client, name string) error {
	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		tokens, err := GetBackingTokensForKubeconfigName(client, name)
		if err != nil {
			return false, err
		}
		return len(tokens) == 0, nil
	})
}
