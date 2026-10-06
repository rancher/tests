package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	apisV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	extclusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/kubeapi/workloads/daemonsets"
	"github.com/rancher/shepherd/extensions/kubeconfig"
	"github.com/rancher/shepherd/extensions/kubectl"
	"github.com/rancher/tests/actions/kubeapi/workloads/pods"
	authnv1 "k8s.io/api/authentication/v1"
	authnv1beta1 "k8s.io/api/authentication/v1beta1"
	authzv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	KubeAPIAuthEndpoint  = "http://127.0.0.1:6440/v1/authenticate"
	KubeAPIAuthNamespace = "cattle-system"
	KubeAPIAuthDaemonSet = "kube-api-auth"

	TokenReviewKind = "TokenReview"

	kubeAPIAuthAppLabel  = "app=" + KubeAPIAuthDaemonSet
	kubeAPIAuthLogBuffer = "64KB"
	statusMarker         = "__STATUS__"
	rancherProxyPath     = "/k8s/clusters/"
)

var (
	TokenReviewAPIV1     = authnv1.SchemeGroupVersion.String()
	TokenReviewAPIV1Beta = authnv1beta1.SchemeGroupVersion.String()
)

// TokenReviewResult holds what kube-api-auth answered a TokenReview with
type TokenReviewResult struct {
	StatusCode    int
	APIVersion    string
	Kind          string
	Authenticated bool
	Username      string
	Groups        []string
	Error         string
	Body          string
}

type tokenReviewReply struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Status     struct {
		Authenticated bool   `json:"authenticated"`
		Error         string `json:"error"`
		User          struct {
			Username string   `json:"username"`
			Groups   []string `json:"groups"`
		} `json:"user"`
	} `json:"status"`
}

// NewTokenReviewPayload builds a TokenReview body carrying the given apiVersion
func NewTokenReviewPayload(apiVersion, kind, token string) string {
	return fmt.Sprintf(`{"apiVersion":%q,"kind":%q,"spec":{"token":%q}}`, apiVersion, kind, token)
}

// NewTokenReviewPayloadWithoutAPIVersion builds a TokenReview body that names no apiVersion at all
func NewTokenReviewPayloadWithoutAPIVersion(kind, token string) string {
	return fmt.Sprintf(`{"kind":%q,"spec":{"token":%q}}`, kind, token)
}

// ReviewToken sends kube-api-auth a TokenReview carrying the given apiVersion, kind and token
func ReviewToken(client *rancher.Client, clusterID, apiVersion, kind, token string) (*TokenReviewResult, error) {
	return PostTokenReview(client, clusterID, NewTokenReviewPayload(apiVersion, kind, token))
}

// PostTokenReview sends a TokenReview to kube-api-auth from the host network of a control plane node
func PostTokenReview(client *rancher.Client, clusterID, payload string) (*TokenReviewResult, error) {
	command := []string{"sh", "-c", fmt.Sprintf(
		`curl -s -w '%s%%{http_code}' -X POST %s -H 'Content-Type: application/json' --data-binary %s`,
		statusMarker, KubeAPIAuthEndpoint, shellQuote(payload))}

	output, err := kubectl.CommandOnControlPlane(client, clusterID, command, kubeAPIAuthLogBuffer)
	if err != nil {
		return nil, fmt.Errorf("sending a TokenReview to %s: %w", KubeAPIAuthEndpoint, err)
	}

	return parseTokenReviewOutput(output)
}

func parseTokenReviewOutput(output string) (*TokenReviewResult, error) {
	marker := strings.LastIndex(output, statusMarker)
	if marker < 0 {
		return nil, fmt.Errorf("the TokenReview request wrote no status code, kube-api-auth may not be listening on %s: %s",
			KubeAPIAuthEndpoint, output)
	}

	body := output[:marker]

	statusCode, err := strconv.Atoi(strings.TrimSpace(output[marker+len(statusMarker):]))
	if err != nil {
		return nil, fmt.Errorf("reading the status code the TokenReview request wrote: %w", err)
	}

	result := &TokenReviewResult{StatusCode: statusCode, Body: strings.TrimSpace(body)}

	reply := new(tokenReviewReply)
	if err := json.Unmarshal([]byte(body), reply); err != nil {
		return result, nil
	}

	result.APIVersion = reply.APIVersion
	result.Kind = reply.Kind
	result.Authenticated = reply.Status.Authenticated
	result.Username = reply.Status.User.Username
	result.Groups = reply.Status.User.Groups
	result.Error = reply.Status.Error

	return result, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func tokenFromKubeconfig(generated string) (string, error) {
	raw, err := clientcmd.Load([]byte(generated))
	if err != nil {
		return "", fmt.Errorf("parsing the generated kubeconfig: %w", err)
	}

	for _, authInfo := range raw.AuthInfos {
		if strings.Contains(authInfo.Token, ":") {
			return authInfo.Token, nil
		}
	}

	return "", fmt.Errorf("the generated kubeconfig carries no token of the form accessKey:secret")
}

// ClusterAuthTokenFromKubeconfig generates the cluster's kubeconfig as the given user and returns the token it carries
func ClusterAuthTokenFromKubeconfig(client *rancher.Client, clusterID string) (string, error) {
	generated, err := aceKubeconfig(client, clusterID)
	if err != nil {
		return "", err
	}

	return tokenFromKubeconfig(generated)
}

// WaitForClusterAuthToken polls the downstream cluster until Rancher has synced the cluster auth token of the given access key
func WaitForClusterAuthToken(client *rancher.Client, clusterID, accessKey string) error {
	downstream, err := client.WranglerContext.DownStreamClusterWranglerContext(clusterID)
	if err != nil {
		return fmt.Errorf("reaching cluster %s to look for cluster auth token %s: %w", clusterID, accessKey, err)
	}

	var lastErr error

	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true,
		func(context.Context) (bool, error) {
			_, lastErr = downstream.Cluster.ClusterAuthToken().Get(KubeAPIAuthNamespace, accessKey, metav1.GetOptions{})

			return lastErr == nil, nil
		})
	if pollErr != nil {
		return fmt.Errorf("cluster auth token %s never appeared in %s on cluster %s: %w",
			accessKey, KubeAPIAuthNamespace, clusterID, lastErr)
	}

	return nil
}

// KubeAPIAuthImage returns the image the kube-api-auth daemon set runs on the given cluster
func KubeAPIAuthImage(client *rancher.Client, clusterID string) (string, error) {
	daemonSet, err := daemonsets.GetDaemonSetByName(client, clusterID, KubeAPIAuthNamespace, KubeAPIAuthDaemonSet)
	if err != nil {
		return "", fmt.Errorf("the %s daemon set is absent from %s on cluster %s, so the authorized cluster endpoint is not deployed: %w",
			KubeAPIAuthDaemonSet, KubeAPIAuthNamespace, clusterID, err)
	}

	for _, container := range daemonSet.Spec.Template.Spec.Containers {
		if container.Name == KubeAPIAuthDaemonSet {
			return container.Image, nil
		}
	}

	return "", fmt.Errorf("the %s daemon set runs no container named %s", KubeAPIAuthDaemonSet, KubeAPIAuthDaemonSet)
}

// EchoesRequestAPIVersion reports whether kube-api-auth answers a TokenReview in the apiVersion the request carried
func EchoesRequestAPIVersion(client *rancher.Client, clusterID, token string) (bool, error) {
	result, err := ReviewToken(client, clusterID, TokenReviewAPIV1, TokenReviewKind, token)
	if err != nil {
		return false, err
	}

	if result.StatusCode != http.StatusOK {
		return false, fmt.Errorf("a valid token should be reviewed, but %s answered %d with body %q",
			KubeAPIAuthDaemonSet, result.StatusCode, result.Body)
	}

	if !result.Authenticated {
		return false, fmt.Errorf("a valid token should authenticate, but %s answered %q", KubeAPIAuthDaemonSet, result.Body)
	}

	return result.APIVersion == TokenReviewAPIV1, nil
}

// EnsureACEEnabled turns the authorized cluster endpoint on when it is off and returns a func restoring the state it found
func EnsureACEEnabled(client *rancher.Client, clusterID string) (func() error, error) {
	cluster, _, err := provisioningCluster(client, clusterID)
	if err != nil {
		return nil, err
	}

	restore := func() error { return nil }

	if !cluster.Spec.LocalClusterAuthEndpoint.Enabled {
		if err := setACE(client, clusterID, true); err != nil {
			return restore, err
		}

		restore = func() error { return setACE(client, clusterID, false) }
	}

	if err := daemonsets.WaitForDaemonSetReady(client, clusterID, KubeAPIAuthNamespace, KubeAPIAuthDaemonSet); err != nil {
		return restore, fmt.Errorf("the %s daemon set never became ready on cluster %s: %w", KubeAPIAuthDaemonSet, clusterID, err)
	}

	return restore, nil
}

func setACE(client *rancher.Client, clusterID string, enabled bool) error {
	cluster, clusterObj, err := provisioningCluster(client, clusterID)
	if err != nil {
		return err
	}

	cluster.Spec.LocalClusterAuthEndpoint.Enabled = enabled

	if _, err := extclusters.UpdateK3SRKE2Cluster(client, clusterObj, cluster); err != nil {
		return fmt.Errorf("setting the authorized cluster endpoint of cluster %s to %t: %w", clusterID, enabled, err)
	}

	return nil
}

func provisioningCluster(client *rancher.Client, clusterID string) (*apisV1.Cluster, *steveV1.SteveAPIObject, error) {
	cluster, err := client.Management.Cluster.ByID(clusterID)
	if err != nil {
		return nil, nil, fmt.Errorf("retrieving cluster %s: %w", clusterID, err)
	}

	provisioning, clusterObj, err := extclusters.GetProvisioningClusterByName(client, cluster.Name, namespaces.FleetDefault)
	if err != nil {
		return nil, nil, fmt.Errorf("retrieving the provisioning cluster of %s: %w", clusterID, err)
	}

	return provisioning, clusterObj, nil
}

// KubeAPIAuthLogs returns what every kube-api-auth pod on the cluster has logged
func KubeAPIAuthLogs(client *rancher.Client, clusterID string) (string, error) {
	podList, err := pods.GetPodsByLabelSelector(client, clusterID, KubeAPIAuthNamespace, kubeAPIAuthAppLabel)
	if err != nil {
		return "", fmt.Errorf("listing the %s pods on cluster %s: %w", KubeAPIAuthDaemonSet, clusterID, err)
	}

	if len(podList) == 0 {
		return "", fmt.Errorf("cluster %s runs no %s pod to read logs from", clusterID, KubeAPIAuthDaemonSet)
	}

	combined := strings.Builder{}

	for _, pod := range podList {
		logs, err := kubeconfig.GetPodLogs(client, clusterID, pod.Name, KubeAPIAuthNamespace, kubeAPIAuthLogBuffer)
		if err != nil {
			return "", fmt.Errorf("reading the logs of %s/%s on cluster %s: %w", KubeAPIAuthNamespace, pod.Name, clusterID, err)
		}

		combined.WriteString(logs)
		combined.WriteString("\n")
	}

	return combined.String(), nil
}

func aceKubeconfig(client *rancher.Client, clusterID string) (string, error) {
	cluster, err := client.Management.Cluster.ByID(clusterID)
	if err != nil {
		return "", fmt.Errorf("retrieving cluster %s to generate its kubeconfig: %w", clusterID, err)
	}

	generated, err := client.Management.Cluster.ActionGenerateKubeconfig(cluster)
	if err != nil {
		return "", fmt.Errorf("generating the kubeconfig for cluster %s: %w", clusterID, err)
	}

	return generated.Config, nil
}

// ACERestConfigs returns a rest config per authorized cluster endpoint context, which reach the cluster directly rather than through the Rancher proxy
func ACERestConfigs(generated string) (map[string]*rest.Config, error) {
	raw, err := clientcmd.Load([]byte(generated))
	if err != nil {
		return nil, fmt.Errorf("parsing the generated kubeconfig: %w", err)
	}

	configs := map[string]*rest.Config{}

	for name, kubeContext := range raw.Contexts {
		cluster, found := raw.Clusters[kubeContext.Cluster]
		if !found || strings.Contains(cluster.Server, rancherProxyPath) {
			continue
		}

		restConfig, err := clientcmd.NewNonInteractiveClientConfig(*raw, name, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("building a client for the %s context: %w", name, err)
		}

		configs[name] = restConfig
	}

	if len(configs) == 0 {
		return nil, fmt.Errorf("the generated kubeconfig names no authorized cluster endpoint context, only the Rancher proxy")
	}

	return configs, nil
}

// SyncedACEKubeconfig generates the cluster's kubeconfig as the given user and returns it once the token it carries has synced downstream
func SyncedACEKubeconfig(client, userClient *rancher.Client, clusterID string) (string, error) {
	generated, err := aceKubeconfig(userClient, clusterID)
	if err != nil {
		return "", err
	}

	token, err := tokenFromKubeconfig(generated)
	if err != nil {
		return "", err
	}

	accessKey := strings.SplitN(token, ":", 2)[0]

	if err := WaitForClusterAuthToken(client, clusterID, accessKey); err != nil {
		return "", err
	}

	return generated, nil
}

// ACEClientsets returns a clientset per authorized cluster endpoint context of the kubeconfig the given user is issued
func ACEClientsets(client, userClient *rancher.Client, clusterID string) (map[string]*kubernetes.Clientset, error) {
	generated, err := SyncedACEKubeconfig(client, userClient, clusterID)
	if err != nil {
		return nil, err
	}

	restConfigs, err := ACERestConfigs(generated)
	if err != nil {
		return nil, err
	}

	clientsets := map[string]*kubernetes.Clientset{}

	for name, restConfig := range restConfigs {
		clientset, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			return nil, fmt.Errorf("building a clientset for the %s context: %w", name, err)
		}

		clientsets[name] = clientset
	}

	return clientsets, nil
}

// CanI reports whether the identity behind the given clientset is allowed the verb on the resource
func CanI(clientset kubernetes.Interface, verb, resource string) (bool, error) {
	review, err := clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(context.Background(), &authzv1.SelfSubjectAccessReview{
		Spec: authzv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authzv1.ResourceAttributes{Verb: verb, Resource: resource},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return false, fmt.Errorf("asking the cluster whether %s %s is allowed: %w", verb, resource, err)
	}

	return review.Status.Allowed, nil
}
