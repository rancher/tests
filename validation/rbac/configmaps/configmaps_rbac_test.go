//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package configmaps

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	extconfigmapapi "github.com/rancher/shepherd/extensions/kubeapi/configmaps"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	configmapapi "github.com/rancher/tests/actions/kubeapi/configmaps"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	deploymentapi "github.com/rancher/tests/actions/kubeapi/workloads/deployments"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	k8sError "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	data = map[string]string{"foo": "bar"}
)

type ConfigMapRBACTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
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
			adminProject, namespace, err := projectapi.CreateProjectAndNamespace(cm.client, cm.cluster.ID)
			assert.NoError(cm.T(), err)

			_, standardUserClient, err := rbac.AddUserWithRoleToCluster(cm.client, tt.member, tt.role.String(), cm.cluster, adminProject)
			assert.NoError(cm.T(), err)

			configMapCreatedByUser, err := configmapapi.CreateConfigMap(standardUserClient, cm.cluster.ID, namespace.Name, nil, nil, data)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(cm.T(), err)
				_, err = deploymentapi.CreateDeployment(standardUserClient, cm.cluster.ID, namespace.Name, "", 1, "", configMapCreatedByUser.Name, false, true, false, true)
				assert.NoError(cm.T(), err)
				getConfigMapAsAdmin, err := extconfigmapapi.GetConfigMapByName(cm.client, cm.cluster.ID, namespace.Name, configMapCreatedByUser.Name)
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
			adminProject, namespace, err := projectapi.CreateProjectAndNamespace(cm.client, cm.cluster.ID)
			assert.NoError(cm.T(), err)

			_, standardUserClient, err := rbac.AddUserWithRoleToCluster(cm.client, tt.member, tt.role.String(), cm.cluster, adminProject)
			assert.NoError(cm.T(), err)

			configMapCreatedByUser, err := configmapapi.CreateConfigMap(standardUserClient, cm.cluster.ID, namespace.Name, nil, nil, data)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(cm.T(), err)
				_, err = deploymentapi.CreateDeployment(standardUserClient, cm.cluster.ID, namespace.Name, "", 1, "", configMapCreatedByUser.Name, true, false, false, true)
				assert.NoError(cm.T(), err)
				getConfigMapAsAdmin, err := extconfigmapapi.GetConfigMapByName(cm.client, cm.cluster.ID, namespace.Name, configMapCreatedByUser.Name)
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
			adminProject, namespace, err := projectapi.CreateProjectAndNamespace(cm.client, cm.cluster.ID)
			assert.NoError(cm.T(), err)

			_, standardUserClient, err := rbac.AddUserWithRoleToCluster(cm.client, tt.member, tt.role.String(), cm.cluster, adminProject)
			assert.NoError(cm.T(), err)

			createdConfigMap, err := configmapapi.CreateConfigMap(cm.client, cm.cluster.ID, namespace.Name, nil, nil, data)
			assert.NoError(cm.T(), err)

			createdConfigMap.Data["foo1"] = "bar1"
			configMapUpdatedByUser, userErr := extconfigmapapi.UpdateConfigMap(standardUserClient, cm.cluster.ID, createdConfigMap.Namespace, createdConfigMap)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(cm.T(), userErr)
				_, err = deploymentapi.CreateDeployment(cm.client, cm.cluster.ID, namespace.Name, "", 1, "", createdConfigMap.Name, true, false, false, true)
				assert.NoError(cm.T(), err)
				getCMAsAdmin, err := extconfigmapapi.GetConfigMapByName(cm.client, cm.cluster.ID, namespace.Name, configMapUpdatedByUser.Name)
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
			adminProject, namespace, err := projectapi.CreateProjectAndNamespace(cm.client, cm.cluster.ID)
			assert.NoError(cm.T(), err)

			_, standardUserClient, err := rbac.AddUserWithRoleToCluster(cm.client, tt.member, tt.role.String(), cm.cluster, adminProject)
			assert.NoError(cm.T(), err)

			configMapCreatedByAdmin, err := configmapapi.CreateConfigMap(cm.client, cm.cluster.ID, namespace.Name, nil, nil, data)
			assert.NoError(cm.T(), err)

			configMapListAsUser, err := extconfigmapapi.ListConfigMaps(standardUserClient, cm.cluster.ID, namespace.Name, metav1.ListOptions{
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
			adminProject, namespace, err := projectapi.CreateProjectAndNamespace(cm.client, cm.cluster.ID)
			assert.NoError(cm.T(), err)

			_, standardUserClient, err := rbac.AddUserWithRoleToCluster(cm.client, tt.member, tt.role.String(), cm.cluster, adminProject)
			assert.NoError(cm.T(), err)

			configMapCreatedByAdmin, err := configmapapi.CreateConfigMap(cm.client, cm.cluster.ID, namespace.Name, nil, nil, data)
			assert.NoError(cm.T(), err)

			err = extconfigmapapi.DeleteConfigMap(standardUserClient, cm.cluster.ID, namespace.Name, configMapCreatedByAdmin.Name, true)
			configMapListAsAdmin, listErr := extconfigmapapi.ListConfigMaps(cm.client, cm.cluster.ID, namespace.Name, metav1.ListOptions{
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
	createdProject, err := projectapi.CreateProjectWithTemplate(standardUserClient, cm.cluster.ID, projectTemplate)
	require.NoError(cm.T(), err)

	err = projectapi.WaitForProjectFinalizerToUpdate(standardUserClient, createdProject.Name, createdProject.Namespace, 2)
	require.NoError(cm.T(), err)

	namespace, err := namespaceapi.CreateNamespace(standardUserClient, cm.cluster.ID, createdProject.Name, namegen.AppendRandomString("testns-"), "", nil, nil)
	require.NoError(cm.T(), err)

	configMapCreatedByAdmin, err := configmapapi.CreateConfigMap(cm.client, cm.cluster.ID, namespace.Name, nil, nil, data)
	require.NoError(cm.T(), err)

	log.Infof("Validating CRUD operations on config maps in the project %s created by cluster member %s", createdProject.Name, standardUser.Username)
	configMapCreatedByClusterMember, err := configmapapi.CreateConfigMap(standardUserClient, cm.cluster.ID, namespace.Name, nil, nil, data)
	require.NoError(cm.T(), err)
	_, err = deploymentapi.CreateDeployment(standardUserClient, cm.cluster.ID, namespace.Name, "", 1, "", configMapCreatedByClusterMember.Name, true, false, false, true)
	require.NoError(cm.T(), err)

	configMapListAsClusterMember, err := extconfigmapapi.ListConfigMaps(standardUserClient, cm.cluster.ID, namespace.Name, metav1.ListOptions{})
	require.NoError(cm.T(), err)
	configMapListAsAdmin, err := extconfigmapapi.ListConfigMaps(cm.client, cm.cluster.ID, namespace.Name, metav1.ListOptions{})
	require.NoError(cm.T(), err)
	require.Equal(cm.T(), len(configMapListAsClusterMember.Items), len(configMapListAsAdmin.Items))

	configMapCreatedByAdmin.Data["foo1"] = "bar1"
	updatedConfigMap, err := extconfigmapapi.UpdateConfigMap(standardUserClient, cm.cluster.ID, namespace.Name, configMapCreatedByAdmin)
	require.NoError(cm.T(), err)
	require.Contains(cm.T(), updatedConfigMap.Data, "foo1")
	require.Equal(cm.T(), updatedConfigMap.Data["foo1"], "bar1")

	err = extconfigmapapi.DeleteConfigMap(standardUserClient, cm.cluster.ID, namespace.Name, configMapCreatedByAdmin.Name, true)
	require.NoError(cm.T(), err)
	configMapListAsClusterMember, err = extconfigmapapi.ListConfigMaps(standardUserClient, cm.cluster.ID, namespace.Name, metav1.ListOptions{})
	require.NoError(cm.T(), err)
	require.Equal(cm.T(), len(configMapListAsClusterMember.Items), len(configMapListAsAdmin.Items)-1)
}

func TestConfigMapRBACTestSuite(t *testing.T) {
	suite.Run(t, new(ConfigMapRBACTestSuite))
}
