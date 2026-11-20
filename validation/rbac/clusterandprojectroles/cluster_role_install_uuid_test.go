//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package clusterandprojectroles

import (
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extfeaturesapi "github.com/rancher/shepherd/extensions/kubeapi/features"
	extrbacapi "github.com/rancher/shepherd/extensions/kubeapi/rbac"
	extsettingsapi "github.com/rancher/shepherd/extensions/kubeapi/settings"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/rbac"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (rb *ClusterRoleTestSuite) TestRoleTemplateClusterRoleCarriesInstallUUID() {
	aggregated, err := extfeaturesapi.IsFeatureEnabled(rb.client, rbacapi.AggregatedRoleTemplatesFeatureFlag)
	require.NoError(rb.T(), err, "Failed to read the %s feature", rbacapi.AggregatedRoleTemplatesFeatureFlag)
	if aggregated {
		rb.T().Skipf("The %s feature is on, so ClusterRoles are built by the aggregated role template controllers, which do not set the install-uuid owner label", rbacapi.AggregatedRoleTemplatesFeatureFlag)
	}

	subSession := rb.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := rb.client.WithSession(subSession)
	require.NoError(rb.T(), err)

	installUUID, err := extsettingsapi.GetGlobalSettingValue(rb.client, rbacapi.InstallUUIDSetting)
	require.NoError(rb.T(), err, "Failed to read the %s setting", rbacapi.InstallUUIDSetting)
	require.NotEmpty(rb.T(), installUUID, "The %s setting should hold this Rancher install's UUID", rbacapi.InstallUUIDSetting)

	rules := []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}}}
	roleTemplate, err := rbacapi.CreateRoleTemplate(subClient, rbacapi.ClusterContext, rules, nil, false, false, nil)
	require.NoError(rb.T(), err, "Failed to create a cluster role template")

	user, _, err := rbac.SetupUser(subClient, rbac.StandardUser.String())
	require.NoError(rb.T(), err)

	rb.T().Logf("Binding user %s to role template %s on cluster %s", user.ID, roleTemplate.Name, rb.cluster.ID)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(subClient, rb.cluster.ID, user.ID, roleTemplate.Name)
	require.NoError(rb.T(), err)

	require.NoError(rb.T(), rbacapi.WaitForClusterRoleExistence(rb.client, rb.cluster.ID, roleTemplate.Name, true),
		"Rancher should create the ClusterRole for role template %s on cluster %s", roleTemplate.Name, rb.cluster.ID)

	clusterRole, err := extrbacapi.GetClusterRole(rb.client, rb.cluster.ID, roleTemplate.Name)
	require.NoError(rb.T(), err)
	require.Equal(rb.T(), roleTemplate.Name, clusterRole.Annotations[rbacapi.ClusterRoleOwnerAnnotation], "The ClusterRole should name its role template as owner")
	require.Equal(rb.T(), installUUID, clusterRole.Labels[rbacapi.ClusterRoleOwnerInstallUUIDLabel], "The ClusterRole should carry the UUID of the Rancher install that created it")
}

func (rb *ClusterRoleTestSuite) TestOrphanSweepKeepsClusterRoleOfAnotherInstall() {
	aggregated, err := extfeaturesapi.IsFeatureEnabled(rb.client, rbacapi.AggregatedRoleTemplatesFeatureFlag)
	require.NoError(rb.T(), err, "Failed to read the %s feature", rbacapi.AggregatedRoleTemplatesFeatureFlag)
	if aggregated {
		rb.T().Skipf("The %s feature is on, so ClusterRoles are built by the aggregated role template controllers, which do not set the install-uuid owner label", rbacapi.AggregatedRoleTemplatesFeatureFlag)
	}

	subSession := rb.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := rb.client.WithSession(subSession)
	require.NoError(rb.T(), err)

	installUUID, err := extsettingsapi.GetGlobalSettingValue(rb.client, rbacapi.InstallUUIDSetting)
	require.NoError(rb.T(), err, "Failed to read the %s setting", rbacapi.InstallUUIDSetting)
	require.NotEmpty(rb.T(), installUUID, "The %s setting should hold this Rancher install's UUID", rbacapi.InstallUUIDSetting)

	foreign, err := extrbacapi.CreateClusterRole(subClient, rb.cluster.ID, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:        namegen.AppendRandomString("orphan-foreign"),
			Annotations: map[string]string{rbacapi.ClusterRoleOwnerAnnotation: namegen.AppendRandomString("rt-missing")},
			Labels:      map[string]string{rbacapi.ClusterRoleOwnerInstallUUIDLabel: namegen.AppendRandomString("another-install")},
		},
	})
	require.NoError(rb.T(), err, "Failed to create a ClusterRole owned by another install's role template")

	own, err := extrbacapi.CreateClusterRole(subClient, rb.cluster.ID, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:        namegen.AppendRandomString("orphan-own"),
			Annotations: map[string]string{rbacapi.ClusterRoleOwnerAnnotation: namegen.AppendRandomString("rt-missing")},
			Labels:      map[string]string{rbacapi.ClusterRoleOwnerInstallUUIDLabel: installUUID},
		},
	})
	require.NoError(rb.T(), err, "Failed to create a ClusterRole owned by a role template this install does not have")

	require.NoError(rb.T(), rbacapi.WaitForClusterRoleExistence(rb.client, rb.cluster.ID, own.Name, false),
		"The orphan sweep should delete ClusterRole %s, whose role template this install does not have", own.Name)

	require.NoError(rb.T(), rbacapi.VerifyClusterRoleRetained(rb.client, rb.cluster.ID, foreign.Name, defaults.OneMinuteTimeout),
		"The orphan sweep should keep ClusterRole %s, which another Rancher install owns", foreign.Name)
}

func (rb *ClusterRoleTestSuite) TestInstallUUIDLabelCannotBeChangedOrRemovedOnLocalCluster() {
	subSession := rb.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := rb.client.WithSession(subSession)
	require.NoError(rb.T(), err)

	installUUID, err := extsettingsapi.GetGlobalSettingValue(rb.client, rbacapi.InstallUUIDSetting)
	require.NoError(rb.T(), err, "Failed to read the %s setting", rbacapi.InstallUUIDSetting)
	require.NotEmpty(rb.T(), installUUID, "The %s setting should hold this Rancher install's UUID", rbacapi.InstallUUIDSetting)

	labelled, err := extrbacapi.CreateClusterRole(subClient, extclusterapi.LocalCluster, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   namegen.AppendRandomString("install-uuid-labelled"),
			Labels: map[string]string{rbacapi.ClusterRoleOwnerInstallUUIDLabel: installUUID},
		},
	})
	require.NoError(rb.T(), err, "Creating a ClusterRole with the install-uuid label should be allowed")

	changed, err := extrbacapi.GetClusterRole(rb.client, extclusterapi.LocalCluster, labelled.Name)
	require.NoError(rb.T(), err)

	changed.Labels[rbacapi.ClusterRoleOwnerInstallUUIDLabel] = namegen.AppendRandomString("another-install")
	_, err = rb.client.WranglerContext.RBAC.ClusterRole().Update(changed)
	require.Error(rb.T(), err, "The webhook on the local cluster should reject changing the install-uuid label")
	require.True(rb.T(), apierrors.IsBadRequest(err), "Changing the install-uuid label should be rejected as a bad request, got: %v", err)

	removed, err := extrbacapi.GetClusterRole(rb.client, extclusterapi.LocalCluster, labelled.Name)
	require.NoError(rb.T(), err)

	delete(removed.Labels, rbacapi.ClusterRoleOwnerInstallUUIDLabel)
	_, err = rb.client.WranglerContext.RBAC.ClusterRole().Update(removed)
	require.Error(rb.T(), err, "The webhook on the local cluster should reject removing the install-uuid label")
	require.True(rb.T(), apierrors.IsBadRequest(err), "Removing the install-uuid label should be rejected as a bad request, got: %v", err)

	current, err := extrbacapi.GetClusterRole(rb.client, extclusterapi.LocalCluster, labelled.Name)
	require.NoError(rb.T(), err)
	require.Equal(rb.T(), installUUID, current.Labels[rbacapi.ClusterRoleOwnerInstallUUIDLabel], "The install-uuid label should be unchanged after both rejected updates")
}
