//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package projects

import (
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	podapi "github.com/rancher/tests/actions/kubeapi/workloads/pods"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
)

type ProjectsNegativeResourceQuotaTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (pnrq *ProjectsNegativeResourceQuotaTestSuite) TearDownSuite() {
	pnrq.session.Cleanup()
}

func (pnrq *ProjectsNegativeResourceQuotaTestSuite) SetupSuite() {
	pnrq.session = session.NewSession()

	client, err := rancher.NewClient("", pnrq.session)
	require.NoError(pnrq.T(), err)
	pnrq.client = client

	log.Info("Getting cluster name from the config file and append cluster details in the struct")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(pnrq.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(pnrq.client, clusterName)
	require.NoError(pnrq.T(), err, "Error getting cluster ID")
	pnrq.cluster, err = pnrq.client.Management.Cluster.ByID(clusterID)
	require.NoError(pnrq.T(), err)
}

func (pnrq *ProjectsNegativeResourceQuotaTestSuite) TestNegativeNamespaceQuotaFailsValidationOnCreate() {
	subSession := pnrq.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a standard user and add the user to the downstream cluster as cluster owner.")
	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(pnrq.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), pnrq.cluster, nil)
	require.NoError(pnrq.T(), err, "Failed to add the user as a cluster owner to the downstream cluster")

	log.Info("Creating a project with a valid CPU and memory resource quota.")
	projectQuota := &v3.ResourceQuotaLimit{
		LimitsCPU:    "2",
		LimitsMemory: "2Gi",
	}
	namespaceQuota := &v3.ResourceQuotaLimit{
		LimitsCPU:    "1",
		LimitsMemory: "1Gi",
	}
	validNamespaceLimits := map[string]string{
		"limitsCpu":    "1",
		"limitsMemory": "1Gi",
	}

	projectTemplate := projectapi.NewProjectTemplate(pnrq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, projectQuota, nil, namespaceQuota, nil)
	createdProject, firstNamespace, err := projectapi.CreateProjectWithTemplateAndNamespace(standardUserClient, pnrq.cluster.ID, projectTemplate)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the first namespace %s with a valid quota is validated successfully.", firstNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, firstNamespace.Name, validNamespaceLimits, nil, true)
	require.NoError(pnrq.T(), err)

	log.Info("Creating a second namespace with a negative limitsCpu value.")
	negativeLimits := map[string]string{
		"limitsCpu":    "-1",
		"limitsMemory": "1Gi",
	}
	negativeAnnotation, err := namespaceapi.BuildResourceQuotaAnnotation(negativeLimits, nil)
	require.NoError(pnrq.T(), err)
	annotations := map[string]string{
		namespaceapi.ResourceQuotaAnnotation: negativeAnnotation,
	}
	secondNamespace, err := namespaceapi.CreateNamespace(standardUserClient, pnrq.cluster.ID, createdProject.Name, namegen.AppendRandomString("testns"), "", nil, annotations)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the negative namespace quota in %s fails validation (ResourceQuotaValidated=False).", secondNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, negativeLimits, nil, false, projectapi.NegativeResourceQuotaValidationMessage)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the enforcing ResourceQuota in namespace %s has the negative limitsCpu zeroed to 0 while the valid limitsMemory is preserved.", secondNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, map[string]string{"limits.cpu": "0", "limits.memory": "1Gi"})
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the valid first namespace %s is still validated (negative sibling does not affect it).", firstNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, firstNamespace.Name, validNamespaceLimits, nil, true)
	require.NoError(pnrq.T(), err)
}

func (pnrq *ProjectsNegativeResourceQuotaTestSuite) TestNegativeNamespaceQuotaFailsValidationOnUpdate() {
	subSession := pnrq.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a standard user and add the user to the downstream cluster as cluster owner.")
	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(pnrq.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), pnrq.cluster, nil)
	require.NoError(pnrq.T(), err, "Failed to add the user as a cluster owner to the downstream cluster")

	log.Info("Creating a project with a valid CPU and memory resource quota.")
	projectQuota := &v3.ResourceQuotaLimit{
		LimitsCPU:    "2",
		LimitsMemory: "2Gi",
	}
	namespaceQuota := &v3.ResourceQuotaLimit{
		LimitsCPU:    "1",
		LimitsMemory: "1Gi",
	}
	validNamespaceLimits := map[string]string{
		"limitsCpu":    "1",
		"limitsMemory": "1Gi",
	}

	projectTemplate := projectapi.NewProjectTemplate(pnrq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, projectQuota, nil, namespaceQuota, nil)
	_, namespace, err := projectapi.CreateProjectWithTemplateAndNamespace(standardUserClient, pnrq.cluster.ID, projectTemplate)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the namespace %s with a valid quota is validated successfully.", namespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, namespace.Name, validNamespaceLimits, nil, true)
	require.NoError(pnrq.T(), err)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, namespace.Name, map[string]string{"limits.cpu": "1", "limits.memory": "1Gi"})
	require.NoError(pnrq.T(), err)

	log.Infof("Updating the namespace %s quota to set a negative limitsCpu value.", namespace.Name)
	negativeLimits := map[string]string{
		"limitsCpu":    "-2",
		"limitsMemory": "1Gi",
	}
	err = namespaceapi.UpdateNamespaceResourceQuotaAnnotation(standardUserClient, pnrq.cluster.ID, namespace.Name, negativeLimits, nil)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the negative update to namespace %s fails validation (ResourceQuotaValidated=False).", namespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, namespace.Name, negativeLimits, nil, false, projectapi.NegativeResourceQuotaValidationMessage)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the enforcing ResourceQuota in namespace %s remains at the previous valid values after the update.", namespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, namespace.Name, map[string]string{"limits.cpu": "1", "limits.memory": "1Gi"})
	require.NoError(pnrq.T(), err)
}

func (pnrq *ProjectsNegativeResourceQuotaTestSuite) TestCorrectedNamespaceQuotaPassesValidationOnUpdate() {
	subSession := pnrq.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a standard user and add the user to the downstream cluster as cluster owner.")
	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(pnrq.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), pnrq.cluster, nil)
	require.NoError(pnrq.T(), err, "Failed to add the user as a cluster owner to the downstream cluster")

	log.Info("Creating a project with a valid CPU and memory resource quota.")
	projectQuota := &v3.ResourceQuotaLimit{
		LimitsCPU:    "2",
		LimitsMemory: "2Gi",
	}
	namespaceQuota := &v3.ResourceQuotaLimit{
		LimitsCPU:    "1",
		LimitsMemory: "1Gi",
	}
	validNamespaceLimits := map[string]string{
		"limitsCpu":    "1",
		"limitsMemory": "1Gi",
	}

	projectTemplate := projectapi.NewProjectTemplate(pnrq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, projectQuota, nil, namespaceQuota, nil)
	createdProject, firstNamespace, err := projectapi.CreateProjectWithTemplateAndNamespace(standardUserClient, pnrq.cluster.ID, projectTemplate)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the first namespace %s with a valid quota is validated successfully.", firstNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, firstNamespace.Name, validNamespaceLimits, nil, true)
	require.NoError(pnrq.T(), err)

	log.Info("Creating a second namespace with a negative limitsCpu value.")
	negativeLimits := map[string]string{
		"limitsCpu":    "-1",
		"limitsMemory": "1Gi",
	}
	negativeAnnotation, err := namespaceapi.BuildResourceQuotaAnnotation(negativeLimits, nil)
	require.NoError(pnrq.T(), err)
	secondNamespace, err := namespaceapi.CreateNamespace(standardUserClient, pnrq.cluster.ID, createdProject.Name, namegen.AppendRandomString("testns"), "", nil, map[string]string{namespaceapi.ResourceQuotaAnnotation: negativeAnnotation})
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the negative namespace quota in %s fails validation (ResourceQuotaValidated=False).", secondNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, negativeLimits, nil, false, projectapi.NegativeResourceQuotaValidationMessage)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the enforcing ResourceQuota in namespace %s has the negative limitsCpu zeroed to 0 at creation while the valid limitsMemory is preserved.", secondNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, map[string]string{"limits.cpu": "0", "limits.memory": "1Gi"})
	require.NoError(pnrq.T(), err)

	log.Infof("Correcting the namespace %s quota to valid positive values and verifying it now passes validation.", secondNamespace.Name)
	err = namespaceapi.UpdateNamespaceResourceQuotaAnnotation(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, validNamespaceLimits, nil)
	require.NoError(pnrq.T(), err)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, validNamespaceLimits, nil, true)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the enforcing ResourceQuota in namespace %s reflects the corrected values.", secondNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, map[string]string{"limits.cpu": "1", "limits.memory": "1Gi"})
	require.NoError(pnrq.T(), err)
}

func (pnrq *ProjectsNegativeResourceQuotaTestSuite) TestCorrectedNegativeUpdateClearsStatus() {
	subSession := pnrq.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a standard user and add the user to the downstream cluster as cluster owner.")
	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(pnrq.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), pnrq.cluster, nil)
	require.NoError(pnrq.T(), err, "Failed to add the user as a cluster owner to the downstream cluster")

	log.Info("Creating a project with a valid CPU and memory resource quota.")
	projectQuota := &v3.ResourceQuotaLimit{
		LimitsCPU:    "2",
		LimitsMemory: "2Gi",
	}
	namespaceQuota := &v3.ResourceQuotaLimit{
		LimitsCPU:    "1",
		LimitsMemory: "1Gi",
	}
	validNamespaceLimits := map[string]string{
		"limitsCpu":    "1",
		"limitsMemory": "1Gi",
	}

	projectTemplate := projectapi.NewProjectTemplate(pnrq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, projectQuota, nil, namespaceQuota, nil)
	_, namespace, err := projectapi.CreateProjectWithTemplateAndNamespace(standardUserClient, pnrq.cluster.ID, projectTemplate)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the namespace %s with a valid quota is validated successfully.", namespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, namespace.Name, validNamespaceLimits, nil, true)
	require.NoError(pnrq.T(), err)

	log.Infof("Updating the namespace %s quota to a negative limitsCpu value and verifying it fails validation.", namespace.Name)
	negativeLimits := map[string]string{
		"limitsCpu":    "-1",
		"limitsMemory": "1Gi",
	}
	err = namespaceapi.UpdateNamespaceResourceQuotaAnnotation(standardUserClient, pnrq.cluster.ID, namespace.Name, negativeLimits, nil)
	require.NoError(pnrq.T(), err)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, namespace.Name, negativeLimits, nil, false, projectapi.NegativeResourceQuotaValidationMessage)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the enforcing ResourceQuota in namespace %s remains at the previous valid values.", namespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, namespace.Name, map[string]string{"limits.cpu": "1", "limits.memory": "1Gi"})
	require.NoError(pnrq.T(), err)

	log.Infof("Correcting the namespace %s quota back to the original valid values and verifying the status returns to validated with the negative message cleared.", namespace.Name)
	err = namespaceapi.UpdateNamespaceResourceQuotaAnnotation(standardUserClient, pnrq.cluster.ID, namespace.Name, validNamespaceLimits, nil)
	require.NoError(pnrq.T(), err)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, namespace.Name, validNamespaceLimits, nil, true)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the enforcing ResourceQuota in namespace %s reflects the corrected values.", namespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, namespace.Name, map[string]string{"limits.cpu": "1", "limits.memory": "1Gi"})
	require.NoError(pnrq.T(), err)
}

func (pnrq *ProjectsNegativeResourceQuotaTestSuite) TestNegativeSiblingDoesNotAllowOversubscription() {
	subSession := pnrq.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a standard user and add the user to the downstream cluster as cluster owner.")
	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(pnrq.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), pnrq.cluster, nil)
	require.NoError(pnrq.T(), err, "Failed to add the user as a cluster owner to the downstream cluster")

	log.Info("Creating a project with a 2Gi project-level memory quota.")
	projectQuota := &v3.ResourceQuotaLimit{
		LimitsMemory: "2Gi",
	}
	namespaceQuota := &v3.ResourceQuotaLimit{
		LimitsMemory: "1Gi",
	}

	projectTemplate := projectapi.NewProjectTemplate(pnrq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, projectQuota, nil, namespaceQuota, nil)
	createdProject, firstNamespace, err := projectapi.CreateProjectWithTemplateAndNamespace(standardUserClient, pnrq.cluster.ID, projectTemplate)
	require.NoError(pnrq.T(), err)

	log.Infof("Updating the first namespace %s to a negative limitsMemory value.", firstNamespace.Name)
	negativeSiblingLimits := map[string]string{
		"limitsMemory": "-1Gi",
	}
	err = namespaceapi.UpdateNamespaceResourceQuotaAnnotation(standardUserClient, pnrq.cluster.ID, firstNamespace.Name, negativeSiblingLimits, nil)
	require.NoError(pnrq.T(), err)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, firstNamespace.Name, negativeSiblingLimits, nil, false, projectapi.NegativeResourceQuotaValidationMessage)
	require.NoError(pnrq.T(), err)

	log.Info("Creating a second namespace that consumes the entire project memory quota (2Gi).")
	fullProjectLimits := map[string]string{
		"limitsMemory": "2Gi",
	}
	fullProjectAnnotation, err := namespaceapi.BuildResourceQuotaAnnotation(fullProjectLimits, nil)
	require.NoError(pnrq.T(), err)
	secondNamespace, err := namespaceapi.CreateNamespace(standardUserClient, pnrq.cluster.ID, createdProject.Name, namegen.AppendRandomString("testns"), "", nil, map[string]string{namespaceapi.ResourceQuotaAnnotation: fullProjectAnnotation})
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the second namespace %s is validated.", secondNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, fullProjectLimits, nil, true)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the enforcing ResourceQuota in the second namespace %s is set to the full 2Gi project memory.", secondNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, map[string]string{"limits.memory": "2Gi"})
	require.NoError(pnrq.T(), err)

	log.Info("Creating a third namespace that would exceed the project memory quota.")
	overLimits := map[string]string{
		"limitsMemory": "1Gi",
	}
	overAnnotation, err := namespaceapi.BuildResourceQuotaAnnotation(overLimits, nil)
	require.NoError(pnrq.T(), err)
	thirdNamespace, err := namespaceapi.CreateNamespace(standardUserClient, pnrq.cluster.ID, createdProject.Name, namegen.AppendRandomString("testns"), "", nil, map[string]string{namespaceapi.ResourceQuotaAnnotation: overAnnotation})
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the third namespace %s fails validation.", thirdNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, thirdNamespace.Name, overLimits, nil, false, projectapi.ExceedsProjectLimitValidationMessage, projectapi.OversubscribedResourceQuotaValidationMessage)
	require.NoError(pnrq.T(), err)

	log.Infof("Attempting to create a memory-consuming pod in the oversubscribed third namespace %s to confirm enforcement blocks oversubscription.", thirdNamespace.Name)
	podMemory := map[corev1.ResourceName]string{
		corev1.ResourceMemory: "256Mi",
	}
	_, err = podapi.CreatePodWithResources(standardUserClient, pnrq.cluster.ID, thirdNamespace.Name, podapi.PauseImage, podMemory, podMemory, false)
	require.Error(pnrq.T(), err, "Expected pod creation in the oversubscribed namespace to be rejected")
	require.Contains(pnrq.T(), err.Error(), projectapi.ExceedededResourceQuotaErrorMessage)
}

func (pnrq *ProjectsNegativeResourceQuotaTestSuite) TestMixedValidAndNegativeFieldsFailValidation() {
	subSession := pnrq.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a standard user and add the user to the downstream cluster as cluster owner.")
	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(pnrq.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), pnrq.cluster, nil)
	require.NoError(pnrq.T(), err, "Failed to add the user as a cluster owner to the downstream cluster")

	log.Info("Creating a project with CPU and memory resource quota.")
	projectQuota := &v3.ResourceQuotaLimit{
		LimitsCPU:    "4",
		LimitsMemory: "4Gi",
	}
	namespaceQuota := &v3.ResourceQuotaLimit{
		LimitsCPU:    "1",
		LimitsMemory: "1Gi",
	}
	validNamespaceLimits := map[string]string{
		"limitsCpu":    "1",
		"limitsMemory": "1Gi",
	}

	projectTemplate := projectapi.NewProjectTemplate(pnrq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, projectQuota, nil, namespaceQuota, nil)
	_, namespace, err := projectapi.CreateProjectWithTemplateAndNamespace(standardUserClient, pnrq.cluster.ID, projectTemplate)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the namespace %s with a valid quota is validated successfully.", namespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, namespace.Name, validNamespaceLimits, nil, true)
	require.NoError(pnrq.T(), err)

	log.Infof("Updating the namespace %s to mix a valid limitsMemory with a negative limitsCpu.", namespace.Name)
	mixedLimits := map[string]string{
		"limitsCpu":    "-1",
		"limitsMemory": "1Gi",
	}
	err = namespaceapi.UpdateNamespaceResourceQuotaAnnotation(standardUserClient, pnrq.cluster.ID, namespace.Name, mixedLimits, nil)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the namespace %s fails validation because of the negative limitsCpu, even though limitsMemory is valid.", namespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, namespace.Name, mixedLimits, nil, false, projectapi.NegativeResourceQuotaValidationMessage)
	require.NoError(pnrq.T(), err)

	log.Infof("Correcting the negative field in namespace %s and verifying it is validated successfully.", namespace.Name)
	err = namespaceapi.UpdateNamespaceResourceQuotaAnnotation(standardUserClient, pnrq.cluster.ID, namespace.Name, validNamespaceLimits, nil)
	require.NoError(pnrq.T(), err)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, namespace.Name, validNamespaceLimits, nil, true)
	require.NoError(pnrq.T(), err)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, namespace.Name, map[string]string{"limits.cpu": "1", "limits.memory": "1Gi"})
	require.NoError(pnrq.T(), err)
}

func (pnrq *ProjectsNegativeResourceQuotaTestSuite) TestCombinedOversubscribedAndNegativeFieldsFailValidation() {
	subSession := pnrq.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a standard user and add the user to the downstream cluster as cluster owner.")
	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(pnrq.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), pnrq.cluster, nil)
	require.NoError(pnrq.T(), err, "Failed to add the user as a cluster owner to the downstream cluster")

	log.Info("Creating a project whose namespace default consumes the entire project memory.")
	projectQuota := &v3.ResourceQuotaLimit{
		LimitsCPU:    "2",
		LimitsMemory: "2Gi",
	}
	namespaceQuota := &v3.ResourceQuotaLimit{
		LimitsCPU:    "1",
		LimitsMemory: "2Gi",
	}
	firstNamespaceLimits := map[string]string{
		"limitsCpu":    "1",
		"limitsMemory": "2Gi",
	}

	projectTemplate := projectapi.NewProjectTemplate(pnrq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, projectQuota, nil, namespaceQuota, nil)
	createdProject, firstNamespace, err := projectapi.CreateProjectWithTemplateAndNamespace(standardUserClient, pnrq.cluster.ID, projectTemplate)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the first namespace %s consuming the full project memory (2Gi) is validated successfully.", firstNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, firstNamespace.Name, firstNamespaceLimits, nil, true)
	require.NoError(pnrq.T(), err)

	log.Info("Creating a second namespace that both oversubscribes memory and sets a negative CPU.")
	combinedBadLimits := map[string]string{
		"limitsCpu":    "-1",
		"limitsMemory": "1Gi",
	}
	combinedBadAnnotation, err := namespaceapi.BuildResourceQuotaAnnotation(combinedBadLimits, nil)
	require.NoError(pnrq.T(), err)
	secondNamespace, err := namespaceapi.CreateNamespace(standardUserClient, pnrq.cluster.ID, createdProject.Name, namegen.AppendRandomString("testns"), "", nil, map[string]string{namespaceapi.ResourceQuotaAnnotation: combinedBadAnnotation})
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the second namespace %s fails validation and the message reports the oversubscribed memory field.", secondNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, combinedBadLimits, nil, false, projectapi.OversubscribedResourceQuotaValidationMessage)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the same failure on namespace %s also reports the negative cpu field.", secondNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, combinedBadLimits, nil, false, projectapi.NegativeResourceQuotaValidationMessage)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the enforcing ResourceQuota in namespace %s has both the oversubscribed memory and the negative cpu zeroed to 0.", secondNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, map[string]string{"limits.cpu": "0", "limits.memory": "0"})
	require.NoError(pnrq.T(), err)
}

func (pnrq *ProjectsNegativeResourceQuotaTestSuite) TestNegativeExtendedNamespaceQuotaFailsValidationOnCreate() {
	subSession := pnrq.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a standard user and add the user to the downstream cluster as cluster owner.")
	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(pnrq.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), pnrq.cluster, nil)
	require.NoError(pnrq.T(), err, "Failed to add the user as a cluster owner to the downstream cluster")

	log.Info("Creating a project with a valid extended ephemeral-storage resource quota.")
	projectExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "400Mi",
	}
	namespaceExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "120Mi",
	}

	projectTemplate := projectapi.NewProjectTemplate(pnrq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, nil, projectExtended, nil, namespaceExtended)
	createdProject, firstNamespace, err := projectapi.CreateProjectWithTemplateAndNamespace(standardUserClient, pnrq.cluster.ID, projectTemplate)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the first namespace %s with a valid extended quota is validated successfully.", firstNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, firstNamespace.Name, nil, namespaceExtended, true)
	require.NoError(pnrq.T(), err)

	log.Info("Creating a second namespace with a negative extended ephemeral-storage limit at creation time.")
	negativeExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "-100Mi",
	}
	negativeAnnotation, err := namespaceapi.BuildResourceQuotaAnnotation(nil, negativeExtended)
	require.NoError(pnrq.T(), err)
	annotations := map[string]string{
		namespaceapi.ResourceQuotaAnnotation: negativeAnnotation,
	}
	secondNamespace, err := namespaceapi.CreateNamespace(standardUserClient, pnrq.cluster.ID, createdProject.Name, namegen.AppendRandomString("testns"), "", nil, annotations)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the negative extended quota in namespace %s fails validation (ResourceQuotaValidated=False).", secondNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, nil, negativeExtended, false, projectapi.NegativeResourceQuotaValidationMessage)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the enforcing ResourceQuota in namespace %s has the negative extended ephemeral-storage zeroed to 0.", secondNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, map[string]string{projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "0"})
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the valid first namespace %s is still validated successfully.", firstNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, firstNamespace.Name, nil, namespaceExtended, true)
	require.NoError(pnrq.T(), err)
}

func (pnrq *ProjectsNegativeResourceQuotaTestSuite) TestNegativeExtendedNamespaceQuotaFailsValidationOnUpdate() {
	subSession := pnrq.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a standard user and add the user to the downstream cluster as cluster owner.")
	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(pnrq.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), pnrq.cluster, nil)
	require.NoError(pnrq.T(), err, "Failed to add the user as a cluster owner to the downstream cluster")

	log.Info("Creating a project with a valid extended ephemeral-storage resource quota.")
	projectExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "400Mi",
	}
	namespaceExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "120Mi",
	}

	projectTemplate := projectapi.NewProjectTemplate(pnrq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, nil, projectExtended, nil, namespaceExtended)
	_, namespace, err := projectapi.CreateProjectWithTemplateAndNamespace(standardUserClient, pnrq.cluster.ID, projectTemplate)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the namespace %s with a valid extended quota is validated successfully.", namespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, namespace.Name, nil, namespaceExtended, true)
	require.NoError(pnrq.T(), err)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, namespace.Name, map[string]string{projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "120Mi"})
	require.NoError(pnrq.T(), err)

	log.Infof("Updating the namespace %s extended quota to set a negative ephemeral-storage value.", namespace.Name)
	negativeExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "-100Mi",
	}
	err = namespaceapi.UpdateNamespaceResourceQuotaAnnotation(standardUserClient, pnrq.cluster.ID, namespace.Name, nil, negativeExtended)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the negative extended update to namespace %s fails validation (ResourceQuotaValidated=False).", namespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, namespace.Name, nil, negativeExtended, false, projectapi.NegativeResourceQuotaValidationMessage)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the enforcing ResourceQuota in namespace %s remains at the previous valid extended values after the rejected update.", namespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, namespace.Name, map[string]string{projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "120Mi"})
	require.NoError(pnrq.T(), err)
}

func (pnrq *ProjectsNegativeResourceQuotaTestSuite) TestCorrectedExtendedNamespaceQuotaPassesValidationOnUpdate() {
	subSession := pnrq.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a standard user and add the user to the downstream cluster as cluster owner.")
	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(pnrq.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), pnrq.cluster, nil)
	require.NoError(pnrq.T(), err, "Failed to add the user as a cluster owner to the downstream cluster")

	log.Info("Creating a project with a valid extended ephemeral-storage resource quota.")
	projectExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "400Mi",
	}
	namespaceExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "120Mi",
	}

	projectTemplate := projectapi.NewProjectTemplate(pnrq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, nil, projectExtended, nil, namespaceExtended)
	createdProject, firstNamespace, err := projectapi.CreateProjectWithTemplateAndNamespace(standardUserClient, pnrq.cluster.ID, projectTemplate)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the first namespace %s with a valid extended quota is validated successfully.", firstNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, firstNamespace.Name, nil, namespaceExtended, true)
	require.NoError(pnrq.T(), err)

	log.Info("Creating a second namespace with a negative extended ephemeral-storage limit.")
	negativeExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "-100Mi",
	}
	negativeAnnotation, err := namespaceapi.BuildResourceQuotaAnnotation(nil, negativeExtended)
	require.NoError(pnrq.T(), err)
	secondNamespace, err := namespaceapi.CreateNamespace(standardUserClient, pnrq.cluster.ID, createdProject.Name, namegen.AppendRandomString("testns"), "", nil, map[string]string{namespaceapi.ResourceQuotaAnnotation: negativeAnnotation})
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the negative extended quota in namespace %s fails validation (ResourceQuotaValidated=False).", secondNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, nil, negativeExtended, false, projectapi.NegativeResourceQuotaValidationMessage)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the enforcing ResourceQuota in namespace %s has the negative extended ephemeral-storage zeroed to 0 at creation.", secondNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, map[string]string{projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "0"})
	require.NoError(pnrq.T(), err)

	log.Infof("Correcting the namespace %s extended quota to a valid positive value and verifying it now passes validation.", secondNamespace.Name)
	correctedExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "100Mi",
	}
	err = namespaceapi.UpdateNamespaceResourceQuotaAnnotation(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, nil, correctedExtended)
	require.NoError(pnrq.T(), err)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, nil, correctedExtended, true)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the enforcing ResourceQuota in namespace %s reflects the corrected extended value.", secondNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, map[string]string{projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "100Mi"})
	require.NoError(pnrq.T(), err)
}

func (pnrq *ProjectsNegativeResourceQuotaTestSuite) TestCorrectedExtendedNegativeUpdateClearsStatus() {
	subSession := pnrq.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a standard user and add the user to the downstream cluster as cluster owner.")
	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(pnrq.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), pnrq.cluster, nil)
	require.NoError(pnrq.T(), err, "Failed to add the user as a cluster owner to the downstream cluster")

	log.Info("Creating a project with a valid extended ephemeral-storage resource quota.")
	projectExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "400Mi",
	}
	namespaceExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "120Mi",
	}

	projectTemplate := projectapi.NewProjectTemplate(pnrq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, nil, projectExtended, nil, namespaceExtended)
	_, namespace, err := projectapi.CreateProjectWithTemplateAndNamespace(standardUserClient, pnrq.cluster.ID, projectTemplate)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the namespace %s with a valid extended quota is validated successfully.", namespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, namespace.Name, nil, namespaceExtended, true)
	require.NoError(pnrq.T(), err)

	log.Infof("Updating the namespace %s extended quota to a negative ephemeral-storage value and verifying it fails validation.", namespace.Name)
	negativeExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "-100Mi",
	}
	err = namespaceapi.UpdateNamespaceResourceQuotaAnnotation(standardUserClient, pnrq.cluster.ID, namespace.Name, nil, negativeExtended)
	require.NoError(pnrq.T(), err)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, namespace.Name, nil, negativeExtended, false, projectapi.NegativeResourceQuotaValidationMessage)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the enforcing ResourceQuota in namespace %s remains at the previous valid extended value.", namespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, namespace.Name, map[string]string{projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "120Mi"})
	require.NoError(pnrq.T(), err)

	log.Infof("Correcting the namespace %s extended quota back to the original valid value and verifying the status returns to validated with the negative message cleared.", namespace.Name)
	err = namespaceapi.UpdateNamespaceResourceQuotaAnnotation(standardUserClient, pnrq.cluster.ID, namespace.Name, nil, namespaceExtended)
	require.NoError(pnrq.T(), err)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, namespace.Name, nil, namespaceExtended, true)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the enforcing ResourceQuota in namespace %s reflects the corrected extended value.", namespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, namespace.Name, map[string]string{projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "120Mi"})
	require.NoError(pnrq.T(), err)
}

func (pnrq *ProjectsNegativeResourceQuotaTestSuite) TestNegativeExtendedSiblingDoesNotAllowOversubscription() {
	subSession := pnrq.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a standard user and add the user to the downstream cluster as cluster owner.")
	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(pnrq.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), pnrq.cluster, nil)
	require.NoError(pnrq.T(), err, "Failed to add the user as a cluster owner to the downstream cluster")

	log.Info("Creating a project with a 2Gi project-level extended ephemeral-storage quota.")
	projectExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "2Gi",
	}
	namespaceExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "1Gi",
	}

	projectTemplate := projectapi.NewProjectTemplate(pnrq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, nil, projectExtended, nil, namespaceExtended)
	createdProject, firstNamespace, err := projectapi.CreateProjectWithTemplateAndNamespace(standardUserClient, pnrq.cluster.ID, projectTemplate)
	require.NoError(pnrq.T(), err)

	log.Infof("Updating the first namespace %s to a negative extended ephemeral-storage value.", firstNamespace.Name)
	negativeSiblingExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "-1Gi",
	}
	err = namespaceapi.UpdateNamespaceResourceQuotaAnnotation(standardUserClient, pnrq.cluster.ID, firstNamespace.Name, nil, negativeSiblingExtended)
	require.NoError(pnrq.T(), err)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, firstNamespace.Name, nil, negativeSiblingExtended, false, projectapi.NegativeResourceQuotaValidationMessage)
	require.NoError(pnrq.T(), err)

	log.Info("Creating a second namespace that consumes the entire project extended quota (2Gi).")
	fullProjectExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "2Gi",
	}
	fullProjectExtendedAnnotation, err := namespaceapi.BuildResourceQuotaAnnotation(nil, fullProjectExtended)
	require.NoError(pnrq.T(), err)
	secondNamespace, err := namespaceapi.CreateNamespace(standardUserClient, pnrq.cluster.ID, createdProject.Name, namegen.AppendRandomString("testns"), "", nil, map[string]string{namespaceapi.ResourceQuotaAnnotation: fullProjectExtendedAnnotation})
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the second namespace %s is validated, the negative extended sibling is zeroed.", secondNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, nil, fullProjectExtended, true)
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the enforcing ResourceQuota in the second namespace %s is set to the full 2Gi extended ephemeral-storage.", secondNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, pnrq.cluster.ID, secondNamespace.Name, map[string]string{projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "2Gi"})
	require.NoError(pnrq.T(), err)

	log.Info("Creating a third namespace that would exceed the project extended quota.")
	overExtended := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "1Gi",
	}
	overExtendedAnnotation, err := namespaceapi.BuildResourceQuotaAnnotation(nil, overExtended)
	require.NoError(pnrq.T(), err)
	thirdNamespace, err := namespaceapi.CreateNamespace(standardUserClient, pnrq.cluster.ID, createdProject.Name, namegen.AppendRandomString("testns"), "", nil, map[string]string{namespaceapi.ResourceQuotaAnnotation: overExtendedAnnotation})
	require.NoError(pnrq.T(), err)

	log.Infof("Verifying that the third namespace %s fails validation.", thirdNamespace.Name)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, pnrq.cluster.ID, thirdNamespace.Name, nil, overExtended, false, projectapi.ExceedsProjectLimitValidationMessage, projectapi.OversubscribedResourceQuotaValidationMessage)
	require.NoError(pnrq.T(), err)

	log.Infof("Attempting to create an ephemeral-storage-consuming pod in the oversubscribed third namespace %s to confirm enforcement blocks oversubscription.", thirdNamespace.Name)
	podEphemeral := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: "256Mi",
	}
	_, err = podapi.CreatePodWithResources(standardUserClient, pnrq.cluster.ID, thirdNamespace.Name, podapi.PauseImage, podEphemeral, podEphemeral, false)
	require.Error(pnrq.T(), err, "Expected pod creation in the oversubscribed namespace to be rejected")
	require.Contains(pnrq.T(), err.Error(), projectapi.ExceedededResourceQuotaErrorMessage)
}

func TestProjectsNegativeResourceQuotaTestSuite(t *testing.T) {
	suite.Run(t, new(ProjectsNegativeResourceQuotaTestSuite))
}
