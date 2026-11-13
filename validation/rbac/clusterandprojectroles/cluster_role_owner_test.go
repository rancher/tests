//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package clusterandprojectroles

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extfeaturesapi "github.com/rancher/shepherd/extensions/kubeapi/features"
	extrbacapi "github.com/rancher/shepherd/extensions/kubeapi/rbac"
	extsettingsapi "github.com/rancher/shepherd/extensions/kubeapi/settings"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	actionsClusters "github.com/rancher/tests/actions/clusters"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/rbac"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ClusterRoleOwnerTestSuite struct {
	suite.Suite
	client    *rancher.Client
	session   *session.Session
	clusterID string
}

func (cro *ClusterRoleOwnerTestSuite) TearDownSuite() {
	cro.session.Cleanup()
}

func (cro *ClusterRoleOwnerTestSuite) SetupSuite() {
	cro.session = session.NewSession()

	client, err := rancher.NewClient("", cro.session)
	require.NoError(cro.T(), err)
	cro.client = client

	hasFix, err := actionsClusters.IsRancherVersionAbove(cro.client, "v2.16.0-0")
	require.NoError(cro.T(), err, "Failed to read the Rancher server version")
	if !hasFix {
		cro.T().Skip("Known issue: https://github.com/rancher/rancher/issues/51011")
	}

	aggregated, err := extfeaturesapi.IsFeatureEnabled(cro.client, rbacapi.AggregatedRoleTemplatesFeatureFlag)
	require.NoError(cro.T(), err, "Failed to read the %s feature", rbacapi.AggregatedRoleTemplatesFeatureFlag)
	if aggregated {
		cro.T().Skipf("The %s feature is on, so ClusterRoles are built by the aggregated role template controllers, which do not set the install-uuid owner label", rbacapi.AggregatedRoleTemplatesFeatureFlag)
	}

	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(cro.T(), clusterName, "Cluster name should be set")
	cro.clusterID, err = clusters.GetClusterIDByName(cro.client, clusterName)
	require.NoError(cro.T(), err, "Error getting cluster ID for cluster: %s", clusterName)
}

func (cro *ClusterRoleOwnerTestSuite) TestRoleTemplateClusterRoleCarriesInstallUUID() {
	subSession := cro.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := cro.client.WithSession(subSession)
	require.NoError(cro.T(), err)

	installUUID, err := extsettingsapi.GetGlobalSettingValue(cro.client, rbacapi.InstallUUIDSetting)
	require.NoError(cro.T(), err, "Failed to read the %s setting", rbacapi.InstallUUIDSetting)
	require.NotEmpty(cro.T(), installUUID, "The %s setting should hold this Rancher install's UUID", rbacapi.InstallUUIDSetting)

	rules := []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}}}
	roleTemplate, err := rbacapi.CreateRoleTemplate(subClient, rbacapi.ClusterContext, rules, nil, false, false, nil)
	require.NoError(cro.T(), err, "Failed to create a cluster role template")

	user, _, err := rbac.SetupUser(subClient, rbac.StandardUser.String())
	require.NoError(cro.T(), err)

	cro.T().Logf("Binding user %s to role template %s on cluster %s", user.ID, roleTemplate.Name, cro.clusterID)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(subClient, cro.clusterID, user.ID, roleTemplate.Name)
	require.NoError(cro.T(), err)

	require.NoError(cro.T(), rbacapi.WaitForClusterRoleExistence(cro.client, cro.clusterID, roleTemplate.Name, true),
		"Rancher should create the ClusterRole for role template %s on cluster %s", roleTemplate.Name, cro.clusterID)

	clusterContext, err := extclusterapi.GetClusterWranglerContext(cro.client, cro.clusterID)
	require.NoError(cro.T(), err)

	clusterRole, err := clusterContext.RBAC.ClusterRole().Get(roleTemplate.Name, metav1.GetOptions{})
	require.NoError(cro.T(), err)
	require.Equal(cro.T(), roleTemplate.Name, clusterRole.Annotations[rbacapi.ClusterRoleOwnerAnnotation], "The ClusterRole should name its role template as owner")
	require.Equal(cro.T(), installUUID, clusterRole.Labels[rbacapi.ClusterRoleOwnerInstallUUIDLabel], "The ClusterRole should carry the UUID of the Rancher install that created it")
}

func (cro *ClusterRoleOwnerTestSuite) TestOrphanSweepKeepsClusterRoleOfAnotherInstall() {
	subSession := cro.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := cro.client.WithSession(subSession)
	require.NoError(cro.T(), err)

	installUUID, err := extsettingsapi.GetGlobalSettingValue(cro.client, rbacapi.InstallUUIDSetting)
	require.NoError(cro.T(), err, "Failed to read the %s setting", rbacapi.InstallUUIDSetting)
	require.NotEmpty(cro.T(), installUUID, "The %s setting should hold this Rancher install's UUID", rbacapi.InstallUUIDSetting)

	foreign, err := extrbacapi.CreateClusterRole(subClient, cro.clusterID, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:        namegen.AppendRandomString("orphan-foreign"),
			Annotations: map[string]string{rbacapi.ClusterRoleOwnerAnnotation: namegen.AppendRandomString("rt-missing")},
			Labels:      map[string]string{rbacapi.ClusterRoleOwnerInstallUUIDLabel: namegen.AppendRandomString("another-install")},
		},
	})
	require.NoError(cro.T(), err, "Failed to create a ClusterRole owned by another install's role template")

	own, err := extrbacapi.CreateClusterRole(subClient, cro.clusterID, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:        namegen.AppendRandomString("orphan-own"),
			Annotations: map[string]string{rbacapi.ClusterRoleOwnerAnnotation: namegen.AppendRandomString("rt-missing")},
			Labels:      map[string]string{rbacapi.ClusterRoleOwnerInstallUUIDLabel: installUUID},
		},
	})
	require.NoError(cro.T(), err, "Failed to create a ClusterRole owned by a role template this install does not have")

	require.NoError(cro.T(), rbacapi.WaitForClusterRoleExistence(cro.client, cro.clusterID, own.Name, false),
		"The orphan sweep should delete ClusterRole %s, whose role template this install does not have", own.Name)

	require.NoError(cro.T(), rbacapi.VerifyClusterRoleRetained(cro.client, cro.clusterID, foreign.Name, defaults.OneMinuteTimeout),
		"The orphan sweep should keep ClusterRole %s, which another Rancher install owns", foreign.Name)
}

func (cro *ClusterRoleOwnerTestSuite) TestInstallUUIDLabelCannotBeChangedOrRemovedOnLocalCluster() {
	subSession := cro.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := cro.client.WithSession(subSession)
	require.NoError(cro.T(), err)

	installUUID, err := extsettingsapi.GetGlobalSettingValue(cro.client, rbacapi.InstallUUIDSetting)
	require.NoError(cro.T(), err, "Failed to read the %s setting", rbacapi.InstallUUIDSetting)
	require.NotEmpty(cro.T(), installUUID, "The %s setting should hold this Rancher install's UUID", rbacapi.InstallUUIDSetting)

	labelled, err := extrbacapi.CreateClusterRole(subClient, extclusterapi.LocalCluster, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   namegen.AppendRandomString("install-uuid-labelled"),
			Labels: map[string]string{rbacapi.ClusterRoleOwnerInstallUUIDLabel: installUUID},
		},
	})
	require.NoError(cro.T(), err, "Creating a ClusterRole with the install-uuid label should be allowed")

	clusterContext, err := extclusterapi.GetClusterWranglerContext(cro.client, extclusterapi.LocalCluster)
	require.NoError(cro.T(), err)

	changed := labelled.DeepCopy()
	changed.Labels[rbacapi.ClusterRoleOwnerInstallUUIDLabel] = namegen.AppendRandomString("another-install")
	_, err = clusterContext.RBAC.ClusterRole().Update(changed)
	require.Error(cro.T(), err, "The webhook on the local cluster should reject changing the install-uuid label")
	require.True(cro.T(), apierrors.IsBadRequest(err), "Changing the install-uuid label should be rejected as a bad request, got: %v", err)

	removed := labelled.DeepCopy()
	delete(removed.Labels, rbacapi.ClusterRoleOwnerInstallUUIDLabel)
	_, err = clusterContext.RBAC.ClusterRole().Update(removed)
	require.Error(cro.T(), err, "The webhook on the local cluster should reject removing the install-uuid label")
	require.True(cro.T(), apierrors.IsBadRequest(err), "Removing the install-uuid label should be rejected as a bad request, got: %v", err)

	current, err := clusterContext.RBAC.ClusterRole().Get(labelled.Name, metav1.GetOptions{})
	require.NoError(cro.T(), err)
	require.Equal(cro.T(), installUUID, current.Labels[rbacapi.ClusterRoleOwnerInstallUUIDLabel], "The install-uuid label should be unchanged after both rejected updates")
}

func TestClusterRoleOwnerTestSuite(t *testing.T) {
	suite.Run(t, new(ClusterRoleOwnerTestSuite))
}
