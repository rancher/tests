package pods

import (
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	corev1 "k8s.io/api/core/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// CreatePodFromConfig creates a Pod from a config using steve
func CreatePodFromConfig(client *v1.Client, clusterID string, pod *corev1.Pod) (*corev1.Pod, error) {
	podResp, err := client.SteveType(stevetypes.Pod).Create(pod)
	if err != nil {
		return nil, err
	}

	newPod := new(corev1.Pod)
	err = v1.ConvertToK8sType(podResp.JSONResp, newPod)
	if err != nil {
		return nil, err
	}

	return newPod, nil
}

// WatchAndWaitPodContainerRunning is a helper to watch and wait all pod containers running
func WatchAndWaitPodContainerRunning(client *rancher.Client, clusterID, namespaceName string) error {
	steveclient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	namespacedClient := steveclient.SteveType(stevetypes.Pod).NamespacedSteveClient(namespaceName)

	backoff := kwait.Backoff{
		Duration: 5 * time.Second,
		Factor:   1,
		Jitter:   0,
		Steps:    10,
	}

	err = kwait.ExponentialBackoff(backoff, func() (finished bool, err error) {
		podsResp, err := namespacedClient.List(nil)
		if err != nil {
			return false, err
		}

		for _, podResp := range podsResp.Data {
			podStatus := &corev1.PodStatus{}
			err = v1.ConvertToK8sType(podResp.Status, podStatus)
			if err != nil {
				return false, err
			}

			for _, containerStatus := range podStatus.ContainerStatuses {
				if containerStatus.State.Running == nil && podResp.State.Name != "completed" {
					return false, nil
				}
			}
		}
		return true, nil
	})
	if err != nil {
		return err
	}

	return nil
}
