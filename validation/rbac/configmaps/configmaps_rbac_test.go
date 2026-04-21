//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package configmaps

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/shepherd/pkg/wrangler"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	configmapapi "github.com/rancher/tests/actions/kubeapi/configmaps"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/workloads/deployment"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	k8sError "k8s.io/apimachinery/pkg/api/errors"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	data = map[string]string{"foo": "bar"}
)

type ConfigMapRBACTestSuite struct {
	suite.Suite
	client     *rancher.Client
	session    *session.Session
	cluster    *management.Cluster
	ctxAsAdmin *wrangler.Context
}

func (cm *ConfigMapRBACTestSuite) TearDownSuite() {
	cm.session.Cleanup()
}

func (cm *ConfigMapRBACTestSuite) SetupSuite() {
	cm.session = session.NewSession()

	client, err := rancher.NewClient("", cm.session)
	require.NoError(cm.T(), err)
	cm.client = client

	log.Info("Getting cluster name from the config file and append cluster details in cm")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(cm.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(cm.client, clusterName)
	require.NoError(cm.T(), err, "Error getting cluster ID")
	cm.cluster, err = cm.client.Management.Cluster.ByID(clusterID)
	require.NoError(cm.T(), err)

	cm.ctxAsAdmin, err = clusterapi.GetClusterWranglerContext(cm.client, clusterID)
	require.NoError(cm.T(), err)
}

func (cm *ConfigMapRBACTestSuite) TestCreateConfigmapAsVolume() {
	subSession := cm.session.NewSession()
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
		cm.Run("Validate config map creation for user with role "+tt.role.String(), func() {
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(cm.client, cm.cluster.ID)
			assert.NoError(cm.T(), err)

			_, standardUserClient, err := rbac.AddUserWithRoleToCluster(cm.client, tt.member, tt.role.String(), cm.cluster, adminProject)
			assert.NoError(cm.T(), err)

			configMapCreatedByUser, err := configmapapi.CreateConfigMap(standardUserClient, cm.cluster.ID, namespace.Name, nil, nil, data)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(cm.T(), err)
				_, err = deployment.CreateDeployment(standardUserClient, cm.cluster.ID, namespace.Name, 1, "", configMapCreatedByUser.Name, false, true, false, true)
				assert.NoError(cm.T(), err)
				getConfigMapAsAdmin, err := cm.ctxAsAdmin.Core.ConfigMap().Get(namespace.Name, configMapCreatedByUser.Name, metaV1.GetOptions{})
				assert.NoError(cm.T(), err)
				assert.Equal(cm.T(), getConfigMapAsAdmin.Data, configMapCreatedByUser.Data)
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(cm.T(), err)
				assert.True(cm.T(), k8sError.IsForbidden(err))
			}
		})
	}
}

func (cm *ConfigMapRBACTestSuite) TestCreateConfigmapAsEnvVar() {
	subSession := cm.session.NewSession()
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
		cm.Run("Validate config map creation of config map and verify adding it as a an env variable for user with role "+tt.role.String(), func() {
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(cm.client, cm.cluster.ID)
			assert.NoError(cm.T(), err)

			_, standardUserClient, err := rbac.AddUserWithRoleToCluster(cm.client, tt.member, tt.role.String(), cm.cluster, adminProject)
			assert.NoError(cm.T(), err)

			configMapCreatedByUser, err := configmapapi.CreateConfigMap(standardUserClient, cm.cluster.ID, namespace.Name, nil, nil, data)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(cm.T(), err)
				_, err = deployment.CreateDeployment(standardUserClient, cm.cluster.ID, namespace.Name, 1, "", configMapCreatedByUser.Name, true, false, false, true)
				assert.NoError(cm.T(), err)
				getConfigMapAsAdmin, err := cm.ctxAsAdmin.Core.ConfigMap().Get(namespace.Name, configMapCreatedByUser.Name, metaV1.GetOptions{})
				assert.NoError(cm.T(), err)
				assert.Equal(cm.T(), getConfigMapAsAdmin.Data, configMapCreatedByUser.Data)
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(cm.T(), err)
				assert.True(cm.T(), k8sError.IsForbidden(err))
			}
		})
	}
}

func (cm *ConfigMapRBACTestSuite) TestUpdateConfigmap() {
	subSession := cm.session.NewSession()
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
		cm.Run("Validate updating config map for user with role "+tt.role.String(), func() {
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(cm.client, cm.cluster.ID)
			assert.NoError(cm.T(), err)

			_, standardUserClient, err := rbac.AddUserWithRoleToCluster(cm.client, tt.member, tt.role.String(), cm.cluster, adminProject)
			assert.NoError(cm.T(), err)

			configmapCreate, err := configmapapi.CreateConfigMap(cm.client, cm.cluster.ID, namespace.Name, nil, nil, data)
			assert.NoError(cm.T(), err)

			configmapCreate.Data["foo1"] = "bar1"
			userDownstreamWranglerContext, err := clusterapi.GetClusterWranglerContext(standardUserClient, cm.cluster.ID)
			assert.NoError(cm.T(), err)
			configMapUpdatedByUser, userErr := userDownstreamWranglerContext.Core.ConfigMap().Update(configmapCreate)

			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(cm.T(), userErr)
				_, err = deployment.CreateDeployment(cm.client, cm.cluster.ID, namespace.Name, 1, "", configmapCreate.Name, true, false, false, true)
				assert.NoError(cm.T(), err)
				getCMAsAdmin, err := cm.ctxAsAdmin.Core.ConfigMap().Get(namespace.Name, configMapUpdatedByUser.Name, metaV1.GetOptions{})
				assert.NoError(cm.T(), err)
				assert.Equal(cm.T(), configMapUpdatedByUser.Data, getCMAsAdmin.Data)
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(cm.T(), userErr)
				assert.True(cm.T(), k8sError.IsForbidden(userErr))
			}
		})
	}
}

func (cm *ConfigMapRBACTestSuite) TestListConfigmaps() {
	subSession := cm.session.NewSession()
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
		cm.Run("Validate listing config maps for user with role "+tt.role.String(), func() {
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(cm.client, cm.cluster.ID)
			assert.NoError(cm.T(), err)

			_, standardUserClient, err := rbac.AddUserWithRoleToCluster(cm.client, tt.member, tt.role.String(), cm.cluster, adminProject)
			assert.NoError(cm.T(), err)

			configMapCreatedByAdmin, err := configmapapi.CreateConfigMap(cm.client, cm.cluster.ID, namespace.Name, nil, nil, data)
			assert.NoError(cm.T(), err)

			downstreamWranglerContext, err := clusterapi.GetClusterWranglerContext(standardUserClient, cm.cluster.ID)
			assert.NoError(cm.T(), err)
			configMapListAsUser, err := downstreamWranglerContext.Core.ConfigMap().List(namespace.Name, metaV1.ListOptions{
				FieldSelector: "metadata.name=" + configMapCreatedByAdmin.Name,
			})

			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String(), rbac.ReadOnly.String():
				assert.NoError(cm.T(), err)
				assert.Equal(cm.T(), len(configMapListAsUser.Items), 1)
			case rbac.ClusterMember.String():
				assert.Error(cm.T(), err)
				assert.True(cm.T(), k8sError.IsForbidden(err))
			}
		})
	}
}

func (cm *ConfigMapRBACTestSuite) TestDeleteConfigmap() {
	subSession := cm.session.NewSession()
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
		cm.Run("Validate deletion of config map for user with role "+tt.role.String(), func() {
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(cm.client, cm.cluster.ID)
			assert.NoError(cm.T(), err)

			_, standardUserClient, err := rbac.AddUserWithRoleToCluster(cm.client, tt.member, tt.role.String(), cm.cluster, adminProject)
			assert.NoError(cm.T(), err)

			configMapCreatedByAdmin, err := configmapapi.CreateConfigMap(cm.client, cm.cluster.ID, namespace.Name, nil, nil, data)
			assert.NoError(cm.T(), err)

			userDownstreamWranglerContext, err := clusterapi.GetClusterWranglerContext(standardUserClient, cm.cluster.ID)
			assert.NoError(cm.T(), err)
			err = userDownstreamWranglerContext.Core.ConfigMap().Delete(namespace.Name, configMapCreatedByAdmin.Name, &metaV1.DeleteOptions{})
			configMapListAsAdmin, listErr := cm.ctxAsAdmin.Core.ConfigMap().List(namespace.Name, metaV1.ListOptions{
				FieldSelector: "metadata.name=" + configMapCreatedByAdmin.Name,
			})
			assert.NoError(cm.T(), listErr)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(cm.T(), err)
				assert.Equal(cm.T(), len(configMapListAsAdmin.Items), 0)
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(cm.T(), err)
				assert.True(cm.T(), k8sError.IsForbidden(err))
				assert.Equal(cm.T(), len(configMapListAsAdmin.Items), 1)
			}
		})
	}
}

func (cm *ConfigMapRBACTestSuite) TestCRUDConfigmapAsClusterMember() {
	subSession := cm.session.NewSession()
	defer subSession.Cleanup()

	role := rbac.ClusterMember.String()
	log.Info("Creating a standard user and adding them to cluster as a cluster member.")
	standardUser, standardUserClient, err := rbac.AddUserWithRoleToCluster(cm.client, rbac.StandardUser.String(), role, cm.cluster, nil)
	require.NoError(cm.T(), err)

	projectTemplate := projectapi.NewProjectTemplate(cm.cluster.ID)
	projectTemplate.Annotations = map[string]string{
		"field.cattle.io/creatorId": standardUser.ID,
	}
	createdProject, err := standardUserClient.WranglerContext.Mgmt.Project().Create(projectTemplate)
	require.NoError(cm.T(), err)

	err = projectapi.WaitForProjectFinalizerToUpdate(standardUserClient, createdProject.Name, createdProject.Namespace, 2)
	require.NoError(cm.T(), err)

	namespace, err := namespaceapi.CreateNamespaceUsingWrangler(standardUserClient, cm.cluster.ID, createdProject.Name, nil)
	require.NoError(cm.T(), err)

	configMapCreatedByAdmin, err := configmapapi.CreateConfigMap(cm.client, cm.cluster.ID, namespace.Name, nil, nil, data)
	require.NoError(cm.T(), err)

	log.Infof("Validating CRUD operations on config maps in the project %s created by cluster member %s", createdProject.Name, standardUser.Username)
	configMapCreatedByClusterMember, err := configmapapi.CreateConfigMap(standardUserClient, cm.cluster.ID, namespace.Name, nil, nil, data)
	require.NoError(cm.T(), err)
	_, err = deployment.CreateDeployment(standardUserClient, cm.cluster.ID, namespace.Name, 1, "", configMapCreatedByClusterMember.Name, true, false, false, true)
	require.NoError(cm.T(), err)

	standardUserContext, err := clusterapi.GetClusterWranglerContext(standardUserClient, cm.cluster.ID)
	require.NoError(cm.T(), err)
	configMapListAsClusterMember, err := standardUserContext.Core.ConfigMap().List(namespace.Name, metaV1.ListOptions{})
	require.NoError(cm.T(), err)
	configMapListAsAdmin, err := cm.ctxAsAdmin.Core.ConfigMap().List(namespace.Name, metaV1.ListOptions{})
	require.NoError(cm.T(), err)
	require.Equal(cm.T(), len(configMapListAsClusterMember.Items), len(configMapListAsAdmin.Items))

	configMapCreatedByAdmin.Data["foo1"] = "bar1"
	updatedConfigMap, err := standardUserContext.Core.ConfigMap().Update(configMapCreatedByAdmin)
	require.NoError(cm.T(), err)
	require.Contains(cm.T(), updatedConfigMap.Data, "foo1")
	require.Equal(cm.T(), updatedConfigMap.Data["foo1"], "bar1")

	err = standardUserContext.Core.ConfigMap().Delete(namespace.Name, configMapCreatedByAdmin.Name, &metaV1.DeleteOptions{})
	require.NoError(cm.T(), err)
	configMapListAsClusterMember, err = standardUserContext.Core.ConfigMap().List(namespace.Name, metaV1.ListOptions{})
	require.NoError(cm.T(), err)
	require.Equal(cm.T(), len(configMapListAsClusterMember.Items), len(configMapListAsAdmin.Items)-1)
}

func TestConfigMapRBACTestSuite(t *testing.T) {
	suite.Run(t, new(ConfigMapRBACTestSuite))
}
