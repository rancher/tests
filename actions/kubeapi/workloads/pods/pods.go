package pods

import (
	"context"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	PauseImage       = "registry.k8s.io/pause:3.9"
	DefaultImageName = "nginx"
)

// CreatePodWithResources creates a pod with arbitrary resource requests and limits
func CreatePodWithResources(client *rancher.Client, clusterID, namespace, imageName string, requests, limits map[corev1.ResourceName]string, waitForPod bool) (*corev1.Pod, error) {
	if imageName == "" {
		imageName = DefaultImageName
	}

	ctx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	resources := corev1.ResourceRequirements{}

	if len(requests) > 0 {
		resources.Requests = corev1.ResourceList{}
		for name, value := range requests {
			resources.Requests[name] = resource.MustParse(value)
		}
	}

	if len(limits) > 0 {
		resources.Limits = corev1.ResourceList{}
		for name, value := range limits {
			resources.Limits[name] = resource.MustParse(value)
		}
	}

	container := corev1.Container{
		Name:            namegen.AppendRandomString("container-"),
		Image:           imageName,
		ImagePullPolicy: corev1.PullIfNotPresent,
	}

	if len(resources.Requests) > 0 || len(resources.Limits) > 0 {
		container.Resources = resources
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namegen.AppendRandomString("pod-"),
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				container,
			},
		},
	}

	createdPod, err := ctx.Core.Pod().Create(pod)
	if err != nil {
		return nil, err
	}

	if waitForPod {
		err = WaitForPodRunning(client, clusterID, namespace, createdPod.Name)
		if err != nil {
			return nil, err
		}
	}

	return createdPod, nil
}

// WaitForPodRunning waits until the specified pod reaches the Running state
func WaitForPodRunning(client *rancher.Client, clusterID, namespace, podName string) error {
	ctx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	return kwait.PollUntilContextTimeout(context.Background(), defaults.FiveHundredMillisecondTimeout, defaults.OneMinuteTimeout, true, func(context.Context) (bool, error) {
		pod, err := ctx.Core.Pod().Get(namespace, podName, metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}

		switch pod.Status.Phase {
		case corev1.PodRunning:
			return true, nil
		case corev1.PodFailed:
			return false, fmt.Errorf("pod %s failed: %s", pod.Name, pod.Status.Message)
		default:
			return false, nil
		}
	},
	)
}

// DeletePod deletes the specified pod from the given namespace using wrangler context
func DeletePod(client *rancher.Client, clusterID, namespace, podName string) error {
	ctx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	err = ctx.Core.Pod().Delete(namespace, podName, &metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	return nil
}
