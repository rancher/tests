//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package projects

import (
	"fmt"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/workloads/deployment"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ProjectsTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (pr *ProjectsTestSuite) TearDownSuite() {
	pr.session.Cleanup()
}

func (pr *ProjectsTestSuite) SetupSuite() {
	pr.session = session.NewSession()

	client, err := rancher.NewClient("", pr.session)
	require.NoError(pr.T(), err)

	pr.client = client

	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(pr.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(pr.client, clusterName)
	require.NoError(pr.T(), err, "Error getting cluster ID")
	pr.cluster, err = pr.client.Management.Cluster.ByID(clusterID)
	require.NoError(pr.T(), err)
}

func (pr *ProjectsTestSuite) TestProjectsCrudLocalCluster() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create a project in the local cluster and verify that the project can be listed.")
	createdProject, err := projectapi.CreateProject(pr.client, clusterapi.LocalCluster)
	require.NoError(pr.T(), err)

	projectList, err := pr.client.WranglerContext.Mgmt.Project().List(createdProject.Namespace, metav1.ListOptions{
		FieldSelector: "metadata.name=" + createdProject.Name,
	})
	require.NoError(pr.T(), err)
	require.Equal(pr.T(), 1, len(projectList.Items), "Expected exactly one project.")

	log.Info("Verify that the project can be updated by adding a label.")
	currentProject := projectList.Items[0]
	if currentProject.Labels == nil {
		currentProject.Labels = make(map[string]string)
	}
	currentProject.Labels["hello"] = "world"
	_, err = pr.client.WranglerContext.Mgmt.Project().Update(&currentProject)
	require.NoError(pr.T(), err, "Failed to update project.")

	updatedProjectList, err := pr.client.WranglerContext.Mgmt.Project().List(createdProject.Namespace, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", "hello", "world"),
	})
	require.NoError(pr.T(), err)
	require.Equal(pr.T(), 1, len(updatedProjectList.Items), "Expected one project in the list")

	log.Info("Delete the project.")
	err = projectapi.DeleteProject(pr.client, createdProject.Namespace, createdProject.Name, true)
	require.NoError(pr.T(), err, "Failed to delete project")
}

func (pr *ProjectsTestSuite) TestProjectsCrudDownstreamCluster() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create a standard user and add the user to the downstream cluster as cluster owner.")
	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(pr.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), pr.cluster, nil)
	require.NoError(pr.T(), err, "Failed to add the user as a cluster owner to the downstream cluster")

	log.Info("Create a project in the downstream cluster and verify that the project can be listed.")
	createdProject, err := projectapi.CreateProject(pr.client, pr.cluster.ID)
	require.NoError(pr.T(), err, "Failed to create project")

	currentProject, err := standardUserClient.WranglerContext.Mgmt.Project().Get(createdProject.Namespace, createdProject.Name, metav1.GetOptions{})
	require.NoError(pr.T(), err, "Failed to get project.")

	log.Info("Verify that the project can be updated by adding a label.")
	if currentProject.Labels == nil {
		currentProject.Labels = make(map[string]string)
	}
	currentProject.Labels["hello"] = "world"
	_, err = standardUserClient.WranglerContext.Mgmt.Project().Update(currentProject)
	require.NoError(pr.T(), err, "Failed to update project.")

	updatedProjectList, err := standardUserClient.WranglerContext.Mgmt.Project().List(createdProject.Namespace, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", "hello", "world"),
	})
	require.NoError(pr.T(), err)
	require.Equal(pr.T(), 1, len(updatedProjectList.Items), "Expected one project in the list")

	log.Info("Delete the project.")
	err = projectapi.DeleteProject(standardUserClient, createdProject.Namespace, createdProject.Name, true)
	require.NoError(pr.T(), err, "Failed to delete project")
}

func (pr *ProjectsTestSuite) TestDeleteSystemProject() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Delete the System Project in the local cluster.")
	projectList, err := pr.client.WranglerContext.Mgmt.Project().List(clusterapi.LocalCluster, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", projectapi.SystemProjectLabel, "true"),
	})
	require.NoError(pr.T(), err)
	require.Equal(pr.T(), 1, len(projectList.Items), "Expected one project in the list")

	systemProjectName := projectList.Items[0].ObjectMeta.Name
	err = projectapi.DeleteProject(pr.client, clusterapi.LocalCluster, systemProjectName, false)
	require.Error(pr.T(), err, "Failed to delete project")
	expectedErrorMessage := "admission webhook \"rancher.cattle.io.projects.management.cattle.io\" denied the request: System Project cannot be deleted"
	require.Equal(pr.T(), expectedErrorMessage, err.Error())

	log.Info("Delete the System Project in the downstream cluster.")
	projectList, err = pr.client.WranglerContext.Mgmt.Project().List(pr.cluster.ID, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", projectapi.SystemProjectLabel, "true"),
	})
	require.NoError(pr.T(), err)
	require.Equal(pr.T(), 1, len(projectList.Items), "Expected one project in the list")

	systemProjectName = projectList.Items[0].ObjectMeta.Name
	err = projectapi.DeleteProject(pr.client, pr.cluster.ID, systemProjectName, false)
	require.Error(pr.T(), err, "Failed to delete project")
	expectedErrorMessage = "admission webhook \"rancher.cattle.io.projects.management.cattle.io\" denied the request: System Project cannot be deleted"
	require.Equal(pr.T(), expectedErrorMessage, err.Error())
}

func (pr *ProjectsTestSuite) TestMoveNamespaceOutOfProject() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create a standard user and add the user to the downstream cluster as cluster owner.")
	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(pr.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), pr.cluster, nil)
	require.NoError(pr.T(), err, "Failed to add the user as a cluster owner to the downstream cluster")

	log.Info("Create a project in the downstream cluster and a namespace in the project.")
	createdProject, createdNamespace, err := projects.CreateProjectAndNamespaceUsingWrangler(pr.client, pr.cluster.ID)
	require.NoError(pr.T(), err)

	log.Info("Verify that the namespace has the label and annotation referencing the project.")
	err = namespaceapi.WaitForProjectIDUpdate(standardUserClient, pr.cluster.ID, createdProject.Name, createdNamespace.Name)
	require.NoError(pr.T(), err)

	log.Info("Move the namespace out of the project.")
	updatedNamespace, err := namespaceapi.GetNamespaceByName(standardUserClient, pr.cluster.ID, createdNamespace.Name)
	require.NoError(pr.T(), err)
	delete(updatedNamespace.Labels, namespaceapi.ProjectIDAnnotation)
	delete(updatedNamespace.Annotations, namespaceapi.ProjectIDAnnotation)

	downstreamContext, err := clusterapi.GetClusterWranglerContext(pr.client, pr.cluster.ID)
	require.NoError(pr.T(), err)

	currentNamespace, err := namespaceapi.GetNamespaceByName(standardUserClient, pr.cluster.ID, updatedNamespace.Name)
	require.NoError(pr.T(), err)
	updatedNamespace.ResourceVersion = currentNamespace.ResourceVersion
	_, err = downstreamContext.Core.Namespace().Update(updatedNamespace)
	require.NoError(pr.T(), err, "Failed to move the namespace out of the project")

	log.Info("Verify that the namespace does not have the label and annotation referencing the project.")
	err = namespaceapi.WaitForProjectIDUpdate(standardUserClient, pr.cluster.ID, createdProject.Name, updatedNamespace.Name)
	require.Error(pr.T(), err)
}

func (pr *ProjectsTestSuite) TestProjectWithResourceQuotaAndContainerDefaultResourceLimit() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create a standard user and add the user to the downstream cluster as cluster owner.")
	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(pr.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), pr.cluster, nil)
	require.NoError(pr.T(), err, "Failed to add the user as a cluster owner to the downstream cluster")

	log.Info("Create a project (with resource quota and container default resource limit) and a namespace in the project.")
	namespacePodLimit := "2"
	projectPodLimit := "3"
	cpuLimit := "100m"
	cpuReservation := "50m"
	memoryLimit := "64Mi"
	memoryReservation := "32Mi"
	projectTemplate := projectapi.NewProjectTemplate(pr.cluster.ID)
	projectTemplate.Spec.ContainerDefaultResourceLimit.LimitsCPU = cpuLimit
	projectTemplate.Spec.ContainerDefaultResourceLimit.RequestsCPU = cpuReservation
	projectTemplate.Spec.ContainerDefaultResourceLimit.LimitsMemory = memoryLimit
	projectTemplate.Spec.ContainerDefaultResourceLimit.RequestsMemory = memoryReservation
	projectTemplate.Spec.NamespaceDefaultResourceQuota.Limit.Pods = namespacePodLimit
	projectTemplate.Spec.ResourceQuota.Limit.Pods = projectPodLimit
	createdProject, createdNamespace, err := createProjectAndNamespace(standardUserClient, pr.cluster.ID, projectTemplate)
	require.NoError(pr.T(), err)

	log.Info("Verify that the namespace has the label and annotation referencing the project.")
	err = namespaceapi.WaitForProjectIDUpdate(standardUserClient, pr.cluster.ID, createdProject.Name, createdNamespace.Name)
	require.NoError(pr.T(), err)

	log.Info("Verify that the pod limits and container default resource limit in the Project spec is accurate.")
	projectSpec := createdProject.Spec
	require.Equal(pr.T(), namespacePodLimit, projectSpec.NamespaceDefaultResourceQuota.Limit.Pods, "Namespace pod limit mismatch")
	require.Equal(pr.T(), projectPodLimit, projectSpec.ResourceQuota.Limit.Pods, "Project pod limit mismatch")
	require.Equal(pr.T(), cpuLimit, projectSpec.ContainerDefaultResourceLimit.LimitsCPU, "CPU limit mismatch")
	require.Equal(pr.T(), cpuReservation, projectSpec.ContainerDefaultResourceLimit.RequestsCPU, "CPU reservation mismatch")
	require.Equal(pr.T(), memoryLimit, projectSpec.ContainerDefaultResourceLimit.LimitsMemory, "Memory limit mismatch")
	require.Equal(pr.T(), memoryReservation, projectSpec.ContainerDefaultResourceLimit.RequestsMemory, "Memory reservation mismatch")

	log.Info("Verify that the namespace has the annotation: field.cattle.io/resourceQuota.")
	err = checkAnnotationExistsInNamespace(standardUserClient, pr.cluster.ID, createdNamespace.Name, projectapi.ResourceQuotaAnnotation, true)
	require.NoError(pr.T(), err, "'field.cattle.io/resourceQuota' annotation should exist")

	log.Info("Verify that the resource quota validation for the namespace is successful.")
	err = checkNamespaceResourceQuotaValidationStatus(standardUserClient, pr.cluster.ID, createdNamespace.Name, namespacePodLimit, true, "")
	require.NoError(pr.T(), err)

	log.Info("Verify that the resource quota object is created for the namespace and the pod limit in the resource quota is set to 2.")
	err = checkNamespaceResourceQuota(standardUserClient, pr.cluster.ID, createdNamespace.Name, 2)
	require.NoError(pr.T(), err)

	log.Info("Verify that the limit range object is created for the namespace and the resource limit in the limit range is accurate.")
	err = checkLimitRange(standardUserClient, pr.cluster.ID, createdNamespace.Name, cpuLimit, cpuReservation, memoryLimit, memoryReservation)
	require.NoError(pr.T(), err)

	log.Info("Create a deployment in the namespace with two replicas and verify that the pods are created.")
	createdDeployment, err := deployment.CreateDeployment(standardUserClient, pr.cluster.ID, createdNamespace.Name, 2, "", "", false, false, false, true)
	require.NoError(pr.T(), err, "Failed to create deployment in the namespace")

	log.Info("Verify that the resource limits and requests for the container in the pod spec is accurate.")
	err = checkContainerResources(standardUserClient, pr.cluster.ID, createdNamespace.Name, createdDeployment.Name, cpuLimit, cpuReservation, memoryLimit, memoryReservation)
	require.NoError(pr.T(), err)
}

func TestProjectsTestSuite(t *testing.T) {
	suite.Run(t, new(ProjectsTestSuite))
}
