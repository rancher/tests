package daemonset

import (
	"strings"

	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	appv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const workloadSelectorLabel = "workload.user.cattle.io/workloadselector"

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

// SetUniqueSelector gives the daemonset a workload selector label that no other daemonset shares
func SetUniqueSelector(daemonset *appv1.DaemonSet) {
	value := namegen.AppendRandomString(strings.TrimSuffix(daemonset.GenerateName, "-"))

	if daemonset.Spec.Selector == nil {
		daemonset.Spec.Selector = &metav1.LabelSelector{}
	}
	if daemonset.Spec.Selector.MatchLabels == nil {
		daemonset.Spec.Selector.MatchLabels = map[string]string{}
	}
	if daemonset.Spec.Template.Labels == nil {
		daemonset.Spec.Template.Labels = map[string]string{}
	}

	daemonset.Spec.Selector.MatchLabels[workloadSelectorLabel] = value
	daemonset.Spec.Template.Labels[workloadSelectorLabel] = value
}
