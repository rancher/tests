package webhook

import (
	"context"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	extkubeapi "github.com/rancher/shepherd/extensions/kubeapi"
	extdeploymentsapi "github.com/rancher/shepherd/extensions/kubeapi/workloads/deployments"
	"github.com/rancher/shepherd/pkg/session"
	chartsactions "github.com/rancher/tests/actions/charts"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	RancherWebhookConfigurationName        = "rancher.cattle.io"
	AdmissionRegistrationGroup             = "admissionregistration.k8s.io"
	AdmissionRegistrationVersion           = "v1"
	MutatingWebhookConfigurationResource   = "mutatingwebhookconfigurations"
	ValidatingWebhookConfigurationResource = "validatingwebhookconfigurations"
)

var (
	mutatingWebhookConfigurationGVR = schema.GroupVersionResource{
		Group:    AdmissionRegistrationGroup,
		Version:  AdmissionRegistrationVersion,
		Resource: MutatingWebhookConfigurationResource,
	}
	validatingWebhookConfigurationGVR = schema.GroupVersionResource{
		Group:    AdmissionRegistrationGroup,
		Version:  AdmissionRegistrationVersion,
		Resource: ValidatingWebhookConfigurationResource,
	}
)

// ScaleRancherWebhook scales the rancher-webhook deployment on the local cluster to the given replica count
func ScaleRancherWebhook(client *rancher.Client, clusterID string, replicas int32) error {
	deployment, err := extdeploymentsapi.GetDeploymentByName(client, clusterID, chartsactions.RancherWebhookNamespace, chartsactions.RancherWebhookName)
	if err != nil {
		return fmt.Errorf("failed to get %s deployment: %w", chartsactions.RancherWebhookName, err)
	}

	deployment.Spec.Replicas = &replicas

	_, err = extdeploymentsapi.UpdateDeployment(client, clusterID, deployment, false)
	if err != nil {
		return fmt.Errorf("failed to scale %s deployment to %d replicas: %w", chartsactions.RancherWebhookName, replicas, err)
	}

	deployment, err = extdeploymentsapi.GetDeploymentByName(client, clusterID, chartsactions.RancherWebhookNamespace, chartsactions.RancherWebhookName)
	if err != nil {
		return fmt.Errorf("failed to get %s deployment: %w", chartsactions.RancherWebhookName, err)
	}

	if *deployment.Spec.Replicas != replicas {
		return fmt.Errorf("failed to scale %s deployment to %d replicas", chartsactions.RancherWebhookName, replicas)
	}

	return nil
}

// DeleteWebhookConfigurations removes the mutating and validating webhook configurations so admission requests are no longer routed to rancher-webhook
func DeleteWebhookConfigurations(client *rancher.Client, clusterID string) error {
	mutatingWebhookConfigurations, err := extkubeapi.ResourceForClient(client, clusterID, "", mutatingWebhookConfigurationGVR)
	if err != nil {
		return fmt.Errorf("failed to get a resource client for mutating webhook configurations: %w", err)
	}

	if err := mutatingWebhookConfigurations.Delete(context.TODO(), RancherWebhookConfigurationName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete mutating webhook configuration %s: %w", RancherWebhookConfigurationName, err)
	}

	validatingWebhookConfigurations, err := extkubeapi.ResourceForClient(client, clusterID, "", validatingWebhookConfigurationGVR)
	if err != nil {
		return fmt.Errorf("failed to get a resource client for validating webhook configurations: %w", err)
	}

	if err := validatingWebhookConfigurations.Delete(context.TODO(), RancherWebhookConfigurationName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete validating webhook configuration %s: %w", RancherWebhookConfigurationName, err)
	}

	return nil
}

// SimulateRancherWebhookDown backs up the mutating and validating webhook configurations, scales rancher-webhook to 0 replicas, and removes the configurations so admission requests are no longer routed to rancher-webhook.
func SimulateRancherWebhookDown(client *rancher.Client, clusterID string) (mutatingBackup, validatingBackup *unstructured.Unstructured, err error) {
	mutatingBackup, validatingBackup, err = BackupWebhookConfigurations(client, clusterID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to back up webhook configurations: %w", err)
	}

	if err := ScaleRancherWebhook(client, clusterID, 0); err != nil {
		return nil, nil, err
	}

	if err := DeleteWebhookConfigurations(client, clusterID); err != nil {
		return nil, nil, err
	}

	return mutatingBackup, validatingBackup, nil
}

// RestoreRancherWebhook scales rancher-webhook back up and reapplies the mutating and validating webhook configurations captured by SimulateRancherWebhookDown.
func RestoreRancherWebhook(client *rancher.Client, clusterID string, mutatingBackup, validatingBackup *unstructured.Unstructured) error {
	if mutatingBackup == nil || validatingBackup == nil {
		return fmt.Errorf("no webhook configuration backup to restore")
	}

	if err := ScaleRancherWebhook(client, clusterID, 1); err != nil {
		return err
	}

	if err := extdeploymentsapi.WaitForDeploymentActive(client, clusterID, chartsactions.RancherWebhookNamespace, chartsactions.RancherWebhookName); err != nil {
		return fmt.Errorf("timed out waiting for the %s deployment to become active: %w", chartsactions.RancherWebhookName, err)
	}

	restoreClient := *client
	restoreClient.Session = session.NewSession()

	mutatingWebhookConfigurations, err := extkubeapi.ResourceForClient(&restoreClient, clusterID, "", mutatingWebhookConfigurationGVR)
	if err != nil {
		return fmt.Errorf("failed to get a resource client for mutating webhook configurations: %w", err)
	}

	if err := recreate(mutatingWebhookConfigurations, mutatingBackup); err != nil {
		return fmt.Errorf("failed to restore mutating webhook configuration %s: %w", RancherWebhookConfigurationName, err)
	}

	validatingWebhookConfigurations, err := extkubeapi.ResourceForClient(&restoreClient, clusterID, "", validatingWebhookConfigurationGVR)
	if err != nil {
		return fmt.Errorf("failed to get a resource client for validating webhook configurations: %w", err)
	}

	if err := recreate(validatingWebhookConfigurations, validatingBackup); err != nil {
		return fmt.Errorf("failed to restore validating webhook configuration %s: %w", RancherWebhookConfigurationName, err)
	}

	return nil
}

// BackupWebhookConfigurations fetches and deep-copies the mutating and validating webhook configurations so they can be reapplied later
func BackupWebhookConfigurations(client *rancher.Client, clusterID string) (mutating, validating *unstructured.Unstructured, err error) {
	mutatingWebhookConfigurations, err := extkubeapi.ResourceForClient(client, clusterID, "", mutatingWebhookConfigurationGVR)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get a resource client for mutating webhook configurations: %w", err)
	}

	mutating, err = mutatingWebhookConfigurations.Get(context.TODO(), RancherWebhookConfigurationName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get mutating webhook configuration %s: %w", RancherWebhookConfigurationName, err)
	}

	validatingWebhookConfigurations, err := extkubeapi.ResourceForClient(client, clusterID, "", validatingWebhookConfigurationGVR)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get a resource client for validating webhook configurations: %w", err)
	}

	validating, err = validatingWebhookConfigurations.Get(context.TODO(), RancherWebhookConfigurationName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get validating webhook configuration %s: %w", RancherWebhookConfigurationName, err)
	}

	return mutating.DeepCopy(), validating.DeepCopy(), nil
}

func recreate(resourceClient dynamic.ResourceInterface, obj *unstructured.Unstructured) error {
	restored := obj.DeepCopy()
	restored.SetResourceVersion("")
	restored.SetUID("")
	restored.SetCreationTimestamp(metav1.Time{})
	restored.SetGeneration(0)
	restored.SetManagedFields(nil)
	unstructured.RemoveNestedField(restored.Object, "metadata", "selfLink")

	if _, err := resourceClient.Create(context.TODO(), restored, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	return nil
}
