package kubeconfigs

import (
	"context"
	"fmt"

	extapi "github.com/rancher/rancher/pkg/apis/ext.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults"
	extkubeconfigapi "github.com/rancher/shepherd/extensions/kubeapi/kubeconfigs"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
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

// CreateKubeconfig creates a kubeconfig using wrangler context and returns the created kubeconfig object
func CreateKubeconfig(client *rancher.Client, clusters []string, currentContext string, ttl *int64) (*extapi.Kubeconfig, error) {
	name := namegen.AppendRandomString("test-kubeconfig")
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

	createdKubeconfig, err := extkubeconfigapi.CreateKubeconfig(client, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubeconfig: %w", err)
	}

	return createdKubeconfig, nil
}

// GetBackingV3TokensForKubeconfig returns all the backing v3 tokens created for a specific kubeconfig name
func GetBackingV3TokensForKubeconfig(client *rancher.Client, kubeconfigName string) ([]management.Token, error) {
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

// GetBackingExtTokensForKubeconfig returns all the backing ext tokens created for a specific kubeconfig name
func GetBackingExtTokensForKubeconfig(client *rancher.Client, kubeconfigName string) (*extapi.TokenList, error) {
	if kubeconfigName == "" {
		return nil, fmt.Errorf("kubeconfig name cannot be empty")
	}

	return client.WranglerContext.Ext.Token().List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", KubeconfigIDLabel, kubeconfigName),
	})
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

// WaitForBackingV3TokenDeletion polls until the backing v3 Token with the given name is deleted or the timeout is reached.
func WaitForBackingV3TokenDeletion(client *rancher.Client, name string) error {
	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		tokens, err := GetBackingV3TokensForKubeconfig(client, name)
		if err != nil {
			return false, err
		}
		return len(tokens) == 0, nil
	})
}

// WaitForBackingExtTokenDeletion polls until the backing ext Token with the given name is deleted or the timeout is reached.
func WaitForBackingExtTokenDeletion(client *rancher.Client, name string) error {
	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		tokens, err := GetBackingExtTokensForKubeconfig(client, name)
		if err != nil {
			return false, err
		}
		return len(tokens.Items) == 0, nil
	})
}
