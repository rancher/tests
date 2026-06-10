package autoscaling

import (
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	exthpaapi "github.com/rancher/shepherd/extensions/kubeapi/hpa"
	extdeploymentsapi "github.com/rancher/shepherd/extensions/kubeapi/workloads/deployments"
	"github.com/rancher/shepherd/extensions/workloads"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	deploymentapi "github.com/rancher/tests/actions/kubeapi/workloads/deployments"
	"github.com/sirupsen/logrus"
	appv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	HpaCPUUtilizationValue = int32(50)
	HpaMemoryAvgValue      = "32Mi"
)

// NewHPAObject builds an autoscalingv2.HorizontalPodAutoscaler targeting the given deployment.
func NewHPAObject(name, namespace, workloadName string, minReplicas, maxReplicas int32, metrics []autoscalingv2.MetricSpec) *autoscalingv2.HorizontalPodAutoscaler {
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       workloadName,
			},
			MinReplicas: &minReplicas,
			MaxReplicas: maxReplicas,
			Metrics:     metrics,
		},
	}
}

// BuildCPUUtilizationMetric returns a MetricSpec for CPU utilization targeting the given percentage.
func BuildCPUUtilizationMetric(utilization int32) autoscalingv2.MetricSpec {
	return autoscalingv2.MetricSpec{
		Type: autoscalingv2.ResourceMetricSourceType,
		Resource: &autoscalingv2.ResourceMetricSource{
			Name: corev1.ResourceCPU,
			Target: autoscalingv2.MetricTarget{
				Type:               autoscalingv2.UtilizationMetricType,
				AverageUtilization: &utilization,
			},
		},
	}
}

// BuildMemoryAverageValueMetric returns a MetricSpec for memory average value.
func BuildMemoryAverageValueMetric(avgValue string) autoscalingv2.MetricSpec {
	quantity := resource.MustParse(avgValue)
	return autoscalingv2.MetricSpec{
		Type: autoscalingv2.ResourceMetricSourceType,
		Resource: &autoscalingv2.ResourceMetricSource{
			Name: corev1.ResourceMemory,
			Target: autoscalingv2.MetricTarget{
				Type:         autoscalingv2.AverageValueMetricType,
				AverageValue: &quantity,
			},
		},
	}
}

// CreateHPAWorkload creates a deployment with resource requests and limits required for HPA.
func CreateHPAWorkload(client *rancher.Client, clusterID, namespaceName string) (*appv1.Deployment, error) {
	replicas := int32(1)
	hpaContainerName := namegen.AppendRandomString("hpa-test-container")
	container := workloads.NewContainer(
		hpaContainerName,
		deploymentapi.NginxImageName,
		corev1.PullIfNotPresent,
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

	deploymentTemplate := &appv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "hpa-workload-",
			Namespace:    namespaceName,
		},
		Spec: appv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": hpaContainerName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": hpaContainerName},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{container},
				},
			},
		},
	}

	createdDeployment, err := extdeploymentsapi.CreateDeploymentWithTemplate(client, clusterID, deploymentTemplate, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment from template: %w", err)
	}

	return createdDeployment, nil
}

// CreateHPA creates a workload (if nil) and an HPA targeting it, then waits for the HPA to become active.
func CreateHPA(client *rancher.Client, clusterID, namespaceName string, existingWorkload *appv1.Deployment, waitForActive bool) (*autoscalingv2.HorizontalPodAutoscaler, *appv1.Deployment, error) {
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
	metrics := []autoscalingv2.MetricSpec{BuildCPUUtilizationMetric(HpaCPUUtilizationValue)}

	hpaObj := NewHPAObject(hpaName, namespaceName, workload.Name, minReplicas, maxReplicas, metrics)

	logrus.Infof("Creating HPA %s targeting workload %s", hpaName, workload.Name)
	createdHPA, err := exthpaapi.CreateHPA(client, clusterID, hpaObj, waitForActive)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create HPA: %w", err)
	}

	return createdHPA, workload, nil
}
