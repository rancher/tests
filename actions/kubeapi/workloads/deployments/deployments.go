package deployments

import (
	"fmt"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	extdeploymentsapi "github.com/rancher/shepherd/extensions/kubeapi/workloads/deployments"
	"github.com/rancher/shepherd/extensions/workloads"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	podsapi "github.com/rancher/tests/actions/kubeapi/workloads/pods"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	NginxImageName             = "nginx"
	RestartAnnotation          = "kubectl.kubernetes.io/restartedAt"
	RancherDeploymentName      = "rancher"
	RancherDeploymentNamespace = "cattle-system"
)

// CreateDeploymentFromPodTemplate creates a deployment in the specified cluster and namespace using the provided pod template. If waitForActive is true, it will wait for the deployment to be active before returning.
func CreateDeploymentFromPodTemplate(client *rancher.Client, clusterID, deploymentName, namespaceName string, podTemplate corev1.PodTemplateSpec, replicaCount int, waitForActive bool) (*appv1.Deployment, error) {
	replicas := int32(replicaCount)

	selectorLabels := map[string]string{
		"workload.user.cattle.io/workloadselector": fmt.Sprintf("apps.deployment-%v-%v", namespaceName, deploymentName),
	}

	// Ensure pod template has matching labels
	if podTemplate.ObjectMeta.Labels == nil {
		podTemplate.ObjectMeta.Labels = make(map[string]string)
	}
	for k, v := range selectorLabels {
		podTemplate.ObjectMeta.Labels[k] = v
	}

	deploymentTemplate := &appv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespaceName,
		},
		Spec: appv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabels,
			},
			Template: podTemplate,
		},
	}

	createdDeployment, err := extdeploymentsapi.CreateDeploymentWithTemplate(client, clusterID, deploymentTemplate, waitForActive)
	if err != nil {
		return nil, err
	}

	return createdDeployment, nil
}

// CreateDeployment is a helper to create a deployment with or without a secret/configmap. If waitForActive is true, it will wait for the deployment to be active after creation.
func CreateDeployment(client *rancher.Client, clusterID, namespaceName string, imageName string, replicaCount int, secretName, configMapName string, useEnvVars, useVolumes, isRegistrySecret, waitForActive bool) (*appv1.Deployment, error) {
	deploymentName := namegen.AppendRandomString("testdeployment")
	containerName := namegen.AppendRandomString("testcontainer")
	pullPolicy := corev1.PullAlways

	if imageName == "" {
		imageName = NginxImageName
	}

	var podTemplate corev1.PodTemplateSpec

	if secretName != "" || configMapName != "" {
		if isRegistrySecret {
			podTemplate = podsapi.NewPodTemplateWithConfig(imageName, secretName, configMapName, useEnvVars, useVolumes)
			podTemplate.Spec.ImagePullSecrets = append(podTemplate.Spec.ImagePullSecrets, corev1.LocalObjectReference{Name: secretName})
		} else {
			podTemplate = podsapi.NewPodTemplateWithConfig(imageName, secretName, configMapName, useEnvVars, useVolumes)
		}
	} else {
		containerTemplate := workloads.NewContainer(
			containerName,
			imageName,
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

	podTemplate.Spec.RestartPolicy = corev1.RestartPolicyAlways

	createdDeployment, err := CreateDeploymentFromPodTemplate(client, clusterID, deploymentName, namespaceName, podTemplate, replicaCount, waitForActive)
	if err != nil {
		return nil, err
	}

	return createdDeployment, nil
}

// RestartDeployment triggers a rollout restart of a deployment by updating an annotation
func RestartDeployment(client *rancher.Client, clusterID, namespaceName, deploymentName string) error {
	deploymentObj, err := extdeploymentsapi.GetDeploymentByName(client, clusterID, namespaceName, deploymentName)
	if err != nil {
		return fmt.Errorf("error fetching deployment %s in namespace %s: %w", deploymentName, namespaceName, err)
	}

	if deploymentObj.Spec.Template.Annotations == nil {
		deploymentObj.Spec.Template.Annotations = map[string]string{}
	}

	deploymentObj.Spec.Template.Annotations[RestartAnnotation] = time.Now().Format(time.RFC3339)

	_, err = extdeploymentsapi.UpdateDeployment(client, clusterID, deploymentObj, true)
	if err != nil {
		return fmt.Errorf("error restarting deployment %s in namespace %s: %w", deploymentName, namespaceName, err)
	}

	return nil
}

// GetLatestStatusMessageFromDeployment retrieves the latest status message and reason from a deployment for a given status type.
func GetLatestStatusMessageFromDeployment(deployment *appv1.Deployment, messageType string) (string, string, error) {
	latestTime := time.Time{}
	latestMessage := ""
	latestReason := ""

	targetMessageType := appv1.DeploymentConditionType(messageType)

	for _, condition := range deployment.Status.Conditions {
		if condition.Type == targetMessageType && condition.LastUpdateTime.After(latestTime) {
			latestMessage = condition.Message
			latestReason = condition.Reason
			latestTime = condition.LastUpdateTime.Time
		}
	}

	return latestMessage, latestReason, nil
}

// UpdateOrRemoveEnvVarForDeployment is a helper to add, update or remove an environment variable in a deployment
func UpdateOrRemoveEnvVarForDeployment(client *rancher.Client, clusterID, namespaceName, deploymentName, envVarName, envVarValue string) error {
	deploymentObj, err := extdeploymentsapi.GetDeploymentByName(client, clusterID, namespaceName, deploymentName)
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

	_, err = extdeploymentsapi.UpdateDeployment(client, clusterID, modifiedDeployment, true)
	if err != nil {
		return fmt.Errorf("error updating deployment %s in namespace %s: %w", deploymentName, namespaceName, err)
	}

	updatedDeployment, err := extdeploymentsapi.GetDeploymentByName(client, clusterID, namespaceName, deploymentName)
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
