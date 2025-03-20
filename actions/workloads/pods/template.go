package pods

import (
	"github.com/rancher/shepherd/extensions/workloads"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	corev1 "k8s.io/api/core/v1"
)

const (
	nginxImageName = "public.ecr.aws/docker/library/nginx"
)

// CreateContainerAndPodTemplate creates both the container and pod templates
func CreateContainerAndPodTemplate() corev1.PodTemplateSpec {
	containerName := namegen.AppendRandomString("test-container")

	containerTemplate := workloads.NewContainer(
		containerName,
		nginxImageName,
		corev1.PullAlways,
		[]corev1.VolumeMount{},
		[]corev1.EnvFromSource{},
		nil,
		nil,
		nil,
	)

	podTemplate := workloads.NewPodTemplate(
		[]corev1.Container{containerTemplate},
		[]corev1.Volume{},
		[]corev1.LocalObjectReference{},
		nil,
		nil,
	)

	return podTemplate
}
