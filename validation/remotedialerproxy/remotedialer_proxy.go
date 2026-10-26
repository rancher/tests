package remotedialerproxy

import (
	"bytes"
	"context"
	"crypto/tls"
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
	"github.com/rancher/tests/actions/rbac"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
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
	// Log remotedialer proxy version
	t.Run("remotedialer_proxy_version", func(t *testing.T) {
		rdpVersionSetting, err := client.Management.Setting.ByID("remotedialer-proxy-version")
		require.NoError(t, err)
		require.NotEmpty(t, rdpVersionSetting.Value)
		logrus.Infof("Remotedialer Proxy Version: %s", rdpVersionSetting.Value)
	})

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
			require.Equal(t, "Running", phase, "pod %s is not Running", name)

			conditions, ok := status["conditions"].([]any)
			require.True(t, ok, "pod %s has no readable status conditions", name)

			ready := false
			for _, condition := range conditions {
				conditionMap, ok := condition.(map[string]any)
				if !ok {
					continue
				}

				conditionType, _ := conditionMap["type"].(string)
				conditionStatus, _ := conditionMap["status"].(string)

				if conditionType == "Ready" && conditionStatus == "True" {
					ready = true
					break
				}
			}

			require.True(t, ready, "pod %s is not Ready", name)
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

		// Create a temporary cluster-member user.
		standardUser, standardUserClient, err := rbac.AddUserWithRoleToCluster(
			client,
			rbac.StandardUser.String(),
			rbac.ClusterMember.String(),
			clusterObject,
			nil,
		)
		require.NoError(t, err)
		require.NotNil(t, standardUser)

		t.Cleanup(func() {
			bindings, listErr := client.WranglerContext.Mgmt.
				ClusterRoleTemplateBinding().
				List(clusterObject.ID, metav1.ListOptions{})
			if listErr != nil {
				logrus.Errorf("Failed to list cluster role template bindings for cleanup: %v", listErr)
			} else {
				for _, binding := range bindings.Items {
					if binding.UserName != standardUser.Username {
						continue
					}

					if deleteErr := client.WranglerContext.Mgmt.
						ClusterRoleTemplateBinding().
						Delete(binding.Namespace, binding.Name, &metav1.DeleteOptions{}); deleteErr != nil {
						logrus.Errorf(
							"Failed to delete cluster role template binding %s/%s: %v",
							binding.Namespace,
							binding.Name,
							deleteErr,
						)
					}
				}
			}
		})

		// Verify the cluster member can generate a downstream kubeconfig.
		memberKubeConfig, err := kubeconfig.GetKubeconfig(
			standardUserClient,
			clusterObject.ID,
		)
		require.NoError(t, err)
		require.NotNil(t, memberKubeConfig)

		logrus.Infof(
			"Cluster member %s successfully generated downstream kubeconfig",
			standardUser.Username,
		)

		// Configure a Kubernetes client using the cluster-member credentials.
		memberRawConfig, err := (*memberKubeConfig).RawConfig()
		require.NoError(t, err)

		memberContext := proxiedContext(memberRawConfig)
		if memberContext == "" {
			memberContext = memberRawConfig.CurrentContext
			logrus.Warnf(
				"No context containing %q found in member kubeconfig; using current context %q",
				proxyPathMarker,
				memberContext,
			)
		}

		memberOverrides := &clientcmd.ConfigOverrides{
			CurrentContext: memberContext,
		}

		memberRestConfig, err := clientcmd.NewDefaultClientConfig(
			memberRawConfig,
			memberOverrides,
		).ClientConfig()
		require.NoError(t, err)

		logrus.Infof("Cluster-member downstream REST host: %s", memberRestConfig.Host)
		require.Contains(
			t,
			memberRestConfig.Host,
			proxyPathMarker,
			"Cluster-member kubeconfig is not routed through the remotedialer proxy",
		)

		memberClient, err := kubernetes.NewForConfig(memberRestConfig)
		require.NoError(t, err)

		// Node listing is a permitted operation for this cluster member.
		_, err = memberClient.CoreV1().Nodes().List(
			context.Background(),
			metav1.ListOptions{Limit: 1},
		)
		require.NoError(t, err, "Cluster member should be able to list downstream nodes")
		logrus.Info("Cluster member can list downstream nodes")

		// Verify Secret listing is denied in the default namespace.
		_, err = memberClient.CoreV1().Secrets("default").List(
			context.Background(),
			metav1.ListOptions{Limit: 1},
		)
		require.Error(t, err, "Expected cluster member Secret listing to be denied in default namespace")
		require.True(
			t,
			apierrors.IsForbidden(err),
			"Expected Forbidden when cluster member lists Secrets in default namespace; got: %v",
			err,
		)

		logrus.Info("Cluster-member Secret listing correctly denied in namespace default")

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
			logrus.Warnf(
				"No context containing %q found; falling back to current-context %q",
				proxyPathMarker,
				rawConfig.CurrentContext,
			)
		}

		restConfig, err = clientcmd.NewDefaultClientConfig(rawConfig, overrides).ClientConfig()
		require.NoError(t, err)

		logrus.Infof("Downstream REST host: %s", restConfig.Host)
		require.Contains(
			t,
			restConfig.Host,
			proxyPathMarker,
			"REST config is not routed through the remotedialer proxy",
		)

		downstreamClient, err = kubernetes.NewForConfig(restConfig)
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			nodes, err := downstreamClient.CoreV1().Nodes().List(
				context.Background(),
				metav1.ListOptions{},
			)
			return err == nil && len(nodes.Items) > 0
		}, 5*time.Minute, 10*time.Second)
	})

	// Validate exec through remotedialer proxy
	t.Run("exec_validation", func(t *testing.T) {
		require.NotNil(t, downstreamClient, "downstream_kube_access must pass first")
		require.NotNil(t, restConfig, "downstream REST config must be initialized")

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		pods, err := downstreamClient.CoreV1().Pods(rdpNamespace).List(
			ctx,
			metav1.ListOptions{Limit: 10},
		)
		require.NoError(t, err, "failed to list pods in namespace %q for exec validation", rdpNamespace)
		require.NotEmpty(t, pods.Items, "no pods found in namespace %q for exec validation", rdpNamespace)

		var pod corev1.Pod
		for _, p := range pods.Items {
			if strings.Contains(p.Name, "cattle-cluster-agent") {
				pod = p
				break
			}
		}
		require.NotEmpty(
			t,
			pod.Name,
			"no cattle-cluster-agent pod found in namespace %q; discovered pods: %v",
			rdpNamespace,
			func() []string {
				names := make([]string, 0, len(pods.Items))
				for _, p := range pods.Items {
					names = append(names, p.Name)
				}
				return names
			}(),
		)

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
		require.NoError(
			t,
			err,
			"failed to create WebSocket exec executor for pod %s/%s",
			rdpNamespace,
			pod.Name,
		)

		spdyExecutor, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())
		require.NoError(
			t,
			err,
			"failed to create SPDY exec executor for pod %s/%s",
			rdpNamespace,
			pod.Name,
		)

		executor, err := remotecommand.NewFallbackExecutor(
			wsExecutor,
			spdyExecutor,
			httpstream.IsUpgradeFailure,
		)
		require.NoError(
			t,
			err,
			"failed to create fallback exec executor for pod %s/%s",
			rdpNamespace,
			pod.Name,
		)

		var stdout, stderr bytes.Buffer
		err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdout: &stdout,
			Stderr: &stderr,
		})
		require.NoError(
			t,
			err,
			"remote exec failed for pod %s/%s; stdout=%q stderr=%q",
			rdpNamespace,
			pod.Name,
			stdout.String(),
			stderr.String(),
		)

		actualStdout := strings.TrimSpace(stdout.String())
		require.Equal(
			t,
			"rdp-ok",
			actualStdout,
			"remote exec returned unexpected stdout for pod %s/%s; stderr=%q",
			rdpNamespace,
			pod.Name,
			stderr.String(),
		)
		require.Empty(
			t,
			stderr.String(),
			"remote exec unexpectedly wrote to stderr for pod %s/%s; stdout=%q",
			rdpNamespace,
			pod.Name,
			stdout.String(),
		)

		logrus.Infof(
			"RDP exec validation passed for pod=%s/%s: stdout=%q",
			rdpNamespace,
			pod.Name,
			actualStdout,
		)
	})

	// Validate watch through remotedialer proxy
	t.Run("watch_validation", func(t *testing.T) {
		require.NotNil(t, downstreamClient, "downstream_kube_access must pass first")

		const watchTimeout = 30 * time.Second

		ctx, cancel := context.WithTimeout(context.Background(), watchTimeout)
		defer cancel()

		configMapName := fmt.Sprintf(
			"rdp-watch-validation-%d",
			time.Now().UnixNano(),
		)

		w, err := downstreamClient.CoreV1().ConfigMaps(rdpNamespace).Watch(
			ctx,
			metav1.ListOptions{},
		)
		require.NoError(
			t,
			err,
			"failed to establish ConfigMap watch through remotedialer proxy in namespace %q",
			rdpNamespace,
		)
		defer w.Stop()

		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: rdpNamespace,
			},
			Data: map[string]string{
				"validation": "rdp-watch-ok",
			},
		}

		_, err = downstreamClient.CoreV1().ConfigMaps(rdpNamespace).Create(
			ctx,
			configMap,
			metav1.CreateOptions{},
		)
		require.NoError(
			t,
			err,
			"failed to create watch validation ConfigMap %s/%s",
			rdpNamespace,
			configMapName,
		)

		defer func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(
				context.Background(),
				10*time.Second,
			)
			defer cleanupCancel()

			err := downstreamClient.CoreV1().ConfigMaps(rdpNamespace).Delete(
				cleanupCtx,
				configMapName,
				metav1.DeleteOptions{},
			)
			if err != nil && !apierrors.IsNotFound(err) {
				t.Errorf(
					"failed to clean up watch validation ConfigMap %s/%s: %v",
					rdpNamespace,
					configMapName,
					err,
				)
				return
			}

			err = wait.PollUntilContextTimeout(
				cleanupCtx,
				250*time.Millisecond,
				5*time.Second,
				true,
				func(ctx context.Context) (bool, error) {
					_, err := downstreamClient.CoreV1().ConfigMaps(rdpNamespace).Get(
						ctx,
						configMapName,
						metav1.GetOptions{},
					)
					if apierrors.IsNotFound(err) {
						return true, nil
					}
					if err != nil {
						return false, err
					}
					return false, nil
				},
			)
			if err != nil {
				t.Errorf(
					"ConfigMap %s/%s was not confirmed removed after cleanup: %v",
					rdpNamespace,
					configMapName,
					err,
				)
			}
		}()

		for {
			select {
			case event, ok := <-w.ResultChan():
				if !ok {
					t.Fatalf(
						"ConfigMap watch channel closed before receiving Added event for %s/%s",
						rdpNamespace,
						configMapName,
					)
				}

				if event.Type == watch.Error {
					t.Fatalf(
						"ConfigMap watch returned an error event while waiting for %s/%s: %#v",
						rdpNamespace,
						configMapName,
						event.Object,
					)
				}

				watchedConfigMap, ok := event.Object.(*corev1.ConfigMap)
				if !ok {
					t.Fatalf(
						"ConfigMap watch returned unexpected object type %T (event type %s) while waiting for %s/%s",
						event.Object,
						event.Type,
						rdpNamespace,
						configMapName,
					)
				}

				// Ignore events for ConfigMaps unrelated to this validation.
				if watchedConfigMap.Name != configMapName ||
					watchedConfigMap.Namespace != rdpNamespace {
					continue
				}

				require.Equal(
					t,
					watch.Added,
					event.Type,
					"unexpected event type for ConfigMap %s/%s",
					rdpNamespace,
					configMapName,
				)
				require.Equal(
					t,
					"rdp-watch-ok",
					watchedConfigMap.Data["validation"],
					"unexpected validation data in ConfigMap %s/%s",
					rdpNamespace,
					configMapName,
				)

				logrus.Infof(
					"RDP watch received Added event for ConfigMap %s/%s",
					rdpNamespace,
					configMapName,
				)
				return

			case <-ctx.Done():
				t.Fatalf(
					"timed out after %s waiting for Added event for ConfigMap %s/%s through remotedialer proxy: %v",
					watchTimeout,
					rdpNamespace,
					configMapName,
					ctx.Err(),
				)
			}
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

		pod, containerName, targetPort := podWithContainerPort(pods.Items)
		require.NotEmpty(t, pod.Name, "no running pod with a declared container port found in %s", rdpNamespace)
		require.Positive(t, targetPort, "target container port must be positive")

		logrus.Infof(
			"Port-forwarding to pod=%s container=%s port=%d",
			pod.Name,
			containerName,
			targetPort,
		)

		reqURL := downstreamClient.CoreV1().RESTClient().
			Post().
			Resource("pods").
			Namespace(rdpNamespace).
			Name(pod.Name).
			SubResource("portforward").
			URL()

		transport, upgrader, err := spdy.RoundTripperFor(restConfig)
		require.NoError(t, err)

		spdyDialer := spdy.NewDialer(
			upgrader,
			&http.Client{Transport: transport},
			"POST",
			reqURL,
		)

		wsDialer, err := portforward.NewSPDYOverWebsocketDialer(reqURL, restConfig)
		require.NoError(t, err)

		dialer := portforward.NewFallbackDialer(
			wsDialer,
			spdyDialer,
			httpstream.IsUpgradeFailure,
		)

		stopChan := make(chan struct{})
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

		t.Cleanup(func() {
			close(stopChan)
		})

		var localPort int

		select {
		case <-readyChan:
			ports, err := fw.GetPorts()
			require.NoError(t, err)
			require.NotEmpty(t, ports, "port-forward returned no ports")

			localPort = int(ports[0].Local)
			logrus.Infof(
				"RDP port-forward ready on localhost:%d -> %d",
				ports[0].Local,
				ports[0].Remote,
			)

		case err := <-errChan:
			require.NoError(t, err, "port-forward failed before becoming ready")

		case <-ctx.Done():
			require.FailNow(t, "timed out waiting for port-forward readiness")
		}

		httpsClient := &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		}
		defer httpsClient.CloseIdleConnections()

		webhookURL := fmt.Sprintf("https://127.0.0.1:%d/healthz", localPort)
		resp, err := httpsClient.Get(webhookURL)
		require.NoError(
			t,
			err,
			"failed to make HTTPS request through the remotedialer port-forward",
		)
		defer resp.Body.Close()

		require.Equal(
			t,
			http.StatusOK,
			resp.StatusCode,
			"webhook health endpoint returned unexpected status: %s",
			resp.Status,
		)

		logrus.Infof(
			"Rancher webhook health check passed through RDP port-forward: status=%s",
			resp.Status,
		)

		logrus.Infof(
			"Successfully connected through RDP port-forward on localhost:%d",
			localPort,
		)
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

func podWithContainerPort(items []corev1.Pod) (corev1.Pod, string, int32) {
	for _, p := range items {
		if p.Status.Phase != corev1.PodRunning {
			continue
		}

		ready := false
		for _, condition := range p.Status.Conditions {
			if condition.Type == corev1.PodReady &&
				condition.Status == corev1.ConditionTrue {
				ready = true
				break
			}
		}

		if !ready {
			continue
		}

		for _, c := range p.Spec.Containers {
			for _, port := range c.Ports {
				if port.ContainerPort > 0 {
					return p, c.Name, port.ContainerPort
				}
			}
		}
	}

	return corev1.Pod{}, "", 0
}
