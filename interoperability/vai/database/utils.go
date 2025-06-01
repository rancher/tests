package database

import (
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
)

// ListRancherPods returns a list of Rancher pod names
func ListRancherPods(client *rancher.Client) ([]string, error) {
	podList, err := client.Steve.SteveType("pod").NamespacedSteveClient("cattle-system").List(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	var rancherPodNames []string
	for _, pod := range podList.Data {
		if pod.Labels["app"] == "rancher" && !strings.Contains(pod.Name, "webhook") {
			rancherPodNames = append(rancherPodNames, pod.Name)
		}
	}

	return rancherPodNames, nil
}
