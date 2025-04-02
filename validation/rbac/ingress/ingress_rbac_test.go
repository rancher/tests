//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package ingress

import (
	"fmt"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/kubeapi/ingresses"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/workloads/deployment"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	k8sError "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type IngressRBACTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (i *IngressRBACTestSuite) SetupSuite() {
	i.session = session.NewSession()

	client, err := rancher.NewClient("", i.session)
	require.NoError(i.T(), err)
	i.client = client

	log.Info("Getting cluster name from the config file and append cluster details in i")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(i.T(), clusterName, "Cluster name should be set")

	clusterID, err := clusters.GetClusterIDByName(i.client, clusterName)
	require.NoError(i.T(), err)

	i.cluster, err = i.client.Management.Cluster.ByID(clusterID)
	require.NoError(i.T(), err, "Error getting cluster ID")
}

func (i *IngressRBACTestSuite) TearDownSuite() {
	i.session.Cleanup()
}

func (i *IngressRBACTestSuite) TestCreateIngress() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ClusterMember, rbac.StandardUser.String()},
		{rbac.ProjectOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
		{rbac.ReadOnly, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		i.Run("Validate creating ingress as user with role "+tt.role.String(), func() {
			log.Info("Create a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(i.client, i.cluster.ID)
			assert.NoError(i.T(), err)

			log.Infof("Create a standard user and add the user as %s", tt.role)
			_, standardUserClient, err := rbac.AddUserWithRoleToCluster(i.client, tt.member, tt.role.String(), i.cluster, adminProject)
			assert.NoError(i.T(), err)

			log.Info("As a admin, create a deployment")
			deploymentForIngress, err := deployment.CreateDeployment(i.client, i.cluster.ID, namespace.Name, 1, "", "", false, false, false, true)
			assert.NoError(i.T(), err)

			ingressTemplate, err := ingresses.CreateServiceAndIngressTemplateForDeployment(i.client, i.cluster.ID, namespace.Name, deploymentForIngress)
			assert.NoError(i.T(), err)

			log.Infof("As a %v, create a ingress", tt.role.String())

			ingress, err := ingresses.CreateIngress(standardUserClient, i.cluster.ID, ingressTemplate.Name, namespace.Name, &ingressTemplate.Spec)

			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(i.T(), err)
				assert.NotNil(i.T(), ingress)
				assert.Equal(i.T(), ingressTemplate.Name, ingress.Name)

			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(i.T(), err)
				assert.True(i.T(), k8sError.IsForbidden(err))
			}
		})
	}
}

func (i *IngressRBACTestSuite) TestListIngress() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ClusterMember, rbac.StandardUser.String()},
		{rbac.ProjectOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
		{rbac.ReadOnly, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		i.Run("Validate listing ingress as user with role "+tt.role.String(), func() {
			log.Info("Create a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(i.client, i.cluster.ID)
			assert.NoError(i.T(), err)

			log.Infof("Create a standard user and add the user as %s", tt.role)
			_, standardUserClient, err := rbac.AddUserWithRoleToCluster(i.client, tt.member, tt.role.String(), i.cluster, adminProject)
			assert.NoError(i.T(), err)

			log.Info("As a admin, create a deployment")
			deploymentForIngress, err := deployment.CreateDeployment(i.client, i.cluster.ID, namespace.Name, 1, "", "", false, false, false, true)
			assert.NoError(i.T(), err)

			ingressTemplateForDeployment, err := ingresses.CreateServiceAndIngressTemplateForDeployment(i.client, i.cluster.ID, namespace.Name, deploymentForIngress)
			assert.NoError(i.T(), err)

			log.Info("As a admin, create a ingress")
			createdIngressForDeployment, err := ingresses.CreateIngress(i.client, i.cluster.ID, ingressTemplateForDeployment.Name, namespace.Name, &ingressTemplateForDeployment.Spec)
			assert.NoError(i.T(), err)

			log.Infof("As a %v, list the ingress", tt.role.String())
			ingressList, err := ingresses.ListIngresses(standardUserClient, i.cluster.ID, namespace.Name, metav1.ListOptions{})
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String(), rbac.ReadOnly.String():
				assert.NoError(i.T(), err, "failed to list ingress")
				assert.Equal(i.T(), len(ingressList.Items), 1)
				assert.Equal(i.T(), ingressList.Items[0].Name, createdIngressForDeployment.Name)
			case rbac.ClusterMember.String():
				assert.Error(i.T(), err)
				assert.True(i.T(), k8sError.IsForbidden(err))
			}
		})
	}
}

func (i *IngressRBACTestSuite) TestUpdateIngress() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ClusterMember, rbac.StandardUser.String()},
		{rbac.ProjectOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
		{rbac.ReadOnly, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		i.Run("Validate updating ingress as user with role "+tt.role.String(), func() {
			log.Info("Create a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(i.client, i.cluster.ID)
			assert.NoError(i.T(), err)

			log.Infof("Create a standard user and add the user as %s", tt.role)
			_, standardUserClient, err := rbac.AddUserWithRoleToCluster(i.client, tt.member, tt.role.String(), i.cluster, adminProject)
			assert.NoError(i.T(), err)

			log.Info("As a admin, create a deployment")
			deploymentForIngress, err := deployment.CreateDeployment(i.client, i.cluster.ID, namespace.Name, 1, "", "", false, false, false, true)
			assert.NoError(i.T(), err)

			ingressTemplateForDeployment, err := ingresses.CreateServiceAndIngressTemplateForDeployment(i.client, i.cluster.ID, namespace.Name, deploymentForIngress)
			assert.NoError(i.T(), err)

			log.Info("As a admin, create a ingress")
			createdIngressForDeployment, err := ingresses.CreateIngress(i.client, i.cluster.ID, ingressTemplateForDeployment.Name, namespace.Name, &ingressTemplateForDeployment.Spec)
			assert.NoError(i.T(), err)

			log.Infof("As a %v, update the ingress", tt.role.String())
			updatedIngress := createdIngressForDeployment.DeepCopy()
			updatedIngress.Spec.Rules[0].Host = fmt.Sprintf("%s.updated.com", namegen.AppendRandomString("test"))

			_, err = ingresses.UpdateIngress(standardUserClient, i.cluster.ID, namespace.Name, createdIngressForDeployment, updatedIngress)

			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(i.T(), err, "failed to update ingress")

				updatedIngressList, listErr := ingresses.ListIngresses(i.client, i.cluster.ID, namespace.Name, metav1.ListOptions{})
				assert.NoError(i.T(), listErr)
				assert.Equal(i.T(), updatedIngressList.Items[0].Spec.Rules[0].Host, updatedIngress.Spec.Rules[0].Host)

			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(i.T(), err)
				assert.True(i.T(), k8sError.IsForbidden(err))
			}
		})
	}
}

func (i *IngressRBACTestSuite) TestDeleteIngress() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ClusterMember, rbac.StandardUser.String()},
		{rbac.ProjectOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
		{rbac.ReadOnly, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		i.Run("Validate deleting ingress as user with role "+tt.role.String(), func() {
			log.Info("Create a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(i.client, i.cluster.ID)
			assert.NoError(i.T(), err)

			log.Infof("Create a standard user and add the user as %s", tt.role)
			_, standardUserClient, err := rbac.AddUserWithRoleToCluster(i.client, tt.member, tt.role.String(), i.cluster, adminProject)
			assert.NoError(i.T(), err)

			log.Info("As a admin, create a deployment")
			deploymentForIngress, err := deployment.CreateDeployment(i.client, i.cluster.ID, namespace.Name, 1, "", "", false, false, false, true)
			assert.NoError(i.T(), err)

			ingressTemplateForDeployment, err := ingresses.CreateServiceAndIngressTemplateForDeployment(i.client, i.cluster.ID, namespace.Name, deploymentForIngress)
			assert.NoError(i.T(), err)

			log.Info("As a admin, create a ingress")
			createdIngressForDeployment, err := ingresses.CreateIngress(i.client, i.cluster.ID, ingressTemplateForDeployment.Name, namespace.Name, &ingressTemplateForDeployment.Spec)
			assert.NoError(i.T(), err)

			log.Infof("As a %v, delete the ingress", tt.role.String())
			err = ingresses.DeleteIngress(standardUserClient, i.cluster.ID, namespace.Name, createdIngressForDeployment.Name)

			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(i.T(), err, "failed to delete ingress")

				ingressList, listErr := ingresses.ListIngresses(i.client, i.cluster.ID, namespace.Name, metav1.ListOptions{
					FieldSelector: "metadata.name=" + createdIngressForDeployment.Name,
				})
				assert.NoError(i.T(), listErr)
				assert.Equal(i.T(), 0, len(ingressList.Items), "ingress should have been deleted")

			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(i.T(), err)
				assert.True(i.T(), k8sError.IsForbidden(err))
			}
		})
	}
}

func TestIngressRBACTestSuite(t *testing.T) {
	suite.Run(t, new(IngressRBACTestSuite))
}
