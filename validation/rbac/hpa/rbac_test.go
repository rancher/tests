//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package hpa

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/autoscaling"
	hpaapi "github.com/rancher/tests/actions/kubeapi/autoscaling"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RbacHPATestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (h *RbacHPATestSuite) TearDownSuite() {
	h.session.Cleanup()
}

func (h *RbacHPATestSuite) SetupSuite() {
	h.session = session.NewSession()

	client, err := rancher.NewClient("", h.session)
	require.NoError(h.T(), err)
	h.client = client

	log.Info("Getting cluster name from the config file and append cluster details in h")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(h.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(h.client, clusterName)
	require.NoError(h.T(), err, "Error getting cluster ID")
	h.cluster, err = h.client.Management.Cluster.ByID(clusterID)
	require.NoError(h.T(), err)
}

func (h *RbacHPATestSuite) TestCreateHPA() {
	subSession := h.session.NewSession()
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
		h.Run("Validate HPA creation as user with role "+tt.role.String(), func() {
			log.Info("Create a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(h.client, h.cluster.ID)
			assert.NoError(h.T(), err)

			log.Infof("Create a standard user and add the user to a cluster/project role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(h.client, tt.member, tt.role.String(), h.cluster, adminProject)
			assert.NoError(h.T(), err)

			switch tt.role.String() {
			case rbac.ClusterMember.String():
				log.Infof("As a %v, create a project and namespace", tt.role.String())
				userProject, err := projectapi.CreateProject(userClient, h.cluster.ID)
				assert.NoError(h.T(), err)
				userNamespace, err := namespaceapi.CreateNamespaceUsingWrangler(userClient, h.cluster.ID, userProject.Name, nil)
				assert.NoError(h.T(), err)

				log.Infof("As a %v, create an HPA", tt.role.String())
				createdHPA, workload, err := autoscaling.CreateHPA(userClient, h.cluster.ID, userNamespace.Name, nil, 2, 5, autoscaling.DefaultCPUMetrics())
				assert.NoError(h.T(), err, "failed to create HPA")
				assert.Equal(h.T(), *createdHPA.Spec.MinReplicas, int32(2))
				assert.Equal(h.T(), createdHPA.Spec.MaxReplicas, int32(5))

				err = autoscaling.WaitForDeploymentPodCount(userClient, h.cluster.ID, userNamespace.Name, workload.Name, 2)
				assert.NoError(h.T(), err, "workload did not scale to minReplicas")

			case rbac.ReadOnly.String():
				log.Infof("As a %v, attempt to create an HPA (expect forbidden)", tt.role.String())
				workload, err := autoscaling.CreateTestWorkload(h.client, h.cluster.ID, namespace.Name)
				assert.NoError(h.T(), err)

				_, _, err = autoscaling.CreateHPA(userClient, h.cluster.ID, namespace.Name, workload, 2, 5, autoscaling.DefaultCPUMetrics())
				assert.Error(h.T(), err)
				assert.True(h.T(), errors.IsForbidden(err))

			default:
				log.Infof("As a %v, create an HPA", tt.role.String())
				createdHPA, workload, err := autoscaling.CreateHPA(userClient, h.cluster.ID, namespace.Name, nil, 2, 5, autoscaling.DefaultCPUMetrics())
				assert.NoError(h.T(), err, "failed to create HPA")
				assert.Equal(h.T(), *createdHPA.Spec.MinReplicas, int32(2))
				assert.Equal(h.T(), createdHPA.Spec.MaxReplicas, int32(5))

				err = autoscaling.WaitForDeploymentPodCount(userClient, h.cluster.ID, namespace.Name, workload.Name, 2)
				assert.NoError(h.T(), err, "workload did not scale to minReplicas")
			}
		})
	}
}

func (h *RbacHPATestSuite) TestCreateHPANegative() {
	subSession := h.session.NewSession()
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
		h.Run("Validate HPA creation in unshared project as user with role "+tt.role.String(), func() {
			log.Info("Create an unshared project and a namespace in the project.")
			_, unsharedNamespace, err := projects.CreateProjectAndNamespaceUsingWrangler(h.client, h.cluster.ID)
			assert.NoError(h.T(), err)

			log.Infof("Create a standard user and add the user to a separate cluster/project role %s", tt.role)
			sharedProject, _, err := projects.CreateProjectAndNamespaceUsingWrangler(h.client, h.cluster.ID)
			assert.NoError(h.T(), err)
			_, userClient, err := rbac.AddUserWithRoleToCluster(h.client, tt.member, tt.role.String(), h.cluster, sharedProject)
			assert.NoError(h.T(), err)

			switch tt.role.String() {
			case rbac.ClusterOwner.String():
				log.Infof("As a %v, create an HPA in the unshared project (should succeed)", tt.role.String())
				_, _, err = autoscaling.CreateHPA(userClient, h.cluster.ID, unsharedNamespace.Name, nil, 2, 5, autoscaling.DefaultCPUMetrics())
				assert.NoError(h.T(), err)

			default:
				log.Infof("As admin, create a workload in the unshared project")
				workload, err := autoscaling.CreateTestWorkload(h.client, h.cluster.ID, unsharedNamespace.Name)
				assert.NoError(h.T(), err)

				log.Infof("As a %v, attempt to create an HPA in the unshared project (expect forbidden)", tt.role.String())
				_, _, err = autoscaling.CreateHPA(userClient, h.cluster.ID, unsharedNamespace.Name, workload, 2, 5, autoscaling.DefaultCPUMetrics())
				assert.Error(h.T(), err)
				assert.True(h.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (h *RbacHPATestSuite) TestEditHPA() {
	subSession := h.session.NewSession()
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
		h.Run("Validate HPA edit as user with role "+tt.role.String(), func() {
			log.Info("Create a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(h.client, h.cluster.ID)
			assert.NoError(h.T(), err)

			log.Infof("Create a standard user and add the user to a cluster/project role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(h.client, tt.member, tt.role.String(), h.cluster, adminProject)
			assert.NoError(h.T(), err)

			switch tt.role.String() {
			case rbac.ReadOnly.String():
				log.Infof("As a %v, verify edit is forbidden", tt.role.String())
				verifyEditForbidden(h, userClient, h.client, namespace.Name)

			case rbac.ClusterMember.String():
				log.Infof("As a %v, create a project and namespace", tt.role.String())
				userProject, err := projectapi.CreateProject(userClient, h.cluster.ID)
				assert.NoError(h.T(), err)
				userNamespace, err := namespaceapi.CreateNamespaceUsingWrangler(userClient, h.cluster.ID, userProject.Name, nil)
				assert.NoError(h.T(), err)

				log.Infof("As a %v, edit an HPA in own project (should succeed)", tt.role.String())
				editHPA(h, userClient, userNamespace.Name)

				log.Infof("As a %v, verify edit is forbidden in shared project", tt.role.String())
				verifyEditForbidden(h, userClient, h.client, namespace.Name)

			default:
				log.Infof("As a %v, edit an HPA", tt.role.String())
				editHPA(h, userClient, namespace.Name)
			}
		})
	}
}

func (h *RbacHPATestSuite) TestEditHPANegative() {
	subSession := h.session.NewSession()
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
		h.Run("Validate HPA edit in unshared project as user with role "+tt.role.String(), func() {
			log.Info("Create an unshared project and a namespace in the project.")
			_, unsharedNamespace, err := projects.CreateProjectAndNamespaceUsingWrangler(h.client, h.cluster.ID)
			assert.NoError(h.T(), err)

			log.Infof("Create a standard user and add the user to a separate cluster/project role %s", tt.role)
			sharedProject, _, err := projects.CreateProjectAndNamespaceUsingWrangler(h.client, h.cluster.ID)
			assert.NoError(h.T(), err)
			_, userClient, err := rbac.AddUserWithRoleToCluster(h.client, tt.member, tt.role.String(), h.cluster, sharedProject)
			assert.NoError(h.T(), err)

			switch tt.role.String() {
			case rbac.ClusterOwner.String():
				log.Infof("As a %v, edit an HPA in the unshared project (should succeed)", tt.role.String())
				editHPA(h, userClient, unsharedNamespace.Name)

			default:
				log.Infof("As a %v, verify edit is forbidden in the unshared project", tt.role.String())
				verifyEditForbidden(h, userClient, h.client, unsharedNamespace.Name)
			}
		})
	}
}

func (h *RbacHPATestSuite) TestDeleteHPA() {
	subSession := h.session.NewSession()
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
		h.Run("Validate HPA deletion as user with role "+tt.role.String(), func() {
			log.Info("Create a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(h.client, h.cluster.ID)
			assert.NoError(h.T(), err)

			log.Infof("Create a standard user and add the user to a cluster/project role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(h.client, tt.member, tt.role.String(), h.cluster, adminProject)
			assert.NoError(h.T(), err)

			switch tt.role.String() {
			case rbac.ClusterMember.String():
				log.Infof("As a %v, create a project and namespace", tt.role.String())
				userProject, err := projectapi.CreateProject(userClient, h.cluster.ID)
				assert.NoError(h.T(), err)
				userNamespace, err := namespaceapi.CreateNamespaceUsingWrangler(userClient, h.cluster.ID, userProject.Name, nil)
				assert.NoError(h.T(), err)

				log.Infof("As a %v, create and delete an HPA", tt.role.String())
				createdHPA, _, err := autoscaling.CreateHPA(userClient, h.cluster.ID, userNamespace.Name, nil, 2, 5, autoscaling.DefaultCPUMetrics())
				assert.NoError(h.T(), err)
				err = autoscaling.DeleteHPAAndWait(userClient, h.cluster.ID, userNamespace.Name, createdHPA.Name)
				assert.NoError(h.T(), err, "failed to delete HPA")

			case rbac.ReadOnly.String():
				log.Infof("As admin, create an HPA")
				createdHPA, _, err := autoscaling.CreateHPA(h.client, h.cluster.ID, namespace.Name, nil, 2, 5, autoscaling.DefaultCPUMetrics())
				assert.NoError(h.T(), err)

				log.Infof("As a %v, attempt to delete the HPA (expect forbidden)", tt.role.String())
				err = hpaapi.DeleteHPA(userClient, h.cluster.ID, namespace.Name, createdHPA.Name)
				assert.Error(h.T(), err)
				assert.True(h.T(), errors.IsForbidden(err))

			default:
				log.Infof("As a %v, create and delete an HPA", tt.role.String())
				createdHPA, _, err := autoscaling.CreateHPA(userClient, h.cluster.ID, namespace.Name, nil, 2, 5, autoscaling.DefaultCPUMetrics())
				assert.NoError(h.T(), err)
				err = autoscaling.DeleteHPAAndWait(userClient, h.cluster.ID, namespace.Name, createdHPA.Name)
				assert.NoError(h.T(), err, "failed to delete HPA")
			}
		})
	}
}

func (h *RbacHPATestSuite) TestDeleteHPANegative() {
	subSession := h.session.NewSession()
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
		h.Run("Validate HPA deletion in unshared project as user with role "+tt.role.String(), func() {
			log.Info("Create an unshared project and a namespace in the project.")
			_, unsharedNamespace, err := projects.CreateProjectAndNamespaceUsingWrangler(h.client, h.cluster.ID)
			assert.NoError(h.T(), err)

			log.Infof("Create a standard user and add the user to a separate cluster/project role %s", tt.role)
			sharedProject, _, err := projects.CreateProjectAndNamespaceUsingWrangler(h.client, h.cluster.ID)
			assert.NoError(h.T(), err)
			_, userClient, err := rbac.AddUserWithRoleToCluster(h.client, tt.member, tt.role.String(), h.cluster, sharedProject)
			assert.NoError(h.T(), err)

			switch tt.role.String() {
			case rbac.ClusterOwner.String():
				log.Infof("As a %v, create and delete an HPA in the unshared project (should succeed)", tt.role.String())
				createdHPA, _, err := autoscaling.CreateHPA(userClient, h.cluster.ID, unsharedNamespace.Name, nil, 2, 5, autoscaling.DefaultCPUMetrics())
				assert.NoError(h.T(), err)
				err = autoscaling.DeleteHPAAndWait(userClient, h.cluster.ID, unsharedNamespace.Name, createdHPA.Name)
				assert.NoError(h.T(), err, "failed to delete HPA")

			default:
				log.Infof("As admin, create an HPA in the unshared project")
				createdHPA, _, err := autoscaling.CreateHPA(h.client, h.cluster.ID, unsharedNamespace.Name, nil, 2, 5, autoscaling.DefaultCPUMetrics())
				assert.NoError(h.T(), err)

				log.Infof("As a %v, attempt to delete the HPA in the unshared project (expect forbidden)", tt.role.String())
				err = hpaapi.DeleteHPA(userClient, h.cluster.ID, unsharedNamespace.Name, createdHPA.Name)
				assert.Error(h.T(), err)
				assert.True(h.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (h *RbacHPATestSuite) TestListHPA() {
	subSession := h.session.NewSession()
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
		h.Run("Validate listing HPA as user with role "+tt.role.String(), func() {
			log.Info("Create a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(h.client, h.cluster.ID)
			assert.NoError(h.T(), err)

			log.Infof("Create a standard user and add the user to a cluster/project role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(h.client, tt.member, tt.role.String(), h.cluster, adminProject)
			assert.NoError(h.T(), err)

			switch tt.role.String() {
			case rbac.ClusterMember.String():
				log.Infof("As a %v, create a project and namespace", tt.role.String())
				userProject, err := projectapi.CreateProject(userClient, h.cluster.ID)
				assert.NoError(h.T(), err)
				userNamespace, err := namespaceapi.CreateNamespaceUsingWrangler(userClient, h.cluster.ID, userProject.Name, nil)
				assert.NoError(h.T(), err)

				log.Infof("As a %v, create an HPA and list it", tt.role.String())
				createdHPA, _, err := autoscaling.CreateHPA(userClient, h.cluster.ID, userNamespace.Name, nil, 2, 5, autoscaling.DefaultCPUMetrics())
				assert.NoError(h.T(), err)

				hpaList, err := hpaapi.ListHPAs(userClient, h.cluster.ID, userNamespace.Name, metav1.ListOptions{
					FieldSelector: "metadata.name=" + createdHPA.Name,
				})
				assert.NoError(h.T(), err)
				assert.Equal(h.T(), 1, len(hpaList.Items))
				assert.Equal(h.T(), createdHPA.Name, hpaList.Items[0].Name)

			default:
				log.Infof("As admin, create an HPA")
				createdHPA, _, err := autoscaling.CreateHPA(h.client, h.cluster.ID, namespace.Name, nil, 2, 5, autoscaling.DefaultCPUMetrics())
				assert.NoError(h.T(), err)

				log.Infof("As a %v, list HPAs", tt.role.String())
				hpaList, err := hpaapi.ListHPAs(userClient, h.cluster.ID, namespace.Name, metav1.ListOptions{
					FieldSelector: "metadata.name=" + createdHPA.Name,
				})
				assert.NoError(h.T(), err)
				assert.Equal(h.T(), 1, len(hpaList.Items))
				assert.Equal(h.T(), createdHPA.Name, hpaList.Items[0].Name)
			}
		})
	}
}

func (h *RbacHPATestSuite) TestListHPANegative() {
	subSession := h.session.NewSession()
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
		h.Run("Validate listing HPA in unshared project as user with role "+tt.role.String(), func() {
			log.Info("Create an unshared project and a namespace in the project.")
			_, unsharedNamespace, err := projects.CreateProjectAndNamespaceUsingWrangler(h.client, h.cluster.ID)
			assert.NoError(h.T(), err)

			log.Infof("Create a standard user and add the user to a separate cluster/project role %s", tt.role)
			sharedProject, _, err := projects.CreateProjectAndNamespaceUsingWrangler(h.client, h.cluster.ID)
			assert.NoError(h.T(), err)
			_, userClient, err := rbac.AddUserWithRoleToCluster(h.client, tt.member, tt.role.String(), h.cluster, sharedProject)
			assert.NoError(h.T(), err)

			log.Infof("As admin, create an HPA in the unshared project")
			createdHPA, _, err := autoscaling.CreateHPA(h.client, h.cluster.ID, unsharedNamespace.Name, nil, 2, 5, autoscaling.DefaultCPUMetrics())
			assert.NoError(h.T(), err)

			switch tt.role.String() {
			case rbac.ClusterOwner.String():
				log.Infof("As a %v, list HPAs in the unshared project (should succeed)", tt.role.String())
				hpaList, err := hpaapi.ListHPAs(userClient, h.cluster.ID, unsharedNamespace.Name, metav1.ListOptions{
					FieldSelector: "metadata.name=" + createdHPA.Name,
				})
				assert.NoError(h.T(), err)
				assert.Equal(h.T(), 1, len(hpaList.Items))
				assert.Equal(h.T(), createdHPA.Name, hpaList.Items[0].Name)

			default:
				log.Infof("As a %v, list HPAs in the unshared project (expect 0 results)", tt.role.String())
				hpaList, err := hpaapi.ListHPAs(userClient, h.cluster.ID, unsharedNamespace.Name, metav1.ListOptions{
					FieldSelector: "metadata.name=" + createdHPA.Name,
				})
				// The list itself may succeed but return 0 items, or it may return a forbidden error
				if err == nil {
					assert.Equal(h.T(), 0, len(hpaList.Items))
				} else {
					assert.True(h.T(), errors.IsForbidden(err))
				}
			}
		})
	}
}

func (h *RbacHPATestSuite) TestDynamicHPA() {
	subSession := h.session.NewSession()
	defer subSession.Cleanup()

	roles := map[string]string{
		"cluster-owner":  rbac.ClusterOwner.String(),
		"cluster-member": rbac.ClusterMember.String(),
		"project-owner":  rbac.ProjectOwner.String(),
		"project-member": rbac.ProjectMember.String(),
		"read-only":      rbac.ReadOnly.String(),
	}

	userConfig := new(rbac.Config)
	config.LoadConfig(rbac.ConfigurationFileKey, userConfig)
	if userConfig.Role == "" {
		h.T().Skip("No role configured for dynamic HPA test")
	}

	val, ok := roles[userConfig.Role.String()]
	if !ok {
		h.FailNow("Incorrect usage of roles. Please go through the readme for correct role configurations")
	}

	log.Info("Create a project and a namespace in the project.")
	adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(h.client, h.cluster.ID)
	require.NoError(h.T(), err)

	log.Infof("Setting up user with role %s for dynamic HPA test", val)
	_, userClient, err := rbac.AddUserWithRoleToCluster(h.client, rbac.StandardUser.String(), val, h.cluster, adminProject)
	require.NoError(h.T(), err)

	h.Run("Create HPA", func() {
		createdHPA, workload, err := autoscaling.CreateHPA(userClient, h.cluster.ID, namespace.Name, nil, 2, 5, autoscaling.DefaultCPUMetrics())
		assert.NoError(h.T(), err, "failed to create HPA")
		if err == nil {
			assert.Equal(h.T(), *createdHPA.Spec.MinReplicas, int32(2))
			assert.Equal(h.T(), createdHPA.Spec.MaxReplicas, int32(5))
			err = autoscaling.WaitForDeploymentPodCount(userClient, h.cluster.ID, namespace.Name, workload.Name, 2)
			assert.NoError(h.T(), err, "workload did not scale to minReplicas")
		}
	})

	h.Run("Edit HPA", func() {
		editHPA(h, userClient, namespace.Name)
	})

	h.Run("Delete HPA", func() {
		createdHPA, _, err := autoscaling.CreateHPA(userClient, h.cluster.ID, namespace.Name, nil, 2, 5, autoscaling.DefaultCPUMetrics())
		assert.NoError(h.T(), err)
		if err == nil {
			err = autoscaling.DeleteHPAAndWait(userClient, h.cluster.ID, namespace.Name, createdHPA.Name)
			assert.NoError(h.T(), err, "failed to delete HPA")
		}
	})

	h.Run("List HPA", func() {
		createdHPA, _, err := autoscaling.CreateHPA(userClient, h.cluster.ID, namespace.Name, nil, 2, 5, autoscaling.DefaultCPUMetrics())
		assert.NoError(h.T(), err)
		if err == nil {
			hpaList, err := hpaapi.ListHPAs(userClient, h.cluster.ID, namespace.Name, metav1.ListOptions{
				FieldSelector: "metadata.name=" + createdHPA.Name,
			})
			assert.NoError(h.T(), err)
			assert.Equal(h.T(), 1, len(hpaList.Items))
			assert.Equal(h.T(), createdHPA.Name, hpaList.Items[0].Name)
		}
	})
}

// editHPA is a shared sub-procedure that creates a workload, creates an HPA with memory metrics,
// edits the HPA, and validates the changes take effect.
func editHPA(h *RbacHPATestSuite, client *rancher.Client, namespace string) {
	workload, err := autoscaling.CreateTestWorkload(client, h.cluster.ID, namespace)
	assert.NoError(h.T(), err)

	createdHPA, _, err := autoscaling.CreateHPA(client, h.cluster.ID, namespace, workload, 2, 4, autoscaling.DefaultMemoryMetrics())
	assert.NoError(h.T(), err)

	err = autoscaling.WaitForDeploymentPodCount(client, h.cluster.ID, namespace, workload.Name, 2)
	assert.NoError(h.T(), err, "workload did not scale to initial minReplicas")

	updatedMinReplicas := int32(3)
	updatedMaxReplicas := int32(6)
	updatedHPA := createdHPA.DeepCopy()
	updatedHPA.Spec.MinReplicas = &updatedMinReplicas
	updatedHPA.Spec.MaxReplicas = updatedMaxReplicas

	result, err := hpaapi.UpdateHPA(client, h.cluster.ID, namespace, updatedHPA)
	assert.NoError(h.T(), err, "failed to update HPA")

	err = autoscaling.WaitForHPAActive(client, h.cluster.ID, namespace, createdHPA.Name)
	assert.NoError(h.T(), err, "updated HPA did not become active")

	assert.Equal(h.T(), *result.Spec.MinReplicas, int32(3))
	assert.Equal(h.T(), result.Spec.MaxReplicas, int32(6))

	err = autoscaling.WaitForDeploymentPodCount(client, h.cluster.ID, namespace, workload.Name, 3)
	assert.NoError(h.T(), err, "workload did not scale to updated minReplicas")
}

// verifyEditForbidden creates an HPA as admin and verifies that the given user cannot edit it.
func verifyEditForbidden(h *RbacHPATestSuite, userClient, adminClient *rancher.Client, namespace string) {
	createdHPA, _, err := autoscaling.CreateHPA(adminClient, h.cluster.ID, namespace, nil, 2, 5, autoscaling.DefaultCPUMetrics())
	assert.NoError(h.T(), err)

	updatedHPA := createdHPA.DeepCopy()
	updatedMinReplicas := int32(3)
	updatedMaxReplicas := int32(10)
	updatedHPA.Spec.MinReplicas = &updatedMinReplicas
	updatedHPA.Spec.MaxReplicas = updatedMaxReplicas
	updatedHPA.Spec.Metrics = autoscaling.DefaultCPUMetrics()

	_, err = hpaapi.UpdateHPA(userClient, h.cluster.ID, namespace, updatedHPA)
	assert.Error(h.T(), err)
	assert.True(h.T(), errors.IsForbidden(err))
}

func TestRbacHPATestSuite(t *testing.T) {
	suite.Run(t, new(RbacHPATestSuite))
}
