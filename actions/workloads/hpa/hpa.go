package hpa

import (
	"context"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	"github.com/sirupsen/logrus"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// CreateHPA is a helper function that uses the wrangler client to create a horizontal pod autoscaler on a namespace for a specific cluster.
func CreateHPA(client *rancher.Client, clusterID, namespace string, hpa *autoscalingv2.HorizontalPodAutoscaler) (*autoscalingv2.HorizontalPodAutoscaler, error) {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	createdHPA, err := wranglerContext.Autoscaling.HorizontalPodAutoscaler().Create(hpa)
	if err != nil {
		return nil, err
	}

	return createdHPA, nil
}

// UpdateHPA is a helper function that uses the wrangler client to update a horizontal pod autoscaler in a namespace for a specific cluster.
func UpdateHPA(client *rancher.Client, clusterID, namespace string, existingHPA *autoscalingv2.HorizontalPodAutoscaler, updatedHPA *autoscalingv2.HorizontalPodAutoscaler) (*autoscalingv2.HorizontalPodAutoscaler, error) {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	latestHPA, err := wranglerContext.Autoscaling.HorizontalPodAutoscaler().Get(namespace, existingHPA.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	updatedHPA.ResourceVersion = latestHPA.ResourceVersion

	result, err := wranglerContext.Autoscaling.HorizontalPodAutoscaler().Update(updatedHPA)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// DeleteHPA is a helper function that uses the wrangler client to delete a horizontal pod autoscaler from a cluster.
func DeleteHPA(client *rancher.Client, clusterID, namespace, hpaName string) error {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	err = wranglerContext.Autoscaling.HorizontalPodAutoscaler().Delete(namespace, hpaName, &metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveHundredMillisecondTimeout, defaults.TenSecondTimeout, false, func(ctx context.Context) (done bool, err error) {
		_, err = wranglerContext.Autoscaling.HorizontalPodAutoscaler().Get(namespace, hpaName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}

		return false, err
	})
	if err != nil {
		return fmt.Errorf("HPA %s was not deleted within timeout: %w", hpaName, err)
	}

	return nil
}

// HPAList is a struct that contains a list of horizontal pod autoscalers.
type HPAList struct {
	Items []autoscalingv2.HorizontalPodAutoscaler
}

// ListHPAs is a helper function that uses the wrangler client to list horizontal pod autoscalers on a namespace for a specific cluster with its list options.
func ListHPAs(client *rancher.Client, clusterID, namespace string, listOpts metav1.ListOptions) ([]autoscalingv2.HorizontalPodAutoscaler, error) {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	hpas, err := wranglerContext.Autoscaling.HorizontalPodAutoscaler().List(namespace, listOpts)
	if err != nil {
		return nil, err
	}

	return hpas.Items, nil
}

// Names is a method that accepts HPAList as a receiver,
// returns each HPA name in the list as a new slice of strings.
func (list *HPAList) Names() []string {
	var hpaNames []string

	for _, hpa := range list.Items {
		hpaNames = append(hpaNames, hpa.Name)
	}

	return hpaNames
}

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

// WaitForHPAActive polls the wrangler client until the HPA reaches active state.
func WaitForHPAActive(client *rancher.Client, clusterID, namespace, hpaName string) (*autoscalingv2.HorizontalPodAutoscaler, error) {
	var hpaObj *autoscalingv2.HorizontalPodAutoscaler

	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	logrus.Infof("Waiting for HPA %s to become active", hpaName)
	err = kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.FiveMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {

		currentHPA, err := wranglerContext.Autoscaling.HorizontalPodAutoscaler().Get(namespace, hpaName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		for _, condition := range currentHPA.Status.Conditions {
			if condition.Type == autoscalingv2.ScalingActive && condition.Status == corev1.ConditionTrue {
				hpaObj = currentHPA
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("HPA %s did not reach active state: %w", hpaName, err)
	}

	return hpaObj, nil
}
