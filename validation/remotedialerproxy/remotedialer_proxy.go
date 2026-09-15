package remotedialerproxy

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/kubeconfig"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
)

const (
	apiServiceName  = "v1.ext.cattle.io"
	rdpNamespace    = "cattle-system"
	proxyPathMarker = "/k8s/clusters/"
)

func remotedialerProxyValidations(t *testing.T, client *rancher.Client, cluster *steveV1.SteveAPIObject) {
	// Verify apiservice is available
	t.Run("apiservice_available", func(t *testing.T) {
		kubeConfigPtr, err := kubeconfig.GetKubeconfig(client, "local")
		require.NoError(t, err)
		require.NotNil(t, kubeConfigPtr)

		kubeConfig := *kubeConfigPtr

		rawConfig, err := kubeConfig.RawConfig()
		require.NoError(t, err)

		tmpFile, err := os.CreateTemp("", "kubeconfig-*")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		kubeBytes, err := clientcmd.Write(rawConfig)
		require.NoError(t, err)

		_, err = tmpFile.Write(kubeBytes)
		require.NoError(t, err)
		require.NoError(t, tmpFile.Close())

		cmd := exec.Command(
			"kubectl",
			"--kubeconfig", tmpFile.Name(),
			"get", "apiservice", apiServiceName,
			"-o", `jsonpath={.status.conditions[?(@.type=="Available")].status}`,
		)

		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out

		err = cmd.Run()
		require.NoError(t, err, out.String())
		require.Equal(t, "True", strings.TrimSpace(out.String()))

		if err == nil {
			logrus.Infof("APIService %s is available", apiServiceName)
		}
	})

	// Verify remotedialer proxy pods are running
	t.Run("pods_running", func(t *testing.T) {
		list, err := client.Steve.SteveType("pod").List(nil)
		require.NoError(t, err)

		found := false

		for _, p := range list.Data {
			meta, ok := p.JSONResp["metadata"].(map[string]any)
			if !ok {
				continue
			}

			name, _ := meta["name"].(string)
			ns, _ := meta["namespace"].(string)

			if ns != rdpNamespace {
				continue
			}

			if !strings.Contains(name, "rancher") &&
				!strings.Contains(name, "proxy") &&
				!strings.Contains(name, "agent") {
				continue
			}

			status, ok := p.JSONResp["status"].(map[string]any)
			if !ok {
				continue
			}

			phase, _ := status["phase"].(string)
			logrus.Infof("pod=%s status=%s", name, phase)
			require.Equal(t, "Running", phase)
			found = true
		}

		require.True(t, found)
	})

	var downstreamClient *kubernetes.Clientset
	var restConfig *rest.Config

	// Validate downstream kube access through remotedialer proxy
	t.Run("downstream_kube_access", func(t *testing.T) {
		status := &provv1.ClusterStatus{}
		err := steveV1.ConvertToK8sType(cluster.Status, status)
		require.NoError(t, err)

		clusterObject, err := client.Management.Cluster.ByID(status.ClusterName)
		require.NoError(t, err)

		client, err = client.ReLogin()
		require.NoError(t, err)

		kubeConfigPtr, err := kubeconfig.GetKubeconfig(client, clusterObject.ID)
		require.NoError(t, err)

		kubeConfig := *kubeConfigPtr

		rawConfig, err := kubeConfig.RawConfig()
		require.NoError(t, err)

		overrides := &clientcmd.ConfigOverrides{}

		if ctxName := proxiedContext(rawConfig); ctxName != "" {
			logrus.Infof("Using proxied kubeconfig context: %s", ctxName)
			overrides.CurrentContext = ctxName
		} else {
			logrus.Warnf("No context containing %q found; falling back to current-context %q",
				proxyPathMarker, rawConfig.CurrentContext)
		}

		restConfig, err = clientcmd.NewDefaultClientConfig(rawConfig, overrides).ClientConfig()
		require.NoError(t, err)

		logrus.Infof("Downstream REST host: %s", restConfig.Host)
		require.Contains(t, restConfig.Host, proxyPathMarker,
			"REST config is not routed through the remotedialer proxy")

		downstreamClient, err = kubernetes.NewForConfig(restConfig)
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			nodes, err := downstreamClient.CoreV1().Nodes().List(
				context.TODO(),
				metav1.ListOptions{},
			)
			return err == nil && len(nodes.Items) > 0
		}, 5*time.Minute, 10*time.Second)
	})

	// Validate exec through remotedialer proxy
	t.Run("exec_validation", func(t *testing.T) {
		require.NotNil(t, downstreamClient, "downstream_kube_access must pass first")

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		pods, err := downstreamClient.CoreV1().Pods(rdpNamespace).List(
			ctx,
			metav1.ListOptions{Limit: 10},
		)
		require.NoError(t, err)
		require.NotEmpty(t, pods.Items)

		var pod corev1.Pod
		for _, p := range pods.Items {
			if strings.Contains(p.Name, "cattle-cluster-agent") {
				pod = p
				break
			}
		}
		require.NotEmpty(t, pod.Name)

		req := downstreamClient.CoreV1().RESTClient().
			Post().
			Resource("pods").
			Name(pod.Name).
			Namespace(rdpNamespace).
			SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
				Command: []string{"echo", "rdp-ok"},
				Stdout:  true,
				Stderr:  true,
			}, scheme.ParameterCodec)

		wsExecutor, err := remotecommand.NewWebSocketExecutor(restConfig, "GET", req.URL().String())
		require.NoError(t, err)

		spdyExecutor, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())
		require.NoError(t, err)

		executor, err := remotecommand.NewFallbackExecutor(
			wsExecutor,
			spdyExecutor,
			httpstream.IsUpgradeFailure,
		)
		require.NoError(t, err)

		var stdout, stderr bytes.Buffer
		err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdout: &stdout,
			Stderr: &stderr,
		})
		require.NoError(t, err, "stderr: %s", stderr.String())

		require.Contains(t, stdout.String(), "rdp-ok")
	})

	// Validate watch through remotedialer proxy
	t.Run("watch_validation", func(t *testing.T) {
		require.NotNil(t, downstreamClient, "downstream_kube_access must pass first")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		w, err := downstreamClient.CoreV1().Pods(rdpNamespace).Watch(
			ctx,
			metav1.ListOptions{},
		)
		require.NoError(t, err)
		defer w.Stop()

		select {
		case <-w.ResultChan():
			logrus.Info("RDP watch event received")
		case <-ctx.Done():
			require.Fail(t, "no watch events received")
		}
	})

	// Validate port-forward through remotedialer proxy
	t.Run("portforward_validation", func(t *testing.T) {
		require.NotNil(t, downstreamClient, "downstream_kube_access must pass first")

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		pods, err := downstreamClient.CoreV1().Pods(rdpNamespace).List(
			ctx,
			metav1.ListOptions{},
		)
		require.NoError(t, err)
		require.NotEmpty(t, pods.Items)

		pod, targetPort := podWithContainerPort(pods.Items)
		require.NotEmpty(t, pod.Name, "no running pod with a declared container port found in %s", rdpNamespace)
		logrus.Infof("Port-forwarding to pod=%s port=%d", pod.Name, targetPort)

		reqURL := downstreamClient.CoreV1().RESTClient().
			Post().
			Resource("pods").
			Namespace(rdpNamespace).
			Name(pod.Name).
			SubResource("portforward").
			URL()

		transport, upgrader, err := spdy.RoundTripperFor(restConfig)
		require.NoError(t, err)

		spdyDialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", reqURL)

		wsDialer, err := portforward.NewSPDYOverWebsocketDialer(reqURL, restConfig)
		require.NoError(t, err)

		dialer := portforward.NewFallbackDialer(wsDialer, spdyDialer, httpstream.IsUpgradeFailure)

		stopChan := make(chan struct{}, 1)
		readyChan := make(chan struct{})
		errChan := make(chan error, 1)

		fw, err := portforward.New(
			dialer,
			[]string{fmt.Sprintf("0:%d", targetPort)},
			stopChan,
			readyChan,
			os.Stdout,
			os.Stderr,
		)
		require.NoError(t, err)

		go func() {
			errChan <- fw.ForwardPorts()
		}()

		select {
		case <-readyChan:
			ports, err := fw.GetPorts()
			require.NoError(t, err)
			logrus.Infof("RDP port-forward ready on localhost:%d -> %d", ports[0].Local, ports[0].Remote)
		case err := <-errChan:
			require.NoError(t, err, "port-forward failed before becoming ready")
		case <-time.After(60 * time.Second):
			require.Fail(t, "port-forward never became ready")
		}

		close(stopChan)
	})
}

func proxiedContext(rawConfig clientcmdapi.Config) string {
	if cur, ok := rawConfig.Contexts[rawConfig.CurrentContext]; ok {
		if cluster, ok := rawConfig.Clusters[cur.Cluster]; ok &&
			strings.Contains(cluster.Server, proxyPathMarker) {
			return rawConfig.CurrentContext
		}
	}

	for name, kubeContext := range rawConfig.Contexts {
		cluster, ok := rawConfig.Clusters[kubeContext.Cluster]
		if ok && strings.Contains(cluster.Server, proxyPathMarker) {
			return name
		}
	}

	return ""
}

func podWithContainerPort(items []corev1.Pod) (corev1.Pod, int32) {
	for _, p := range items {
		if p.Status.Phase != corev1.PodRunning {
			continue
		}

		for _, c := range p.Spec.Containers {
			for _, port := range c.Ports {
				if port.ContainerPort > 0 {
					return p, port.ContainerPort
				}
			}
		}
	}

	return corev1.Pod{}, 0
}
