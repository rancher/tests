package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	apisV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

	ExtTokenPrefix = "ext/"

	kubeAPIAuthAppLabel  = "app=" + KubeAPIAuthDaemonSet
	kubeAPIAuthLogBuffer = "64KB"
	statusMarker         = "__STATUS__"
	rancherProxyPath     = "/k8s/clusters/"

	kubeAPIServerLabel    = "component=kube-apiserver"
	controlPlaneNodeLabel = "node-role.kubernetes.io/control-plane"
	webhookVersionArg     = "authentication-token-webhook-version"
	unknownConversionLog  = "unknown conversion"
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

// parseTokenReviewOutput splits the status code the curl wrote from the body kube-api-auth answered with
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

// shellQuote wraps a value so it survives being embedded in a sh command
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// tokenFromKubeconfig returns the accessKey:secret token a generated kubeconfig carries
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

	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.FiveMinuteTimeout, true,
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

// WaitForClusterAuthTokenGone polls the downstream cluster until Rancher has withdrawn the cluster auth token of the given access key
func WaitForClusterAuthTokenGone(client *rancher.Client, clusterID, accessKey string) error {
	downstream, err := client.WranglerContext.DownStreamClusterWranglerContext(clusterID)
	if err != nil {
		return fmt.Errorf("reaching cluster %s to watch cluster auth token %s go away: %w", clusterID, accessKey, err)
	}

	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true,
		func(context.Context) (bool, error) {
			_, err := downstream.Cluster.ClusterAuthToken().Get(KubeAPIAuthNamespace, accessKey, metav1.GetOptions{})

			return apierrors.IsNotFound(err), nil
		})
	if pollErr != nil {
		return fmt.Errorf("cluster auth token %s is still synced to cluster %s, so a revoked token keeps working through the authorized cluster endpoint: %w",
			accessKey, clusterID, pollErr)
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
		if err := SetACEEnabled(client, clusterID, true); err != nil {
			return restore, err
		}

		restore = func() error { return SetACEEnabled(client, clusterID, false) }
	}

	if err := daemonsets.WaitForDaemonSetReady(client, clusterID, KubeAPIAuthNamespace, KubeAPIAuthDaemonSet); err != nil {
		return restore, fmt.Errorf("the %s daemon set never became ready on cluster %s: %w", KubeAPIAuthDaemonSet, clusterID, err)
	}

	return restore, nil
}

// SetACEEnabled turns the authorized cluster endpoint of the cluster on or off and waits for the cluster to finish reconciling the change
func SetACEEnabled(client *rancher.Client, clusterID string, enabled bool) error {
	if err := setACE(client, clusterID, enabled); err != nil {
		return err
	}

	return WaitForClusterSettled(client, clusterID)
}

// WaitForClusterSettled polls until the cluster reports itself both updated and ready, which is when the certificates a change reissued have reached the downstream webhooks
func WaitForClusterSettled(client *rancher.Client, clusterID string) error {
	var last map[string]string

	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.TenSecondTimeout, defaults.TenMinuteTimeout, true,
		func(context.Context) (bool, error) {
			cluster, _, err := provisioningCluster(client, clusterID)
			if err != nil {
				return false, nil
			}

			last = map[string]string{}
			for _, condition := range cluster.Status.Conditions {
				last[string(condition.Type)] = string(condition.Status)
			}

			return last["Ready"] == "True" && last["Updated"] == "True", nil
		})
	if pollErr != nil {
		return fmt.Errorf("cluster %s never settled after the change, conditions %v: %w", clusterID, last, pollErr)
	}

	return nil
}

// ACEFQDN returns the address the authorized cluster endpoint is published under, empty when it serves the control plane nodes directly
func ACEFQDN(client *rancher.Client, clusterID string) (string, error) {
	cluster, _, err := provisioningCluster(client, clusterID)
	if err != nil {
		return "", err
	}

	return cluster.Spec.LocalClusterAuthEndpoint.FQDN, nil
}

// ACEEnabled reports whether the authorized cluster endpoint is on for the cluster
func ACEEnabled(client *rancher.Client, clusterID string) (bool, error) {
	cluster, _, err := provisioningCluster(client, clusterID)
	if err != nil {
		return false, err
	}

	return cluster.Spec.LocalClusterAuthEndpoint.Enabled, nil
}

// ControlPlaneNodeCount returns how many control plane nodes the cluster runs, which is how many authorized cluster endpoint contexts it serves
func ControlPlaneNodeCount(client *rancher.Client, clusterID string) (int, error) {
	downstream, err := client.WranglerContext.DownStreamClusterWranglerContext(clusterID)
	if err != nil {
		return 0, fmt.Errorf("reaching cluster %s to count its control plane nodes: %w", clusterID, err)
	}

	nodeList, err := downstream.Core.Node().List(metav1.ListOptions{LabelSelector: controlPlaneNodeLabel})
	if err != nil {
		return 0, fmt.Errorf("listing the control plane nodes of cluster %s: %w", clusterID, err)
	}

	return len(nodeList.Items), nil
}

// APIServerPodsExposed reports whether the cluster runs its kube-apiserver as a pod, which K3s does not since it serves the API from the host process
func APIServerPodsExposed(client *rancher.Client, clusterID string) (bool, error) {
	podList, err := pods.GetPodsByLabelSelector(client, clusterID, namespaces.KubeSystem, kubeAPIServerLabel)
	if err != nil {
		return false, fmt.Errorf("listing the kube-apiserver pods on cluster %s: %w", clusterID, err)
	}

	return len(podList) > 0, nil
}

// APIServerWebhookVersion returns the TokenReview version the cluster's kube-apiserver sends, empty when the flag names none
func APIServerWebhookVersion(client *rancher.Client, clusterID string) (string, error) {
	podList, err := pods.GetPodsByLabelSelector(client, clusterID, namespaces.KubeSystem, kubeAPIServerLabel)
	if err != nil {
		return "", fmt.Errorf("listing the kube-apiserver pods on cluster %s: %w", clusterID, err)
	}

	if len(podList) == 0 {
		return "", fmt.Errorf("cluster %s runs no kube-apiserver pod to read the %s flag from", clusterID, webhookVersionArg)
	}

	prefix := fmt.Sprintf("--%s=", webhookVersionArg)

	for _, container := range podList[0].Spec.Containers {
		for _, argument := range append(append([]string{}, container.Command...), container.Args...) {
			if strings.HasPrefix(argument, prefix) {
				return strings.TrimPrefix(argument, prefix), nil
			}
		}
	}

	return "", nil
}

// APIServerLogs returns what each kube-apiserver pod on the cluster has logged, keyed by pod name so a reader can tell which node wrote an entry
func APIServerLogs(client *rancher.Client, clusterID string) (map[string]string, error) {
	podList, err := pods.GetPodsByLabelSelector(client, clusterID, namespaces.KubeSystem, kubeAPIServerLabel)
	if err != nil {
		return nil, fmt.Errorf("listing the kube-apiserver pods on cluster %s: %w", clusterID, err)
	}

	if len(podList) == 0 {
		return nil, fmt.Errorf("cluster %s runs no kube-apiserver pod to read logs from", clusterID)
	}

	logsByPod := map[string]string{}

	for _, pod := range podList {
		logs, err := kubeconfig.GetPodLogs(client, clusterID, pod.Name, namespaces.KubeSystem, kubeAPIAuthLogBuffer)
		if err != nil {
			return nil, fmt.Errorf("reading the logs of %s/%s on cluster %s: %w", namespaces.KubeSystem, pod.Name, clusterID, err)
		}

		logsByPod[pod.Name] = logs
	}

	return logsByPod, nil
}

// setACE writes the authorized cluster endpoint setting onto the provisioning cluster
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

// provisioningCluster returns the provisioning cluster behind a management cluster id, both typed and as the Steve object an update needs
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

// KubeAPIAuthLogs returns what each kube-api-auth pod on the cluster has logged, keyed by pod name so a reader can tell which node answered
func KubeAPIAuthLogs(client *rancher.Client, clusterID string) (map[string]string, error) {
	podList, err := pods.GetPodsByLabelSelector(client, clusterID, KubeAPIAuthNamespace, kubeAPIAuthAppLabel)
	if err != nil {
		return nil, fmt.Errorf("listing the %s pods on cluster %s: %w", KubeAPIAuthDaemonSet, clusterID, err)
	}

	if len(podList) == 0 {
		return nil, fmt.Errorf("cluster %s runs no %s pod to read logs from", clusterID, KubeAPIAuthDaemonSet)
	}

	logsByPod := map[string]string{}

	for _, pod := range podList {
		logs, err := kubeconfig.GetPodLogs(client, clusterID, pod.Name, KubeAPIAuthNamespace, kubeAPIAuthLogBuffer)
		if err != nil {
			return nil, fmt.Errorf("reading the logs of %s/%s on cluster %s: %w", KubeAPIAuthNamespace, pod.Name, clusterID, err)
		}

		logsByPod[pod.Name] = logs
	}

	return logsByPod, nil
}

// aceKubeconfig returns the kubeconfig the given client is issued for the cluster
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

// ProxyRestConfig returns a rest config for the Rancher proxy context of a generated kubeconfig, which reaches the cluster through Rancher rather than directly
func ProxyRestConfig(generated string) (*rest.Config, error) {
	raw, err := clientcmd.Load([]byte(generated))
	if err != nil {
		return nil, fmt.Errorf("parsing the generated kubeconfig: %w", err)
	}

	for name, kubeContext := range raw.Contexts {
		cluster, found := raw.Clusters[kubeContext.Cluster]
		if !found || !strings.Contains(cluster.Server, rancherProxyPath) {
			continue
		}

		return clientcmd.NewNonInteractiveClientConfig(*raw, name, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	}

	return nil, fmt.Errorf("the generated kubeconfig names no Rancher proxy context")
}

// VerifyNoUnknownConversion reports whether any kube-apiserver has logged a TokenReview conversion failure since the given baseline
func VerifyNoUnknownConversion(client *rancher.Client, clusterID string, baseline map[string]string) error {
	current, err := APIServerLogs(client, clusterID)
	if err != nil {
		return err
	}

	for pod, logs := range current {
		if strings.Contains(logsSince(baseline[pod], logs), unknownConversionLog) {
			return fmt.Errorf("the kube-apiserver on %s logged %q, so it could not read the TokenReview kube-api-auth answered with",
				pod, unknownConversionLog)
		}
	}

	return nil
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

// AccessKeyFromKubeconfig returns the access key of the token a generated kubeconfig carries
func AccessKeyFromKubeconfig(generated string) (string, error) {
	token, err := tokenFromKubeconfig(generated)
	if err != nil {
		return "", err
	}

	return strings.SplitN(token, ":", 2)[0], nil
}

// ClientsetsFromKubeconfig returns a clientset per authorized cluster endpoint context of an already generated kubeconfig
func ClientsetsFromKubeconfig(generated string) (map[string]*kubernetes.Clientset, error) {
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

// ACEClientsets returns a clientset per authorized cluster endpoint context of the kubeconfig the given user is issued
func ACEClientsets(client, userClient *rancher.Client, clusterID string) (map[string]*kubernetes.Clientset, error) {
	generated, err := SyncedACEKubeconfig(client, userClient, clusterID)
	if err != nil {
		return nil, err
	}

	return ClientsetsFromKubeconfig(generated)
}

// NewClusterScopedToken mints a token scoped to the cluster that expires after the given lifetime, and returns it in accessKey:secret form
func NewClusterScopedToken(client *rancher.Client, clusterID string, lifetime time.Duration) (string, error) {
	created, err := client.Management.Token.Create(&management.Token{
		ClusterID:   clusterID,
		Description: "kube-api-auth lifetime coverage",
		TTLMillis:   lifetime.Milliseconds(),
	})
	if err != nil {
		return "", fmt.Errorf("minting a token scoped to cluster %s: %w", clusterID, err)
	}

	if created.Token == "" {
		return "", fmt.Errorf("Rancher returned token %s without its secret, which is only handed back at creation", created.ID)
	}

	return created.Token, nil
}

// ACEClientsetsWithToken returns a clientset per authorized cluster endpoint context that authenticates with the given token rather than the one the kubeconfig carries
func ACEClientsetsWithToken(generated, token string) (map[string]*kubernetes.Clientset, error) {
	restConfigs, err := ACERestConfigs(generated)
	if err != nil {
		return nil, err
	}

	clientsets := map[string]*kubernetes.Clientset{}

	for name, restConfig := range restConfigs {
		swapped := rest.CopyConfig(restConfig)
		swapped.BearerToken = token

		clientset, err := kubernetes.NewForConfig(swapped)
		if err != nil {
			return nil, fmt.Errorf("building a clientset for the %s context: %w", name, err)
		}

		clientsets[name] = clientset
	}

	return clientsets, nil
}

// SetUserEnabled turns the given Rancher user on or off
func SetUserEnabled(client *rancher.Client, userName string, enabled bool) error {
	patch := []byte(fmt.Sprintf(`{"enabled":%t}`, enabled))

	if _, err := client.WranglerContext.Mgmt.User().Patch(userName, types.MergePatchType, patch); err != nil {
		return fmt.Errorf("setting user %s to enabled=%t: %w", userName, enabled, err)
	}

	return nil
}

// WaitForAuthorized polls the cluster through the given clientset until it accepts the credential, outlasting the restart a change to the authorized cluster endpoint causes
func WaitForAuthorized(clientset kubernetes.Interface) error {
	var last error

	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.TenSecondTimeout, defaults.FiveMinuteTimeout, true,
		func(context.Context) (bool, error) {
			_, last = clientset.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})

			return last == nil, nil
		})
	if pollErr != nil {
		return fmt.Errorf("the cluster never accepted the credential, last result %v: %w", last, pollErr)
	}

	return nil
}

// WaitForUnauthorized polls the cluster through the given clientset until it refuses the credential, outlasting the kube-apiserver's webhook answer cache
func WaitForUnauthorized(clientset kubernetes.Interface) error {
	var last error

	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.TenSecondTimeout, defaults.FiveMinuteTimeout, true,
		func(context.Context) (bool, error) {
			_, last = clientset.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})

			return last != nil && apierrors.IsUnauthorized(last), nil
		})
	if pollErr != nil {
		return fmt.Errorf("the cluster still answers the credential rather than refusing it, last result %v: %w", last, pollErr)
	}

	return nil
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
