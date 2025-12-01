package deployment

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	apisV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/stevestates"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/kubectl"
	"github.com/rancher/shepherd/extensions/workloads"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/wrangler"
	"github.com/rancher/tests/actions/kubeapi/workloads/deployments"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	active              = "active"
	defaultNamespace    = "default"
	port                = "port"
	DeploymentSteveType = "apps.deployment"
	nginxImageName      = "nginx"
	ubuntuImageName     = "ubuntu"
	redisImageName      = "redis"
	podSteveType        = "pod"
	restartAnnotation   = "kubectl.kubernetes.io/restartedAt"
)

// CreateDeployment is a helper to create a deployment with or without a secret/configmap
func CreateDeployment(client *rancher.Client, clusterID, namespaceName string, replicaCount int, secretName, configMapName string, useEnvVars, useVolumes, isRegistrySecret, watchDeployment bool) (*appv1.Deployment, error) {
	deploymentName := namegen.AppendRandomString("testdeployment")
	containerName := namegen.AppendRandomString("testcontainer")
	pullPolicy := corev1.PullAlways
	replicas := int32(replicaCount)

	var podTemplate corev1.PodTemplateSpec

	if secretName != "" || configMapName != "" {
		if isRegistrySecret {
			podTemplate = pods.NewPodTemplateWithConfig(secretName, configMapName, useEnvVars, useVolumes)
			podTemplate.Spec.ImagePullSecrets = append(podTemplate.Spec.ImagePullSecrets, corev1.LocalObjectReference{Name: secretName})
		} else {
			podTemplate = pods.NewPodTemplateWithConfig(secretName, configMapName, useEnvVars, useVolumes)
		}
	} else {
		containerTemplate := workloads.NewContainer(
			containerName,
			nginxImageName,
			pullPolicy,
			[]corev1.VolumeMount{},
			[]corev1.EnvFromSource{},
			nil,
			nil,
			nil,
		)
		podTemplate = workloads.NewPodTemplate(
			[]corev1.Container{containerTemplate},
			[]corev1.Volume{},
			[]corev1.LocalObjectReference{},
			nil,
			nil,
		)
	}

	createdDeployment, err := deployments.CreateDeployment(client, clusterID, deploymentName, namespaceName, podTemplate, replicas)
	if err != nil {
		return nil, err
	}

	if watchDeployment {
		err = charts.WatchAndWaitDeployments(client, clusterID, namespaceName, metav1.ListOptions{
			FieldSelector: "metadata.name=" + createdDeployment.Name,
		})
	}

	return createdDeployment, err
}

// UpdateDeployment is a helper to update deployments
func UpdateDeployment(client *rancher.Client, clusterID, namespaceName string, deployment *appv1.Deployment, watchDeployment bool) (*appv1.Deployment, error) {
	var wranglerContext *wrangler.Context
	var err error

	wranglerContext = client.WranglerContext
	if clusterID != "local" {
		wranglerContext, err = client.WranglerContext.DownStreamClusterWranglerContext(clusterID)
		if err != nil {
			return nil, err
		}
	}

	latestDeployment, err := wranglerContext.Apps.Deployment().Get(namespaceName, deployment.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	deployment.ResourceVersion = latestDeployment.ResourceVersion

	updatedDeployment, err := wranglerContext.Apps.Deployment().Update(deployment)
	if err != nil {
		return nil, err
	}

	if watchDeployment {
		err = charts.WatchAndWaitDeployments(client, clusterID, namespaceName, metav1.ListOptions{
			FieldSelector: "metadata.name=" + updatedDeployment.Name,
		})
	}

	return updatedDeployment, err
}

// DeleteDeployment is a helper to delete a deployment
func DeleteDeployment(client *rancher.Client, clusterID string, deployment *appv1.Deployment) error {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	deploymentID := deployment.Namespace + "/" + deployment.Name
	deploymentResp, err := steveClient.SteveType(DeploymentSteveType).ByID(deploymentID)
	if err != nil {
		return err
	}

	err = steveClient.SteveType(DeploymentSteveType).Delete(deploymentResp)
	if err != nil {
		return err
	}

	return nil
}

func RollbackDeployment(client *rancher.Client, clusterID, namespaceName string, deploymentName string, revision int) (string, error) {
	steveclient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return "", err
	}

	namespaceClient := steveclient.SteveType(podSteveType).NamespacedSteveClient(namespaceName)
	podsResp, err := namespaceClient.List(nil)
	if err != nil {
		return "", err
	}

	//Collect the pod IDs that are expected to be deleted after the rollback
	expectBeDeletedIds := []string{}
	for _, podResp := range podsResp.Data {
		expectBeDeletedIds = append(expectBeDeletedIds, podResp.ID)
	}

	//Execute the roolback
	deploymentCmd := fmt.Sprintf("deployment.apps/%s", deploymentName)
	revisionCmd := fmt.Sprintf("--to-revision=%s", strconv.Itoa(revision))
	execCmd := []string{"kubectl", "rollout", "undo", "-n", namespaceName, deploymentCmd, revisionCmd}
	logCmd, err := kubectl.Command(client, nil, clusterID, execCmd, "")
	if err != nil {
		return "", err
	}

	backoff := kwait.Backoff{
		Duration: 5 * time.Second,
		Factor:   1,
		Jitter:   0,
		Steps:    10,
	}

	//Waiting for all expectedToBeDeletedIds to be deleted
	err = kwait.ExponentialBackoff(backoff, func() (finished bool, err error) {
		for _, id := range expectBeDeletedIds {
			//If the expected delete ID doesn't exist, it should be ignored
			podResp, err := namespaceClient.ByID(id)
			if err != nil && strings.Contains(err.Error(), "404 Not Found") {
				continue
			}
			if err != nil {
				return false, err
			}
			if podResp != nil {
				return false, nil
			}
		}
		return true, nil
	})

	return logCmd, err
}

// UpdateOrRemoveEnvVarForDeployment is a helper to add, update or remove an environment variable in a deployment
func UpdateOrRemoveEnvVarForDeployment(client *rancher.Client, namespaceName, deploymentName, envVarName, envVarValue string) error {
	deploymentObj, err := client.WranglerContext.Apps.Deployment().Get(namespaceName, deploymentName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error fetching deployment %s in namespace %s: %w", deploymentName, namespaceName, err)
	}

	modifiedDeployment := deploymentObj.DeepCopy()
	for i := range modifiedDeployment.Spec.Template.Spec.Containers {
		container := &modifiedDeployment.Spec.Template.Spec.Containers[i]
		var envVarExists bool

		for j := 0; j < len(container.Env); j++ {
			if container.Env[j].Name == envVarName {
				envVarExists = true
				if envVarValue == "" {
					container.Env = append(container.Env[:j], container.Env[j+1:]...)
					j--
				} else {
					container.Env[j].Value = envVarValue
				}
				break
			}
		}

		if !envVarExists && envVarValue != "" {
			container.Env = append(container.Env, corev1.EnvVar{
				Name:  envVarName,
				Value: envVarValue,
			})
		}
	}

	_, err = UpdateDeployment(client, rbac.LocalCluster, namespaceName, modifiedDeployment, true)
	if err != nil {
		return fmt.Errorf("error updating deployment %s in namespace %s: %w", deploymentName, namespaceName, err)
	}

	updatedDeployment, err := client.WranglerContext.Apps.Deployment().Get(namespaceName, deploymentName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error fetching updated deployment %s in namespace %s: %w", deploymentName, namespaceName, err)
	}

	for _, container := range updatedDeployment.Spec.Template.Spec.Containers {
		var envVarFound bool
		for _, env := range container.Env {
			if env.Name == envVarName {
				envVarFound = true
				if envVarValue == "" {
					return fmt.Errorf("environment variable %s was not removed", envVarName)
				} else if env.Value != envVarValue {
					return fmt.Errorf("environment variable %s has incorrect value; expected: %s, got: %s", envVarName, envVarValue, env.Value)
				}
				break
			}
		}

		if envVarValue == "" && envVarFound {
			return fmt.Errorf("environment variable %s should have been removed but is still present", envVarName)
		}

		if envVarValue != "" && !envVarFound {
			return fmt.Errorf("environment variable %s should have been added or updated but was not found", envVarName)
		}
	}

	return nil
}

// RestartDeployment triggers a rollout restart of a deployment by updating an annotation
func RestartDeployment(client *rancher.Client, clusterID, namespaceName, deploymentName string) error {
	deploymentObj, err := client.WranglerContext.Apps.Deployment().Get(namespaceName, deploymentName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error fetching deployment %s in namespace %s: %w", deploymentName, namespaceName, err)
	}

	if deploymentObj.Spec.Template.Annotations == nil {
		deploymentObj.Spec.Template.Annotations = map[string]string{}
	}

	deploymentObj.Spec.Template.Annotations[restartAnnotation] = time.Now().Format(time.RFC3339)

	_, err = UpdateDeployment(client, clusterID, namespaceName, deploymentObj, true)
	if err != nil {
		return fmt.Errorf("error waiting for deployment %s in namespace %s to restart: %w", deploymentName, namespaceName, err)
	}

	return nil
}

func WaitForDeploymentUpdate(client *rancher.Client, clusterID, deploymentNamespace, deploymentName string) error {
	cluster, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(clusterID)

	status := &apisV1.ClusterStatus{}
	err = steveV1.ConvertToK8sType(cluster.Status, status)
	if err != nil {
		return err
	}

	downstreamClient, err := client.Steve.ProxyDownstream(status.ClusterName)
	if err != nil {
		return err
	}

	deploymentClient := downstreamClient.SteveType(stevetypes.Deployment)

	logrus.Debugf("Waiting for %s deployment to be updated", deploymentName)
	var deployment *appv1.Deployment
	err = kwait.PollUntilContextTimeout(context.TODO(), 100*time.Millisecond, defaults.TwoMinuteTimeout, true, func(context.Context) (done bool, err error) {
		autoscalerDeployment, err := deploymentClient.ByID(deploymentNamespace + "/" + deploymentName)
		if err != nil {
			return false, nil
		}

		deployment = &appv1.Deployment{}
		err = steveV1.ConvertToK8sType(autoscalerDeployment.JSONResp, deployment)
		if autoscalerDeployment.State.Name == stevestates.Updating {
			return true, nil
		}

		return false, nil
	})

	return err
}
