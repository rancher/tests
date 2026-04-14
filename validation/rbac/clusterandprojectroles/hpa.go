//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package clusterandprojectroles

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/workloads"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
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
	hpaSteveType        = "autoscaling.horizontalpodautoscaler"
	testContainerName   = "test1"
	testDeploymentImage = "nginx"
	cpuUtilizationValue = int32(50)
	memoryAvgValue      = "32Mi"
)

// createHPAWorkload creates a deployment with resource requests and limits required for HPA metrics.
func createHPAWorkload(client *rancher.Client, clusterID, namespaceName string) (*appv1.Deployment, error) {
	deploymentName := namegen.AppendRandomString("test-hpa-wl")

	container := workloads.NewContainer(
		testContainerName,
		testDeploymentImage,
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

// buildCPUUtilizationMetric returns a MetricSpec for CPU utilization targeting the given percentage.
func buildCPUUtilizationMetric(utilization int32) autoscalingv2.MetricSpec {
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

// buildMemoryAverageValueMetric returns a MetricSpec for memory average value.
func buildMemoryAverageValueMetric(avgValue string) autoscalingv2.MetricSpec {
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

// newHPAObject builds an autoscalingv2.HorizontalPodAutoscaler targeting the given deployment.
func newHPAObject(name, namespace, workloadName string, minReplicas, maxReplicas int32, metrics []autoscalingv2.MetricSpec) *autoscalingv2.HorizontalPodAutoscaler {
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

// createHPA creates a workload (if nil) and an HPA targeting it, then waits for the HPA to become active.
func createHPA(client *rancher.Client, steveClient *v1.Client, clusterID, namespaceName string, existingWorkload *appv1.Deployment) (*v1.SteveAPIObject, *appv1.Deployment, error) {
	workload := existingWorkload
	var err error
	if workload == nil {
		workload, err = createHPAWorkload(client, clusterID, namespaceName)
		if err != nil {
			return nil, nil, err
		}
	}

	hpaName := namegen.AppendRandomString("test-hpa")
	minReplicas := int32(2)
	maxReplicas := int32(5)
	metrics := []autoscalingv2.MetricSpec{buildCPUUtilizationMetric(cpuUtilizationValue)}

	hpaObj := newHPAObject(hpaName, namespaceName, workload.Name, minReplicas, maxReplicas, metrics)

	logrus.Infof("Creating HPA %s targeting workload %s", hpaName, workload.Name)
	hpaResp, err := steveClient.SteveType(hpaSteveType).Create(hpaObj)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create HPA: %w", err)
	}

	hpaResp, err = waitForHPAActive(steveClient, hpaResp.ID)
	if err != nil {
		return nil, nil, err
	}

	return hpaResp, workload, nil
}

// editHPA creates a workload and HPA with memory metrics, then updates the HPA to new min/max replicas.
func editHPA(client *rancher.Client, steveClient *v1.Client, clusterID, namespaceName string) (*v1.SteveAPIObject, *appv1.Deployment, error) {
	workload, err := createHPAWorkload(client, clusterID, namespaceName)
	if err != nil {
		return nil, nil, err
	}

	hpaName := namegen.AppendRandomString("test-hpa")
	initialMin := int32(2)
	initialMax := int32(4)
	metrics := []autoscalingv2.MetricSpec{buildMemoryAverageValueMetric(memoryAvgValue)}

	hpaObj := newHPAObject(hpaName, namespaceName, workload.Name, initialMin, initialMax, metrics)

	logrus.Infof("Creating HPA %s with memory metric for editing", hpaName)
	hpaResp, err := steveClient.SteveType(hpaSteveType).Create(hpaObj)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create HPA for edit: %w", err)
	}

	hpaResp, err = waitForHPAActive(steveClient, hpaResp.ID)
	if err != nil {
		return nil, nil, err
	}

	err = waitForDeploymentReplicas(client, clusterID, namespaceName, workload.Name, initialMin)
	if err != nil {
		return nil, nil, fmt.Errorf("workload did not scale to initial minReplicas: %w", err)
	}

	updatedMin := int32(3)
	updatedMax := int32(6)
	updatedHPA := newHPAObject(hpaName, namespaceName, workload.Name, updatedMin, updatedMax, metrics)
	updatedHPA.ResourceVersion = hpaResp.ObjectMeta.ResourceVersion

	logrus.Infof("Updating HPA %s: minReplicas=%d, maxReplicas=%d", hpaName, updatedMin, updatedMax)
	updatedResp, err := steveClient.SteveType(hpaSteveType).Update(hpaResp, updatedHPA)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to update HPA: %w", err)
	}

	updatedResp, err = waitForHPAActive(steveClient, updatedResp.ID)
	if err != nil {
		return nil, nil, err
	}

	return updatedResp, workload, nil
}

// deleteHPA removes an HPA and verifies it no longer exists.
func deleteHPA(steveClient *v1.Client, hpa *v1.SteveAPIObject, namespaceName string) error {
	logrus.Infof("Deleting HPA %s", hpa.Name)
	err := steveClient.SteveType(hpaSteveType).Delete(hpa)
	if err != nil {
		return fmt.Errorf("failed to delete HPA: %w", err)
	}

	err = kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		hpaList, listErr := steveClient.SteveType(hpaSteveType).NamespacedSteveClient(namespaceName).List(url.Values{
			"fieldSelector": {"metadata.name=" + hpa.Name},
		})
		if listErr != nil {
			return false, listErr
		}
		return len(hpaList.Data) == 0, nil
	})
	if err != nil {
		return fmt.Errorf("HPA %s was not deleted within timeout: %w", hpa.Name, err)
	}

	return nil
}

// waitForHPAActive polls the Steve API until the HPA reaches active state.
func waitForHPAActive(steveClient *v1.Client, hpaID string) (*v1.SteveAPIObject, error) {
	var hpaResp *v1.SteveAPIObject

	logrus.Infof("Waiting for HPA %s to become active", hpaID)
	err := kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.FiveMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		hpaResp, err = steveClient.SteveType(hpaSteveType).ByID(hpaID)
		if err != nil {
			return false, nil
		}

		state, ok := hpaResp.JSONResp["metadata"].(map[string]any)["state"].(map[string]any)
		if !ok {
			return false, nil
		}
		if name, ok := state["name"].(string); ok && name == "active" {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("HPA %s did not reach active state: %w", hpaID, err)
	}

	return hpaResp, nil
}

// waitForDeploymentReplicas polls until the deployment has the expected number of available replicas.
func waitForDeploymentReplicas(client *rancher.Client, clusterID, namespaceName, deploymentName string, expectedReplicas int32) error {
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

// verifyHPAFields converts a SteveAPIObject to an HPA and validates its fields.
func verifyHPAFields(hpa *v1.SteveAPIObject, expectedName string, expectedMin, expectedMax int32) (*autoscalingv2.HorizontalPodAutoscaler, error) {
	hpaObj := &autoscalingv2.HorizontalPodAutoscaler{}
	err := v1.ConvertToK8sType(hpa.JSONResp, hpaObj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert HPA response to k8s type: %w", err)
	}

	if hpaObj.Name != expectedName {
		return nil, fmt.Errorf("expected HPA name %s, got %s", expectedName, hpaObj.Name)
	}

	if hpaObj.Spec.MinReplicas != nil && *hpaObj.Spec.MinReplicas != expectedMin {
		return nil, fmt.Errorf("expected minReplicas %d, got %d", expectedMin, *hpaObj.Spec.MinReplicas)
	}

	if hpaObj.Spec.MaxReplicas != expectedMax {
		return nil, fmt.Errorf("expected maxReplicas %d, got %d", expectedMax, hpaObj.Spec.MaxReplicas)
	}

	return hpaObj, nil
}

// verifyEditForbidden creates an HPA as the cluster owner, then verifies that the user client cannot edit it.
func verifyEditForbidden(userSteveClient, ownerSteveClient *v1.Client, ownerClient *rancher.Client, clusterID, namespaceName string) error {
	hpaResp, workload, err := createHPA(ownerClient, ownerSteveClient, clusterID, namespaceName, nil)
	if err != nil {
		return fmt.Errorf("failed to create HPA as owner: %w", err)
	}

	updatedMin := int32(3)
	updatedMax := int32(10)
	metrics := []autoscalingv2.MetricSpec{buildCPUUtilizationMetric(cpuUtilizationValue)}
	updateObj := newHPAObject(hpaResp.Name, namespaceName, workload.Name, updatedMin, updatedMax, metrics)

	_, err = userSteveClient.SteveType(hpaSteveType).Update(hpaResp, updateObj)
	if err == nil {
		return fmt.Errorf("expected forbidden error when editing HPA, but edit succeeded")
	}

	return nil
}

// listHPAsByName lists HPAs in a namespace filtered by name via the Steve API.
func listHPAsByName(steveClient *v1.Client, namespaceName, hpaName string) (*v1.SteveCollection, error) {
	return steveClient.SteveType(hpaSteveType).NamespacedSteveClient(namespaceName).List(url.Values{
		"fieldSelector": {"metadata.name=" + hpaName},
	})
}
