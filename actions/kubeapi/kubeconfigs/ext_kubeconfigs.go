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
	SecretKind                                          = "Secret"
	ExtTokenPrefix                                      = "ext/"
	StatusConditionType                                 = "TokenCreated"
	UserIDLabel                                         = "cattle.io/user-id"
	KubeconfigIDLabel                                   = "authn.management.cattle.io/kubeconfig-id"
	KindLabel                                           = "authn.management.cattle.io/kind"
	KindLabelKubeconfigValue                            = "kubeconfig"
	CattleKindLabel                                     = "cattle.io/kind"
	CattleKindTokenValue                                = "token"
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

// WaitForBackingConfigMapDeletion polls until the backing ConfigMap with the given name is deleted
func WaitForBackingConfigMapDeletion(client *rancher.Client, kubeconfigName string) error {
	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		_, err := client.WranglerContext.Core.ConfigMap().Get(KubeconfigConfigmapNamespace, kubeconfigName, metav1.GetOptions{})
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
func WaitForBackingExtTokenDeletion(client *rancher.Client, kubeconfigName string) error {
	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		tokens, err := GetBackingExtTokensForKubeconfig(client, kubeconfigName)
		if err != nil {
			return false, err
		}
		return len(tokens.Items) == 0, nil
	})
}

// WaitForBackingSecretDeletion polls until the v1.Secret for each given backing token in the cattle-tokens namespace is deleted or the timeout is reached.
func WaitForBackingSecretDeletion(client *rancher.Client, tokens *extapi.TokenList) error {
	for _, token := range tokens.Items {
		err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
			_, err := client.WranglerContext.Core.Secret().Get(KubeconfigConfigmapNamespace, token.Name, metav1.GetOptions{})
			if err != nil {
				if k8serrors.IsNotFound(err) {
					return true, nil
				}
				return false, err
			}
			return false, nil
		})
		if err != nil {
			return fmt.Errorf("backing secret %s was not deleted: %w", token.Name, err)
		}
	}
	return nil
}

// WaitForBackingExtTokenCreation polls until at least one backing ext Token for the given kubeconfig name is created
func WaitForBackingExtTokenCreation(client *rancher.Client, kubeconfigName string) error {
	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		tokens, err := GetBackingExtTokensForKubeconfig(client, kubeconfigName)
		if err != nil {
			return false, err
		}
		return len(tokens.Items) > 0, nil
	})
}

// WaitForBackingConfigMapCreation polls until the backing ConfigMap with the given name is created
func WaitForBackingConfigMapCreation(client *rancher.Client, kubeconfigName string) error {
	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		_, err := client.WranglerContext.Core.ConfigMap().Get(KubeconfigConfigmapNamespace, kubeconfigName, metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
}

// WaitForBackingSecretCreation polls until the v1.Secret for each given backing token in the cattle-tokens namespace is created or the timeout is reached.
func WaitForBackingSecretCreation(client *rancher.Client, tokens *extapi.TokenList) error {
	for _, token := range tokens.Items {
		err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
			_, err := client.WranglerContext.Core.Secret().Get(KubeconfigConfigmapNamespace, token.Name, metav1.GetOptions{})
			if err != nil {
				if k8serrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		})
		if err != nil {
			return fmt.Errorf("backing secret %s was not created: %w", token.Name, err)
		}
	}
	return nil
}
