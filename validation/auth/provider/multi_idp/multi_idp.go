package multiidp

import (
	"fmt"
	"strings"

	managementv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	authactions "github.com/rancher/tests/actions/auth"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const minimumInstances = 2

// LoadInstances reads the OpenLDAP instances from the config and verifies each can be enabled and signed into
func LoadInstances() ([]authactions.NamedOpenLdapInstance, error) {
	instances := authactions.LoadMultiIdpInput().OpenLdap
	if len(instances) < minimumInstances {
		return nil, fmt.Errorf("%s needs at least %d openLdap instances, got %d", authactions.MultiIdpAuthInput, minimumInstances, len(instances))
	}

	seen := map[string]bool{}

	for i := range instances {
		if err := instances[i].Validate(); err != nil {
			return nil, err
		}

		if seen[instances[i].ConfigName] {
			return nil, fmt.Errorf("configName %q is used by more than one instance", instances[i].ConfigName)
		}

		seen[instances[i].ConfigName] = true
	}

	return instances, nil
}

// TearDownInstances disables and deletes every instance, after the test session has run its own cleanup
func TearDownInstances(client *rancher.Client, instances []authactions.NamedOpenLdapInstance) []error {
	var errs []error

	for i := range instances {
		if err := authactions.DeleteNamedOpenLdap(client, &instances[i]); err != nil {
			errs = append(errs, err)
		}
	}

	return errs
}

// CannotListClustersMessage is the Steve error for a user without GET on the provisioning cluster type
const CannotListClustersMessage = "Resource type [provisioning.cattle.io.cluster] has no method GET"

// LoginNamedUser signs a user in through the instance's auth config
func LoginNamedUser(authClient *rancher.Client, instance *authactions.NamedOpenLdapInstance, userInfo authactions.User) (*rancher.Client, error) {
	return authactions.LoginAsNamedAuthUser(authClient, &v3.User{Username: userInfo.Username, Password: userInfo.Password}, instance.ConfigName)
}

// VerifyCannotListClusters returns an error unless the user is denied listing clusters
func VerifyCannotListClusters(userClient *rancher.Client) error {
	_, err := userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
	if err == nil {
		return fmt.Errorf("user should not be able to list clusters")
	}

	if !strings.Contains(err.Error(), CannotListClustersMessage) {
		return fmt.Errorf("expected a missing GET permission error, got: %w", err)
	}

	return nil
}

// DeleteClusterRoleTemplateBinding deletes a binding through the management API
func DeleteClusterRoleTemplateBinding(client *rancher.Client, crtb *managementv3.ClusterRoleTemplateBinding) error {
	return client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
}
