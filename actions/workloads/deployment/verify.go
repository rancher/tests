package deployment

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/workloads"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/wrangler"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	Webhook            = "rancher-webhook"
	SUC                = "system-upgrade-controller"
	Fleet              = "fleet-agent"
	ClusterAgent       = "cattle-cluster-agent"
	revisionAnnotation = "deployment.kubernetes.io/revision"
)

// VerifyDeployment waits for a deployment to be ready in the downstream cluster
func VerifyDeployment(client *rancher.Client, clusterID, namespace, name string) error {
	steveclient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	var deploymentConditions []appv1.DeploymentCondition
	err = kwait.PollUntilContextTimeout(context.TODO(), 1*time.Second, defaults.TenMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		deployment, err := GetDeploymentByName(steveclient, clusterID, namespace, name)
		if err != nil {
			return false, err
		}

		deploymentConditions = deployment.Status.Conditions
		if *deployment.Spec.Replicas == deployment.Status.AvailableReplicas {
			return true, nil
		}

		return false, nil
	})

	if err != nil {
		logrus.Warningf("Failed to verify deployment: %s", deploymentConditions[0].Message)
	}

	return err
}

func VerifyDeploymentUpgrade(client *rancher.Client, clusterName string, namespaceName string, appv1Deployment *appv1.Deployment, expectedRevision string, image string, expectedReplicas int) error {
	logrus.Debugf("Waiting for deployment %s to become active", appv1Deployment.Name)
	err := charts.WatchAndWaitDeployments(client, clusterName, namespaceName, metav1.ListOptions{
		FieldSelector: "metadata.name=" + appv1Deployment.Name,
	})
	if err != nil {
		return err
	}

	logrus.Debug("Waiting for all pods to be running")
	err = pods.WatchAndWaitPodContainerRunning(client, clusterName, namespaceName)
	if err != nil {
		return err
	}

	logrus.Tracef("Verifying rollout history by revision %s", expectedRevision)
	err = VerifyDeploymentRolloutHistory(client, clusterName, namespaceName, appv1Deployment.Name, expectedRevision)
	if err != nil {
		return err
	}

	pods, err := GetDeploymentPods(client, clusterName, namespaceName, appv1Deployment.Name)
	if err != nil {
		return err
	}

	if expectedReplicas != len(pods) {
		err_msg := fmt.Sprintf("expected replica count: %d does not equal pod count: %d", expectedReplicas, len(pods))
		return errors.New(err_msg)
	}

	return err
}

func VerifyDeploymentScale(client *rancher.Client, clusterName string, namespaceName string, scaleDeployment *appv1.Deployment, image string, expectedReplicas int) error {
	logrus.Debug("Waiting for all pods to be running")
	err := pods.WatchAndWaitPodContainerRunning(client, clusterName, namespaceName)
	if err != nil {
		return err
	}

	pods, err := GetDeploymentPods(client, clusterName, namespaceName, scaleDeployment.Name)
	if err != nil {
		return err
	}

	if expectedReplicas != len(pods) {
		err_msg := fmt.Sprintf("expected replica count: %d does not equal pod count: %d", expectedReplicas, len(pods))
		return errors.New(err_msg)
	}

	return err
}

func VerifyDeploymentRolloutHistory(client *rancher.Client, clusterID, namespaceName string, deploymentName string, expectedRevision string) error {
	var wranglerContext *wrangler.Context
	var err error

	err = charts.WatchAndWaitDeployments(client, clusterID, namespaceName, metav1.ListOptions{
		FieldSelector: "metadata.name=" + deploymentName,
	})
	if err != nil {
		return err
	}

	wranglerContext = client.WranglerContext
	if clusterID != "local" {
		wranglerContext, err = client.WranglerContext.DownStreamClusterWranglerContext(clusterID)
		if err != nil {
			return err
		}
	}

	latestDeployment, err := wranglerContext.Apps.Deployment().Get(namespaceName, deploymentName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if latestDeployment.ObjectMeta.Annotations == nil {
		return errors.New("revision empty")
	}

	revision := latestDeployment.ObjectMeta.Annotations[revisionAnnotation]

	if revision != expectedRevision {
		return errors.New("revision not found")
	}

	return nil
}

func VerifyOrchestrationStatus(client *rancher.Client, clusterID, namespaceName string, deploymentName string, isPaused bool) error {
	var wranglerContext *wrangler.Context
	var err error

	err = charts.WatchAndWaitDeployments(client, clusterID, namespaceName, metav1.ListOptions{
		FieldSelector: "metadata.name=" + deploymentName,
	})
	if err != nil {
		return err
	}

	wranglerContext = client.WranglerContext
	if clusterID != "local" {
		wranglerContext, err = client.WranglerContext.DownStreamClusterWranglerContext(clusterID)
		if err != nil {
			return err
		}
	}

	latestDeployment, err := wranglerContext.Apps.Deployment().Get(namespaceName, deploymentName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if isPaused && !latestDeployment.Spec.Paused {
		return errors.New("the orchestration is active")
	}

	if !isPaused && latestDeployment.Spec.Paused {
		return errors.New("the orchestration is paused")
	}

	return nil
}

// VerifyDeploymentSideKick verifies deployments can create a sidekick pod
func VerifyDeploymentSideKick(client *rancher.Client, clusterID, namespace, deploymentName string) error {
	steveclient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	deployment, err := GetDeploymentByName(steveclient, clusterID, namespace, deploymentName)
	if err != nil {
		return err
	}

	err = pods.WatchAndWaitPodContainerRunning(client, clusterID, deployment.Namespace)
	if err != nil {
		return err
	}

	containerName := namegen.AppendRandomString("sidekick-container")
	sideKickContainer := corev1.Container{
		Name:  containerName,
		Image: nginxImageName,
	}

	deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, sideKickContainer)

	updatedDeployment, err := UpdateDeployment(client, clusterID, deployment.Namespace, deployment, true)
	if err != nil {
		return err
	}

	logrus.Debugf("Creating deployment sidekick (%s)", deployment.Name)
	err = charts.WatchAndWaitDeployments(client, clusterID, deployment.Namespace, metav1.ListOptions{
		FieldSelector: "metadata.name=" + updatedDeployment.Name,
	})
	if err != nil {
		return err
	}

	logrus.Tracef("Waiting for all pods to be running deployment: %s", deployment.Name)
	err = pods.WatchAndWaitPodContainerRunning(client, clusterID, deployment.Namespace)

	return err
}

// VerifyDeploymentUpgradeRollback verifies deployments can perform a series of rollbacks
func VerifyDeploymentUpgradeRollback(client *rancher.Client, clusterID, namespace, deploymentName string) error {
	steveclient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	deployment, err := GetDeploymentByName(steveclient, clusterID, namespace, deploymentName)
	if err != nil {
		return err
	}

	startingRevision, err := strconv.Atoi(deployment.ObjectMeta.Annotations[revisionAnnotation])
	if err != nil {
		return err
	}

	containerName := namegen.AppendRandomString("update-test-container")
	updatedContainers := []corev1.Container{
		{
			Name:  containerName,
			Image: redisImageName,
		},
	}

	deployment.Spec.Template.Spec.Containers = updatedContainers

	logrus.Debugf("Updating deployment (%s) image: %s", deployment.Name, redisImageName)
	deployment, err = UpdateDeployment(client, clusterID, deployment.Namespace, deployment, true)
	if err != nil {
		return err
	}

	logrus.Debugf("Verify deployment (%s) upgrade", deployment.Name)
	err = VerifyDeploymentUpgrade(client, clusterID, deployment.Namespace, deployment, strconv.Itoa(startingRevision+1), redisImageName, int(*deployment.Spec.Replicas))
	if err != nil {
		return err
	}

	containerName = namegen.AppendRandomString("update-test-container-two")
	updatedContainers = []corev1.Container{
		{
			Name:  containerName,
			Image: redisImageName,
			TTY:   true,
			Stdin: true,
		},
	}
	deployment.Spec.Template.Spec.Containers = updatedContainers

	logrus.Debugf("Updating deployment (%s) TTY: %v stdin: %v", deployment.Name, true, true)
	deployment, err = UpdateDeployment(client, clusterID, deployment.Namespace, deployment, true)
	if err != nil {
		return err
	}

	err = VerifyDeploymentUpgrade(client, clusterID, deployment.Namespace, deployment, strconv.Itoa(startingRevision+2), ubuntuImageName, int(*deployment.Spec.Replicas))
	if err != nil {
		return err
	}

	logrus.Debugf("Rollback deployment: %s revision: %v", deployment.Name, startingRevision+1)
	logRollback, err := RollbackDeployment(client, clusterID, deployment.Namespace, deployment.Name, startingRevision+1)
	if err != nil {
		return err
	}
	if logRollback == "" {
		return err
	}

	err = VerifyDeploymentUpgrade(client, clusterID, deployment.Namespace, deployment, strconv.Itoa(startingRevision+3), nginxImageName, int(*deployment.Spec.Replicas))
	if err != nil {
		return err
	}

	logrus.Debugf("Rollback deployment: %s revision: %v", deployment.Name, startingRevision+2)
	logRollback, err = RollbackDeployment(client, clusterID, deployment.Namespace, deployment.Name, startingRevision+2)
	if err != nil {
		return err
	}
	if logRollback == "" {
		return err
	}

	err = VerifyDeploymentUpgrade(client, clusterID, deployment.Namespace, deployment, strconv.Itoa(startingRevision+4), redisImageName, int(*deployment.Spec.Replicas))
	if err != nil {
		return err
	}

	logrus.Debugf("Rollback deployment: %s revision: %v", deployment.Name, startingRevision+3)
	logRollback, err = RollbackDeployment(client, clusterID, deployment.Namespace, deployment.Name, startingRevision+3)
	if err != nil {
		return err
	}
	if logRollback == "" {
		return err
	}

	err = VerifyDeploymentUpgrade(client, clusterID, deployment.Namespace, deployment, strconv.Itoa(startingRevision+5), ubuntuImageName, int(*deployment.Spec.Replicas))
	if err != nil {
		return err
	}

	return err
}

// VerifyDeploymentPodScaleUp verifies deployments can scale up replicas
func VerifyDeploymentPodScaleUp(client *rancher.Client, clusterID, namespace, deploymentName string) error {
	steveclient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	deployment, err := GetDeploymentByName(steveclient, clusterID, namespace, deploymentName)
	if err != nil {
		return err
	}

	pods, err := GetDeploymentPods(client, clusterID, deployment.Namespace, deployment.Name)
	if err != nil {
		return err
	}

	logrus.Debugf("Updating deployment (%s) replicas from %v to %v", deployment.Name, *deployment.Spec.Replicas, *deployment.Spec.Replicas+1)
	replicas := int32(*deployment.Spec.Replicas + 1)
	deployment.Spec.Replicas = &replicas

	deployment, err = UpdateDeployment(client, clusterID, deployment.Namespace, deployment, true)
	if err != nil {
		return err
	}

	err = VerifyDeploymentScale(client, clusterID, deployment.Namespace, deployment, deployment.Spec.Template.Spec.Containers[0].Image, len(pods)+1)
	if err != nil {
		return err
	}

	return err
}

// VerifyDeploymentPodScaleDown verifies deployments can scale down replicas
func VerifyDeploymentPodScaleDown(client *rancher.Client, clusterID, namespace, deploymentName string) error {
	steveclient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	deployment, err := GetDeploymentByName(steveclient, clusterID, namespace, deploymentName)
	if err != nil {
		return err
	}

	pods, err := GetDeploymentPods(client, clusterID, deployment.Namespace, deployment.Name)
	if err != nil {
		return err
	}

	logrus.Debugf("Updating deployment (%s) replicas from %v to %v", deployment.Name, *deployment.Spec.Replicas, *deployment.Spec.Replicas-1)
	replicas := int32(*deployment.Spec.Replicas - 1)
	if replicas < 0 {
		return errors.New("Can't scale down a deployment with 0 replicas")
	}
	deployment.Spec.Replicas = &replicas

	deployment, err = UpdateDeployment(client, clusterID, deployment.Namespace, deployment, true)
	if err != nil {
		return err
	}

	err = VerifyDeploymentScale(client, clusterID, deployment.Namespace, deployment, deployment.Spec.Template.Spec.Containers[0].Image, len(pods)-1)
	if err != nil {
		return err
	}

	return err
}

// VerifyDeploymentOrchestration verfies that pod orchestration updates replicas but not image when pausing
func VerifyDeploymentOrchestration(client *rancher.Client, clusterID, namespace, deploymentName string) error {
	steveclient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	deployment, err := GetDeploymentByName(steveclient, clusterID, namespace, deploymentName)
	if err != nil {
		return err
	}

	scaleReplicas := int32(2)
	deploymentPods, err := GetDeploymentPods(client, clusterID, deployment.Namespace, deployment.Name)
	if err != nil {
		return err
	}

	initialPodCount := len(deploymentPods)

	logrus.Debugf("Pausing orchestration on deployment: %s", deployment.Name)
	deployment.Spec.Paused = true
	deployment, err = UpdateDeployment(client, clusterID, deployment.Namespace, deployment, true)
	if err != nil {
		return err
	}

	err = VerifyOrchestrationStatus(client, clusterID, deployment.Namespace, deployment.Name, true)
	if err != nil {
		return err
	}

	replicas := int32(*deployment.Spec.Replicas + scaleReplicas)
	deployment.Spec.Replicas = &replicas
	containerName := namegen.AppendRandomString("pause-redis-container")
	newContainerTemplate := workloads.NewContainer(containerName,
		redisImageName,
		corev1.PullAlways,
		[]corev1.VolumeMount{},
		[]corev1.EnvFromSource{},
		nil,
		nil,
		nil,
	)
	deployment.Spec.Template.Spec.Containers = []corev1.Container{newContainerTemplate}

	logrus.Debugf("Updating deployment (%s) image: %s replicas: %v", deployment.Name, redisImageName, replicas)
	deployment, err = UpdateDeployment(client, clusterID, deployment.Namespace, deployment, true)
	if err != nil {
		return err
	}

	err = pods.WatchAndWaitPodContainerRunning(client, clusterID, deployment.Namespace)
	if err != nil {
		return err
	}

	logrus.Debug("Verifying that the deployment image was not updated and the replica count was increased")
	deploymentPods, err = GetDeploymentPods(client, clusterID, deployment.Namespace, deployment.Name)
	if err != nil {
		return err
	}

	if initialPodCount+int(scaleReplicas) != len(deploymentPods) {
		err_msg := fmt.Sprintf("expected replica count: %d does not equal pod count: %d", initialPodCount+int(scaleReplicas), len(deploymentPods))
		return errors.New(err_msg)
	}

	logrus.Debug("Resuming orchestration")
	deployment.Spec.Paused = false
	deployment, err = UpdateDeployment(client, clusterID, deployment.Namespace, deployment, true)

	logrus.Debugf("Verifying that the deployment image was updated to %s", redisImageName)
	err = VerifyDeploymentScale(client, clusterID, deployment.Namespace, deployment, redisImageName, int(replicas))
	if err != nil {
		return err
	}

	err = VerifyOrchestrationStatus(client, clusterID, deployment.Namespace, deployment.Name, false)
	if err != nil {
		return err
	}

	deploymentPods, err = GetDeploymentPods(client, clusterID, deployment.Namespace, deployment.Name)
	if err != nil {
		return err
	}

	if int(replicas) != len(deploymentPods) {
		err_msg := fmt.Sprintf("expected replica count: %d does not equal pod count: %d", int(replicas), len(deploymentPods))
		return errors.New(err_msg)
	}

	return err
}

// VerifyClusterDeployments verifies that all required deployments are present and available in the cluster
func VerifyClusterDeployments(client *rancher.Client, cluster *v1.SteveAPIObject) error {
	clusterID, err := clusters.GetClusterIDByName(client, cluster.Name)
	if err != nil {
		return err
	}

	var downstreamClient *v1.Client
	err = kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.FiveMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		downstreamClient, err = client.Steve.ProxyDownstream(clusterID)
		if err != nil {
			return false, nil
		}

		return true, nil
	})

	if downstreamClient == nil {
		return fmt.Errorf("failed to get downstream steve client for cluster (%s)", cluster.Name)
	}

	deploymentClient := downstreamClient.SteveType(stevetypes.Deployment)
	requiredDeployments := []string{ClusterAgent, Webhook, Fleet, SUC}

	logrus.Debugf("Verifying all required deployments exist: %v", requiredDeployments)
	err = kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.FifteenMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		clusterDeployments, err := deploymentClient.List(nil)
		if err != nil {
			return false, nil
		}

		for _, deployment := range clusterDeployments.Data {
			k8sDeployment := &appv1.Deployment{}
			err := steveV1.ConvertToK8sType(deployment.JSONResp, k8sDeployment)
			if err != nil {
				return false, nil
			}

			if slices.Contains(requiredDeployments, k8sDeployment.Name) {
				requiredDeployments = slices.Delete(requiredDeployments, slices.Index(requiredDeployments, k8sDeployment.Name), slices.Index(requiredDeployments, k8sDeployment.Name)+1)
			}
		}
		if len(requiredDeployments) != 0 {
			return false, nil
		}

		return true, nil
	})

	if err != nil {
		return fmt.Errorf("Not all required deployments exist on cluster (%s): %v", cluster.Name, requiredDeployments)
	}

	logrus.Debug("Verifying all deployments")
	var failedDeployments []appv1.Deployment
	err = kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.TenMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		clusterDeployments, err := deploymentClient.List(nil)
		if err != nil {
			return false, nil
		}

		failedDeployments = []appv1.Deployment{}
		for _, deploymentObj := range clusterDeployments.Data {
			k8sDeployment := &appv1.Deployment{}
			err := steveV1.ConvertToK8sType(deploymentObj.JSONResp, k8sDeployment)
			if err != nil {
				return false, nil
			}

			if k8sDeployment.Status.AvailableReplicas != *k8sDeployment.Spec.Replicas {
				failedDeployments = append(failedDeployments, *k8sDeployment)
			}
		}

		if len(failedDeployments) > 0 {
			return false, nil
		}

		return true, nil
	})

	if len(failedDeployments) > 0 {
		for _, deploymentObj := range failedDeployments {

			for _, condition := range deploymentObj.Status.Conditions {
				logrus.Error("Deployment:", deploymentObj.Name, "Condition: ", condition.Message)
			}
		}

		return nil
	}

	return err
}
