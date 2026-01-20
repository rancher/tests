//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package projects

import (
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/shepherd/pkg/wrangler"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	podapi "github.com/rancher/tests/actions/kubeapi/workloads/pods"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
)

type ProjectsExtendedResourceQuotaTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) TearDownSuite() {
	perq.session.Cleanup()
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) SetupSuite() {
	perq.session = session.NewSession()

	client, err := rancher.NewClient("", perq.session)
	require.NoError(perq.T(), err)
	perq.client = client

	log.Info("Getting cluster name from the config file and append cluster details in the struct")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(perq.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(perq.client, clusterName)
	require.NoError(perq.T(), err, "Error getting cluster ID")
	perq.cluster, err = perq.client.Management.Cluster.ByID(clusterID)
	require.NoError(perq.T(), err)
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) setupUserForProject() (*rancher.Client, *wrangler.Context) {
	log.Info("Create a standard user and add the user to the downstream cluster as cluster owner.")
	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(perq.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), perq.cluster, nil)
	require.NoError(perq.T(), err, "Failed to add the user as a cluster owner to the downstream cluster")

	standardUserContext, err := clusterapi.GetClusterWranglerContext(standardUserClient, perq.cluster.ID)
	require.NoError(perq.T(), err)

	return standardUserClient, standardUserContext
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) TestProjectLevelExtendedResourceQuota() {
	subSession := perq.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _ := perq.setupUserForProject()

	log.Info("Create a project with extended ephemeral storage quota applied at project level.")
	projectExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "100Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "200Mi",
	}

	namespaceExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "60Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "120Mi",
	}

	projectTemplate := projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, nil, projectExtendedQuota, nil, namespaceExtendedQuota)
	createdProject, firstNamespace, err := projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectTemplate)
	require.NoError(perq.T(), err)
	err = namespaceapi.VerifyAnnotationInNamespace(standardUserClient, perq.cluster.ID, firstNamespace.Name, projectapi.ResourceQuotaAnnotation, true)
	require.NoError(perq.T(), err, "%s annotation should exist", projectapi.ResourceQuotaAnnotation)

	log.Info("Verify that the resource quota object is created for the namespace and the quota limits and requests in the resource quota are accurate.")
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, perq.cluster.ID, firstNamespace.Name, namespaceExtendedQuota)
	require.NoError(perq.T(), err)

	log.Info("Create a pod in the first namespace within the resource quota limits of the namespace.")
	podEphemeralStorageRequest := "60Mi"
	podEphemeralStorageLimit := "120Mi"
	requests := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: podEphemeralStorageRequest,
	}
	limits := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: podEphemeralStorageLimit,
	}
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, firstNamespace.Name, podapi.PauseImage, requests, limits, true)
	require.NoError(perq.T(), err)

	log.Info("Verify that the resource quota usage in the namespace is accurate after creating the pod within quota limits.")
	expectedUsage := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: podEphemeralStorageRequest,
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   podEphemeralStorageLimit,
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, firstNamespace.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Info("Verify that the resource quota usage in the project is accurate after creating the first namespace.")
	err = projectapi.VerifyUsedProjectExtendedResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Info("Create a second namespace in the same project.")
	namespaceName := namegen.AppendRandomString("testns-")
	secondNamespace, err := namespaceapi.CreateNamespace(standardUserClient, perq.cluster.ID, createdProject.Name, namespaceName, "", nil, nil)
	require.NoError(perq.T(), err)

	log.Info("Verify that the resource quota validation for the second namespace fails.")
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, perq.cluster.ID, secondNamespace.Name, nil, namespaceExtendedQuota, false, "exceeds project limit")
	require.NoError(perq.T(), err)

	log.Info("Attempt to create a pod in the second namespace exceeding the resource quota limits of the project and verify that the pod creation fails with exceeded quota error.")
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, secondNamespace.Name, podapi.PauseImage, requests, limits, false)
	require.Error(perq.T(), err)
	require.Contains(perq.T(), err.Error(), projectapi.ExceedededResourceQuotaErrorMessage)

	log.Info("Verify that resource quota usage in the second namespace remains unchanged after failed pod creation.")
	expectedUsage = map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: namespaceapi.InitialUsedResourceQuotaValue,
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   namespaceapi.InitialUsedResourceQuotaValue,
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, secondNamespace.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Info("Verify project-level quota usage remains unchanged.")
	expectedUsage = map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: podEphemeralStorageRequest,
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   podEphemeralStorageLimit,
	}
	err = projectapi.VerifyUsedProjectExtendedResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, expectedUsage)
	require.NoError(perq.T(), err)
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) TestProjectLevelExtendedPodCountQuota() {
	subSession := perq.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _ := perq.setupUserForProject()

	log.Info("Create project with extended project-level pod count quota = 1")
	projectExtendedQuota := map[string]string{
		projectapi.ExtendedPodResourceQuotaKey: "1",
	}

	namespaceExtendedQuota := map[string]string{
		projectapi.ExtendedPodResourceQuotaKey: "1",
	}

	projectTemplate := projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, nil, projectExtendedQuota, nil, namespaceExtendedQuota)
	createdProject, ns1, err := projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectTemplate)
	require.NoError(perq.T(), err)
	err = namespaceapi.VerifyAnnotationInNamespace(standardUserClient, perq.cluster.ID, ns1.Name, projectapi.ResourceQuotaAnnotation, true)
	require.NoError(perq.T(), err, "%s annotation should exist", projectapi.ResourceQuotaAnnotation)

	log.Info("Verify that the resource quota object is created for the namespace and the quota limits and requests in the resource quota are accurate.")
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, namespaceExtendedQuota)
	require.NoError(perq.T(), err)

	log.Info("Create a pod in the first namespace (should succeed).")
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns1.Name, podapi.PauseImage, nil, nil, true)
	require.NoError(perq.T(), err)

	log.Info("Create second namespace in same project.")
	ns2Name := namegen.AppendRandomString("testns-")
	ns2, err := namespaceapi.CreateNamespace(standardUserClient, perq.cluster.ID, createdProject.Name, ns2Name, "", nil, nil)
	require.NoError(perq.T(), err)

	log.Info("Attempt to create pod in second namespace - SHOULD FAIL if project-level extended quota is enforced.")
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns2.Name, podapi.PauseImage, nil, nil, false)
	require.Error(perq.T(), err)
	require.Contains(perq.T(), err.Error(), projectapi.ExceedededResourceQuotaErrorMessage)
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) TestProjectLevelExistingResourceQuota() {
	subSession := perq.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _ := perq.setupUserForProject()

	log.Info("Create a project with existing pod count resource quota.")
	projectPodLimit := "1"
	namespacePodLimit := "1"

	projectExistingQuota := &v3.ResourceQuotaLimit{
		Pods: projectPodLimit,
	}
	namespaceExistingQuota := &v3.ResourceQuotaLimit{
		Pods: namespacePodLimit,
	}

	projectTemplate := projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, projectExistingQuota, nil, namespaceExistingQuota, nil)
	createdProject, firstNamespace, err := projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectTemplate)
	require.NoError(perq.T(), err)

	log.Info("Create a second namespace in the same project.")
	existingLimits := map[string]string{"pods": namespacePodLimit}
	ns2Name := namegen.AppendRandomString("testns2-")
	ns2, err := namespaceapi.CreateNamespace(standardUserClient, perq.cluster.ID, createdProject.Name, ns2Name, "", nil, nil)
	require.NoError(perq.T(), err)
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, perq.cluster.ID, ns2.Name, existingLimits, nil, false, "exceeds project limit")
	require.NoError(perq.T(), err)

	log.Info("Create a pod in the first namespace.")
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, firstNamespace.Name, podapi.PauseImage, nil, nil, true)
	require.NoError(perq.T(), err)

	log.Info("Attempt to create another pod in the second namespace and verify that it fails with exceeded quota error")
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns2.Name, podapi.PauseImage, nil, nil, false)
	require.Error(perq.T(), err, "expected project-level pod quota to block pods across namespaces")
	require.Contains(perq.T(), err.Error(), projectapi.ExceedededResourceQuotaErrorMessage)
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) TestNamespaceLevelExtendedResourceQuota() {
	subSession := perq.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _ := perq.setupUserForProject()

	log.Info("Create a project with extended ephemeral storage resource quota.")
	projectExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "200Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "400Mi",
	}

	namespaceExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "50Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "100Mi",
	}
	projectTemplate := projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, nil, projectExtendedQuota, nil, namespaceExtendedQuota)
	createdProject, ns1, err := projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectTemplate)
	require.NoError(perq.T(), err)
	require.Equal(perq.T(), projectExtendedQuota, createdProject.Spec.ResourceQuota.Limit.Extended, "Project extended quota mismatch")
	require.Equal(perq.T(), namespaceExtendedQuota, createdProject.Spec.NamespaceDefaultResourceQuota.Limit.Extended, "Namespace extended quota mismatch")

	log.Infof("Verify that the namespace has the annotation: %s.", projectapi.ResourceQuotaAnnotation)
	err = namespaceapi.VerifyAnnotationInNamespace(standardUserClient, perq.cluster.ID, ns1.Name, projectapi.ResourceQuotaAnnotation, true)
	require.NoError(perq.T(), err, "%s annotation should exist", projectapi.ResourceQuotaAnnotation)

	log.Info("Verify the resource quota object created for the namespace has the correct hard and used limits.")
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, namespaceExtendedQuota)
	require.NoError(perq.T(), err)
	expectedUsage := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: namespaceapi.InitialUsedResourceQuotaValue,
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   namespaceapi.InitialUsedResourceQuotaValue,
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Infof("Create a pod in the namespace %s with the quota limits within the namespace quota limits, and verify that the pod is created.", ns1.Name)
	podEphemeralStorageRequest := "50Mi"
	podEphemeralStorageLimit := "100Mi"

	requests := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: podEphemeralStorageRequest,
	}

	limits := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: podEphemeralStorageLimit,
	}
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns1.Name, podapi.PauseImage, requests, limits, true)
	require.NoError(perq.T(), err, "Failed to create pod with ephemeral storage requests and limits")

	log.Info("Verify the resource quota object in the namespace has the correct used limits.")
	expectedUsage = map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: podEphemeralStorageRequest,
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   podEphemeralStorageLimit,
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Info("Attempt to create another pod in the namespace exceeding the namespace quota limits, and verify that the pod creation fails with exceeded quota error.")
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns1.Name, podapi.PauseImage, requests, limits, false)
	require.Error(perq.T(), err, "Pod creation with resource quota limits exceeding namespace quota should fail")
	require.Contains(perq.T(), err.Error(), projectapi.ExceedededResourceQuotaErrorMessage)

	log.Info("Verify that resource quota usage in the namespace remains unchanged after failed pod creation.")
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Info("Create another namespace in the same project.")
	ns2Name := namegen.AppendRandomString("testns")
	ns2, err := namespaceapi.CreateNamespace(standardUserClient, perq.cluster.ID, createdProject.Name, ns2Name, "", nil, nil)
	require.NoError(perq.T(), err)

	log.Info("Verify that the resource quota usage in the project is accurate.")
	expectedUsage = map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "100Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "200Mi",
	}
	err = projectapi.VerifyUsedProjectExtendedResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Infof("Create a pod in the namespace %s with the quota limits within the namespace quota limits, and verify that the pods are created.", ns2.Name)
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns2.Name, podapi.PauseImage, requests, limits, true)
	require.NoError(perq.T(), err, "Failed to create pod with ephemeral storage requests and limits")

	log.Info("Verify that the resource quota usage in the namespace is accurate after creating a pod within the quota limits.")
	expectedUsage = map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: podEphemeralStorageRequest,
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   podEphemeralStorageLimit,
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns2.Name, expectedUsage)
	require.NoError(perq.T(), err)
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) TestNamespaceLevelExtendedPodCountQuota() {
	subSession := perq.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _ := perq.setupUserForProject()

	log.Info("Create a project with extended pod count resource quota.")
	projectExtendedQuota := map[string]string{
		projectapi.ExtendedPodResourceQuotaKey: "10",
	}

	namespaceExtendedQuota := map[string]string{
		projectapi.ExtendedPodResourceQuotaKey: "1",
	}

	projectTemplate := projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, nil, projectExtendedQuota, nil, namespaceExtendedQuota)
	createdProject, ns1, err := projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectTemplate)
	require.NoError(perq.T(), err)
	require.Equal(perq.T(), projectExtendedQuota, createdProject.Spec.ResourceQuota.Limit.Extended, "Project extended quota mismatch")
	require.Equal(perq.T(), namespaceExtendedQuota, createdProject.Spec.NamespaceDefaultResourceQuota.Limit.Extended, "Namespace extended quota mismatch")

	log.Infof("Verify that the namespace has the annotation: %s.", projectapi.ResourceQuotaAnnotation)
	err = namespaceapi.VerifyAnnotationInNamespace(standardUserClient, perq.cluster.ID, ns1.Name, projectapi.ResourceQuotaAnnotation, true)
	require.NoError(perq.T(), err, "%s annotation should exist", projectapi.ResourceQuotaAnnotation)

	log.Info("Verify the resource quota object created for the namespace has the correct hard and used limits.")
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, namespaceExtendedQuota)
	require.NoError(perq.T(), err)
	expectedUsage := map[string]string{
		projectapi.ExtendedPodResourceQuotaKey: namespaceapi.InitialUsedResourceQuotaValue,
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Infof("Create a pod in the namespace %s with the quota limits within the namespace quota limits, and verify that the pod is created.", ns1.Name)
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns1.Name, podapi.PauseImage, nil, nil, true)
	require.NoError(perq.T(), err)

	log.Info("Verify the resource quota object in the namespace has the correct used limits.")
	expectedUsage = map[string]string{
		projectapi.ExtendedPodResourceQuotaKey: "1",
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Info("Attempt to create another pod in the namespace exceeding the namespace quota limits, and verify that the pod creation fails with exceeded quota error.")
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns1.Name, podapi.PauseImage, nil, nil, false)
	require.Error(perq.T(), err)
	require.Contains(perq.T(), err.Error(), projectapi.ExceedededResourceQuotaErrorMessage)

	log.Info("Verify that resource quota usage in the namespace remains unchanged after failed pod creation.")
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Info("Create another namespace in the same project.")
	ns2Name := namegen.AppendRandomString("testns")
	ns2, err := namespaceapi.CreateNamespace(standardUserClient, perq.cluster.ID, createdProject.Name, ns2Name, "", nil, nil)
	require.NoError(perq.T(), err)

	log.Info("Verify that the resource quota usage in the project is accurate.")
	expectedUsage = map[string]string{
		projectapi.ExtendedPodResourceQuotaKey: "2",
	}
	err = projectapi.VerifyUsedProjectExtendedResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Infof("Create a pod in the namespace %s with the quota limits within the namespace quota limits, and verify that the pods are created.", ns2.Name)
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns2.Name, podapi.PauseImage, nil, nil, true)
	require.NoError(perq.T(), err)

	log.Info("Verify that the resource quota usage in the namespace is accurate after creating a pod within the quota limits.")
	expectedUsage = map[string]string{
		projectapi.ExtendedPodResourceQuotaKey: "1",
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns2.Name, expectedUsage)
	require.NoError(perq.T(), err)
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) TestNamespaceLevelShorthandExtendedResourceQuota() {
	subSession := perq.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _ := perq.setupUserForProject()

	log.Info("Create a project with shorthand extended ephemeral storage resource quota.")
	projectExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaKey: "200Mi",
	}

	namespaceExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaKey: "50Mi",
	}
	projectTemplate := projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, nil, projectExtendedQuota, nil, namespaceExtendedQuota)
	createdProject, ns1, err := projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectTemplate)
	require.NoError(perq.T(), err)
	require.Equal(perq.T(), projectExtendedQuota, createdProject.Spec.ResourceQuota.Limit.Extended, "Project extended quota mismatch")
	require.Equal(perq.T(), namespaceExtendedQuota, createdProject.Spec.NamespaceDefaultResourceQuota.Limit.Extended, "Namespace extended quota mismatch")

	log.Info("Verify the resource quota object created for the namespace has the correct hard and used limits.")
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, namespaceExtendedQuota)
	require.NoError(perq.T(), err)
	expectedUsage := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaKey: namespaceapi.InitialUsedResourceQuotaValue,
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Infof("Create a pod in the namespace %s with the quota limits within the namespace quota limits, and verify that the pod is created.", ns1.Name)
	podEphemeralStorageRequest := "50Mi"
	podEphemeralStorageLimit := "100Mi"

	requests := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: podEphemeralStorageRequest,
	}

	limits := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: podEphemeralStorageLimit,
	}
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns1.Name, podapi.PauseImage, requests, limits, true)
	require.NoError(perq.T(), err, "Failed to create pod with ephemeral storage requests and limits")

	log.Info("Verify the resource quota object in the namespace has the correct used limits.")
	expectedUsage = map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaKey: podEphemeralStorageRequest,
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Info("Attempt to create another pod in the namespace exceeding the namespace quota limits, and verify that the pod creation fails with exceeded quota error.")
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns1.Name, podapi.PauseImage, requests, limits, false)
	require.Error(perq.T(), err, "Pod creation with resource quota limits exceeding namespace quota should fail")
	require.Contains(perq.T(), err.Error(), projectapi.ExceedededResourceQuotaErrorMessage)

	log.Info("Verify that resource quota usage in the namespace remains unchanged after failed pod creation.")
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Info("Create another namespace in the same project.")
	ns2Name := namegen.AppendRandomString("testns")
	ns2, err := namespaceapi.CreateNamespace(standardUserClient, perq.cluster.ID, createdProject.Name, ns2Name, "", nil, nil)
	require.NoError(perq.T(), err)

	log.Info("Verify that the resource quota usage in the project is accurate.")
	expectedUsage = map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaKey: "100Mi",
	}
	err = projectapi.VerifyUsedProjectExtendedResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Infof("Create a pod in the namespace %s with the quota limits within the namespace quota limits, and verify that the pods are created.", ns2.Name)
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns2.Name, podapi.PauseImage, requests, limits, true)
	require.NoError(perq.T(), err, "Failed to create pod with ephemeral storage requests and limits")

	log.Info("Verify that the resource quota usage in the namespace is accurate after creating a pod within the quota limits.")
	expectedUsage = map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaKey: podEphemeralStorageRequest,
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns2.Name, expectedUsage)
	require.NoError(perq.T(), err)
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) TestNamespaceLevelExistingResourceQuota() {
	subSession := perq.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _ := perq.setupUserForProject()

	log.Info("Create a project with existing pod count resource quota.")
	projectPodLimit := "10"
	namespacePodLimit := "1"

	projectExistingQuota := &v3.ResourceQuotaLimit{
		Pods: projectPodLimit,
	}
	namespaceExistingQuota := &v3.ResourceQuotaLimit{
		Pods: namespacePodLimit,
	}

	projectTemplate := projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, projectExistingQuota, nil, namespaceExistingQuota, nil)
	createdProject, ns1, err := projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectTemplate)
	require.NoError(perq.T(), err)
	require.Equal(perq.T(), projectPodLimit, createdProject.Spec.ResourceQuota.Limit.Pods, "Project existing quota mismatch")
	require.Equal(perq.T(), namespacePodLimit, createdProject.Spec.NamespaceDefaultResourceQuota.Limit.Pods, "Namespace existing quota mismatch")

	log.Infof("Verify that the namespace has the annotation: %s.", projectapi.ResourceQuotaAnnotation)
	err = namespaceapi.VerifyAnnotationInNamespace(standardUserClient, perq.cluster.ID, ns1.Name, projectapi.ResourceQuotaAnnotation, true)
	require.NoError(perq.T(), err, "%s annotation should exist", projectapi.ResourceQuotaAnnotation)

	log.Info("Verify the resource quota object created for the namespace has the correct hard and used limits.")
	expectedNamespaceQuota := map[string]string{
		projectapi.ExistingPodResourceQuotaKey: namespacePodLimit,
	}
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedNamespaceQuota)
	require.NoError(perq.T(), err)
	expectedUsage := map[string]string{
		projectapi.ExistingPodResourceQuotaKey: namespaceapi.InitialUsedResourceQuotaValue,
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Infof("Create a pod in the namespace %s with the quota limits within the namespace quota limits, and verify that the pod is created.", ns1.Name)
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns1.Name, podapi.PauseImage, nil, nil, true)
	require.NoError(perq.T(), err)

	log.Info("Verify the resource quota object in the namespace has the correct used limits.")
	expectedUsage = map[string]string{
		projectapi.ExistingPodResourceQuotaKey: "1",
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Info("Attempt to create another pod in the namespace exceeding the namespace quota limits, and verify that the pod creation fails with exceeded quota error.")
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns1.Name, podapi.PauseImage, nil, nil, false)
	require.Error(perq.T(), err)
	require.Contains(perq.T(), err.Error(), projectapi.ExceedededResourceQuotaErrorMessage)

	log.Info("Verify that resource quota usage in the namespace remains unchanged after failed pod creation.")
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsage)
	require.NoError(perq.T(), err)

	log.Info("Create another namespace in the same project.")
	ns2Name := namegen.AppendRandomString("testns")
	ns2, err := namespaceapi.CreateNamespace(standardUserClient, perq.cluster.ID, createdProject.Name, ns2Name, "", nil, nil)
	require.NoError(perq.T(), err)
	projectExistingUsed := map[string]string{
		projectapi.ExistingPodResourceQuotaKey: "2",
	}
	err = projectapi.VerifyUsedProjectExistingResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, projectExistingUsed)
	require.NoError(perq.T(), err)

	log.Infof("Create a pod in the namespace %s with the quota limits within the namespace quota limits, and verify that the pods are created.", ns2.Name)
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns2.Name, podapi.PauseImage, nil, nil, true)
	require.NoError(perq.T(), err)

	log.Info("Verify that the resource quota usage in the namespace is accurate after creating a pod within the quota limits.")
	expectedUsage = map[string]string{
		projectapi.ExistingPodResourceQuotaKey: "1",
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns2.Name, expectedUsage)
	require.NoError(perq.T(), err)
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) TestNamespaceMixedQuotaExceedExtendedEphemeralStorage() {
	subSession := perq.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _ := perq.setupUserForProject()

	log.Info("Create a project with existing pod count quota and extended ephemeral storage quota.")
	projectPodCount := "10"
	namespacePodCount := "2"
	projectExistingQuota := &v3.ResourceQuotaLimit{
		Pods: projectPodCount,
	}
	namespaceExistingQuota := &v3.ResourceQuotaLimit{
		Pods: namespacePodCount,
	}
	projectExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "200Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "400Mi",
	}
	namespaceExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "50Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "100Mi",
	}

	projectTemplate := projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, projectExistingQuota, projectExtendedQuota, namespaceExistingQuota, namespaceExtendedQuota)
	createdProject, ns1, err := projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectTemplate)
	require.NoError(perq.T(), err)
	require.Equal(perq.T(), projectPodCount, createdProject.Spec.ResourceQuota.Limit.Pods)
	require.Equal(perq.T(), projectExtendedQuota, createdProject.Spec.ResourceQuota.Limit.Extended)
	require.Equal(perq.T(), namespacePodCount, createdProject.Spec.NamespaceDefaultResourceQuota.Limit.Pods)
	require.Equal(perq.T(), namespaceExtendedQuota, createdProject.Spec.NamespaceDefaultResourceQuota.Limit.Extended)

	log.Infof("Verify namespace %s has resource quota annotation.", ns1.Name)
	err = namespaceapi.VerifyAnnotationInNamespace(standardUserClient, perq.cluster.ID, ns1.Name, projectapi.ResourceQuotaAnnotation, true)
	require.NoError(perq.T(), err)

	log.Info("Verify initial hard and used limits in namespace.")
	expectedHard := map[string]string{
		projectapi.ExistingPodResourceQuotaKey:                  namespacePodCount,
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "50Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "100Mi",
	}
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedHard)
	require.NoError(perq.T(), err)

	expectedUsed := map[string]string{
		projectapi.ExistingPodResourceQuotaKey:                  namespaceapi.InitialUsedResourceQuotaValue,
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: namespaceapi.InitialUsedResourceQuotaValue,
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   namespaceapi.InitialUsedResourceQuotaValue,
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsed)
	require.NoError(perq.T(), err)

	log.Info("Create pod within both pod-count and ephemeral-storage limits.")
	requests := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: "50Mi",
	}
	limits := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: "100Mi",
	}
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns1.Name, podapi.PauseImage, requests, limits, true)
	require.NoError(perq.T(), err)

	expectedUsed = map[string]string{
		projectapi.ExistingPodResourceQuotaKey:                  "1",
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "50Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "100Mi",
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsed)
	require.NoError(perq.T(), err)

	log.Info("Attempt to create another pod and verify that it fails due to exceeding ephemeral-storage limits.")
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns1.Name, podapi.PauseImage, requests, limits, false)
	require.Error(perq.T(), err)
	require.Contains(perq.T(), err.Error(), projectapi.ExceedededResourceQuotaErrorMessage)

	log.Info("Verify project level used quota is accurate.")
	projectExtendedUsed := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "50Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "100Mi",
	}
	projectExistingUsed := map[string]string{
		projectapi.ExistingPodResourceQuotaKey: namespacePodCount,
	}
	err = projectapi.VerifyUsedProjectExtendedResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, projectExtendedUsed)
	require.NoError(perq.T(), err)
	err = projectapi.VerifyUsedProjectExistingResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, projectExistingUsed)
	require.NoError(perq.T(), err)
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) TestNamespaceMixedQuotaExceedExistingPodCount() {
	subSession := perq.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _ := perq.setupUserForProject()

	log.Info("Create a project with existing pod count quota and extended ephemeral storage quota.")
	projectPodCount := "10"
	namespacePodCount := "1"
	projectExistingQuota := &v3.ResourceQuotaLimit{
		Pods: projectPodCount,
	}
	namespaceExistingQuota := &v3.ResourceQuotaLimit{
		Pods: namespacePodCount,
	}
	projectExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "300Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "500Mi",
	}
	namespaceExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "100Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "200Mi",
	}

	projectTemplate := projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, projectExistingQuota, projectExtendedQuota, namespaceExistingQuota, namespaceExtendedQuota)
	createdProject, ns1, err := projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectTemplate)
	require.NoError(perq.T(), err)
	require.Equal(perq.T(), projectPodCount, createdProject.Spec.ResourceQuota.Limit.Pods)
	require.Equal(perq.T(), projectExtendedQuota, createdProject.Spec.ResourceQuota.Limit.Extended)
	require.Equal(perq.T(), namespacePodCount, createdProject.Spec.NamespaceDefaultResourceQuota.Limit.Pods)
	require.Equal(perq.T(), namespaceExtendedQuota, createdProject.Spec.NamespaceDefaultResourceQuota.Limit.Extended)

	log.Infof("Verify namespace %s has resource quota annotation.", ns1.Name)
	err = namespaceapi.VerifyAnnotationInNamespace(standardUserClient, perq.cluster.ID, ns1.Name, projectapi.ResourceQuotaAnnotation, true)
	require.NoError(perq.T(), err)

	log.Info("Verify initial hard and used limits in namespace.")
	expectedHard := map[string]string{
		projectapi.ExistingPodResourceQuotaKey:                  namespacePodCount,
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "100Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "200Mi",
	}
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedHard)
	require.NoError(perq.T(), err)

	expectedUsed := map[string]string{
		projectapi.ExistingPodResourceQuotaKey:                  namespaceapi.InitialUsedResourceQuotaValue,
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: namespaceapi.InitialUsedResourceQuotaValue,
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   namespaceapi.InitialUsedResourceQuotaValue,
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsed)
	require.NoError(perq.T(), err)

	log.Info("Create a pod within both pod-count and ephemeral-storage limits.")
	requests := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: "50Mi",
	}
	limits := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: "100Mi",
	}
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns1.Name, podapi.PauseImage, requests, limits, true)
	require.NoError(perq.T(), err)

	expectedUsed = map[string]string{
		projectapi.ExistingPodResourceQuotaKey:                  "1",
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "50Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "100Mi",
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsed)
	require.NoError(perq.T(), err)

	log.Info("Attempt to create another pod and verify that it fails due to exceeding pod limits.")
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns1.Name, podapi.PauseImage, requests, limits, false)
	require.Error(perq.T(), err)
	require.Contains(perq.T(), err.Error(), projectapi.ExceedededResourceQuotaErrorMessage)

	log.Info("Verify project level used quota is accurate.")
	projectExtendedUsed := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "100Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "200Mi",
	}
	projectExistingUsed := map[string]string{
		projectapi.ExistingPodResourceQuotaKey: namespacePodCount,
	}
	err = projectapi.VerifyUsedProjectExtendedResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, projectExtendedUsed)
	require.NoError(perq.T(), err)
	err = projectapi.VerifyUsedProjectExistingResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, projectExistingUsed)
	require.NoError(perq.T(), err)
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) TestNamespaceLevelExistingOverridesExtended() {
	subSession := perq.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _ := perq.setupUserForProject()

	log.Info("Create project with existing and extended pod count resource quotas that conflict.")
	projectPodCount := "10"
	namespacePodCount := "1"

	projectExistingQuota := &v3.ResourceQuotaLimit{
		Pods: projectPodCount,
	}

	namespaceExistingQuota := &v3.ResourceQuotaLimit{
		Pods: namespacePodCount,
	}

	projectExtendedQuota := map[string]string{
		projectapi.ExtendedPodResourceQuotaKey:                  "5",
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "200Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "400Mi",
	}

	namespaceExtendedQuota := map[string]string{
		projectapi.ExtendedPodResourceQuotaKey:                  "3",
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "50Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "100Mi",
	}

	projectTemplate := projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, projectExistingQuota, projectExtendedQuota, namespaceExistingQuota, namespaceExtendedQuota)
	createdProject, ns1, err := projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectTemplate)
	require.NoError(perq.T(), err)
	require.Equal(perq.T(), projectPodCount, createdProject.Spec.ResourceQuota.Limit.Pods)
	require.Equal(perq.T(), namespacePodCount, createdProject.Spec.NamespaceDefaultResourceQuota.Limit.Pods)
	require.Equal(perq.T(), projectExtendedQuota, createdProject.Spec.ResourceQuota.Limit.Extended)
	require.Equal(perq.T(), namespaceExtendedQuota, createdProject.Spec.NamespaceDefaultResourceQuota.Limit.Extended)

	log.Infof("Verify namespace %s has resource quota annotation.", ns1.Name)
	err = namespaceapi.VerifyAnnotationInNamespace(standardUserClient, perq.cluster.ID, ns1.Name, projectapi.ResourceQuotaAnnotation, true)
	require.NoError(perq.T(), err)

	log.Info("Verify initial hard and used limits in namespace.")
	expectedHard := map[string]string{
		projectapi.ExistingPodResourceQuotaKey:                  namespacePodCount,
		projectapi.ExtendedPodResourceQuotaKey:                  "3",
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "50Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "100Mi",
	}
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedHard)
	require.NoError(perq.T(), err)

	expectedUsed := map[string]string{
		projectapi.ExistingPodResourceQuotaKey:                  namespaceapi.InitialUsedResourceQuotaValue,
		projectapi.ExtendedPodResourceQuotaKey:                  namespaceapi.InitialUsedResourceQuotaValue,
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: namespaceapi.InitialUsedResourceQuotaValue,
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   namespaceapi.InitialUsedResourceQuotaValue,
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsed)
	require.NoError(perq.T(), err)

	log.Info("Create a pod within both pod-count and ephemeral-storage limits.")
	requests := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: "50Mi",
	}
	limits := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: "100Mi",
	}

	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns1.Name, podapi.PauseImage, requests, limits, true)
	require.NoError(perq.T(), err)

	expectedUsed = map[string]string{
		projectapi.ExistingPodResourceQuotaKey:                  "1",
		projectapi.ExtendedPodResourceQuotaKey:                  "1",
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "50Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "100Mi",
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsed)
	require.NoError(perq.T(), err)

	log.Info("Attempt to create another pod and verify that it fails due to exceeding EXISTING pod limits.")
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns1.Name, podapi.PauseImage, requests, limits, false)
	require.Error(perq.T(), err)
	require.Contains(perq.T(), err.Error(), projectapi.ExceedededResourceQuotaErrorMessage)

	log.Info("Verify used quota remains unchanged after failure.")
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns1.Name, expectedUsed)
	require.NoError(perq.T(), err)

	log.Info("Verify project-level used quota is accurate.")
	projectExtendedUsed := map[string]string{
		projectapi.ExtendedPodResourceQuotaKey:                  "3",
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "50Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "100Mi",
	}
	projectExistingUsed := map[string]string{
		projectapi.ExistingPodResourceQuotaKey: namespacePodCount,
	}
	err = projectapi.VerifyUsedProjectExtendedResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, projectExtendedUsed)
	require.NoError(perq.T(), err)
	err = projectapi.VerifyUsedProjectExistingResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, projectExistingUsed)
	require.NoError(perq.T(), err)
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) TestProjectResourceQuotaUsedLimitOnNamespaceDeleteAndCreate() {
	subSession := perq.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _ := perq.setupUserForProject()

	log.Info("Create project with existing and extended resource quotas.")
	projectExistingQuota := &v3.ResourceQuotaLimit{
		Pods: "10",
	}

	projectExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "500Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "1Gi",
	}

	namespaceExistingQuota := &v3.ResourceQuotaLimit{
		Pods: "2",
	}

	namespaceExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "50Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "100Mi",
	}

	projectTemplate := projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, projectExistingQuota, projectExtendedQuota, namespaceExistingQuota, namespaceExtendedQuota)
	createdProject, _, err := projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectTemplate)
	require.NoError(perq.T(), err)

	log.Info("Verify initial project UsedLimit after first namespace creation.")
	expectedProjectExistingUsed := map[string]string{
		projectapi.ExistingPodResourceQuotaKey: "2",
	}
	expectedProjectExtendedUsed := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "50Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "100Mi",
	}

	err = projectapi.VerifyUsedProjectExistingResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, expectedProjectExistingUsed)
	require.NoError(perq.T(), err)
	err = projectapi.VerifyUsedProjectExtendedResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, expectedProjectExtendedUsed)
	require.NoError(perq.T(), err)

	log.Info("Create a second namespace in the same project and verify project used quota increases.")
	ns2Name := namegen.AppendRandomString("testns-")
	ns2, err := namespaceapi.CreateNamespace(standardUserClient, perq.cluster.ID, createdProject.Name, ns2Name, "", nil, nil)
	require.NoError(perq.T(), err)

	log.Info("Verify project UsedLimit is updated after namespace creation.")
	expectedProjectExistingUsed = map[string]string{
		projectapi.ExistingPodResourceQuotaKey: "4",
	}
	expectedProjectExtendedUsed = map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "100Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "200Mi",
	}

	err = projectapi.VerifyUsedProjectExistingResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, expectedProjectExistingUsed)
	require.NoError(perq.T(), err)
	err = projectapi.VerifyUsedProjectExtendedResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, expectedProjectExtendedUsed)
	require.NoError(perq.T(), err)

	log.Infof("Delete namespace %s and verify project used quota decreases.", ns2.Name)
	err = namespaceapi.DeleteNamespace(standardUserClient, perq.cluster.ID, ns2.Name)
	require.NoError(perq.T(), err)

	log.Info("Verify project UsedLimit is updated after namespace deletion.")
	expectedProjectExistingUsed = map[string]string{
		projectapi.ExistingPodResourceQuotaKey: "2",
	}
	expectedProjectExtendedUsed = map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "50Mi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "100Mi",
	}

	err = projectapi.VerifyUsedProjectExistingResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, expectedProjectExistingUsed)
	require.NoError(perq.T(), err)
	err = projectapi.VerifyUsedProjectExtendedResourceQuota(standardUserClient, perq.cluster.ID, createdProject.Name, expectedProjectExtendedUsed)
	require.NoError(perq.T(), err)
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) TestMoveNamespaceWithoutQuotaToProjectWithExtendedQuota() {
	subSession := perq.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _ := perq.setupUserForProject()

	log.Info("Create a Project without resource quota.")
	projectWithoutQuota, ns, err := projectapi.CreateProjectAndNamespace(standardUserClient, perq.cluster.ID)
	require.NoError(perq.T(), err)
	err = projectapi.VerifyProjectHasNoExtendedResourceQuota(standardUserClient, perq.cluster.ID, projectWithoutQuota.Name)
	require.NoError(perq.T(), err)

	log.Info("Verify namespace initially has no resource quota annotation.")
	err = namespaceapi.VerifyAnnotationInNamespace(standardUserClient, perq.cluster.ID, ns.Name, projectapi.ResourceQuotaAnnotation, false)
	require.NoError(perq.T(), err)

	log.Info("Create another project with extended ephemeral storage quota.")
	projectExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "100Gi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "200Gi",
	}

	namespaceExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "10Gi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "20Gi",
	}

	projectTemplate := projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, nil, projectExtendedQuota, nil, namespaceExtendedQuota)
	projectWithQuota, _, err := projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectTemplate)
	require.NoError(perq.T(), err)

	log.Infof("Verify used limit for project %s before namespace move.", projectWithQuota.Name)
	err = projectapi.VerifyUsedProjectExtendedResourceQuota(standardUserClient, perq.cluster.ID, projectWithQuota.Name, namespaceExtendedQuota)
	require.NoError(perq.T(), err)

	log.Infof("Move namespace %s from %s to Project %s.", ns.Name, projectWithoutQuota.Name, projectWithQuota.Name)
	err = namespaceapi.MoveNamespaceToProject(standardUserClient, perq.cluster.ID, ns.Name, projectWithQuota.Name)
	require.NoError(perq.T(), err)

	log.Info("Verify resource quota annotation exists in the moved namespace.")
	err = namespaceapi.VerifyAnnotationInNamespace(standardUserClient, perq.cluster.ID, ns.Name, projectapi.ResourceQuotaAnnotation, true)
	require.NoError(perq.T(), err)

	log.Info("Verify ResourceQuota hard limits in the moved namespace.")
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns.Name, namespaceExtendedQuota)
	require.NoError(perq.T(), err)

	log.Info("Verify initial used values for the resources in the moved namespace.")
	expectedUsed := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: namespaceapi.InitialUsedResourceQuotaValue,
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   namespaceapi.InitialUsedResourceQuotaValue,
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns.Name, expectedUsed)
	require.NoError(perq.T(), err)

	log.Info("Create a pod within extended quota limits.")
	requests := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: "10Gi",
	}
	limits := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: "20Gi",
	}

	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns.Name, podapi.PauseImage, requests, limits, true)
	require.NoError(perq.T(), err)

	log.Info("Verify ResourceQuota used limit in the namespace is updated.")
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns.Name, namespaceExtendedQuota)
	require.NoError(perq.T(), err)

	log.Info("Attempt to create pod exceeding extended quota limits.")
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns.Name, podapi.PauseImage, requests, limits, false)
	require.Error(perq.T(), err)
	require.Contains(perq.T(), err.Error(), projectapi.ExceedededResourceQuotaErrorMessage)

	log.Info("Verify ResourceQuota used remains unchanged after failure.")
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns.Name, namespaceExtendedQuota)
	require.NoError(perq.T(), err)

	log.Infof("Verify used limit for project %s is updated after namespace move.", projectWithQuota.Name)
	expectedProjectUsed := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "20Gi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "40Gi",
	}
	err = projectapi.VerifyUsedProjectExtendedResourceQuota(standardUserClient, perq.cluster.ID, projectWithQuota.Name, expectedProjectUsed)
	require.NoError(perq.T(), err)
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) TestMoveNamespaceWithExtendedQuotaToProjectWithoutQuota() {
	subSession := perq.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _ := perq.setupUserForProject()

	log.Info("Create a project with extended ephemeral storage quota.")
	projectExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "100Gi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "200Gi",
	}

	namespaceExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "10Gi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "20Gi",
	}

	projectWithQuotaTemplate := projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectWithQuotaTemplate, nil, projectExtendedQuota, nil, namespaceExtendedQuota)
	projectWithQuota, ns, err := projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectWithQuotaTemplate)
	require.NoError(perq.T(), err)
	err = projectapi.VerifyUsedProjectExtendedResourceQuota(standardUserClient, perq.cluster.ID, projectWithQuota.Name, namespaceExtendedQuota)
	require.NoError(perq.T(), err)

	log.Info("Verify namespace has the resource quota annotation.")
	err = namespaceapi.VerifyAnnotationInNamespace(standardUserClient, perq.cluster.ID, ns.Name, projectapi.ResourceQuotaAnnotation, true)
	require.NoError(perq.T(), err)

	log.Info("Create a pod within extended quota limits.")
	requests := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: "10Gi",
	}
	limits := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: "20Gi",
	}

	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns.Name, podapi.PauseImage, requests, limits, true)
	require.NoError(perq.T(), err)

	log.Info("Verify resource quota used limit is updated in the namespace.")
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns.Name, namespaceExtendedQuota)
	require.NoError(perq.T(), err)

	log.Info("Verify that creating a pod exceeding extended quota limits fails.")
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns.Name, podapi.PauseImage, requests, limits, false)
	require.Error(perq.T(), err)
	require.Contains(perq.T(), err.Error(), projectapi.ExceedededResourceQuotaErrorMessage)

	log.Info("Create a second project without any resource quota.")
	projectWithoutQuota, _, err := projectapi.CreateProjectAndNamespace(standardUserClient, perq.cluster.ID)
	require.NoError(perq.T(), err)
	err = projectapi.VerifyProjectHasNoExtendedResourceQuota(standardUserClient, perq.cluster.ID, projectWithoutQuota.Name)
	require.NoError(perq.T(), err)

	log.Infof("Move namespace %s from project %s to project %s.", ns.Name, projectWithQuota.Name, projectWithoutQuota.Name)
	err = namespaceapi.MoveNamespaceToProject(standardUserClient, perq.cluster.ID, ns.Name, projectWithoutQuota.Name)
	require.NoError(perq.T(), err)

	log.Info("Verify resource quota is removed from the moved namespace.")
	err = namespaceapi.VerifyAnnotationInNamespace(standardUserClient, perq.cluster.ID, ns.Name, projectapi.ResourceQuotaAnnotation, false)
	require.NoError(perq.T(), err)
	err = namespaceapi.VerifyNamespaceHasNoResourceQuota(standardUserClient, perq.cluster.ID, ns.Name)
	require.NoError(perq.T(), err)

	log.Info("Verify pod creation is no longer quota restricted.")
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns.Name, podapi.PauseImage, requests, limits, true)
	require.NoError(perq.T(), err)

	log.Infof("Verify used limit for project %s is updated after namespace move.", projectWithQuota.Name)
	expectedProjectUsed := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: namespaceapi.InitialUsedResourceQuotaValue,
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   namespaceapi.InitialUsedResourceQuotaValue,
	}
	err = projectapi.VerifyUsedProjectExtendedResourceQuota(standardUserClient, perq.cluster.ID, projectWithQuota.Name, expectedProjectUsed)
	require.NoError(perq.T(), err)
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) TestNamespaceOverrideExtendedQuotaWithinProjectLimits() {
	subSession := perq.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _ := perq.setupUserForProject()

	log.Info("Create project with extended ephemeral-storage quota.")
	projectExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "100Gi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "200Gi",
	}

	namespaceExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "20Gi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "40Gi",
	}

	projectTemplate := projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, nil, projectExtendedQuota, nil, namespaceExtendedQuota)
	_, ns, err := projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectTemplate)
	require.NoError(perq.T(), err)
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns.Name, namespaceExtendedQuota)
	require.NoError(perq.T(), err)

	log.Info("Override namespace ResourceQuota within project limits.")
	validNamespaceOverrideQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "50Gi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "100Gi",
	}

	err = namespaceapi.UpdateNamespaceResourceQuotaAnnotation(standardUserClient, perq.cluster.ID, ns.Name, nil, validNamespaceOverrideQuota)
	require.NoError(perq.T(), err)

	log.Info("Verify namespace ResourceQuota reflects overridden values.")
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns.Name, validNamespaceOverrideQuota)
	require.NoError(perq.T(), err)

	log.Info("Create pod within overridden namespace quota.")
	requests := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: "40Gi",
	}
	limits := map[corev1.ResourceName]string{
		corev1.ResourceEphemeralStorage: "80Gi",
	}
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns.Name, podapi.PauseImage, requests, limits, true)
	require.NoError(perq.T(), err)

	log.Info("Verify ResourceQuota used values are updated correctly in the namespace.")
	expectedUsed := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "40Gi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "80Gi",
	}
	err = namespaceapi.VerifyUsedNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns.Name, expectedUsed)
	require.NoError(perq.T(), err)

	log.Info("Attempt to create a pod exceeding namespace quota.")
	_, err = podapi.CreatePodWithResources(standardUserClient, perq.cluster.ID, ns.Name, podapi.PauseImage, requests, limits, false)
	require.Error(perq.T(), err)
	require.Contains(perq.T(), err.Error(), projectapi.ExceedededResourceQuotaErrorMessage)

	log.Info("Attempt to override namespace quota beyond project limits.")
	invalidNamespaceQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "150Gi",
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit:   "300Gi",
	}
	err = namespaceapi.UpdateNamespaceResourceQuotaAnnotation(standardUserClient, perq.cluster.ID, ns.Name, nil, invalidNamespaceQuota)
	require.NoError(perq.T(), err)

	log.Info("Verify resource quota validation status in the namespace reflects exceeded project limits.")
	err = namespaceapi.VerifyNamespaceResourceQuotaValidationStatus(standardUserClient, perq.cluster.ID, ns.Name, nil, invalidNamespaceQuota, false, "exceeds project limit")
	require.NoError(perq.T(), err)

	log.Info("Verify namespace ResourceQuota remains unchanged after failed override.")
	err = namespaceapi.VerifyNamespaceResourceQuota(standardUserClient, perq.cluster.ID, ns.Name, validNamespaceOverrideQuota)
	require.NoError(perq.T(), err)
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) TestProjectExtendedQuotaLessThanNamespaceQuota() {
	subSession := perq.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _ := perq.setupUserForProject()

	log.Info("Create a project where project extended ephemeral-storage quota < namespace extended ephemeral-storage quota.")
	projectExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaKey: "10Mi",
	}

	namespaceExtendedQuota := map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaKey: "20Mi",
	}

	projectTemplate := projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, nil, projectExtendedQuota, nil, namespaceExtendedQuota)
	_, _, err := projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectTemplate)
	require.Error(perq.T(), err)
	require.Contains(perq.T(), err.Error(), projectapi.NamespaceQuotaExceedsProjectQuotaErrorMessage)

	projectExtendedQuota = map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "10Mi",
	}

	namespaceExtendedQuota = map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaLimit: "20Mi",
	}

	projectTemplate = projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, nil, projectExtendedQuota, nil, namespaceExtendedQuota)
	_, _, err = projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectTemplate)
	require.Error(perq.T(), err)
	require.Contains(perq.T(), err.Error(), projectapi.NamespaceQuotaExceedsProjectQuotaErrorMessage)

	projectExtendedQuota = map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "10Mi",
	}

	namespaceExtendedQuota = map[string]string{
		projectapi.ExtendedEphemeralStorageResourceQuotaRequest: "20Mi",
	}
	projectTemplate = projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, nil, projectExtendedQuota, nil, namespaceExtendedQuota)
	_, _, err = projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectTemplate)
	require.Error(perq.T(), err)
	require.Contains(perq.T(), err.Error(), projectapi.NamespaceQuotaExceedsProjectQuotaErrorMessage)
}

func (perq *ProjectsExtendedResourceQuotaTestSuite) TestProjectExistingQuotaLessThanNamespaceQuota() {
	subSession := perq.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _ := perq.setupUserForProject()

	log.Info("Create a project where project existing pod quota < namespace existing pod quota.")
	projectExistingQuota := &v3.ResourceQuotaLimit{
		Pods: "1",
	}
	namespaceExistingQuota := &v3.ResourceQuotaLimit{
		Pods: "2",
	}
	projectTemplate := projectapi.NewProjectTemplate(perq.cluster.ID)
	projectapi.ApplyProjectAndNamespaceResourceQuotas(projectTemplate, projectExistingQuota, nil, namespaceExistingQuota, nil)
	_, _, err := projectapi.CreateProjectAndNamespaceWithTemplate(standardUserClient, perq.cluster.ID, projectTemplate)
	require.Error(perq.T(), err)
	require.Contains(perq.T(), err.Error(), projectapi.NamespaceQuotaExceedsProjectQuotaErrorMessage)
}

func TestProjectsExtendedResourceQuotaTestSuite(t *testing.T) {
	suite.Run(t, new(ProjectsExtendedResourceQuotaTestSuite))
}
