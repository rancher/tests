package rbac

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/workloads"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	deploymentapi "github.com/rancher/tests/actions/kubeapi/workloads/deployments"
	hpaapi "github.com/rancher/tests/actions/kubeapi/workloads/horizontalpodautoscalers"
	"github.com/sirupsen/logrus"
	appv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	hpaContainerName       = "test1"
	hpaCPUUtilizationValue = int32(50)
	hpaMemoryAvgValue      = "32Mi"
)

// CreateHPAWorkload creates a deployment with resource requests and limits required for HPA metrics.
func CreateHPAWorkload(client *rancher.Client, clusterID, namespaceName string) (*appv1.Deployment, error) {
	deploymentName := namegen.AppendRandomString("test-hpa-wl")

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

	podTemplate := workloads.NewPodTemplate(
		[]corev1.Container{container},
		[]corev1.Volume{},
		[]corev1.LocalObjectReference{},
		nil, nil,
	)

	replicas := int32(1)
	createdDeployment, err := deploymentapi.CreateDeployment(client, clusterID, deploymentName, namespaceName, podTemplate, replicas)
	if err != nil {
		return nil, fmt.Errorf("failed to create workload: %w", err)
	}

	logrus.Infof("Waiting for deployment %s to become active", createdDeployment.Name)
	err = deploymentapi.WatchAndWaitDeployments(client, clusterID, namespaceName, metav1.ListOptions{
		FieldSelector: "metadata.name=" + createdDeployment.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed waiting for deployment to be active: %w", err)
	}

	return createdDeployment, nil
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

	err = WaitForDeploymentReplicas(client, clusterID, namespaceName, workload.Name, initialMin)
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

// DeleteHPA removes an HPA and verifies it no longer exists.
func DeleteHPA(client *rancher.Client, clusterID, namespaceName, hpaName string) error {
	logrus.Infof("Deleting HPA %s", hpaName)
	return hpaapi.DeleteHPA(client, clusterID, namespaceName, hpaName)
}

// WaitForDeploymentReplicas polls until the deployment has the expected number of available replicas.
func WaitForDeploymentReplicas(client *rancher.Client, clusterID, namespaceName, deploymentName string, expectedReplicas int32) error {
	logrus.Infof("Waiting for deployment %s to scale to %d replicas", deploymentName, expectedReplicas)
	return kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.FiveMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		wranglerCtx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
		if err != nil {
			return false, err
		}

		dep, err := wranglerCtx.Apps.Deployment().Get(namespaceName, deploymentName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		return dep.Status.AvailableReplicas >= expectedReplicas, nil
	})
}

// VerifyHPAFields validates the fields of an HPA object.
func VerifyHPAFields(hpa *autoscalingv2.HorizontalPodAutoscaler, expectedName string, expectedMin, expectedMax int32) error {
	if hpa.Name != expectedName {
		return fmt.Errorf("expected HPA name %s, got %s", expectedName, hpa.Name)
	}

	if hpa.Spec.MinReplicas != nil && *hpa.Spec.MinReplicas != expectedMin {
		return fmt.Errorf("expected minReplicas %d, got %d", expectedMin, *hpa.Spec.MinReplicas)
	}

	if hpa.Spec.MaxReplicas != expectedMax {
		return fmt.Errorf("expected maxReplicas %d, got %d", expectedMax, hpa.Spec.MaxReplicas)
	}

	return nil
}

// VerifyEditForbidden creates an HPA as the cluster owner, then verifies that the user client cannot edit it.
func VerifyEditForbidden(userClient, ownerClient *rancher.Client, clusterID, namespaceName string) error {
	hpaResp, workload, err := CreateHPA(ownerClient, clusterID, namespaceName, nil)
	if err != nil {
		return fmt.Errorf("failed to create HPA as owner: %w", err)
	}

	updatedMin := int32(3)
	updatedMax := int32(10)
	metrics := []autoscalingv2.MetricSpec{hpaapi.BuildCPUUtilizationMetric(hpaCPUUtilizationValue)}
	updateObj := hpaapi.NewHPAObject(hpaResp.Name, namespaceName, workload.Name, updatedMin, updatedMax, metrics)

	_, err = hpaapi.UpdateHPA(userClient, clusterID, namespaceName, hpaResp, updateObj)
	if err == nil {
		return fmt.Errorf("expected forbidden error when editing HPA, but edit succeeded")
	}

	return nil
}

// ListHPAsByName lists HPAs in a namespace filtered by name.
func ListHPAsByName(client *rancher.Client, clusterID, namespaceName, hpaName string) (*hpaapi.HPAList, error) {
	return hpaapi.ListHPAs(client, clusterID, namespaceName, metav1.ListOptions{
		FieldSelector: "metadata.name=" + hpaName,
	})
}
