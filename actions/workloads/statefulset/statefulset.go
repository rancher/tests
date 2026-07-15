package statefulset

import (
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	appv1 "k8s.io/api/apps/v1"
)

// CreateStatefulSetFromConfig creates a Pod from a config using steve
func CreateStatefulSetFromConfig(client *v1.Client, clusterID string, statefulSet *appv1.StatefulSet) (*appv1.StatefulSet, error) {
	statefulSetResp, err := client.SteveType("apps.statefulset").Create(statefulSet)
	if err != nil {
		return nil, err
	}

	newStatefulSet := new(appv1.StatefulSet)
	err = v1.ConvertToK8sType(statefulSetResp.JSONResp, newStatefulSet)
	if err != nil {
		return nil, err
	}

	return newStatefulSet, nil
}
