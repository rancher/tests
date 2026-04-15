package horizontalpodautoscalers

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/api/scheme"
	"github.com/rancher/shepherd/pkg/wait"
	"github.com/sirupsen/logrus"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
)

// HPAGroupVersionResource is the required Group Version Resource for accessing horizontal pod autoscalers in a cluster,
// using the dynamic client.
var HPAGroupVersionResource = schema.GroupVersionResource{
	Group:    "autoscaling",
	Version:  "v2",
	Resource: "horizontalpodautoscalers",
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

// WatchAndWaitHPAs is a helper function that watches the HPAs
// sequentially in a specific namespace and waits until they reach a desired conditions state.
func WatchAndWaitHPAs(client *rancher.Client, clusterID, namespace string, listOptions metav1.ListOptions) error {
	adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
	if err != nil {
		return err
	}
	adminDynamicClient, err := adminClient.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return err
	}

	adminHPAResource := adminDynamicClient.Resource(HPAGroupVersionResource).Namespace(namespace)

	hpas, err := adminHPAResource.List(context.TODO(), listOptions)
	if err != nil {
		return err
	}

	var hpaList []autoscalingv2.HorizontalPodAutoscaler

	for _, unstructuredHPA := range hpas.Items {
		newHPA := &autoscalingv2.HorizontalPodAutoscaler{}
		err := scheme.Scheme.Convert(&unstructuredHPA, newHPA, unstructuredHPA.GroupVersionKind())
		if err != nil {
			return err
		}

		hpaList = append(hpaList, *newHPA)
	}

	for _, hpa := range hpaList {
		watchInterface, err := adminHPAResource.Watch(context.TODO(), metav1.ListOptions{
			FieldSelector:  "metadata.name=" + hpa.Name,
			TimeoutSeconds: &defaults.WatchTimeoutSeconds,
		})
		if err != nil {
			return err
		}

		wait.WatchWait(watchInterface, func(event watch.Event) (ready bool, err error) {
			hpaUnstructured := event.Object.(*unstructured.Unstructured)
			hpaObj := &autoscalingv2.HorizontalPodAutoscaler{}

			err = scheme.Scheme.Convert(hpaUnstructured, hpaObj, hpaUnstructured.GroupVersionKind())
			if err != nil {
				return false, err
			}

			for _, condition := range hpaObj.Status.Conditions {
				if condition.Type == autoscalingv2.ScalingActive && condition.Status == corev1.ConditionTrue {
					return true, nil
				}
			}
			return false, nil
		})
	}

	return nil
}

// WaitForHPAActive polls the dynamic client until the HPA reaches active state.
func WaitForHPAActive(client *rancher.Client, clusterID, namespace, hpaName string) (*autoscalingv2.HorizontalPodAutoscaler, error) {
	var hpaObj *autoscalingv2.HorizontalPodAutoscaler

	logrus.Infof("Waiting for HPA %s to become active", hpaName)
	err := kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.FiveMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
		if err != nil {
			return false, nil
		}

		hpaResource := dynamicClient.Resource(HPAGroupVersionResource).Namespace(namespace)
		unstructuredHPA, err := hpaResource.Get(context.TODO(), hpaName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		hpaObj = &autoscalingv2.HorizontalPodAutoscaler{}
		err = scheme.Scheme.Convert(unstructuredHPA, hpaObj, unstructuredHPA.GroupVersionKind())
		if err != nil {
			return false, nil
		}

		for _, condition := range hpaObj.Status.Conditions {
			if condition.Type == autoscalingv2.ScalingActive && condition.Status == corev1.ConditionTrue {
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
