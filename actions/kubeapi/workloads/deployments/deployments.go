package deployments

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/api/scheme"
	"github.com/rancher/shepherd/pkg/wait"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	appv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
)

const (
	restartAnnotation = "kubectl.kubernetes.io/restartedAt"
)

// DeploymentGroupVersionResource is the required Group Version Resource for accessing deployments in a cluster,
// using the dynamic client.
var DeploymentGroupVersionResource = schema.GroupVersionResource{
	Group:    "apps",
	Version:  "v1",
	Resource: "deployments",
}

// WatchAndWaitDeployments is a helper function that watches the deployments
// sequentially in a specific namespace and waits until number of expected replicas is equal to number of available replicas.
func WatchAndWaitDeployments(client *rancher.Client, clusterID, namespace string, listOptions metav1.ListOptions) error {
	adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
	if err != nil {
		return err
	}
	adminDynamicClient, err := adminClient.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return err
	}

	adminDeploymentResource := adminDynamicClient.Resource(DeploymentGroupVersionResource).Namespace(namespace)

	deployments, err := adminDeploymentResource.List(context.TODO(), listOptions)
	if err != nil {
		return err
	}

	var deploymentList []appv1.Deployment

	for _, unstructuredDeployment := range deployments.Items {
		newDeployment := &appv1.Deployment{}
		err := scheme.Scheme.Convert(&unstructuredDeployment, newDeployment, unstructuredDeployment.GroupVersionKind())
		if err != nil {
			return err
		}

		deploymentList = append(deploymentList, *newDeployment)
	}

	for _, deployment := range deploymentList {
		watchAppInterface, err := adminDeploymentResource.Watch(context.TODO(), metav1.ListOptions{
			FieldSelector:  "metadata.name=" + deployment.Name,
			TimeoutSeconds: &defaults.WatchTimeoutSeconds,
		})
		if err != nil {
			return err
		}

		wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
			deploymentsUnstructured := event.Object.(*unstructured.Unstructured)
			deployment := &appv1.Deployment{}

			err = scheme.Scheme.Convert(deploymentsUnstructured, deployment, deploymentsUnstructured.GroupVersionKind())
			if err != nil {
				return false, err
			}

			if *deployment.Spec.Replicas == deployment.Status.AvailableReplicas {
				return true, nil
			}
			return false, nil
		})
	}

	return nil
}

// RestartDeployment triggers a rollout restart of a deployment by updating an annotation
func RestartDeployment(client *rancher.Client, clusterID, namespaceName, deploymentName string) error {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	deploymentObj, err := wranglerContext.Apps.Deployment().Get(namespaceName, deploymentName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error fetching deployment %s in namespace %s: %w", deploymentName, namespaceName, err)
	}

	if deploymentObj.Spec.Template.Annotations == nil {
		deploymentObj.Spec.Template.Annotations = map[string]string{}
	}

	deploymentObj.Spec.Template.Annotations[restartAnnotation] = time.Now().Format(time.RFC3339)

	_, err = UpdateDeployment(client, clusterID, namespaceName, deploymentObj, true)
	if err != nil {
		return fmt.Errorf("error restarting deployment %s in namespace %s: %w", deploymentName, namespaceName, err)
	}

	return nil
}

// WaitForDeploymentActive waits for a deployment to become active by polling until replicas match available replicas
func WaitForDeploymentActive(client *rancher.Client, clusterID, namespaceName, deploymentName string) error {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return fmt.Errorf("error getting wrangler context for cluster %s: %w", clusterID, err)
	}

	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.FiveMinuteTimeout, false, func(ctx context.Context) (done bool, err error) {
		deployment, err := wranglerContext.Apps.Deployment().Get(namespaceName, deploymentName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("error fetching deployment %s in namespace %s: %w", deploymentName, namespaceName, err)
		}

		if deployment.Spec.Replicas != nil &&
			*deployment.Spec.Replicas == deployment.Status.UpdatedReplicas &&
			*deployment.Spec.Replicas == deployment.Status.ReadyReplicas &&
			*deployment.Spec.Replicas == deployment.Status.AvailableReplicas {
			return true, nil
		}

		return false, nil
	})

	if err != nil {
		return fmt.Errorf("deployment %s in namespace %s did not become active: %w", deploymentName, namespaceName, err)
	}

	return nil
}
