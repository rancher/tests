//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package clusterandprojectroles

import (
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

// allRBACRoles is the common set of roles tested across all RBAC subtests.
var allRBACRoles = []rbac.Role{
	rbac.ClusterOwner,
	rbac.ProjectOwner,
	rbac.ProjectMember,
	rbac.ReadOnly,
	rbac.ClusterMember,
}

// roleContext holds the user client, Steve client, and namespace prepared for a specific role's subtest.
type roleContext struct {
	userClient      *rancher.Client
	userSteveClient *v1.Client
	namespaceName   string
}

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

// setupUserForRole creates a standard user, assigns the given role scoped to the project
// (or cluster-scoped when project is nil), and returns the user's clients.
func (h *HPATestSuite) setupUserForRole(role rbac.Role, project *v3.Project) (*management.User, *rancher.Client, error) {
	return rbac.AddUserWithRoleToCluster(h.client, rbac.StandardUser.String(), role.String(), h.cluster, project)
}

// setupRoleInSharedProject creates a user with the given role in the shared project and returns
// a roleContext ready for use. For ClusterMember, a new project and namespace are created instead
// since cluster members cannot be scoped to another user's project.
func (h *HPATestSuite) setupRoleInSharedProject(role rbac.Role) roleContext {
	h.T().Helper()

	if role == rbac.ClusterMember {
		_, userClient, err := h.setupUserForRole(role, nil)
		require.NoError(h.T(), err)

		_, userNamespace, err := projects.CreateProjectAndNamespaceUsingWrangler(userClient, h.cluster.ID)
		require.NoError(h.T(), err)

		userSteveClient, err := userClient.Steve.ProxyDownstream(h.cluster.ID)
		require.NoError(h.T(), err)

		return roleContext{
			userClient:      userClient,
			userSteveClient: userSteveClient,
			namespaceName:   userNamespace.Name,
		}
	}

	_, userClient, err := h.setupUserForRole(role, h.sharedProject)
	require.NoError(h.T(), err)

	userSteveClient, err := userClient.Steve.ProxyDownstream(h.cluster.ID)
	require.NoError(h.T(), err)

	return roleContext{
		userClient:      userClient,
		userSteveClient: userSteveClient,
		namespaceName:   h.sharedNamespace.Name,
	}
}

// setupRoleForNegativeTest creates a user with the given role for cross-project (negative) testing.
// Cluster-scoped roles get nil project; project-scoped roles get the shared project.
func (h *HPATestSuite) setupRoleForNegativeTest(role rbac.Role) roleContext {
	h.T().Helper()

	var project *v3.Project
	if role != rbac.ClusterMember {
		project = h.sharedProject
	}

	_, userClient, err := h.setupUserForRole(role, project)
	require.NoError(h.T(), err)

	userSteveClient, err := userClient.Steve.ProxyDownstream(h.cluster.ID)
	require.NoError(h.T(), err)

	return roleContext{
		userClient:      userClient,
		userSteveClient: userSteveClient,
		namespaceName:   h.unsharedNamespace.Name,
	}
}

// ---------------------------------------------------------------------------
// Basic CRUD Tests
// ---------------------------------------------------------------------------

// TestCreate validates that an HPA can be created, its fields match expectations,
// and the target workload scales to minReplicas.
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

// TestEdit validates that an HPA can be updated with new min/max replicas and the
// workload scales to the updated minReplicas.
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

// TestDelete validates that an HPA can be deleted and no longer appears in the API.
func (h *HPATestSuite) TestDelete() {
	subSession := h.session.NewSession()
	defer subSession.Cleanup()

	hpaResp, _, err := createHPA(h.client, h.steveClient, h.cluster.ID, h.sharedNamespace.Name, nil)
	require.NoError(h.T(), err)

	err = deleteHPA(h.steveClient, hpaResp, h.sharedNamespace.Name)
	require.NoError(h.T(), err)
}

// ---------------------------------------------------------------------------
// RBAC Tests — Create
// ---------------------------------------------------------------------------

// TestRBACCreate validates HPA creation permissions in the user's own/shared project.
//   - ClusterOwner, ProjectOwner, ProjectMember, ClusterMember: allowed
//   - ReadOnly: forbidden (403)
func (h *HPATestSuite) TestRBACCreate() {
	for _, role := range allRBACRoles {
		role := role
		h.Run(role.String(), func() {
			subSession := h.session.NewSession()
			defer subSession.Cleanup()

			rc := h.setupRoleInSharedProject(role)

			if role == rbac.ReadOnly {
				log.Infof("Verifying that %s cannot create HPA", role)
				_, _, err := createHPA(rc.userClient, rc.userSteveClient, h.cluster.ID, rc.namespaceName, nil)
				require.Error(h.T(), err)
				return
			}

			log.Infof("Verifying that %s can create HPA", role)
			hpaResp, _, err := createHPA(rc.userClient, rc.userSteveClient, h.cluster.ID, rc.namespaceName, nil)
			require.NoError(h.T(), err)
			require.NoError(h.T(), deleteHPA(rc.userSteveClient, hpaResp, rc.namespaceName))
		})
	}
}

// TestRBACCreateNegative validates cross-project HPA creation.
//   - ClusterOwner: allowed (has cross-project access)
//   - All others: forbidden (403)
func (h *HPATestSuite) TestRBACCreateNegative() {
	for _, role := range allRBACRoles {
		role := role
		h.Run(role.String(), func() {
			subSession := h.session.NewSession()
			defer subSession.Cleanup()

			if role == rbac.ClusterOwner {
				log.Info("Verifying cluster-owner can create HPA in unshared project")
				_, userClient, err := h.setupUserForRole(role, nil)
				require.NoError(h.T(), err)
				userSteveClient, err := userClient.Steve.ProxyDownstream(h.cluster.ID)
				require.NoError(h.T(), err)

				hpaResp, _, err := createHPA(userClient, userSteveClient, h.cluster.ID, h.unsharedNamespace.Name, nil)
				require.NoError(h.T(), err)
				require.NoError(h.T(), deleteHPA(userSteveClient, hpaResp, h.unsharedNamespace.Name))
				return
			}

			log.Infof("Verifying that %s cannot create HPA in unshared project", role)
			// Create workload as admin so the user only needs HPA-create permission
			workload, err := createHPAWorkload(h.client, h.cluster.ID, h.unsharedNamespace.Name)
			require.NoError(h.T(), err)

			rc := h.setupRoleForNegativeTest(role)
			_, _, err = createHPA(rc.userClient, rc.userSteveClient, h.cluster.ID, h.unsharedNamespace.Name, workload)
			require.Error(h.T(), err)
		})
	}
}

// ---------------------------------------------------------------------------
// RBAC Tests — Edit
// ---------------------------------------------------------------------------

// TestRBACEdit validates HPA edit permissions in the user's own/shared project.
//   - ClusterOwner, ProjectOwner, ProjectMember: allowed
//   - ClusterMember: allowed in own project, forbidden in the shared project
//   - ReadOnly: forbidden (403)
func (h *HPATestSuite) TestRBACEdit() {
	for _, role := range allRBACRoles {
		role := role
		h.Run(role.String(), func() {
			subSession := h.session.NewSession()
			defer subSession.Cleanup()

			switch role {
			case rbac.ReadOnly:
				log.Info("Verifying that read-only user cannot edit HPA")
				rc := h.setupRoleInSharedProject(role)
				err := verifyEditForbidden(rc.userSteveClient, h.steveClient, h.client, h.cluster.ID, h.sharedNamespace.Name)
				require.NoError(h.T(), err)

			case rbac.ClusterMember:
				// Cluster members can edit HPAs in their own project...
				log.Info("Verifying that cluster-member can edit HPA in own project")
				rc := h.setupRoleInSharedProject(role)

				updatedHPA, _, err := editHPA(rc.userClient, rc.userSteveClient, h.cluster.ID, rc.namespaceName)
				require.NoError(h.T(), err)
				require.NoError(h.T(), deleteHPA(rc.userSteveClient, updatedHPA, rc.namespaceName))

				// ...but not in another user's project
				log.Info("Verifying that cluster-member cannot edit HPA in shared project")
				err = verifyEditForbidden(rc.userSteveClient, h.steveClient, h.client, h.cluster.ID, h.sharedNamespace.Name)
				require.NoError(h.T(), err)

			default:
				// ClusterOwner, ProjectOwner, ProjectMember
				log.Infof("Verifying that %s can edit HPA", role)
				rc := h.setupRoleInSharedProject(role)

				updatedHPA, _, err := editHPA(rc.userClient, rc.userSteveClient, h.cluster.ID, rc.namespaceName)
				require.NoError(h.T(), err)
				require.NoError(h.T(), deleteHPA(rc.userSteveClient, updatedHPA, rc.namespaceName))
			}
		})
	}
}

// TestRBACEditNegative validates cross-project HPA edit.
//   - ClusterOwner: allowed
//   - All others: forbidden (403)
func (h *HPATestSuite) TestRBACEditNegative() {
	for _, role := range allRBACRoles {
		role := role
		h.Run(role.String(), func() {
			subSession := h.session.NewSession()
			defer subSession.Cleanup()

			if role == rbac.ClusterOwner {
				log.Info("Verifying cluster-owner can edit HPA in unshared project")
				_, userClient, err := h.setupUserForRole(role, nil)
				require.NoError(h.T(), err)
				userSteveClient, err := userClient.Steve.ProxyDownstream(h.cluster.ID)
				require.NoError(h.T(), err)

				updatedHPA, _, err := editHPA(userClient, userSteveClient, h.cluster.ID, h.unsharedNamespace.Name)
				require.NoError(h.T(), err)
				require.NoError(h.T(), deleteHPA(userSteveClient, updatedHPA, h.unsharedNamespace.Name))
				return
			}

			log.Infof("Verifying that %s cannot edit HPA in unshared project", role)
			rc := h.setupRoleForNegativeTest(role)
			err := verifyEditForbidden(rc.userSteveClient, h.steveClient, h.client, h.cluster.ID, h.unsharedNamespace.Name)
			require.NoError(h.T(), err)
		})
	}
}

// ---------------------------------------------------------------------------
// RBAC Tests — Delete
// ---------------------------------------------------------------------------

// TestRBACDelete validates HPA deletion permissions in the user's own/shared project.
//   - ClusterOwner, ProjectOwner, ProjectMember, ClusterMember: allowed
//   - ReadOnly: forbidden (403)
func (h *HPATestSuite) TestRBACDelete() {
	for _, role := range allRBACRoles {
		role := role
		h.Run(role.String(), func() {
			subSession := h.session.NewSession()
			defer subSession.Cleanup()

			rc := h.setupRoleInSharedProject(role)

			if role == rbac.ReadOnly {
				log.Info("Verifying that read-only user cannot delete HPA")
				// Create HPA as admin since read-only cannot create
				hpaResp, _, err := createHPA(h.client, h.steveClient, h.cluster.ID, rc.namespaceName, nil)
				require.NoError(h.T(), err)

				err = rc.userSteveClient.SteveType(hpaSteveType).Delete(hpaResp)
				require.Error(h.T(), err)

				// Cleanup as admin
				require.NoError(h.T(), deleteHPA(h.steveClient, hpaResp, rc.namespaceName))
				return
			}

			log.Infof("Verifying that %s can delete HPA", role)
			hpaResp, _, err := createHPA(rc.userClient, rc.userSteveClient, h.cluster.ID, rc.namespaceName, nil)
			require.NoError(h.T(), err)
			require.NoError(h.T(), deleteHPA(rc.userSteveClient, hpaResp, rc.namespaceName))
		})
	}
}

// TestRBACDeleteNegative validates cross-project HPA deletion.
//   - ClusterOwner: allowed
//   - All others: forbidden (403)
func (h *HPATestSuite) TestRBACDeleteNegative() {
	for _, role := range allRBACRoles {
		role := role
		h.Run(role.String(), func() {
			subSession := h.session.NewSession()
			defer subSession.Cleanup()

			if role == rbac.ClusterOwner {
				log.Info("Verifying cluster-owner can delete HPA in unshared project")
				_, userClient, err := h.setupUserForRole(role, nil)
				require.NoError(h.T(), err)
				userSteveClient, err := userClient.Steve.ProxyDownstream(h.cluster.ID)
				require.NoError(h.T(), err)

				hpaResp, _, err := createHPA(userClient, userSteveClient, h.cluster.ID, h.unsharedNamespace.Name, nil)
				require.NoError(h.T(), err)
				require.NoError(h.T(), deleteHPA(userSteveClient, hpaResp, h.unsharedNamespace.Name))
				return
			}

			log.Infof("Verifying that %s cannot delete HPA in unshared project", role)
			// Create HPA as admin in unshared project
			hpaResp, _, err := createHPA(h.client, h.steveClient, h.cluster.ID, h.unsharedNamespace.Name, nil)
			require.NoError(h.T(), err)

			rc := h.setupRoleForNegativeTest(role)
			err = rc.userSteveClient.SteveType(hpaSteveType).Delete(hpaResp)
			require.Error(h.T(), err)

			// Cleanup as admin
			require.NoError(h.T(), deleteHPA(h.steveClient, hpaResp, h.unsharedNamespace.Name))
		})
	}
}

// ---------------------------------------------------------------------------
// RBAC Tests — List
// ---------------------------------------------------------------------------

// TestRBACList validates that all roles can list an HPA in their own/shared project.
// Every role (including ReadOnly) should see exactly 1 result.
func (h *HPATestSuite) TestRBACList() {
	for _, role := range allRBACRoles {
		role := role
		h.Run(role.String(), func() {
			subSession := h.session.NewSession()
			defer subSession.Cleanup()

			rc := h.setupRoleInSharedProject(role)

			// ClusterMember creates its own HPA; all others use admin-created HPA in the shared namespace
			var hpaResp *v1.SteveAPIObject
			var cleanupSteveClient *v1.Client
			if role == rbac.ClusterMember {
				var err error
				hpaResp, _, err = createHPA(rc.userClient, rc.userSteveClient, h.cluster.ID, rc.namespaceName, nil)
				require.NoError(h.T(), err)
				cleanupSteveClient = rc.userSteveClient
			} else {
				var err error
				hpaResp, _, err = createHPA(h.client, h.steveClient, h.cluster.ID, rc.namespaceName, nil)
				require.NoError(h.T(), err)
				cleanupSteveClient = h.steveClient
			}

			log.Infof("Verifying that %s can list HPA", role)
			hpaList, err := listHPAsByName(rc.userSteveClient, rc.namespaceName, hpaResp.Name)
			require.NoError(h.T(), err)
			require.Len(h.T(), hpaList.Data, 1)
			require.Equal(h.T(), hpaSteveType, hpaList.Data[0].Type)
			require.Equal(h.T(), hpaResp.Name, hpaList.Data[0].Name)

			require.NoError(h.T(), deleteHPA(cleanupSteveClient, hpaResp, rc.namespaceName))
		})
	}
}

// TestRBACListNegative validates cross-project HPA listing.
//   - ClusterOwner: sees 1 result (has cross-project access)
//   - All others: see 0 results (empty list)
func (h *HPATestSuite) TestRBACListNegative() {
	for _, role := range allRBACRoles {
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
				userSteveClient, err := userClient.Steve.ProxyDownstream(h.cluster.ID)
				require.NoError(h.T(), err)

				hpaList, err := listHPAsByName(userSteveClient, h.unsharedNamespace.Name, hpaResp.Name)
				require.NoError(h.T(), err)
				require.Len(h.T(), hpaList.Data, 1)
				require.Equal(h.T(), hpaSteveType, hpaList.Data[0].Type)
				require.Equal(h.T(), hpaResp.Name, hpaList.Data[0].Name)
			} else {
				log.Infof("Verifying that %s cannot list HPA in unshared project", role)
				rc := h.setupRoleForNegativeTest(role)

				hpaList, err := listHPAsByName(rc.userSteveClient, h.unsharedNamespace.Name, hpaResp.Name)
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
