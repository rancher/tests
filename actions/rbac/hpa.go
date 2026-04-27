package rbac

import (
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/workloads"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	workloadsapi "github.com/rancher/tests/actions/kubeapi/workloads/deployments"
	deploymentapi "github.com/rancher/tests/actions/workloads/deployment"
	hpaapi "github.com/rancher/tests/actions/workloads/hpa"
	"github.com/sirupsen/logrus"
	appv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	hpaContainerName       = "test1"
	hpaCPUUtilizationValue = int32(50)
	hpaMemoryAvgValue      = "32Mi"
)

// CreateHPAWorkload creates a deployment with resource requests and limits required for HPA metrics.
func CreateHPAWorkload(client *rancher.Client, clusterID, namespaceName string) (*appv1.Deployment, error) {
	replicas := 1
	createdDeployment, err := deploymentapi.CreateDeployment(client, clusterID, namespaceName, replicas, "", "", false, false, false, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment: %w", err)
	}

	container := workloads.NewContainer(
		hpaContainerName,
		ImageName,
		corev1.PullAlways,
		[]corev1.VolumeMount{},
		[]corev1.EnvFromSource{},
		nil, nil, nil,
	)
	container.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("64Mi"),
			corev1.ResourceCPU:    resource.MustParse("100m"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("512Mi"),
			corev1.ResourceCPU:    resource.MustParse("1000m"),
		},
	}

	createdDeployment.Spec.Template.Spec.Containers = []corev1.Container{container}
	updatedDeployment, err := deploymentapi.UpdateDeployment(client, clusterID, namespaceName, createdDeployment, true)
	if err != nil {
		return nil, fmt.Errorf("failed to update deployment with container spec: %w", err)
	}
	return updatedDeployment, nil
}

// CreateHPA creates a workload (if nil) and an HPA targeting it, then waits for the HPA to become active.
func CreateHPA(client *rancher.Client, clusterID, namespaceName string, existingWorkload *appv1.Deployment) (*autoscalingv2.HorizontalPodAutoscaler, *appv1.Deployment, error) {
	workload := existingWorkload
	var err error
	if workload == nil {
		workload, err = CreateHPAWorkload(client, clusterID, namespaceName)
		if err != nil {
			return nil, nil, err
		}
	}

	hpaName := namegen.AppendRandomString("test-hpa")
	minReplicas := int32(2)
	maxReplicas := int32(5)
	metrics := []autoscalingv2.MetricSpec{hpaapi.BuildCPUUtilizationMetric(hpaCPUUtilizationValue)}

	hpaObj := hpaapi.NewHPAObject(hpaName, namespaceName, workload.Name, minReplicas, maxReplicas, metrics)

	logrus.Infof("Creating HPA %s targeting workload %s", hpaName, workload.Name)
	createdHPA, err := hpaapi.CreateHPA(client, clusterID, namespaceName, hpaObj)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create HPA: %w", err)
	}

	createdHPA, err = hpaapi.WaitForHPAActive(client, clusterID, namespaceName, createdHPA.Name)
	if err != nil {
		return nil, nil, err
	}

	return createdHPA, workload, nil
}

// EditHPA creates a workload and HPA with memory metrics, then updates the HPA to new min/max replicas.
func EditHPA(client *rancher.Client, clusterID, namespaceName string) (*autoscalingv2.HorizontalPodAutoscaler, *appv1.Deployment, error) {
	workload, err := CreateHPAWorkload(client, clusterID, namespaceName)
	if err != nil {
		return nil, nil, err
	}

	hpaName := namegen.AppendRandomString("test-hpa")
	initialMin := int32(2)
	initialMax := int32(4)
	metrics := []autoscalingv2.MetricSpec{hpaapi.BuildMemoryAverageValueMetric(hpaMemoryAvgValue)}

	hpaObj := hpaapi.NewHPAObject(hpaName, namespaceName, workload.Name, initialMin, initialMax, metrics)

	logrus.Infof("Creating HPA %s with memory metric for editing", hpaName)
	createdHPA, err := hpaapi.CreateHPA(client, clusterID, namespaceName, hpaObj)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create HPA for edit: %w", err)
	}

	createdHPA, err = hpaapi.WaitForHPAActive(client, clusterID, namespaceName, createdHPA.Name)
	if err != nil {
		return nil, nil, err
	}

	err = workloadsapi.WaitForDeploymentActive(client, clusterID, namespaceName, workload.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("workload did not scale to initial minReplicas: %w", err)
	}

	updatedMin := int32(3)
	updatedMax := int32(6)
	updatedHPA := hpaapi.NewHPAObject(hpaName, namespaceName, workload.Name, updatedMin, updatedMax, metrics)

	logrus.Infof("Updating HPA %s: minReplicas=%d, maxReplicas=%d", hpaName, updatedMin, updatedMax)
	resultHPA, err := hpaapi.UpdateHPA(client, clusterID, namespaceName, createdHPA, updatedHPA)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to update HPA: %w", err)
	}

	resultHPA, err = hpaapi.WaitForHPAActive(client, clusterID, namespaceName, resultHPA.Name)
	if err != nil {
		return nil, nil, err
	}

	return resultHPA, workload, nil
}

// ListHPAsByName lists HPAs in a namespace filtered by name.
func ListHPAsByName(client *rancher.Client, clusterID, namespaceName, hpaName string) ([]autoscalingv2.HorizontalPodAutoscaler, error) {
	return hpaapi.ListHPAs(client, clusterID, namespaceName, metav1.ListOptions{
		FieldSelector: "metadata.name=" + hpaName,
	})
}
