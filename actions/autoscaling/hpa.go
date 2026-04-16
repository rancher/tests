package autoscaling

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/workloads"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	hpaapi "github.com/rancher/tests/actions/kubeapi/autoscaling"
	deploymentapi "github.com/rancher/tests/actions/kubeapi/workloads/deployments"
	"github.com/sirupsen/logrus"
	appv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	hpaNamePrefix      = "hpa"
	workloadNamePrefix = "workload"
	containerName      = "test1"
	containerImage     = "ranchertest/mytestcontainer"
	deploymentKind     = "Deployment"
	deploymentAPI      = "apps/v1"
)

// DefaultCPUMetrics returns the default CPU metrics for HPA tests.
func DefaultCPUMetrics() []autoscalingv2.MetricSpec {
	utilizationValue := int32(50)
	return []autoscalingv2.MetricSpec{
		{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceCPU,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: &utilizationValue,
				},
			},
		},
	}
}

// DefaultMemoryMetrics returns the default memory metrics for HPA edit tests.
func DefaultMemoryMetrics() []autoscalingv2.MetricSpec {
	averageValue := resource.MustParse("32Mi")
	return []autoscalingv2.MetricSpec{
		{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceMemory,
				Target: autoscalingv2.MetricTarget{
					Type:         autoscalingv2.AverageValueMetricType,
					AverageValue: &averageValue,
				},
			},
		},
	}
}

// CreateTestWorkload creates a Deployment workload with the default container spec for HPA tests.
func CreateTestWorkload(client *rancher.Client, clusterID, namespace string) (*appv1.Deployment, error) {
	workloadName := namegen.AppendRandomString(workloadNamePrefix)

	containerTemplate := workloads.NewContainer(
		containerName,
		containerImage,
		corev1.PullAlways,
		[]corev1.VolumeMount{},
		[]corev1.EnvFromSource{},
		nil,
		nil,
		nil,
	)
	containerTemplate.Resources = corev1.ResourceRequirements{
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
		[]corev1.Container{containerTemplate},
		[]corev1.Volume{},
		[]corev1.LocalObjectReference{},
		nil,
		nil,
	)

	createdDeployment, err := deploymentapi.CreateDeployment(client, clusterID, workloadName, namespace, podTemplate, 1)
	if err != nil {
		return nil, fmt.Errorf("failed to create workload: %w", err)
	}

	logrus.Infof("Waiting for workload %s to become active", workloadName)
	err = deploymentapi.WaitForDeploymentActive(client, clusterID, namespace, workloadName)
	if err != nil {
		return nil, fmt.Errorf("workload %s did not become active: %w", workloadName, err)
	}

	return createdDeployment, nil
}

// CreateHPA creates an HPA targeting the given workload with specified min/max replicas and metrics.
// If workload is nil, a new test workload is created first.
func CreateHPA(client *rancher.Client, clusterID, namespace string, workload *appv1.Deployment, minReplicas int32, maxReplicas int32, metrics []autoscalingv2.MetricSpec) (*autoscalingv2.HorizontalPodAutoscaler, *appv1.Deployment, error) {
	var err error
	if workload == nil {
		workload, err = CreateTestWorkload(client, clusterID, namespace)
		if err != nil {
			return nil, nil, err
		}
	}

	hpaName := namegen.AppendRandomString(hpaNamePrefix)
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hpaName,
			Namespace: namespace,
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: deploymentAPI,
				Kind:       deploymentKind,
				Name:       workload.Name,
			},
			MinReplicas: &minReplicas,
			MaxReplicas: maxReplicas,
			Metrics:     metrics,
		},
	}

	logrus.Infof("Creating HPA %s targeting workload %s", hpaName, workload.Name)
	createdHPA, err := hpaapi.CreateHPA(client, clusterID, namespace, hpa)
	if err != nil {
		return nil, workload, fmt.Errorf("failed to create HPA: %w", err)
	}

	logrus.Infof("Waiting for HPA %s to become active", hpaName)
	err = WaitForHPAActive(client, clusterID, namespace, hpaName)
	if err != nil {
		return createdHPA, workload, fmt.Errorf("HPA %s did not become active: %w", hpaName, err)
	}

	return createdHPA, workload, nil
}

// WaitForHPAActive polls until the HPA reports the expected conditions or a timeout.
func WaitForHPAActive(client *rancher.Client, clusterID, namespace, hpaName string) error {
	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.FiveMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		hpaList, err := hpaapi.ListHPAs(client, clusterID, namespace, metav1.ListOptions{
			FieldSelector: "metadata.name=" + hpaName,
		})
		if err != nil {
			return false, nil
		}

		if len(hpaList.Items) == 0 {
			return false, nil
		}

		hpa := hpaList.Items[0]
		for _, condition := range hpa.Status.Conditions {
			if condition.Type == autoscalingv2.ScalingActive && condition.Status == corev1.ConditionTrue {
				return true, nil
			}
		}

		return false, nil
	})
}

// WaitForDeploymentPodCount polls until the deployment has the expected number of available replicas.
func WaitForDeploymentPodCount(client *rancher.Client, clusterID, namespace, deploymentName string, expectedCount int32) error {
	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.FiveMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		deploymentList, err := deploymentapi.ListDeployments(client, clusterID, namespace, metav1.ListOptions{
			FieldSelector: "metadata.name=" + deploymentName,
		})
		if err != nil {
			return false, nil
		}

		if len(deploymentList.Items) == 0 {
			return false, nil
		}

		dep := deploymentList.Items[0]
		if dep.Status.AvailableReplicas == expectedCount {
			return true, nil
		}

		return false, nil
	})
}

// DeleteHPAAndWait deletes an HPA and polls until it is no longer found.
func DeleteHPAAndWait(client *rancher.Client, clusterID, namespace, hpaName string) error {
	logrus.Infof("Deleting HPA %s", hpaName)
	err := hpaapi.DeleteHPA(client, clusterID, namespace, hpaName)
	if err != nil {
		return fmt.Errorf("failed to delete HPA %s: %w", hpaName, err)
	}

	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, 30*time.Second, true, func(ctx context.Context) (done bool, err error) {
		hpaList, err := hpaapi.ListHPAs(client, clusterID, namespace, metav1.ListOptions{
			FieldSelector: "metadata.name=" + hpaName,
		})
		if err != nil {
			return false, nil
		}

		if len(hpaList.Items) == 0 {
			return true, nil
		}

		return false, nil
	})
}
