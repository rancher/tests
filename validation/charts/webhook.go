package charts

import (
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/kubeapi/webhook"
	"github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/rancher/tests/actions/charts"
	corev1 "k8s.io/api/core/v1"
)

const (
	resourceName = "rancher.cattle.io"
	admin        = "admin"
	localCluster = "local"
)

func getWebhookNames(client *rancher.Client, clusterID, resourceName string) ([]string, error) {
	webhookList, err := webhook.GetWebhook(client, clusterID, resourceName)
	if err != nil {
		return nil, err
	}

	var webhookL []string
	for _, webhook := range webhookList.Webhooks {
		webhookL = append(webhookL, webhook.Name)
	}

	return webhookL, nil

}

func validateWebhookPodLogs(podLogs string) interface{} {

	delimiter := "\n"
	segments := strings.Split(podLogs, delimiter)

	for _, segment := range segments {
		if strings.Contains(segment, "level=error") {
			return "Error logs in webhook" + segment
		}
	}
	return nil
}

func getWebhookPodSpec(client *rancher.Client, clusterID string) (*corev1.PodSpec, string, error) {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return nil, "", err
	}

	podList, err := steveClient.SteveType(pods.PodResourceSteveType).NamespacedSteveClient(charts.RancherWebhookNamespace).List(nil)
	if err != nil {
		return nil, "", err
	}

	for _, pod := range podList.Data {
		if strings.Contains(pod.Name, charts.RancherWebhookName) {
			podSpec := &corev1.PodSpec{}
			if err := v1.ConvertToK8sType(pod.Spec, podSpec); err != nil {
				return nil, "", err
			}

			return podSpec, pod.Name, nil
		}
	}

	return nil, "", fmt.Errorf("webhook pod not found in namespace %s", charts.RancherWebhookNamespace)
}

func validateWebhookPodSecurityContext(podSpec *corev1.PodSpec) error {
	if len(podSpec.Containers) == 0 {
		return fmt.Errorf("webhook pod has no containers")
	}

	securityContext := podSpec.Containers[0].SecurityContext
	if securityContext == nil {
		return fmt.Errorf("webhook container securityContext is not set")
	}

	if securityContext.AllowPrivilegeEscalation == nil {
		return fmt.Errorf("webhook container allowPrivilegeEscalation is not set")
	}

	if *securityContext.AllowPrivilegeEscalation {
		return fmt.Errorf("webhook container allowPrivilegeEscalation is %t, expected false", *securityContext.AllowPrivilegeEscalation)
	}

	if securityContext.ReadOnlyRootFilesystem == nil {
		return fmt.Errorf("webhook container readOnlyRootFilesystem is not set")
	}

	if !*securityContext.ReadOnlyRootFilesystem {
		return fmt.Errorf("webhook container readOnlyRootFilesystem is %t, expected true", *securityContext.ReadOnlyRootFilesystem)
	}

	return nil
}
