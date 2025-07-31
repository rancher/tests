package kubeconfigs

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	extapi "github.com/rancher/rancher/pkg/apis/ext.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// VerifyKubeconfigTokens validates tokens and ownerReferences based on cluster type.
func VerifyKubeconfigTokens(client *rancher.Client, kubeconfigObj *extapi.Kubeconfig, clusterType string) error {
	tokenOwnerRefs := map[string]struct{}{}
	for _, ownerRef := range kubeconfigObj.OwnerReferences {
		if ownerRef.Kind == TokenKind {
			tokenOwnerRefs[ownerRef.Name] = struct{}{}
		}
	}

	tokenCreatedConds := []string{}
	for _, cond := range kubeconfigObj.Status.Conditions {
		if cond.Type == StatusConditionType && cond.Status == TrueConditionStatus {
			tokenCreatedConds = append(tokenCreatedConds, cond.Message)
		}
	}

	expectedTokenCount := 1
	if strings.ToLower(clusterType) == AceClusterType {
		expectedTokenCount = len(kubeconfigObj.Spec.Clusters) + 1
	}

	if len(kubeconfigObj.Status.Tokens) != expectedTokenCount {
		return fmt.Errorf("unexpected number of tokens for cluster type %s: got %d, want %d",
			clusterType, len(kubeconfigObj.Status.Tokens), expectedTokenCount)
	}

	if len(tokenOwnerRefs) != expectedTokenCount {
		return fmt.Errorf("unexpected number of ownerReference tokens for cluster type %s: got %d, want %d",
			clusterType, len(tokenOwnerRefs), expectedTokenCount)
	}

	if len(tokenCreatedConds) != expectedTokenCount {
		return fmt.Errorf("unexpected number of TokenCreated conditions for cluster type %s: got %d, want %d",
			clusterType, len(tokenCreatedConds), expectedTokenCount)
	}

	for _, tokenName := range kubeconfigObj.Status.Tokens {
		if _, exists := tokenOwnerRefs[tokenName]; !exists {
			return fmt.Errorf("token %q in status.tokens is missing from ownerReferences", tokenName)
		}
	}

	return nil
}

// VerifyKubeconfigSpec validates the kubeconfig spec against expected values.
// For ACE clusters, it validates against the worker-node context.
func VerifyKubeconfigSpec(kubeconfigObj *extapi.Kubeconfig, expectedClusters []string, expectedCurrentContext string, expectedTTL int64, clusterType string) error {
	if len(kubeconfigObj.Spec.Clusters) != len(expectedClusters) {
		return fmt.Errorf("clusters length mismatch: got %d, want %d", len(kubeconfigObj.Spec.Clusters), len(expectedClusters))
	}

	clusterMap := make(map[string]struct{}, len(kubeconfigObj.Spec.Clusters))
	for _, c := range kubeconfigObj.Spec.Clusters {
		clusterMap[c] = struct{}{}
	}
	for _, ec := range expectedClusters {
		if _, found := clusterMap[ec]; !found {
			return fmt.Errorf("expected cluster %q not found in kubeconfig spec clusters", ec)
		}
	}

	actualContext := kubeconfigObj.Spec.CurrentContext
	if strings.EqualFold(clusterType, AceClusterType) {
		var baseClusterName string
		for _, clusterName := range kubeconfigObj.Spec.Clusters {
			if !strings.Contains(clusterName, "pool0") {
				baseClusterName = clusterName
				break
			}
		}
		expectedContext, err := getACEWorkerNodeContextFromSpec(kubeconfigObj, baseClusterName)
		if err != nil {
			return err
		}
		if !strings.EqualFold(actualContext, expectedContext) {
			return fmt.Errorf("currentContext mismatch for ACE: got %q, want %q (worker-node context)", actualContext, expectedContext)
		}
	} else {
		if !strings.EqualFold(actualContext, expectedCurrentContext) {
			return fmt.Errorf("currentContext mismatch: got %q, want %q (case-insensitive)", actualContext, expectedCurrentContext)
		}
	}

	if kubeconfigObj.Spec.TTL != expectedTTL {
		return fmt.Errorf("TTL mismatch: got %d, want %d", kubeconfigObj.Spec.TTL, expectedTTL)
	}

	return nil
}

// VerifyAllContextsUsable loads all contexts in the kubeconfig and verifies them.
func VerifyAllContextsUsable(kubeconfigFile string, skipRancherContext bool) error {
	config, err := clientcmd.LoadFromFile(kubeconfigFile)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	for ctxName := range config.Contexts {
		if err := verifyClusterContextUsable(kubeconfigFile, ctxName, skipRancherContext); err != nil {
			return err
		}
	}
	return nil
}

// VerifyKubeconfigContent validates kubeconfig content for ACE or Non-ACE clusters.
// isACE = true for ACE cluster, false for Non-ACE cluster.
// currentContextOverride allows overriding which cluster should be the current context.
func VerifyKubeconfigContent(client *rancher.Client, kubeconfigFile string, expectedClusterIDs []string, rancherHost string, isACE bool, currentContextOverride string) error {
	kc, err := clientcmd.LoadFromFile(kubeconfigFile)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig file %s: %w", kubeconfigFile, err)
	}

	clusterNameToID, _, err := GetMapClusterNameToID(client, expectedClusterIDs)
	if err != nil {
		return err
	}

	rancherURL := fmt.Sprintf("https://%s", rancherHost)
	if err := validateRancherEntry(kc, rancherURL); err != nil {
		return err
	}

	workerNodePattern := regexp.MustCompile(`^https://\d+\.\d+\.\d+\.\d+:6443$`)

	for _, id := range expectedClusterIDs {
		if err := validateCluster(kc, clusterNameToID, id, isACE, workerNodePattern); err != nil {
			return err
		}
	}

	expectedContext := currentContextOverride
	if expectedContext == "" && len(expectedClusterIDs) > 0 {
		if isACE {
			for name, id := range clusterNameToID {
				if id == expectedClusterIDs[0] {
					expectedContext, err = getACEWorkerNodeContext(kc, name)
					if err != nil {
						return err
					}
					break
				}
			}
		} else {
			for name, id := range clusterNameToID {
				if id == expectedClusterIDs[0] {
					expectedContext = name
					break
				}
			}
		}
	}

	if kc.CurrentContext != expectedContext {
		return fmt.Errorf("current-context is %q, want %q", kc.CurrentContext, expectedContext)
	}

	return nil
}

// VerifyKubeconfigContentMixed validates kubeconfig content for both ACE and Non-ACE clusters.
func VerifyKubeconfigContentMixed(client *rancher.Client, kubeconfigFile string, nonACEClusterIDs, aceClusterIDs []string, rancherHost string, currentContextOverride string) error {
	kc, err := clientcmd.LoadFromFile(kubeconfigFile)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig file %s: %w", kubeconfigFile, err)
	}

	allClusterIDs := append(nonACEClusterIDs, aceClusterIDs...)
	clusterNameToID, _, err := GetMapClusterNameToID(client, allClusterIDs)
	if err != nil {
		return err
	}

	rancherURL := fmt.Sprintf("https://%s", rancherHost)
	if err := validateRancherEntry(kc, rancherURL); err != nil {
		return err
	}

	workerNodePattern := regexp.MustCompile(`^https://\d+\.\d+\.\d+\.\d+:6443$`)

	for _, id := range nonACEClusterIDs {
		if err := validateCluster(kc, clusterNameToID, id, false, workerNodePattern); err != nil {
			return err
		}
	}

	for _, id := range aceClusterIDs {
		if err := validateCluster(kc, clusterNameToID, id, true, workerNodePattern); err != nil {
			return err
		}
	}

	expectedContext := currentContextOverride
	if expectedContext == "" {
		if len(nonACEClusterIDs) > 0 {
			for name, id := range clusterNameToID {
				if id == nonACEClusterIDs[0] {
					expectedContext = name
					break
				}
			}
		} else if len(aceClusterIDs) > 0 {
			for name, id := range clusterNameToID {
				if id == aceClusterIDs[0] {
					expectedContext, err = getACEWorkerNodeContext(kc, name)
					if err != nil {
						return err
					}
					break
				}
			}
		}
	}

	if kc.CurrentContext != expectedContext {
		return fmt.Errorf("current-context is %q, want %q", kc.CurrentContext, expectedContext)
	}

	return nil
}

func verifyClusterContextUsable(kubeconfigFile, contextName string, skipRancherContext bool) error {
	if skipRancherContext && contextName == RancherContext {
		return nil
	}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigFile},
		&clientcmd.ConfigOverrides{
			CurrentContext: contextName,
		}).ClientConfig()
	if err != nil {
		return fmt.Errorf("failed to create client config for context %q: %w", contextName, err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes clientset for context %q: %w", contextName, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaults.TenSecondTimeout)
	defer cancel()
	_, err = clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to verify context %q by listing nodes: %w", contextName, err)
	}

	log.Infof("Context %q is usable.", contextName)
	return nil
}

func getACEWorkerNodeContext(kc *clientcmdapi.Config, baseClusterName string) (string, error) {
	pattern := fmt.Sprintf(`^%s-pool\d+.*$`, regexp.QuoteMeta(baseClusterName))
	workerNodeRegex := regexp.MustCompile(pattern)
	log.WithField("pattern", pattern).Info("Expected worker-node context pattern")
	log.Info("Available contexts in kubeconfig:")

	for name := range kc.Contexts {
		log.WithField("context", name).Info("Checking context")
		if name == RancherContext {
			continue
		}
		if workerNodeRegex.MatchString(name) {
			log.WithField("matched_context", name).Info("Matched worker-node context")
			return name, nil
		}
	}
	return "", fmt.Errorf("no ACE worker-node context found matching pattern %q", pattern)
}

func validateRancherEntry(kc *clientcmdapi.Config, rancherURL string) error {
	rancherCluster, ok := kc.Clusters[RancherContext]
	if !ok {
		return fmt.Errorf(`rancher cluster entry not found`)
	}
	if !strings.Contains(rancherCluster.Server, rancherURL) {
		return fmt.Errorf("rancher cluster server URL mismatch: got %s, want to contain %s", rancherCluster.Server, rancherURL)
	}
	rancherCtx, ok := kc.Contexts[RancherContext]
	if !ok {
		return fmt.Errorf(`context %q not found`, RancherContext)
	}
	if rancherCtx.AuthInfo != RancherContext || rancherCtx.Cluster != RancherContext {
		return fmt.Errorf(`context %q should have user and cluster set to %q`, RancherContext, RancherContext)
	}
	if _, ok := kc.AuthInfos[RancherContext]; !ok {
		return fmt.Errorf(`user %q not found in kubeconfig users`, RancherContext)
	}
	return nil
}

func validateCluster(kc *clientcmdapi.Config, clusterNameToID map[string]string, id string, isACE bool, workerNodePattern *regexp.Regexp) error {
	clusterName := ""
	for name, cid := range clusterNameToID {
		if cid == id {
			clusterName = name
			break
		}
	}

	cluster, ok := kc.Clusters[clusterName]
	if !ok {
		if isACE {
			return fmt.Errorf("ACE cluster %q not found in kubeconfig", clusterName)
		}
		return fmt.Errorf("non-ACE cluster %q not found in kubeconfig", clusterName)
	}

	ctx, ok := kc.Contexts[clusterName]
	if !ok {
		return fmt.Errorf("context for cluster %q not found", clusterName)
	}

	if isACE {
		if ctx.AuthInfo != clusterName || ctx.Cluster != clusterName {
			return fmt.Errorf("context for ACE cluster %q has invalid user or cluster values", clusterName)
		}
		if cluster.Server != "" && !strings.Contains(cluster.Server, "/k8s/clusters/"+id) && !workerNodePattern.MatchString(cluster.Server) {
			return fmt.Errorf("ACE cluster %s server URL mismatch: got %s", clusterName, cluster.Server)
		}
		if _, ok := kc.AuthInfos[clusterName]; !ok {
			return fmt.Errorf("ACE cluster %s should have its own user entry", clusterName)
		}
	} else {
		if ctx.AuthInfo != RancherContext || ctx.Cluster != clusterName {
			return fmt.Errorf("context for non-ACE cluster %q has invalid user or cluster values", clusterName)
		}
		if !strings.Contains(cluster.Server, "/k8s/clusters/"+id) {
			return fmt.Errorf("non-ACE cluster %s server URL mismatch: got %s", clusterName, cluster.Server)
		}
		if _, ok := kc.AuthInfos[clusterName]; ok {
			return fmt.Errorf("non-ACE cluster %s should not have its own user entry", clusterName)
		}
	}

	return nil
}

func getACEWorkerNodeContextFromSpec(kcObj *extapi.Kubeconfig, baseClusterName string) (string, error) {
	for _, ctxName := range kcObj.Spec.Clusters {
		if strings.HasPrefix(ctxName, baseClusterName) {
			return ctxName, nil
		}
	}
	return "", fmt.Errorf("no ACE worker-node context found starting with %q", baseClusterName)
}
