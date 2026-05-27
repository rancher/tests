package pods

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appv1 "k8s.io/api/apps/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	Webhook          = "rancher-webhook"
	SUC              = "system-upgrade-controller"
	Fleet            = "fleet-agent"
	ClusterAgent     = "cattle-cluster-agent"
	helmPrefix       = "helm"
	errImagePull     = "ErrImagePull"
	imagePullBackOff = "ImagePullBackOff"
	crashLoopBackOff = "CrashLoopBackOff"
)

// VerifyReadyDaemonsetPods tries to poll the Steve API to verify the expected number of daemonset pods are in the Ready
// state
func VerifyReadyDaemonsetPods(t *testing.T, client *rancher.Client, cluster *v1.SteveAPIObject) {
	status := &provv1.ClusterStatus{}
	err := v1.ConvertToK8sType(cluster.Status, status)
	require.NoError(t, err)

	daemonsetequals := false

	err = kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.TenMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		daemonsets, err := client.Steve.SteveType("apps.daemonset").ByID(status.ClusterName)
		require.NoError(t, err)

		daemonsetsStatusType := &appv1.DaemonSetStatus{}
		err = v1.ConvertToK8sType(daemonsets.Status, daemonsetsStatusType)
		require.NoError(t, err)

		if daemonsetsStatusType.DesiredNumberScheduled == daemonsetsStatusType.NumberAvailable {
			return true, nil
		}
		return false, err
	})
	require.NoError(t, err)

	daemonsets, err := client.Steve.SteveType("apps.daemonset").ByID(status.ClusterName)
	require.NoError(t, err)

	daemonsetsStatusType := &appv1.DaemonSetStatus{}
	err = v1.ConvertToK8sType(daemonsets.Status, daemonsetsStatusType)
	require.NoError(t, err)

	if daemonsetsStatusType.DesiredNumberScheduled == daemonsetsStatusType.NumberAvailable {
		daemonsetequals = true
	}

	assert.Truef(t, daemonsetequals, "Ready Daemonset Pods didn't match expected")
}

// VerifyClusterPods validates that all pods (excluding Helm pods) are in a good state.
func VerifyClusterPods(client *rancher.Client, cluster *v1.SteveAPIObject) error {
	status := &provv1.ClusterStatus{}
	if err := v1.ConvertToK8sType(cluster.Status, status); err != nil {
		return err
	}

	badPodsMap := make(map[string]*v1.SteveAPIObject)

	err := kwait.PollUntilContextTimeout(
		context.TODO(),
		10*time.Second,
		defaults.TenMinuteTimeout,
		true,
		func(ctx context.Context) (bool, error) {
			downstreamClient, err := client.Steve.ProxyDownstream(status.ClusterName)
			if err != nil {
				return false, nil
			}

			steveClient := downstreamClient.SteveType(stevetypes.Pod)
			clusterPods, err := steveClient.List(nil)
			if err != nil {
				return false, nil
			}

			badPodsMap = make(map[string]*v1.SteveAPIObject)

			for _, pod := range clusterPods.Data {
				isHelm := strings.Contains(pod.Name, helmPrefix)
				if isHelm {
					continue
				}

				statusMap, ok := pod.Status.(map[string]interface{})
				if !ok {
					continue
				}
				statuses, ok := statusMap["containerStatuses"].([]interface{})
				if !ok {
					continue
				}

				for _, s := range statuses {
					cs, ok := s.(map[string]interface{})
					if !ok {
						continue
					}

					containerName, _ := cs["name"].(string)
					podState := "NotReady"

					if stateMap, ok := cs["state"].(map[string]interface{}); ok {
						if waiting, ok := stateMap["waiting"].(map[string]interface{}); ok {
							if reason, ok := waiting["reason"].(string); ok && reason != "" {
								podState = reason
							}
						} else if terminated, ok := stateMap["terminated"].(map[string]interface{}); ok {
							if reason, ok := terminated["reason"].(string); ok && reason != "" {
								podState = reason
							}
						}
					}

					isReady, _ := pods.IsPodReady(&pod)
					if !isReady && !isHelm {
						key := pod.Namespace + "/" + pod.Name + "/" + containerName

						if pod.Annotations == nil {
							pod.Annotations = make(map[string]string)
						}
						pod.Annotations["podState"] = podState

						badPodsMap[key] = &pod
						break
					}
				}
			}

			return len(badPodsMap) == 0, nil
		},
	)

	for _, pod := range badPodsMap {
		images := getPodImages(pod)
		podState := "NotReady"
		if state, ok := pod.Annotations["podState"]; ok {
			podState = state
		}

		fields := logrus.Fields{
			"pod":    pod.Name,
			"state":  podState,
			"images": images,
		}
		logrus.WithFields(fields).Error("Pod NotReady")
	}

	if len(badPodsMap) > 0 {
		return errors.New("Detected pod(s) in a bad state. Check logs for details.")
	}

	return err
}

// getPodImages grabs all container images from a pod.
func getPodImages(pod *v1.SteveAPIObject) []string {
	var images []string

	statusMap, ok := pod.Status.(map[string]interface{})
	if !ok {
		return images
	}

	statuses, ok := statusMap["containerStatuses"].([]interface{})
	if !ok {
		return images
	}

	for _, s := range statuses {
		cs, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		if image, ok := cs["image"].(string); ok && image != "" {
			images = append(images, image)
		}
	}

	return images
}
