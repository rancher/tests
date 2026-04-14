//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package clusterandprojectroles

import (
	"net/url"
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
)

type HPATestSuite struct {
	suite.Suite
	session           *session.Session
	client            *rancher.Client
	cluster           *management.Cluster
	steveClient       *v1.Client
	sharedProject     *v3.Project
	sharedNamespace   *corev1.Namespace
	unsharedProject   *v3.Project
	unsharedNamespace *corev1.Namespace
}

func (h *HPATestSuite) TearDownSuite() {
	h.session.Cleanup()
}

func (h *HPATestSuite) SetupSuite() {
	h.session = session.NewSession()

	client, err := rancher.NewClient("", h.session)
	require.NoError(h.T(), err)
	h.client = client

	log.Info("Getting cluster name from the config file")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(h.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(h.client, clusterName)
	require.NoError(h.T(), err, "Error getting cluster ID")
	h.cluster, err = h.client.Management.Cluster.ByID(clusterID)
	require.NoError(h.T(), err)

	h.steveClient, err = h.client.Steve.ProxyDownstream(clusterID)
	require.NoError(h.T(), err)

	log.Info("Creating shared project and namespace for HPA tests")
	h.sharedProject, h.sharedNamespace, err = projects.CreateProjectAndNamespaceUsingWrangler(h.client, clusterID)
	require.NoError(h.T(), err)

	log.Info("Creating unshared project and namespace for negative RBAC tests")
	h.unsharedProject, h.unsharedNamespace, err = projects.CreateProjectAndNamespaceUsingWrangler(h.client, clusterID)
	require.NoError(h.T(), err)
}

func (h *HPATestSuite) getDownstreamSteveClient(userClient *rancher.Client) (*v1.Client, error) {
	return userClient.Steve.ProxyDownstream(h.cluster.ID)
}

func (h *HPATestSuite) setupUserForRole(role rbac.Role, project *v3.Project) (*management.User, *rancher.Client, error) {
	return rbac.AddUserWithRoleToCluster(h.client, rbac.StandardUser.String(), role.String(), h.cluster, project)
}

// TestCreate validates basic HPA creation.
func (h *HPATestSuite) TestCreate() {
	subSession := h.session.NewSession()
	defer subSession.Cleanup()

	hpaResp, workload, err := createHPA(h.client, h.steveClient, h.cluster.ID, h.sharedNamespace.Name, nil)
	require.NoError(h.T(), err)

	hpaObj, err := verifyHPAFields(hpaResp, hpaResp.Name, 2, 5)
	require.NoError(h.T(), err)
	require.Equal(h.T(), hpaSteveType, hpaResp.Type)

	err = waitForDeploymentReplicas(h.client, h.cluster.ID, h.sharedNamespace.Name, workload.Name, *hpaObj.Spec.MinReplicas)
	require.NoError(h.T(), err)

	require.NoError(h.T(), deleteHPA(h.steveClient, hpaResp, h.sharedNamespace.Name))
}

// TestEdit validates HPA update with new min/max replicas.
func (h *HPATestSuite) TestEdit() {
	subSession := h.session.NewSession()
	defer subSession.Cleanup()

	updatedHPA, workload, err := editHPA(h.client, h.steveClient, h.cluster.ID, h.sharedNamespace.Name)
	require.NoError(h.T(), err)

	_, err = verifyHPAFields(updatedHPA, updatedHPA.Name, 3, 6)
	require.NoError(h.T(), err)
	require.Equal(h.T(), hpaSteveType, updatedHPA.Type)

	err = waitForDeploymentReplicas(h.client, h.cluster.ID, h.sharedNamespace.Name, workload.Name, 3)
	require.NoError(h.T(), err)

	require.NoError(h.T(), deleteHPA(h.steveClient, updatedHPA, h.sharedNamespace.Name))
}

// TestDelete validates HPA deletion.
func (h *HPATestSuite) TestDelete() {
	subSession := h.session.NewSession()
	defer subSession.Cleanup()

	hpaResp, _, err := createHPA(h.client, h.steveClient, h.cluster.ID, h.sharedNamespace.Name, nil)
	require.NoError(h.T(), err)

	err = deleteHPA(h.steveClient, hpaResp, h.sharedNamespace.Name)
	require.NoError(h.T(), err)
}

// TestRBACCreate validates HPA creation permissions for each role.
func (h *HPATestSuite) TestRBACCreate() {
	roles := []rbac.Role{rbac.ClusterOwner, rbac.ProjectOwner, rbac.ProjectMember, rbac.ReadOnly, rbac.ClusterMember}

	for _, role := range roles {
		role := role
		h.Run(role.String(), func() {
			subSession := h.session.NewSession()
			defer subSession.Cleanup()

			var userClient *rancher.Client
			var userSteveClient *v1.Client
			var namespaceName string
			var err error

			if role == rbac.ClusterMember {
				_, userClient, err = h.setupUserForRole(role, nil)
				require.NoError(h.T(), err)

				userProject, userNamespace, err := projects.CreateProjectAndNamespaceUsingWrangler(userClient, h.cluster.ID)
				require.NoError(h.T(), err)
				_ = userProject
				namespaceName = userNamespace.Name
			} else {
				_, userClient, err = h.setupUserForRole(role, h.sharedProject)
				require.NoError(h.T(), err)
				namespaceName = h.sharedNamespace.Name
			}

			userSteveClient, err = h.getDownstreamSteveClient(userClient)
			require.NoError(h.T(), err)

			if role == rbac.ReadOnly {
				log.Infof("Verifying that %s cannot create HPA", role)
				_, _, err := createHPA(userClient, userSteveClient, h.cluster.ID, namespaceName, nil)
				require.Error(h.T(), err)
			} else {
				log.Infof("Verifying that %s can create HPA", role)
				hpaResp, _, err := createHPA(userClient, userSteveClient, h.cluster.ID, namespaceName, nil)
				require.NoError(h.T(), err)
				require.NoError(h.T(), deleteHPA(userSteveClient, hpaResp, namespaceName))
			}
		})
	}
}

// TestRBACCreateNegative validates cross-project HPA creation is denied for non-cluster-owners.
func (h *HPATestSuite) TestRBACCreateNegative() {
	roles := []rbac.Role{rbac.ClusterOwner, rbac.ProjectOwner, rbac.ProjectMember, rbac.ReadOnly, rbac.ClusterMember}

	for _, role := range roles {
		role := role
		h.Run(role.String(), func() {
			subSession := h.session.NewSession()
			defer subSession.Cleanup()

			if role == rbac.ClusterOwner {
				log.Info("Verifying cluster-owner can create HPA in unshared project")
				_, userClient, err := h.setupUserForRole(role, nil)
				require.NoError(h.T(), err)
				userSteveClient, err := h.getDownstreamSteveClient(userClient)
				require.NoError(h.T(), err)
				hpaResp, _, err := createHPA(userClient, userSteveClient, h.cluster.ID, h.unsharedNamespace.Name, nil)
				require.NoError(h.T(), err)
				require.NoError(h.T(), deleteHPA(userSteveClient, hpaResp, h.unsharedNamespace.Name))
			} else {
				log.Infof("Verifying that %s cannot create HPA in unshared project", role)
				// Create workload as admin in unshared project
				workload, err := createHPAWorkload(h.client, h.cluster.ID, h.unsharedNamespace.Name)
				require.NoError(h.T(), err)

				_, userClient, err := h.setupUserForRole(role, h.sharedProject)
				require.NoError(h.T(), err)
				userSteveClient, err := h.getDownstreamSteveClient(userClient)
				require.NoError(h.T(), err)

				_, _, err = createHPA(userClient, userSteveClient, h.cluster.ID, h.unsharedNamespace.Name, workload)
				require.Error(h.T(), err)
			}
		})
	}
}

// TestRBACEdit validates HPA edit permissions for each role.
func (h *HPATestSuite) TestRBACEdit() {
	roles := []rbac.Role{rbac.ClusterOwner, rbac.ProjectOwner, rbac.ProjectMember, rbac.ReadOnly, rbac.ClusterMember}

	for _, role := range roles {
		role := role
		h.Run(role.String(), func() {
			subSession := h.session.NewSession()
			defer subSession.Cleanup()

			switch role {
			case rbac.ReadOnly:
				log.Info("Verifying that read-only user cannot edit HPA")
				_, userClient, err := h.setupUserForRole(role, h.sharedProject)
				require.NoError(h.T(), err)
				userSteveClient, err := h.getDownstreamSteveClient(userClient)
				require.NoError(h.T(), err)
				err = verifyEditForbidden(userSteveClient, h.steveClient, h.client, h.cluster.ID, h.sharedNamespace.Name)
				require.NoError(h.T(), err)

			case rbac.ClusterMember:
				log.Info("Verifying that cluster-member can edit HPA in own project")
				_, userClient, err := h.setupUserForRole(role, nil)
				require.NoError(h.T(), err)

				userProject, userNamespace, err := projects.CreateProjectAndNamespaceUsingWrangler(userClient, h.cluster.ID)
				require.NoError(h.T(), err)
				_ = userProject
				userSteveClient, err := h.getDownstreamSteveClient(userClient)
				require.NoError(h.T(), err)

				updatedHPA, _, err := editHPA(userClient, userSteveClient, h.cluster.ID, userNamespace.Name)
				require.NoError(h.T(), err)
				require.NoError(h.T(), deleteHPA(userSteveClient, updatedHPA, userNamespace.Name))

				log.Info("Verifying that cluster-member cannot edit HPA in shared project")
				err = verifyEditForbidden(userSteveClient, h.steveClient, h.client, h.cluster.ID, h.sharedNamespace.Name)
				require.NoError(h.T(), err)

			default:
				log.Infof("Verifying that %s can edit HPA", role)
				_, userClient, err := h.setupUserForRole(role, h.sharedProject)
				require.NoError(h.T(), err)
				userSteveClient, err := h.getDownstreamSteveClient(userClient)
				require.NoError(h.T(), err)
				updatedHPA, _, err := editHPA(userClient, userSteveClient, h.cluster.ID, h.sharedNamespace.Name)
				require.NoError(h.T(), err)
				require.NoError(h.T(), deleteHPA(userSteveClient, updatedHPA, h.sharedNamespace.Name))
			}
		})
	}
}

// TestRBACEditNegative validates cross-project HPA edit is denied for non-cluster-owners.
func (h *HPATestSuite) TestRBACEditNegative() {
	roles := []rbac.Role{rbac.ClusterOwner, rbac.ProjectOwner, rbac.ProjectMember, rbac.ReadOnly, rbac.ClusterMember}

	for _, role := range roles {
		role := role
		h.Run(role.String(), func() {
			subSession := h.session.NewSession()
			defer subSession.Cleanup()

			if role == rbac.ClusterOwner {
				log.Info("Verifying cluster-owner can edit HPA in unshared project")
				_, userClient, err := h.setupUserForRole(role, nil)
				require.NoError(h.T(), err)
				userSteveClient, err := h.getDownstreamSteveClient(userClient)
				require.NoError(h.T(), err)
				updatedHPA, _, err := editHPA(userClient, userSteveClient, h.cluster.ID, h.unsharedNamespace.Name)
				require.NoError(h.T(), err)
				require.NoError(h.T(), deleteHPA(userSteveClient, updatedHPA, h.unsharedNamespace.Name))
			} else {
				log.Infof("Verifying that %s cannot edit HPA in unshared project", role)
				var project *v3.Project
				if role == rbac.ClusterMember {
					project = nil
				} else {
					project = h.sharedProject
				}
				_, userClient, err := h.setupUserForRole(role, project)
				require.NoError(h.T(), err)
				userSteveClient, err := h.getDownstreamSteveClient(userClient)
				require.NoError(h.T(), err)
				err = verifyEditForbidden(userSteveClient, h.steveClient, h.client, h.cluster.ID, h.unsharedNamespace.Name)
				require.NoError(h.T(), err)
			}
		})
	}
}

// TestRBACDelete validates HPA deletion permissions for each role.
func (h *HPATestSuite) TestRBACDelete() {
	roles := []rbac.Role{rbac.ClusterOwner, rbac.ProjectOwner, rbac.ProjectMember, rbac.ReadOnly, rbac.ClusterMember}

	for _, role := range roles {
		role := role
		h.Run(role.String(), func() {
			subSession := h.session.NewSession()
			defer subSession.Cleanup()

			var userClient *rancher.Client
			var userSteveClient *v1.Client
			var namespaceName string
			var err error

			if role == rbac.ClusterMember {
				_, userClient, err = h.setupUserForRole(role, nil)
				require.NoError(h.T(), err)
				userProject, userNamespace, err := projects.CreateProjectAndNamespaceUsingWrangler(userClient, h.cluster.ID)
				require.NoError(h.T(), err)
				_ = userProject
				namespaceName = userNamespace.Name
			} else {
				_, userClient, err = h.setupUserForRole(role, h.sharedProject)
				require.NoError(h.T(), err)
				namespaceName = h.sharedNamespace.Name
			}

			userSteveClient, err = h.getDownstreamSteveClient(userClient)
			require.NoError(h.T(), err)

			if role == rbac.ReadOnly {
				log.Info("Verifying that read-only user cannot delete HPA")
				hpaResp, _, err := createHPA(h.client, h.steveClient, h.cluster.ID, namespaceName, nil)
				require.NoError(h.T(), err)

				err = userSteveClient.SteveType(hpaSteveType).Delete(hpaResp)
				require.Error(h.T(), err)

				// Cleanup as admin
				require.NoError(h.T(), deleteHPA(h.steveClient, hpaResp, namespaceName))
			} else {
				log.Infof("Verifying that %s can delete HPA", role)
				hpaResp, _, err := createHPA(userClient, userSteveClient, h.cluster.ID, namespaceName, nil)
				require.NoError(h.T(), err)
				require.NoError(h.T(), deleteHPA(userSteveClient, hpaResp, namespaceName))
			}
		})
	}
}

// TestRBACDeleteNegative validates cross-project HPA deletion is denied for non-cluster-owners.
func (h *HPATestSuite) TestRBACDeleteNegative() {
	roles := []rbac.Role{rbac.ClusterOwner, rbac.ProjectOwner, rbac.ProjectMember, rbac.ReadOnly, rbac.ClusterMember}

	for _, role := range roles {
		role := role
		h.Run(role.String(), func() {
			subSession := h.session.NewSession()
			defer subSession.Cleanup()

			if role == rbac.ClusterOwner {
				log.Info("Verifying cluster-owner can delete HPA in unshared project")
				_, userClient, err := h.setupUserForRole(role, nil)
				require.NoError(h.T(), err)
				userSteveClient, err := h.getDownstreamSteveClient(userClient)
				require.NoError(h.T(), err)
				hpaResp, _, err := createHPA(userClient, userSteveClient, h.cluster.ID, h.unsharedNamespace.Name, nil)
				require.NoError(h.T(), err)
				require.NoError(h.T(), deleteHPA(userSteveClient, hpaResp, h.unsharedNamespace.Name))
			} else {
				log.Infof("Verifying that %s cannot delete HPA in unshared project", role)
				// Create HPA as admin in unshared project
				hpaResp, _, err := createHPA(h.client, h.steveClient, h.cluster.ID, h.unsharedNamespace.Name, nil)
				require.NoError(h.T(), err)

				var project *v3.Project
				if role == rbac.ClusterMember {
					project = nil
				} else {
					project = h.sharedProject
				}
				_, userClient, err := h.setupUserForRole(role, project)
				require.NoError(h.T(), err)
				userSteveClient, err := h.getDownstreamSteveClient(userClient)
				require.NoError(h.T(), err)

				err = userSteveClient.SteveType(hpaSteveType).Delete(hpaResp)
				require.Error(h.T(), err)

				// Cleanup as admin
				require.NoError(h.T(), deleteHPA(h.steveClient, hpaResp, h.unsharedNamespace.Name))
			}
		})
	}
}

// TestRBACList validates HPA list permissions for each role.
func (h *HPATestSuite) TestRBACList() {
	roles := []rbac.Role{rbac.ClusterOwner, rbac.ProjectOwner, rbac.ProjectMember, rbac.ReadOnly, rbac.ClusterMember}

	for _, role := range roles {
		role := role
		h.Run(role.String(), func() {
			subSession := h.session.NewSession()
			defer subSession.Cleanup()

			var userClient *rancher.Client
			var userSteveClient *v1.Client
			var namespaceName string
			var hpaResp *v1.SteveAPIObject
			var err error

			if role == rbac.ClusterMember {
				_, userClient, err = h.setupUserForRole(role, nil)
				require.NoError(h.T(), err)

				userProject, userNamespace, err := projects.CreateProjectAndNamespaceUsingWrangler(userClient, h.cluster.ID)
				require.NoError(h.T(), err)
				_ = userProject
				namespaceName = userNamespace.Name
				userSteveClient, err = h.getDownstreamSteveClient(userClient)
				require.NoError(h.T(), err)

				hpaResp, _, err = createHPA(userClient, userSteveClient, h.cluster.ID, namespaceName, nil)
				require.NoError(h.T(), err)
			} else {
				// Create HPA as admin in shared project
				hpaResp, _, err = createHPA(h.client, h.steveClient, h.cluster.ID, h.sharedNamespace.Name, nil)
				require.NoError(h.T(), err)
				namespaceName = h.sharedNamespace.Name

				_, userClient, err = h.setupUserForRole(role, h.sharedProject)
				require.NoError(h.T(), err)
				userSteveClient, err = h.getDownstreamSteveClient(userClient)
				require.NoError(h.T(), err)
			}

			log.Infof("Verifying that %s can list HPA", role)
			hpaList, err := userSteveClient.SteveType(hpaSteveType).NamespacedSteveClient(namespaceName).List(url.Values{
				"fieldSelector": {"metadata.name=" + hpaResp.Name},
			})
			require.NoError(h.T(), err)
			require.Len(h.T(), hpaList.Data, 1)
			require.Equal(h.T(), hpaSteveType, hpaList.Data[0].Type)
			require.Equal(h.T(), hpaResp.Name, hpaList.Data[0].Name)

			// Cleanup
			if role == rbac.ClusterMember {
				require.NoError(h.T(), deleteHPA(userSteveClient, hpaResp, namespaceName))
			} else {
				require.NoError(h.T(), deleteHPA(h.steveClient, hpaResp, namespaceName))
			}
		})
	}
}

// TestRBACListNegative validates cross-project HPA listing returns empty for non-cluster-owners.
func (h *HPATestSuite) TestRBACListNegative() {
	roles := []rbac.Role{rbac.ClusterOwner, rbac.ProjectOwner, rbac.ProjectMember, rbac.ReadOnly, rbac.ClusterMember}

	for _, role := range roles {
		role := role
		h.Run(role.String(), func() {
			subSession := h.session.NewSession()
			defer subSession.Cleanup()

			// Create HPA as admin in unshared project
			hpaResp, _, err := createHPA(h.client, h.steveClient, h.cluster.ID, h.unsharedNamespace.Name, nil)
			require.NoError(h.T(), err)

			if role == rbac.ClusterOwner {
				log.Info("Verifying cluster-owner can list HPA in unshared project")
				_, userClient, err := h.setupUserForRole(role, nil)
				require.NoError(h.T(), err)
				userSteveClient, err := h.getDownstreamSteveClient(userClient)
				require.NoError(h.T(), err)

				hpaList, err := userSteveClient.SteveType(hpaSteveType).NamespacedSteveClient(h.unsharedNamespace.Name).List(url.Values{
					"fieldSelector": {"metadata.name=" + hpaResp.Name},
				})
				require.NoError(h.T(), err)
				require.Len(h.T(), hpaList.Data, 1)
				require.Equal(h.T(), hpaSteveType, hpaList.Data[0].Type)
				require.Equal(h.T(), hpaResp.Name, hpaList.Data[0].Name)
			} else {
				log.Infof("Verifying that %s cannot list HPA in unshared project", role)
				var project *v3.Project
				if role == rbac.ClusterMember {
					project = nil
				} else {
					project = h.sharedProject
				}
				_, userClient, err := h.setupUserForRole(role, project)
				require.NoError(h.T(), err)
				userSteveClient, err := h.getDownstreamSteveClient(userClient)
				require.NoError(h.T(), err)

				hpaList, err := userSteveClient.SteveType(hpaSteveType).NamespacedSteveClient(h.unsharedNamespace.Name).List(url.Values{
					"fieldSelector": {"metadata.name=" + hpaResp.Name},
				})
				require.NoError(h.T(), err)
				require.Empty(h.T(), hpaList.Data)
			}

			// Cleanup as admin
			require.NoError(h.T(), deleteHPA(h.steveClient, hpaResp, h.unsharedNamespace.Name))
		})
	}
}

func TestHPATestSuite(t *testing.T) {
	suite.Run(t, new(HPATestSuite))
}
