package daemonset

import (
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	appv1 "k8s.io/api/apps/v1"
)

// CreateDaemonSetFromConfig creates a daemonset from a config using steve
func CreateDaemonSetFromConfig(client *v1.Client, clusterID string, daemonset *appv1.DaemonSet) (*appv1.DaemonSet, error) {
	daemonsetResp, err := client.SteveType("apps.daemonset").Create(daemonset)
	if err != nil {
		return nil, err
	}

	newDaemonSet := new(appv1.DaemonSet)
	err = v1.ConvertToK8sType(daemonsetResp.JSONResp, newDaemonSet)
	if err != nil {
		return nil, err
	}

	return newDaemonSet, nil
}
