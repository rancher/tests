package remotedialerproxy

import (
	"bytes"
	"context"
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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
)

const (
	apiServiceName = "v1.ext.cattle.io"
	rdpNamespace   = "cattle-system"
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

		restConfig, err = clientcmd.NewDefaultClientConfig(
			rawConfig,
			&clientcmd.ConfigOverrides{},
		).ClientConfig()
		require.NoError(t, err)

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
		pods, err := downstreamClient.CoreV1().Pods("cattle-system").List(
			context.TODO(),
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
			Namespace("cattle-system").
			SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
				Command: []string{"echo", "rdp-ok"},
				Stdout:  true,
				Stderr:  true,
			}, scheme.ParameterCodec)

		executor, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())
		require.NoError(t, err)

		var stdout, stderr bytes.Buffer
		err = executor.Stream(remotecommand.StreamOptions{
			Stdout: &stdout,
			Stderr: &stderr,
		})
		require.NoError(t, err)

		require.Contains(t, stdout.String(), "rdp-ok")
	})

	// Validate watch through remotedialer proxy
	t.Run("watch_validation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		w, err := downstreamClient.CoreV1().Pods("cattle-system").Watch(
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
		pods, err := downstreamClient.CoreV1().Pods("cattle-system").List(
			context.TODO(),
			metav1.ListOptions{Limit: 1},
		)
		require.NoError(t, err)
		require.NotEmpty(t, pods.Items)

		pod := pods.Items[0]

		reqURL := downstreamClient.CoreV1().RESTClient().
			Post().
			Resource("pods").
			Namespace("cattle-system").
			Name(pod.Name).
			SubResource("portforward").
			URL()

		transport, upgrader, err := spdy.RoundTripperFor(restConfig)
		require.NoError(t, err)

		dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", reqURL)

		stopChan := make(chan struct{}, 1)
		readyChan := make(chan struct{})

		fw, err := portforward.New(
			dialer,
			[]string{"8080:80"},
			stopChan,
			readyChan,
			os.Stdout,
			os.Stderr,
		)
		require.NoError(t, err)

		go func() {
			_ = fw.ForwardPorts()
		}()

		select {
		case <-readyChan:
			logrus.Info("RDP port-forward ready")
		case <-time.After(30 * time.Second):
			require.Fail(t, "port-forward never became ready")
		}

		close(stopChan)
	})
}
