package globalrolesv2

import (
	"context"
	"fmt"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"

	"github.com/rancher/shepherd/pkg/config"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/kubeapi/namespaces"
	"github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/rbac"

	"github.com/rancher/shepherd/extensions/users"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	localcluster        = "local"
	namespace           = "fleet-default"
	localPrefix         = "local://"
	clusterContext      = "cluster"
	projectContext      = "project"
	globalDataNamespace = "cattle-global-data"
)

var (
	readSecretsPolicy = rbacv1.PolicyRule{
		Verbs:     []string{"get", "list", "watch"},
		APIGroups: []string{""},
		Resources: []string{"secrets"},
	}

	readCRTBsPolicy = rbacv1.PolicyRule{
		Verbs:     []string{"get", "list", "watch"},
		APIGroups: []string{"management.cattle.io"},
		Resources: []string{"clusterroletemplatebindings"},
	}

	readPods = rbacv1.PolicyRule{
		Verbs:     []string{"get", "list", "watch"},
		APIGroups: []string{""},
		Resources: []string{"pods"},
	}

	readAllResourcesPolicy = rbacv1.PolicyRule{
		Verbs:     []string{"get", "list", "watch"},
		APIGroups: []string{"*"},
		Resources: []string{"*"},
	}
)

func getGlobalRoleBindingForUserWrangler(client *rancher.Client, grName, userID string) (string, error) {
	grblist, err := client.WranglerContext.Mgmt.GlobalRoleBinding().List(metav1.ListOptions{})
	if err != nil {
		return "", err
	}

	for _, grbs := range grblist.Items {
		if grbs.GlobalRoleName == grName && grbs.UserName == userID {
			return grbs.Name, nil
		}
	}
	return "", nil
}

func createDownstreamCluster(client *rancher.Client, clusterType string) (*management.Cluster, *v1.SteveAPIObject, *clusters.ClusterConfig, error) {
	provisioningConfig := new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, provisioningConfig)

	awsEC2Configs := new(ec2.AWSEC2Configs)
	config.LoadConfig(ec2.ConfigurationFileKey, awsEC2Configs)

	nodeProviders := provisioningConfig.NodeProviders[0]
	externalNodeProvider := provisioning.ExternalNodeProviderSetup(nodeProviders)
	testClusterConfig := clusters.ConvertConfigToClusterConfig(provisioningConfig)
	testClusterConfig.CNI = provisioningConfig.CNIs[0]

	var clusterObject *management.Cluster
	var steveObject *v1.SteveAPIObject
	var err error

	switch clusterType {
	case "K3S":
		nodeAndRoles := []provisioninginput.MachinePools{
			provisioninginput.AllRolesMachinePool,
		}
		testClusterConfig.MachinePools = nodeAndRoles
		testClusterConfig.KubernetesVersion = provisioningConfig.K3SKubernetesVersions[0]
		steveObject, err = provisioning.CreateProvisioningCustomCluster(client, &externalNodeProvider, testClusterConfig, awsEC2Configs)
	case "RKE2":
		nodeAndRoles := []provisioninginput.MachinePools{
			provisioninginput.AllRolesMachinePool,
		}
		testClusterConfig.MachinePools = nodeAndRoles
		testClusterConfig.KubernetesVersion = provisioningConfig.RKE2KubernetesVersions[0]
		steveObject, err = provisioning.CreateProvisioningCustomCluster(client, &externalNodeProvider, testClusterConfig, awsEC2Configs)
	default:
		return nil, nil, nil, fmt.Errorf("unsupported cluster type: %s", clusterType)
	}

	if err != nil {
		return nil, nil, nil, err
	}

	return clusterObject, steveObject, testClusterConfig, nil
}

func createGlobalRoleAndUser(client *rancher.Client, inheritedClusterrole []string) (*management.User, *v3.GlobalRole, error) {
	gr, err := rbacapi.CreateGlobalRoleWithInheritedClusterRolesWrangler(client, inheritedClusterrole)
	if err != nil {
		return nil, nil, err
	}

	createdUser, err := users.CreateUserWithRole(client, users.UserConfig(), rbac.StandardUser.String(), gr.Name)
	if err != nil {
		return nil, nil, err
	}

	return createdUser, gr, err
}

func crtbStatus(client *rancher.Client, crtbName string, selector labels.Selector) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaults.TwoMinuteTimeout)
	defer cancel()

	err := kwait.PollUntilContextCancel(ctx, defaults.FiveHundredMillisecondTimeout, false, func(ctx context.Context) (done bool, err error) {
		crtbs, err := rbacapi.ListClusterRoleTemplateBindings(client, metav1.ListOptions{
			LabelSelector: selector.String(),
		})
		if err != nil {
			return false, err
		}

		for _, newcrtb := range crtbs.Items {
			if crtbName == newcrtb.Name {
				return false, nil
			}
		}
		return true, nil
	})

	return err
}

func createGlobalRoleWithNamespacedRules(client *rancher.Client, namespacedRules map[string][]rbacv1.PolicyRule) (*v3.GlobalRole, error) {
	gr := v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: namegen.AppendRandomString("test-nsr"),
		},
		InheritedClusterRoles: []string{},
		NamespacedRules:       namespacedRules,
	}
	createdGR, err := rbacapi.CreateGlobalRole(client, &gr)
	if err != nil {
		return nil, err
	}
	return createdGR, nil
}

func createProjectAndAddANamespace(client *rancher.Client, nsPrefix string) (string, error) {
	project := projects.NewProjectTemplate(localcluster)
	customProject, err := client.WranglerContext.Mgmt.Project().Create(project)
	if err != nil {
		return "", err
	}
	customNS1, err := namespaces.CreateNamespaceUsingWrangler(client, localcluster, customProject.Name, nil)
	return customNS1.Name, err
}
