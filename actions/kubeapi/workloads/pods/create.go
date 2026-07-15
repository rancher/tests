package pods

import (
	"github.com/rancher/shepherd/clients/rancher"
	extpodapi "github.com/rancher/shepherd/extensions/kubeapi/workloads/pods"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	PauseImage       = "registry.k8s.io/pause:3.9"
	DefaultImageName = "nginx"
)

// CreatePod creates a pod in the specified cluster and namespace with a template that has a single container
func CreatePod(client *rancher.Client, clusterID, namespace, imageName string, waitForPod bool) (*corev1.Pod, error) {
	if imageName == "" {
		imageName = DefaultImageName
	}
	podTemplate := CreateContainerAndPodTemplate(imageName)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namegen.AppendRandomString("testpod-"),
			Namespace: namespace,
		},
		Spec: podTemplate.Spec,
	}
	return extpodapi.CreatePodWithTemplate(client, clusterID, pod, waitForPod)
}

// CreatePodWithResources creates a pod with arbitrary resource requests and limits. If waitForPod is true, it will wait for the pod to be running
func CreatePodWithResources(client *rancher.Client, clusterID, namespace, imageName string, requests, limits map[corev1.ResourceName]string, waitForPod bool) (*corev1.Pod, error) {
	if imageName == "" {
		imageName = DefaultImageName
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
		Name:            namegen.AppendRandomString("testcontainer-"),
		Image:           imageName,
		ImagePullPolicy: corev1.PullIfNotPresent,
	}

	if len(resources.Requests) > 0 || len(resources.Limits) > 0 {
		container.Resources = resources
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namegen.AppendRandomString("testpod-"),
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				container,
			},
		},
	}

	createdPod, err := extpodapi.CreatePodWithTemplate(client, clusterID, pod, waitForPod)
	if err != nil {
		return nil, err
	}

	return createdPod, nil
}
