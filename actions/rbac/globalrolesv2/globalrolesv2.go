package globalrolesv2

import (
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"

	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
)

const (
	Localcluster        = "local"
	OwnerLabel          = "authz.management.cattle.io/grb-owner"
	Namespace           = "fleet-default"
	LocalPrefix         = "local://"
	ClusterContext      = "cluster"
	ProjectContext      = "project"
	bindingLabel        = "membership-binding-owner"
	GlobalDataNamespace = "cattle-global-data"
	defaultNamespace    = "default"
)

var (
	GlobalRole = v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "",
		},
		InheritedClusterRoles: []string{},
	}

	GlobalRoleBinding = &v3.GlobalRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "",
		},
		GlobalRoleName: "",
		UserName:       "",
	}

	ReadSecretsPolicy = rbacv1.PolicyRule{
		Verbs:     []string{"get", "list", "watch"},
		APIGroups: []string{""},
		Resources: []string{"secrets"},
	}

	ReadCRTBsPolicy = rbacv1.PolicyRule{
		Verbs:     []string{"get", "list", "watch"},
		APIGroups: []string{"management.cattle.io"},
		Resources: []string{"clusterroletemplatebindings"},
	}

	ReadPods = rbacv1.PolicyRule{
		Verbs:     []string{"get", "list", "watch"},
		APIGroups: []string{""},
		Resources: []string{"pods"},
	}

	ReadAllResourcesPolicy = rbacv1.PolicyRule{
		Verbs:     []string{"get", "list", "watch"},
		APIGroups: []string{"*"},
		Resources: []string{"*"},
	}

	Secret = corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: namegen.AppendRandomString("secret-"),
		},
		Data: map[string][]byte{
			"key": []byte(namegen.RandStringLower(5)),
		},
	}
)

func GetGlobalRoleBindingForUser(client *rancher.Client, userID string) (string, error) {
	grblist, err := client.WranglerContext.Mgmt.GlobalRoleBinding().List(metav1.ListOptions{})
	if err != nil {
		return "", err
	}

	for _, grbs := range grblist.Items {
		if grbs.GlobalRoleName == GlobalRole.Name && grbs.UserName == userID {
			return grbs.Name, nil
		}
	}
	return "", nil
}

func GetCRBsForCRTBs(client *rancher.Client, crtbs *v3.ClusterRoleTemplateBindingList) (*rbacv1.ClusterRoleBindingList, error) {
	var downstreamCRBs rbacv1.ClusterRoleBindingList

	for _, crtb := range crtbs.Items {
		labelKey := fmt.Sprintf("%s_%s", crtb.ClusterName, crtb.Name)
		req, err := labels.NewRequirement(labelKey, selection.In, []string{bindingLabel})

		if err != nil {
			return nil, err
		}

		selector := labels.NewSelector().Add(*req)
		downstreamCRBsForCRTB, err := rbacapi.ListClusterRoleBindings(client, Localcluster, metav1.ListOptions{
			LabelSelector: selector.String(),
		})

		if err != nil {
			return nil, err
		}

		downstreamCRBs.Items = append(downstreamCRBs.Items, downstreamCRBsForCRTB.Items...)
	}

	return &downstreamCRBs, nil
}

func GetRBsForCRTBs(client *rancher.Client, crtbs *v3.ClusterRoleTemplateBindingList) (*rbacv1.RoleBindingList, error) {
	var downstreamRBs rbacv1.RoleBindingList

	for _, crtb := range crtbs.Items {
		roleTemplateName := crtb.RoleTemplateName

		if strings.Contains(roleTemplateName, "rt") {
			listOpt := metav1.ListOptions{
				FieldSelector: "metadata.name=" + roleTemplateName,
			}
			roleTemplateList, err := rbacapi.ListRoleTemplates(client, listOpt)
			if err != nil {
				return nil, err
			}
			roleTemplateName = roleTemplateList.Items[0].RoleTemplateNames[0]
		}

		nameSelector := fmt.Sprintf("metadata.name=%s-%s", crtb.Name, roleTemplateName)
		namespaceSelector := fmt.Sprintf("metadata.namespace=%s", crtb.ClusterName)
		combinedSelector := fmt.Sprintf("%s,%s", nameSelector, namespaceSelector)
		downstreamRBsForCRTB, err := rbacapi.ListRoleBindings(client, Localcluster, "", metav1.ListOptions{
			FieldSelector: combinedSelector,
		})

		if err != nil {
			return nil, err
		}

		downstreamRBs.Items = append(downstreamRBs.Items, downstreamRBsForCRTB.Items...)
	}

	return &downstreamRBs, nil
}
