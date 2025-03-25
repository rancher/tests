package globalrolesv2

import (
	"github.com/rancher/shepherd/clients/rancher"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"

	"github.com/rancher/shepherd/extensions/defaults"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

func ListClusterRoleTemplateBindingsForInheritedClusterRoles(client *rancher.Client, grbOwner string, expectedCount int) (*v3.ClusterRoleTemplateBindingList, error) {
	req, err := labels.NewRequirement(OwnerLabel, selection.In, []string{grbOwner})

	if err != nil {
		return nil, err
	}

	selector := labels.NewSelector().Add(*req)

	var crtbs *v3.ClusterRoleTemplateBindingList

	err = kwait.Poll(defaults.FiveHundredMillisecondTimeout, defaults.OneMinuteTimeout, func() (done bool, pollErr error) {
		crtbs, pollErr = rbacapi.ListClusterRoleTemplateBindings(client, metav1.ListOptions{
			LabelSelector: selector.String(),
		})
		if pollErr != nil {
			return false, pollErr
		}
		if len(crtbs.Items) == expectedCount {
			return true, nil
		}
		return false, nil
	})

	if err != nil {
		return nil, err
	}

	return crtbs, nil
}
