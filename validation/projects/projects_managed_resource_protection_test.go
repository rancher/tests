//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package projects

import (
	"context"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extnamespaceapi "github.com/rancher/shepherd/extensions/kubeapi/namespaces"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/shepherd/pkg/wrangler"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	deploymentsapi "github.com/rancher/tests/actions/kubeapi/workloads/deployments"
	podapi "github.com/rancher/tests/actions/kubeapi/workloads/pods"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	k8sError "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	forbiddenChangeMessage  = "users are forbidden from changing resources managed by Rancher"
	forbiddenDeleteMessage  = "users are forbidden from deleting resources managed by Rancher"
	forbiddenCreateMessage  = "users are forbidden from creating resources managed by Rancher. Remove the marker label"
	forbiddenPromoteMessage = "users are forbidden from promoting resources to Rancher management. Remove the marker label"

	projectQuotaLimitsCPU      = "2000m"
	projectQuotaLimitsMemory   = "2Gi"
	namespaceQuotaLimitsCPU    = "2000m"
	namespaceQuotaLimitsMemory = "2Gi"
	containerLimitsCPU         = "1000m"
	containerLimitsMemory      = "1Gi"
	containerRequestsCPU       = "500m"
	containerRequestsMemory    = "512Mi"
)

type ProjectsManagedResourceProtectionTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (p *ProjectsManagedResourceProtectionTestSuite) TearDownSuite() {
	p.session.Cleanup()
}

func (p *ProjectsManagedResourceProtectionTestSuite) SetupSuite() {
	p.session = session.NewSession()

	client, err := rancher.NewClient("", p.session)
	require.NoError(p.T(), err)
	p.client = client

	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(p.T(), clusterName, "Downstream cluster name should be set in the config file")
	clusterID, err := clusters.GetClusterIDByName(p.client, clusterName)
	require.NoError(p.T(), err, "Error getting downstream cluster ID")
	p.cluster, err = p.client.Management.Cluster.ByID(clusterID)
	require.NoError(p.T(), err)
}

func (p *ProjectsManagedResourceProtectionTestSuite) setupProjectWithManagedResources() (*rancher.Client, *wrangler.Context, string, *corev1.ResourceQuota, *corev1.LimitRange) {
	log.Info("Create a project with resource quota and container default limits, and a namespace in the project.")
	projectTemplate := projectapi.NewProjectTemplate(p.cluster.ID)
	projectTemplate.Spec.ResourceQuota.Limit.LimitsCPU = projectQuotaLimitsCPU
	projectTemplate.Spec.ResourceQuota.Limit.LimitsMemory = projectQuotaLimitsMemory
	projectTemplate.Spec.NamespaceDefaultResourceQuota.Limit.LimitsCPU = namespaceQuotaLimitsCPU
	projectTemplate.Spec.NamespaceDefaultResourceQuota.Limit.LimitsMemory = namespaceQuotaLimitsMemory
	projectTemplate.Spec.ContainerDefaultResourceLimit.LimitsCPU = containerLimitsCPU
	projectTemplate.Spec.ContainerDefaultResourceLimit.LimitsMemory = containerLimitsMemory
	projectTemplate.Spec.ContainerDefaultResourceLimit.RequestsCPU = containerRequestsCPU
	projectTemplate.Spec.ContainerDefaultResourceLimit.RequestsMemory = containerRequestsMemory

	containerDefaultLimit := namespaceapi.ContainerDefaultResourceLimit(containerLimitsCPU, containerLimitsMemory, containerRequestsCPU, containerRequestsMemory)
	createdProject, namespace, err := projectapi.CreateProjectWithTemplateAndNamespaceContainerLimit(p.client, p.cluster.ID, projectTemplate, containerDefaultLimit)
	require.NoError(p.T(), err)

	log.Info("Create a standard user and add the user to the project as project owner.")
	standardUser, standardUserClient, err := rbac.AddUserWithRoleToCluster(p.client, rbac.StandardUser.String(), rbac.ProjectOwner.String(), p.cluster, createdProject)
	require.NoError(p.T(), err, "Failed to add the user %s as a project owner to the project %s", standardUser.Name, createdProject.Name)
	standardUserContext, err := extclusterapi.GetClusterWranglerContext(standardUserClient, p.cluster.ID)
	require.NoError(p.T(), err)

	log.Info("Grant the project owner permission to manage the ResourceQuota and LimitRange so requests reach the Rancher webhook rather than being denied by RBAC.")
	err = rbacapi.GrantResourceQuotaAndLimitRangeManagement(p.client, p.cluster.ID, standardUser.ID, namespace.Name)
	require.NoError(p.T(), err, "Failed to grant the user management access to ResourceQuotas and LimitRanges")

	log.Infof("Wait for the Rancher-managed ResourceQuota and LimitRange to be created in namespace %q.", namespace.Name)
	managedRQ, err := namespaceapi.WaitForManagedResourceQuota(p.client, p.cluster.ID, namespace.Name)
	require.NoError(p.T(), err, "Rancher-managed ResourceQuota was not created in the namespace")
	require.True(p.T(), resource.MustParse(namespaceQuotaLimitsCPU).Equal(managedRQ.Spec.Hard[corev1.ResourceLimitsCPU]), "managed ResourceQuota limits.cpu should be %s", namespaceQuotaLimitsCPU)
	require.True(p.T(), resource.MustParse(namespaceQuotaLimitsMemory).Equal(managedRQ.Spec.Hard[corev1.ResourceLimitsMemory]), "managed ResourceQuota limits.memory should be %s", namespaceQuotaLimitsMemory)

	managedLR, err := namespaceapi.WaitForManagedLimitRange(p.client, p.cluster.ID, namespace.Name)
	require.NoError(p.T(), err, "Rancher-managed LimitRange was not created in the namespace")
	require.NotEmpty(p.T(), managedLR.Spec.Limits, "Expected the managed LimitRange to have at least one limit item")
	require.True(p.T(), resource.MustParse(containerLimitsCPU).Equal(managedLR.Spec.Limits[0].Default[corev1.ResourceCPU]), "managed LimitRange default cpu should be %s", containerLimitsCPU)
	require.True(p.T(), resource.MustParse(containerLimitsMemory).Equal(managedLR.Spec.Limits[0].Default[corev1.ResourceMemory]), "managed LimitRange default memory should be %s", containerLimitsMemory)

	log.Info("Verify the namespace has the Rancher resourceQuota and containerDefaultResourceLimit annotations.")
	err = namespaceapi.VerifyAnnotationExistsInNamespace(p.client, p.cluster.ID, namespace.Name, namespaceapi.ResourceQuotaAnnotation, true)
	require.NoError(p.T(), err, "The namespace should have the %s annotation", namespaceapi.ResourceQuotaAnnotation)
	err = namespaceapi.VerifyAnnotationExistsInNamespace(p.client, p.cluster.ID, namespace.Name, namespaceapi.ContainerDefaultResourceLimitAnnotation, true)
	require.NoError(p.T(), err, "The namespace should have the %s annotation", namespaceapi.ContainerDefaultResourceLimitAnnotation)

	return standardUserClient, standardUserContext, namespace.Name, managedRQ, managedLR
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestUpdateManagedResourceQuotaIsRejected() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	_, userContext, namespace, managedRQ, _ := p.setupProjectWithManagedResources()

	log.Info("As the project owner, attempt to update the Rancher-managed ResourceQuota to raise its limits.")
	managedRQ.Spec.Hard[corev1.ResourceLimitsCPU] = resource.MustParse("4")
	managedRQ.Spec.Hard[corev1.ResourceLimitsMemory] = resource.MustParse("4Gi")
	_, err := userContext.Core.ResourceQuota().Update(managedRQ)
	require.Error(p.T(), err, "Expected the update to the managed ResourceQuota to be rejected")
	require.Contains(p.T(), err.Error(), forbiddenChangeMessage)
	require.True(p.T(), k8sError.IsBadRequest(err), "Expected the webhook rejection to return a 400 BadRequest status code")

	log.Info("Verify the Rancher-managed ResourceQuota still exists and is unchanged.")
	currentRQ, err := userContext.Core.ResourceQuota().Get(namespace, managedRQ.Name, metav1.GetOptions{})
	require.NoError(p.T(), err)
	expectedCPU := resource.MustParse(namespaceQuotaLimitsCPU)
	currentCPU := currentRQ.Spec.Hard[corev1.ResourceLimitsCPU]
	require.True(p.T(), expectedCPU.Equal(currentCPU), "expected the managed ResourceQuota CPU limit to remain %s, got %s", expectedCPU.String(), currentCPU.String())
	expectedMemory := resource.MustParse(namespaceQuotaLimitsMemory)
	currentMemory := currentRQ.Spec.Hard[corev1.ResourceLimitsMemory]
	require.True(p.T(), expectedMemory.Equal(currentMemory), "expected the managed ResourceQuota memory limit to remain %s, got %s", expectedMemory.String(), currentMemory.String())
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestDeleteManagedResourceQuotaIsRejected() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	_, userContext, namespace, managedRQ, _ := p.setupProjectWithManagedResources()

	log.Info("As the project owner, attempt to delete the Rancher-managed ResourceQuota.")
	err := userContext.Core.ResourceQuota().Delete(namespace, managedRQ.Name, &metav1.DeleteOptions{})
	require.Error(p.T(), err, "Expected the delete of the managed ResourceQuota to be rejected")
	require.Contains(p.T(), err.Error(), forbiddenDeleteMessage)
	require.True(p.T(), k8sError.IsBadRequest(err), "Expected the webhook rejection to return a 400 BadRequest status code")

	log.Info("Verify the Rancher-managed ResourceQuota still exists.")
	_, err = userContext.Core.ResourceQuota().Get(namespace, managedRQ.Name, metav1.GetOptions{})
	require.NoError(p.T(), err, "The managed ResourceQuota should still exist after the rejected delete")
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestCreateResourceQuotaWithMarkerLabelIsRejected() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	_, userContext, namespace, _, _ := p.setupProjectWithManagedResources()

	log.Info("As the project owner, attempt to create a ResourceQuota having the Rancher-managed marker label.")
	markedRQ := namespaceapi.NewResourceQuota(namespace, namegen.AppendRandomString("managed-rq-"), "4", "4Gi", true)
	_, err := userContext.Core.ResourceQuota().Create(markedRQ)
	require.Error(p.T(), err, "Expected the create of a ResourceQuota with the marker label to be rejected")
	require.Contains(p.T(), err.Error(), forbiddenCreateMessage)
	require.True(p.T(), k8sError.IsBadRequest(err), "Expected the webhook rejection to return a 400 BadRequest status code")

	log.Info("Verify the same ResourceQuota can be created once the marker label is removed and that it is not Rancher-managed.")
	unmarkedRQ := namespaceapi.NewResourceQuota(namespace, markedRQ.Name, "4", "4Gi", false)
	createdRQ, err := userContext.Core.ResourceQuota().Create(unmarkedRQ)
	require.NoError(p.T(), err, "Expected the create of an unmarked ResourceQuota to succeed")
	require.NotEqual(p.T(), "true", createdRQ.Labels[namespaceapi.ManagedResourceQuotaLabel])
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestPromoteUnmanagedResourceQuotaIsRejected() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	_, userContext, namespace, _, _ := p.setupProjectWithManagedResources()

	log.Info("As the project owner, create an unmanaged ResourceQuota.")
	created, err := userContext.Core.ResourceQuota().Create(namespaceapi.NewResourceQuota(namespace, namegen.AppendRandomString("user-rq-"), "1", "1Gi", false))
	require.NoError(p.T(), err)

	log.Info("Attempt to promote the unmanaged ResourceQuota by adding the Rancher-managed marker label.")
	latest, err := userContext.Core.ResourceQuota().Get(namespace, created.Name, metav1.GetOptions{})
	require.NoError(p.T(), err)

	if latest.Labels == nil {
		latest.Labels = map[string]string{}
	}

	latest.Labels[namespaceapi.ManagedResourceQuotaLabel] = "true"
	_, err = userContext.Core.ResourceQuota().Update(latest)
	require.Error(p.T(), err, "Expected the promotion of an unmanaged ResourceQuota to be rejected")
	require.Contains(p.T(), err.Error(), forbiddenPromoteMessage)
	require.True(p.T(), k8sError.IsBadRequest(err), "Expected the webhook rejection to return a 400 BadRequest status code")

	log.Info("Verify the ResourceQuota remains unmanaged.")
	current, err := userContext.Core.ResourceQuota().Get(namespace, created.Name, metav1.GetOptions{})
	require.NoError(p.T(), err)
	require.NotEqual(p.T(), "true", current.Labels[namespaceapi.ManagedResourceQuotaLabel], "The ResourceQuota must remain unmanaged")
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestDemoteManagedResourceQuotaIsRejected() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	_, userContext, namespace, managedRQ, _ := p.setupProjectWithManagedResources()

	log.Info("As the project owner, attempt to demote the Rancher-managed ResourceQuota by removing the marker label.")
	delete(managedRQ.Labels, namespaceapi.ManagedResourceQuotaLabel)
	_, err := userContext.Core.ResourceQuota().Update(managedRQ)
	require.Error(p.T(), err, "Expected the demotion of the managed ResourceQuota to be rejected")
	require.Contains(p.T(), err.Error(), forbiddenChangeMessage)
	require.True(p.T(), k8sError.IsBadRequest(err), "Expected the webhook rejection to return a 400 BadRequest status code")

	log.Info("Verify the Rancher-managed marker is still present on the ResourceQuota.")
	currentRQ, err := userContext.Core.ResourceQuota().Get(namespace, managedRQ.Name, metav1.GetOptions{})
	require.NoError(p.T(), err, "The managed ResourceQuota should still exist")
	require.Equal(p.T(), "true", currentRQ.Labels[namespaceapi.ManagedResourceQuotaLabel], "The managed ResourceQuota should still carry the marker label")
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestUserHasFullAccessToUnmanagedResourceQuota() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	_, userContext, namespace, managedRQ, _ := p.setupProjectWithManagedResources()

	log.Info("As the project owner, create an unmanaged ResourceQuota.")
	created, err := userContext.Core.ResourceQuota().Create(namespaceapi.NewResourceQuota(namespace, namegen.AppendRandomString("user-rq-"), "1", "1Gi", false))
	require.NoError(p.T(), err)
	require.NotEqual(p.T(), "true", created.Labels[namespaceapi.ManagedResourceQuotaLabel], "The user-created ResourceQuota must not carry the Rancher-managed marker")

	log.Info("Update the unmanaged ResourceQuota and verify the change is accepted.")
	latestRQ, err := userContext.Core.ResourceQuota().Get(namespace, created.Name, metav1.GetOptions{})
	require.NoError(p.T(), err)
	latestRQ.Spec.Hard[corev1.ResourceLimitsCPU] = resource.MustParse("2")
	latestRQ.Spec.Hard[corev1.ResourceLimitsMemory] = resource.MustParse("2Gi")
	_, err = userContext.Core.ResourceQuota().Update(latestRQ)
	require.NoError(p.T(), err)

	log.Info("Verify the unmanaged ResourceQuota reflects the updated limits.")
	updated, err := userContext.Core.ResourceQuota().Get(namespace, created.Name, metav1.GetOptions{})
	require.NoError(p.T(), err)
	updatedCPU := updated.Spec.Hard[corev1.ResourceLimitsCPU]
	expectedCPU := resource.MustParse("2")
	require.Equal(p.T(), 0, expectedCPU.Cmp(updatedCPU), "Expected the unmanaged ResourceQuota CPU limit to be updated to 2")

	updatedMemory := updated.Spec.Hard[corev1.ResourceLimitsMemory]
	expectedMemory := resource.MustParse("2Gi")
	require.Equal(p.T(), 0, expectedMemory.Cmp(updatedMemory), "Expected the unmanaged ResourceQuota Memory limit to be updated to 2Gi")

	log.Info("Delete the unmanaged ResourceQuota and verify the delete is accepted.")
	err = userContext.Core.ResourceQuota().Delete(namespace, updated.Name, &metav1.DeleteOptions{})
	require.NoError(p.T(), err, "Expected the delete of the unmanaged ResourceQuota to succeed")

	log.Info("Verify the Rancher-managed ResourceQuota remains present and unchanged.")
	_, err = userContext.Core.ResourceQuota().Get(namespace, managedRQ.Name, metav1.GetOptions{})
	require.NoError(p.T(), err, "The managed ResourceQuota should still exist")
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestUpdateManagedLimitRangeIsRejected() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	_, userContext, namespace, _, managedLR := p.setupProjectWithManagedResources()

	log.Info("As the project owner, attempt to update the Rancher-managed LimitRange defaults.")
	require.NotEmpty(p.T(), managedLR.Spec.Limits, "Expected the managed LimitRange to have at least one limit item")
	managedLR.Spec.Limits[0].Default[corev1.ResourceCPU] = resource.MustParse("4")
	managedLR.Spec.Limits[0].Default[corev1.ResourceMemory] = resource.MustParse("4Gi")
	_, err := userContext.Core.LimitRange().Update(managedLR)
	require.Error(p.T(), err, "Expected the update to the managed LimitRange to be rejected")
	require.Contains(p.T(), err.Error(), forbiddenChangeMessage)
	require.True(p.T(), k8sError.IsBadRequest(err), "Expected the webhook rejection to return a 400 BadRequest status code")

	log.Info("Verify the Rancher-managed LimitRange still exists, its defaults are unchanged, and the marker label remains.")
	currentLR, err := userContext.Core.LimitRange().Get(namespace, managedLR.Name, metav1.GetOptions{})
	require.NoError(p.T(), err, "The managed LimitRange should still exist")
	require.NotEmpty(p.T(), currentLR.Spec.Limits, "Expected the managed LimitRange to have at least one limit item")
	require.True(p.T(), resource.MustParse(containerLimitsCPU).Equal(currentLR.Spec.Limits[0].Default[corev1.ResourceCPU]), "managed LimitRange default cpu should remain %s", containerLimitsCPU)
	require.True(p.T(), resource.MustParse(containerLimitsMemory).Equal(currentLR.Spec.Limits[0].Default[corev1.ResourceMemory]), "managed LimitRange default memory should remain %s", containerLimitsMemory)
	require.Equal(p.T(), "true", currentLR.Labels[namespaceapi.ManagedResourceQuotaLabel], "The managed LimitRange should still carry the marker label")
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestDeleteManagedLimitRangeIsRejected() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	_, userContext, namespace, _, managedLR := p.setupProjectWithManagedResources()

	log.Info("As the project owner, attempt to delete the Rancher-managed LimitRange.")
	err := userContext.Core.LimitRange().Delete(namespace, managedLR.Name, &metav1.DeleteOptions{})
	require.Error(p.T(), err, "Expected the delete of the managed LimitRange to be rejected")
	require.Contains(p.T(), err.Error(), forbiddenDeleteMessage)
	require.True(p.T(), k8sError.IsBadRequest(err), "Expected the webhook rejection to return a 400 BadRequest status code")

	log.Info("Verify the Rancher-managed LimitRange still exists.")
	_, err = userContext.Core.LimitRange().Get(namespace, managedLR.Name, metav1.GetOptions{})
	require.NoError(p.T(), err, "The managed LimitRange should still exist after the rejected delete")
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestCreateLimitRangeWithMarkerLabelIsRejected() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	_, userContext, namespace, _, _ := p.setupProjectWithManagedResources()

	log.Info("As the project owner, attempt to create a LimitRange having the Rancher-managed marker label.")
	markedLR := namespaceapi.NewLimitRange(namespace, namegen.AppendRandomString("managed-lr-"), true)
	_, err := userContext.Core.LimitRange().Create(markedLR)
	require.Error(p.T(), err, "Expected the create of a LimitRange with the marker label to be rejected")
	require.Contains(p.T(), err.Error(), forbiddenCreateMessage)
	require.True(p.T(), k8sError.IsBadRequest(err), "Expected the webhook rejection to return a 400 BadRequest status code")

	log.Info("Verify the same LimitRange can be created once the marker label is removed and that it is not Rancher-managed.")
	unmarkedLR := namespaceapi.NewLimitRange(namespace, markedLR.Name, false)
	createdLR, err := userContext.Core.LimitRange().Create(unmarkedLR)
	require.NoError(p.T(), err, "Expected the create of an unmarked LimitRange to succeed")
	require.NotEqual(p.T(), "true", createdLR.Labels[namespaceapi.ManagedResourceQuotaLabel], "The user-created LimitRange must not carry the Rancher-managed marker")
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestPromoteUnmanagedLimitRangeIsRejected() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	_, userContext, namespace, _, _ := p.setupProjectWithManagedResources()

	log.Info("As the project owner, create an unmanaged LimitRange.")
	created, err := userContext.Core.LimitRange().Create(namespaceapi.NewLimitRange(namespace, namegen.AppendRandomString("user-lr-"), false))
	require.NoError(p.T(), err)

	log.Info("Attempt to promote the unmanaged LimitRange by adding the Rancher-managed marker label.")
	created.Labels = map[string]string{namespaceapi.ManagedResourceQuotaLabel: "true"}
	_, err = userContext.Core.LimitRange().Update(created)
	require.Error(p.T(), err, "Expected the promotion of an unmanaged LimitRange to be rejected")
	require.Contains(p.T(), err.Error(), forbiddenPromoteMessage)
	require.True(p.T(), k8sError.IsBadRequest(err), "Expected the webhook rejection to return a 400 BadRequest status code")

	log.Info("Verify the LimitRange remains unmanaged.")
	current, err := userContext.Core.LimitRange().Get(namespace, created.Name, metav1.GetOptions{})
	require.NoError(p.T(), err)
	require.NotEqual(p.T(), "true", current.Labels[namespaceapi.ManagedResourceQuotaLabel], "The LimitRange must remain unmanaged")
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestDemoteManagedLimitRangeIsRejected() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	_, userContext, namespace, _, managedLR := p.setupProjectWithManagedResources()

	log.Info("As the project owner, attempt to demote the Rancher-managed LimitRange by removing the marker label.")
	delete(managedLR.Labels, namespaceapi.ManagedResourceQuotaLabel)
	_, err := userContext.Core.LimitRange().Update(managedLR)
	require.Error(p.T(), err, "Expected the demotion of the managed LimitRange to be rejected")
	require.Contains(p.T(), err.Error(), forbiddenChangeMessage)
	require.True(p.T(), k8sError.IsBadRequest(err), "Expected the webhook rejection to return a 400 BadRequest status code")

	log.Info("Verify the Rancher-managed marker is still present on the LimitRange.")
	currentLR, err := userContext.Core.LimitRange().Get(namespace, managedLR.Name, metav1.GetOptions{})
	require.NoError(p.T(), err, "The managed LimitRange should still exist")
	require.Equal(p.T(), "true", currentLR.Labels[namespaceapi.ManagedResourceQuotaLabel], "The managed LimitRange should still carry the marker label")
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestUserHasFullAccessToUnmanagedLimitRange() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	_, userContext, namespace, _, managedLR := p.setupProjectWithManagedResources()

	log.Info("As the project owner, create an unmanaged LimitRange.")
	created, err := userContext.Core.LimitRange().Create(namespaceapi.NewLimitRange(namespace, namegen.AppendRandomString("user-lr-"), false))
	require.NoError(p.T(), err)
	require.NotEqual(p.T(), "true", created.Labels[namespaceapi.ManagedResourceQuotaLabel], "The user-created LimitRange must not carry the Rancher-managed marker")

	log.Info("Update the unmanaged LimitRange and verify the change is accepted.")
	require.NotEmpty(p.T(), created.Spec.Limits, "Expected the LimitRange to have at least one limit item")
	created.Spec.Limits[0].Default[corev1.ResourceCPU] = resource.MustParse("4")
	created.Spec.Limits[0].Default[corev1.ResourceMemory] = resource.MustParse("4Gi")
	updated, err := userContext.Core.LimitRange().Update(created)
	require.NoError(p.T(), err, "Expected the update of the unmanaged LimitRange to succeed")
	updatedCPU := updated.Spec.Limits[0].Default[corev1.ResourceCPU]
	expectedCPU := resource.MustParse("4")
	require.Equal(p.T(), 0, expectedCPU.Cmp(updatedCPU), "Expected the unmanaged LimitRange default CPU to be updated to 4")

	log.Info("Delete the unmanaged LimitRange and verify the delete is accepted.")
	err = userContext.Core.LimitRange().Delete(namespace, updated.Name, &metav1.DeleteOptions{})
	require.NoError(p.T(), err, "Expected the delete of the unmanaged LimitRange to succeed")

	log.Info("Verify the Rancher-managed LimitRange remains present and unchanged.")
	currentLR, err := userContext.Core.LimitRange().Get(namespace, managedLR.Name, metav1.GetOptions{})
	require.NoError(p.T(), err, "The managed LimitRange should still exist")
	require.Equal(p.T(), managedLR.Spec, currentLR.Spec, "The managed LimitRange spec should remain unchanged")
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestAdminCannotUpdateManagedResources() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	_, _, namespace, managedRQ, managedLR := p.setupProjectWithManagedResources()

	adminContext, err := extclusterapi.GetClusterWranglerContext(p.client, p.cluster.ID)
	require.NoError(p.T(), err)

	log.Info("As admin, attempt to update the Rancher-managed ResourceQuota.")
	adminRQ, err := adminContext.Core.ResourceQuota().Get(namespace, managedRQ.Name, metav1.GetOptions{})
	require.NoError(p.T(), err)
	adminRQ.Spec.Hard[corev1.ResourceLimitsCPU] = resource.MustParse("4")
	adminRQ.Spec.Hard[corev1.ResourceLimitsMemory] = resource.MustParse("4Gi")
	_, err = adminContext.Core.ResourceQuota().Update(adminRQ)
	require.Error(p.T(), err, "Expected the downstream admin update of the managed ResourceQuota to be rejected")
	require.Contains(p.T(), err.Error(), forbiddenChangeMessage)
	require.True(p.T(), k8sError.IsBadRequest(err), "Expected the webhook rejection to return a 400 BadRequest status code")

	log.Info("As admin, attempt to delete the Rancher-managed ResourceQuota.")
	err = adminContext.Core.ResourceQuota().Delete(namespace, managedRQ.Name, &metav1.DeleteOptions{})
	require.Error(p.T(), err, "Expected the downstream admin delete of the managed ResourceQuota to be rejected")
	require.Contains(p.T(), err.Error(), forbiddenDeleteMessage)
	require.True(p.T(), k8sError.IsBadRequest(err), "Expected the webhook rejection to return a 400 BadRequest status code")

	log.Info("As admin, attempt to update the Rancher-managed LimitRange.")
	adminLR, err := adminContext.Core.LimitRange().Get(namespace, managedLR.Name, metav1.GetOptions{})
	require.NoError(p.T(), err)
	require.NotEmpty(p.T(), adminLR.Spec.Limits, "Expected the managed LimitRange to have at least one limit item")
	adminLR.Spec.Limits[0].Default[corev1.ResourceCPU] = resource.MustParse("4")
	adminLR.Spec.Limits[0].Default[corev1.ResourceMemory] = resource.MustParse("4Gi")
	_, err = adminContext.Core.LimitRange().Update(adminLR)
	require.Error(p.T(), err, "Expected the downstream admin update of the managed LimitRange to be rejected")
	require.Contains(p.T(), err.Error(), forbiddenChangeMessage)
	require.True(p.T(), k8sError.IsBadRequest(err), "Expected the webhook rejection to return a 400 BadRequest status code")

	log.Info("As admin, attempt to delete the Rancher-managed LimitRange.")
	err = adminContext.Core.LimitRange().Delete(namespace, managedLR.Name, &metav1.DeleteOptions{})
	require.Error(p.T(), err, "Expected the downstream admin delete of the managed LimitRange to be rejected")
	require.Contains(p.T(), err.Error(), forbiddenDeleteMessage)
	require.True(p.T(), k8sError.IsBadRequest(err), "Expected the webhook rejection to return a 400 BadRequest status code")
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestRancherReconcilesManagedResourceQuota() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	_, _, namespace, _, _ := p.setupProjectWithManagedResources()

	log.Info("As admin, update the namespace resourceQuota annotation to raise the managed limits.")
	newLimits := map[string]string{
		"limitsCpu":    "1500m",
		"limitsMemory": "1536Mi",
	}
	err := namespaceapi.UpdateNamespaceResourceQuotaAnnotation(p.client, p.cluster.ID, namespace, newLimits, nil)
	require.NoError(p.T(), err)

	log.Info("Verify Rancher reconciles the managed ResourceQuota to the new limits without a webhook rejection.")
	expectedHard := corev1.ResourceList{
		corev1.ResourceLimitsCPU:    resource.MustParse("1500m"),
		corev1.ResourceLimitsMemory: resource.MustParse("1536Mi"),
	}
	_, err = namespaceapi.WaitForManagedResourceQuotaHard(p.client, p.cluster.ID, namespace, expectedHard)
	require.NoError(p.T(), err, "Rancher should reconcile the managed ResourceQuota to the updated limits")
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestRancherReconcilesManagedLimitRange() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	_, _, namespace, _, _ := p.setupProjectWithManagedResources()

	log.Info("As admin, update the namespace containerDefaultResourceLimit annotation to raise the managed LimitRange defaults.")
	adminContext, err := extclusterapi.GetClusterWranglerContext(p.client, p.cluster.ID)
	require.NoError(p.T(), err)

	ns, err := adminContext.Core.Namespace().Get(namespace, metav1.GetOptions{})
	require.NoError(p.T(), err)
	if ns.Annotations == nil {
		ns.Annotations = make(map[string]string)
	}
	ns.Annotations[namespaceapi.ContainerDefaultResourceLimitAnnotation] = namespaceapi.ContainerDefaultResourceLimit("2000m", "2Gi", containerRequestsCPU, containerRequestsMemory)
	_, err = adminContext.Core.Namespace().Update(ns)
	require.NoError(p.T(), err)

	log.Info("Verify Rancher reconciles the managed LimitRange to the new defaults without a webhook rejection.")
	_, err = namespaceapi.WaitForManagedLimitRangeDefault(p.client, p.cluster.ID, namespace, resource.MustParse("2000m"), resource.MustParse("2Gi"))
	require.NoError(p.T(), err, "Rancher should reconcile the managed LimitRange to the updated container default limits")
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestManagedResourceQuotaEnforcesQuotaAndTracksUsage() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, userContext, namespace, managedRQ, _ := p.setupProjectWithManagedResources()

	log.Info("Create a Deployment that exceeds the managed ResourceQuota and verify its pods are rejected by the ResourceQuota admission control.")
	exceedingResources := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("4"),
		corev1.ResourceMemory: resource.MustParse("4Gi"),
	}
	exceedingName := namegen.AppendRandomString("exceed-")
	_, err := deploymentsapi.CreateDeploymentFromPodTemplate(standardUserClient, p.cluster.ID, exceedingName, namespace, podapi.NewResourcePodTemplate(exceedingResources, exceedingResources), 1, false)
	require.NoError(p.T(), err, "Expected the exceeding Deployment object to be created")

	err = deploymentsapi.VerifyDeploymentStatus(standardUserClient, p.cluster.ID, namespace, exceedingName, "ReplicaFailure", "FailedCreate", projectapi.ExceedededResourceQuotaErrorMessage, 0)
	require.NoError(p.T(), err, "Expected the exceeding Deployment's pod creation to be rejected for exceeding the managed ResourceQuota")

	log.Info("Create a Deployment within the managed ResourceQuota and verify it becomes active.")
	requests := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("250m"),
		corev1.ResourceMemory: resource.MustParse("256Mi"),
	}
	limits := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("500m"),
		corev1.ResourceMemory: resource.MustParse("512Mi"),
	}
	withinName := namegen.AppendRandomString("within-")
	_, err = deploymentsapi.CreateDeploymentFromPodTemplate(standardUserClient, p.cluster.ID, withinName, namespace, podapi.NewResourcePodTemplate(requests, limits), 1, true)
	require.NoError(p.T(), err, "Expected the within-quota Deployment object to be created")

	expectedUsed := map[string]string{
		"limits.cpu":    "500m",
		"limits.memory": "512Mi",
	}
	err = kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		if verifyErr := namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, p.cluster.ID, namespace, expectedUsed); verifyErr != nil {
			return false, nil
		}
		return true, nil
	})
	require.NoError(p.T(), err, "The managed ResourceQuota usage should reflect the consumed resources")

	log.Info("Verify the managed ResourceQuota spec.hard is unchanged and it still has the marker label.")
	managed, err := userContext.Core.ResourceQuota().Get(namespace, managedRQ.Name, metav1.GetOptions{})
	require.NoError(p.T(), err)
	require.True(p.T(), resource.MustParse(namespaceQuotaLimitsCPU).Equal(managed.Spec.Hard[corev1.ResourceLimitsCPU]), "managed ResourceQuota limits.cpu should remain %s", namespaceQuotaLimitsCPU)
	require.True(p.T(), resource.MustParse(namespaceQuotaLimitsMemory).Equal(managed.Spec.Hard[corev1.ResourceLimitsMemory]), "managed ResourceQuota limits.memory should remain %s", namespaceQuotaLimitsMemory)
	require.Equal(p.T(), "true", managed.Labels[namespaceapi.ManagedResourceQuotaLabel], "managed ResourceQuota should still carry the marker label")
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestManagedResourceQuotaAllowsMultipleDeploymentsWithinQuota() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _, namespace, _, _ := p.setupProjectWithManagedResources()

	requests := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("250m"),
		corev1.ResourceMemory: resource.MustParse("256Mi"),
	}
	limits := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("250m"),
		corev1.ResourceMemory: resource.MustParse("256Mi"),
	}

	log.Info("Create two Deployments, within the managed ResourceQuota, and verify both become active.")
	deploymentNames := []string{namegen.AppendRandomString("multi-a-"), namegen.AppendRandomString("multi-b-")}
	for _, name := range deploymentNames {
		_, err := deploymentsapi.CreateDeploymentFromPodTemplate(standardUserClient, p.cluster.ID, name, namespace, podapi.NewResourcePodTemplate(requests, limits), 1, true)
		require.NoErrorf(p.T(), err, "Expected the Deployment %q object to be created", name)
	}

	log.Info("Verify the managed ResourceQuota usage reflects both Deployments.")
	expectedUsed := map[string]string{
		"limits.cpu":    "500m",
		"limits.memory": "512Mi",
	}
	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		if verifyErr := namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, p.cluster.ID, namespace, expectedUsed); verifyErr != nil {
			return false, nil
		}
		return true, nil
	})
	require.NoError(p.T(), err, "The managed ResourceQuota usage should reflect both Deployments")

	log.Info("Verify the managed ResourceQuota spec.hard is unchanged and it still has the marker label.")
	managed, err := namespaceapi.WaitForManagedResourceQuota(p.client, p.cluster.ID, namespace)
	require.NoError(p.T(), err)
	require.True(p.T(), resource.MustParse(namespaceQuotaLimitsCPU).Equal(managed.Spec.Hard[corev1.ResourceLimitsCPU]), "managed ResourceQuota limits.cpu should remain %s", namespaceQuotaLimitsCPU)
	require.True(p.T(), resource.MustParse(namespaceQuotaLimitsMemory).Equal(managed.Spec.Hard[corev1.ResourceLimitsMemory]), "managed ResourceQuota limits.memory should remain %s", namespaceQuotaLimitsMemory)
	require.Equal(p.T(), "true", managed.Labels[namespaceapi.ManagedResourceQuotaLabel], "managed ResourceQuota should still carry the marker label")
}

func (p *ProjectsManagedResourceProtectionTestSuite) TestNamespaceDeletionRemovesManagedResources() {
	subSession := p.session.NewSession()
	defer subSession.Cleanup()

	_, _, namespace, _, _ := p.setupProjectWithManagedResources()

	log.Info("Delete the namespace containing the Rancher-managed ResourceQuota and LimitRange.")
	err := extnamespaceapi.DeleteNamespace(p.client, p.cluster.ID, namespace, true)
	require.NoError(p.T(), err)

	adminContext, err := extclusterapi.GetClusterWranglerContext(p.client, p.cluster.ID)
	require.NoError(p.T(), err)

	log.Info("Verify no Rancher-managed ResourceQuota or LimitRange remain for the deleted namespace.")
	rqList, err := adminContext.Core.ResourceQuota().List(namespace, metav1.ListOptions{LabelSelector: namespaceapi.ManagedResourceQuotaLabel + "=true"})
	require.NoError(p.T(), err, "Failed to list ResourceQuotas in the deleted namespace")
	require.Empty(p.T(), rqList.Items, "No Rancher-managed ResourceQuota should remain in the deleted namespace")

	lrList, err := adminContext.Core.LimitRange().List(namespace, metav1.ListOptions{LabelSelector: namespaceapi.ManagedResourceQuotaLabel + "=true"})
	require.NoError(p.T(), err, "Failed to list LimitRanges in the deleted namespace")
	require.Empty(p.T(), lrList.Items, "No Rancher-managed LimitRange should remain in the deleted namespace")
}

func TestProjectsManagedResourceProtectionTestSuite(t *testing.T) {
	suite.Run(t, new(ProjectsManagedResourceProtectionTestSuite))
}
