//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11

package projects

import (
	"fmt"
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	secretsapi "github.com/rancher/tests/actions/kubeapi/secrets"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/secrets"
	"github.com/rancher/tests/actions/workloads/deployment"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ProjectScopedSecretTestSuite struct {
	suite.Suite
	client         *rancher.Client
	session        *session.Session
	cluster        *management.Cluster
	registryConfig *secrets.Config
}

func (pss *ProjectScopedSecretTestSuite) TearDownSuite() {
	pss.session.Cleanup()
}

func (pss *ProjectScopedSecretTestSuite) SetupSuite() {
	pss.session = session.NewSession()

	client, err := rancher.NewClient("", pss.session)
	require.NoError(pss.T(), err)
	pss.client = client

	log.Info("Getting cluster name and registry credentials from the config file and append the details in pss")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(pss.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(pss.client, clusterName)
	require.NoError(pss.T(), err, "Error getting cluster ID")
	pss.cluster, err = pss.client.Management.Cluster.ByID(clusterID)
	require.NoError(pss.T(), err)

	pss.registryConfig = new(secrets.Config)
	config.LoadConfig(secrets.ConfigurationFileKey, pss.registryConfig)
}

func (pss *ProjectScopedSecretTestSuite) testProjectScopedSecret(clusterID string, secretType corev1.SecretType, secretData map[string][]byte) (*v3.Project, []*corev1.Namespace, *corev1.Secret) {
	log.Info("Create a project in the cluster.")
	createdProject, err := projectapi.CreateProject(pss.client, clusterID)
	require.NoError(pss.T(), err)

	log.Info("Create a project scoped secret in the project.")
	createdProjectScopedSecret, err := secrets.CreateProjectScopedSecret(pss.client, clusterID, createdProject.Name, secretData, secretType)
	require.NoError(pss.T(), err)

	log.Info("Verify that the project scoped secret has the expected label")
	err = secrets.ValidateProjectScopedSecretLabel(createdProjectScopedSecret, createdProject.Name)
	require.NoError(pss.T(), err)

	log.Info("Create five namespaces in the project.")
	namespaceList, err := namespaceapi.CreateMultipleNamespacesInProject(pss.client, clusterID, createdProject.Name, 5)
	require.NoError(pss.T(), err)

	log.Info("Verify that the secret is propagated to all the namespaces in the project and the data matches the original project-scoped secret.")
	err = secrets.ValidatePropagatedNamespaceSecrets(pss.client, clusterID, createdProject.Name, createdProjectScopedSecret, namespaceList)
	require.NoError(pss.T(), err)

	return createdProject, namespaceList, createdProjectScopedSecret
}

func (pss *ProjectScopedSecretTestSuite) TestCreateProjectScopedSecretLocalCluster() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	pss.testProjectScopedSecret(clusterapi.LocalCluster, corev1.SecretTypeOpaque, opaqueSecretData)
}

func (pss *ProjectScopedSecretTestSuite) TestCreateProjectScopedOpaqueSecret() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	pss.testProjectScopedSecret(pss.cluster.ID, corev1.SecretTypeOpaque, opaqueSecretData)
}

func (pss *ProjectScopedSecretTestSuite) TestCreateProjectScopedRegistrySecret() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	dockerConfigJSON, err := secrets.CreateRegistrySecretDockerConfigJSON(pss.registryConfig)
	require.NoError(pss.T(), err)

	registrySecretData := map[string][]byte{
		".dockerconfigjson": []byte(dockerConfigJSON),
	}
	pss.testProjectScopedSecret(pss.cluster.ID, corev1.SecretTypeDockerConfigJson, registrySecretData)
}

func (pss *ProjectScopedSecretTestSuite) TestCreateProjectScopedTlsSecret() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	certData, keyData, err := secrets.GenerateSelfSignedCert()
	require.NoError(pss.T(), err)

	tlsSecretData := map[string][]byte{
		corev1.TLSCertKey:       []byte(certData),
		corev1.TLSPrivateKeyKey: []byte(keyData),
	}
	pss.testProjectScopedSecret(pss.cluster.ID, corev1.SecretTypeTLS, tlsSecretData)
}

func (pss *ProjectScopedSecretTestSuite) TestCreateProjectScopedSecretAfterCreatingNamespace() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create a project in the cluster.")
	createdProject, err := projectapi.CreateProject(pss.client, pss.cluster.ID)
	require.NoError(pss.T(), err)

	log.Info("Create five namespaces in the project.")
	namespaceList, err := namespaceapi.CreateMultipleNamespacesInProject(pss.client, pss.cluster.ID, createdProject.Name, 5)
	require.NoError(pss.T(), err)

	log.Info("Create a project scoped secret in the project.")
	createdProjectScopedSecret, err := secrets.CreateProjectScopedSecret(pss.client, pss.cluster.ID, createdProject.Name, opaqueSecretData, corev1.SecretTypeOpaque)
	require.NoError(pss.T(), err)

	log.Info("Verify that the project scoped secret has the expected label")
	err = secrets.ValidateProjectScopedSecretLabel(createdProjectScopedSecret, createdProject.Name)
	require.NoError(pss.T(), err)

	log.Info("Verify that the secret is propagated to all the namespaces in the project and the data matches the original project-scoped secret.")
	err = secrets.ValidatePropagatedNamespaceSecrets(pss.client, pss.cluster.ID, createdProject.Name, createdProjectScopedSecret, namespaceList)
	require.NoError(pss.T(), err)
}

func (pss *ProjectScopedSecretTestSuite) TestUpdateProjectScopedSecret() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	createdProject, namespaceList, createdProjectScopedSecret := pss.testProjectScopedSecret(pss.cluster.ID, corev1.SecretTypeOpaque, opaqueSecretData)

	log.Info("Update the secret data in the Project scoped secret.")
	newData := map[string][]byte{
		"foo": []byte("bar"),
	}
	updatedProjectScopedSecret, err := secrets.UpdateProjectScopedSecret(pss.client, pss.cluster.ID, createdProject.Name, createdProjectScopedSecret.Name, newData)
	require.NoError(pss.T(), err)
	require.Equal(pss.T(), newData, updatedProjectScopedSecret.Data, "Secret data is not as expected")

	log.Info("Verify that the secret data in all the namespaces matches the project-scoped secret.")
	err = secrets.ValidatePropagatedNamespaceSecrets(pss.client, pss.cluster.ID, createdProject.Name, updatedProjectScopedSecret, namespaceList)
	require.NoError(pss.T(), err)
}

func (pss *ProjectScopedSecretTestSuite) TestDeleteProjectScopedSecret() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	createdProject, namespaceList, createdProjectScopedSecret := pss.testProjectScopedSecret(pss.cluster.ID, corev1.SecretTypeOpaque, opaqueSecretData)

	log.Info("Delete the Project scoped secret.")
	backingNamespace := fmt.Sprintf("%s-%s", pss.cluster.ID, createdProject.Name)
	err := secrets.DeleteSecret(pss.client, clusterapi.LocalCluster, backingNamespace, createdProjectScopedSecret.Name)
	require.NoError(pss.T(), err)

	log.Info("Verify that the project scoped secret is deleted.")
	_, err = secretsapi.GetSecretByName(pss.client, clusterapi.LocalCluster, backingNamespace, createdProjectScopedSecret.Name, metav1.GetOptions{})
	require.Error(pss.T(), err)
	require.True(pss.T(), apierrors.IsNotFound(err), "Expected NotFound error, got: %v", err)

	log.Info("Verify that the secret is removed from all the namespaces in the project.")
	err = secrets.WaitForSecretInNamespaces(pss.client, pss.cluster.ID, createdProjectScopedSecret.Name, namespaceList, false)
	require.NoError(pss.T(), err, "Expected propagated secret to be deleted from all namespaces")
}

func (pss *ProjectScopedSecretTestSuite) TestProjectScopedSecretCleanupOnProjectDeletion() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	createdProject, namespaceList, createdProjectScopedSecret := pss.testProjectScopedSecret(pss.cluster.ID, corev1.SecretTypeOpaque, opaqueSecretData)

	log.Infof("Deleting the project: %s", createdProject.Name)
	err := projectapi.DeleteProject(pss.client, pss.cluster.ID, createdProject.Name, true)
	require.NoError(pss.T(), err, "Failed to delete the project")

	log.Info("Verify that the project scoped secret is deleted.")
	backingNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-%s", pss.cluster.ID, createdProject.Name)}}
	err = secrets.WaitForSecretInNamespaces(pss.client, clusterapi.LocalCluster, createdProjectScopedSecret.Name, []*corev1.Namespace{backingNamespace}, false)
	require.NoError(pss.T(), err, "Expected secret to be deleted but it still exists or an unexpected error occurred")

	log.Info("Verify that the secret is removed from all the namespaces in the project.")
	err = secrets.WaitForSecretInNamespaces(pss.client, pss.cluster.ID, createdProjectScopedSecret.Name, namespaceList, false)
	require.NoError(pss.T(), err, "Expected propagated secret to be deleted from all namespaces")
}

func (pss *ProjectScopedSecretTestSuite) TestMoveNamespaceFromProjectWithoutToWithProjectScopedSecret() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	createdProject1, _, createdProjectScopedSecret := pss.testProjectScopedSecret(pss.cluster.ID, corev1.SecretTypeOpaque, opaqueSecretData)

	log.Info("Creating a second project in the cluster.")
	createdProject2, err := projectapi.CreateProject(pss.client, pss.cluster.ID)
	require.NoError(pss.T(), err)

	log.Info("Creating a namespace in the second project.")
	namespaceList2, err := namespaceapi.CreateMultipleNamespacesInProject(pss.client, pss.cluster.ID, createdProject2.Name, 1)
	require.NoError(pss.T(), err)

	targetNamespace := namespaceList2[0]
	log.Infof("Moving namespace '%s' from project '%s' to '%s'", targetNamespace.Name, createdProject2.Name, createdProject1.Name)
	err = namespaceapi.MoveNamespaceToProject(pss.client, pss.cluster.ID, targetNamespace.Name, createdProject1.Name)
	require.NoError(pss.T(), err, "Failed to move namespace to project '%s'", createdProject1.Name)

	log.Infof("Verify that the project scoped secret '%s' is propagated to '%s'", createdProjectScopedSecret.Name, targetNamespace.Name)
	err = secrets.WaitForSecretInNamespaces(pss.client, pss.cluster.ID, createdProjectScopedSecret.Name, []*corev1.Namespace{targetNamespace}, true)
	require.NoError(pss.T(), err, "Project-scoped secret was not propagated to moved namespace")

	namespaceList, err := namespaceapi.GetNamespacesInProject(pss.client, pss.cluster.ID, createdProject1.Name)
	require.NoError(pss.T(), err)
	err = secrets.ValidatePropagatedNamespaceSecrets(pss.client, pss.cluster.ID, createdProject1.Name, createdProjectScopedSecret, namespaceList)
	require.NoError(pss.T(), err)
}

func (pss *ProjectScopedSecretTestSuite) TestMoveNamespaceFromProjectWithToWithoutProjectScopedSecret() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	createdProject1, namespaceList1, createdProjectScopedSecret := pss.testProjectScopedSecret(pss.cluster.ID, corev1.SecretTypeOpaque, opaqueSecretData)

	log.Info("Creating a second project in the cluster.")
	createdProject2, err := projectapi.CreateProject(pss.client, pss.cluster.ID)
	require.NoError(pss.T(), err)

	targetNamespace := namespaceList1[0]
	log.Infof("Moving namespace '%s' from project '%s' to '%s'", targetNamespace.Name, createdProject1.Name, createdProject2.Name)
	err = namespaceapi.MoveNamespaceToProject(pss.client, pss.cluster.ID, targetNamespace.Name, createdProject2.Name)
	require.NoError(pss.T(), err, "Failed to move namespace to project '%s'", createdProject2.Name)

	log.Infof("Verifying project scoped secret '%s' is removed from namespace '%s'", createdProjectScopedSecret.Name, targetNamespace.Name)
	err = secrets.WaitForSecretInNamespaces(pss.client, pss.cluster.ID, createdProjectScopedSecret.Name, []*corev1.Namespace{targetNamespace}, false)
	require.NoError(pss.T(), err, "Project-scoped secret was not removed after namespace moved")

	namespaceList, err := namespaceapi.GetNamespacesInProject(pss.client, pss.cluster.ID, createdProject1.Name)
	require.NoError(pss.T(), err)
	err = secrets.ValidatePropagatedNamespaceSecrets(pss.client, pss.cluster.ID, createdProject1.Name, createdProjectScopedSecret, namespaceList)
	require.NoError(pss.T(), err)
}

func (pss *ProjectScopedSecretTestSuite) TestProjectScopedSecretByRole() {
	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
		{rbac.ProjectOwner, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		subSession := pss.session.NewSession()
		defer subSession.Cleanup()

		pss.Run("Validate CRUD project scoped secret as user with role "+tt.role.String(), func() {
			createdProject, namespaceList, _ := pss.testProjectScopedSecret(pss.cluster.ID, corev1.SecretTypeOpaque, opaqueSecretData)

			log.Infof("Create a standard user and add the user to a cluster/project as role %s", tt.role)
			newUser, standardUserClient, err := rbac.AddUserWithRoleToCluster(pss.client, tt.member, tt.role.String(), pss.cluster, createdProject)
			assert.NoError(pss.T(), err)
			pss.T().Logf("Created user: %v", newUser.Username)

			log.Infof("As a %v, create a new project scoped secret in the downstream cluster's project %v.", tt.role.String(), createdProject.Name)
			createdProjectScopedSecret, err := secrets.CreateProjectScopedSecret(standardUserClient, pss.cluster.ID, createdProject.Name, opaqueSecretData, corev1.SecretTypeOpaque)
			assert.NoError(pss.T(), err, "Failed to create project scoped secret")

			log.Info("Verify that the project scoped secret has the expected label")
			err = secrets.ValidateProjectScopedSecretLabel(createdProjectScopedSecret, createdProject.Name)
			assert.NoError(pss.T(), err)

			log.Info("Verify that the secret is propagated to all the namespaces in the project and the data matches the original project-scoped secret.")
			err = secrets.ValidatePropagatedNamespaceSecrets(standardUserClient, pss.cluster.ID, createdProject.Name, createdProjectScopedSecret, namespaceList)
			assert.NoError(pss.T(), err, "Failed to validate propagated namespace secrets")

			log.Infof("As a %v, delete the project scoped secret %s in project %s", tt.role.String(), createdProjectScopedSecret.Name, createdProject.Name)
			backingNamespace := fmt.Sprintf("%s-%s", pss.cluster.ID, createdProject.Name)
			err = secrets.DeleteSecret(standardUserClient, clusterapi.LocalCluster, backingNamespace, createdProjectScopedSecret.Name)
			assert.NoError(pss.T(), err, "Failed to delete project scoped secret")

			log.Info("Verify that the project scoped secret is deleted.")
			_, err = secretsapi.GetSecretByName(standardUserClient, clusterapi.LocalCluster, backingNamespace, createdProjectScopedSecret.Name, metav1.GetOptions{})
			assert.Error(pss.T(), err)
			assert.True(pss.T(), apierrors.IsNotFound(err), "Expected NotFound error, got: %v", err)

			log.Info("Verify that the secret is removed from all the namespaces in the project.")
			err = secrets.WaitForSecretInNamespaces(standardUserClient, pss.cluster.ID, createdProjectScopedSecret.Name, namespaceList, false)
			assert.NoError(pss.T(), err, "Expected propagated secret to be deleted from all namespaces")

		})
	}
}

func (pss *ProjectScopedSecretTestSuite) TestProjectScopedSecretAsClusterMember() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	role := rbac.ClusterMember.String()
	log.Infof("Create a standard user and add the user to the downstream cluster as %v", role)
	standardUser, standardUserClient, err := rbac.AddUserWithRoleToCluster(pss.client, rbac.StandardUser.String(), role, pss.cluster, nil)
	require.NoError(pss.T(), err)

	projectTemplate := projectapi.NewProjectTemplate(pss.cluster.ID)
	projectTemplate.Annotations = map[string]string{
		"field.cattle.io/creatorId": standardUser.ID,
	}
	createdProject, err := standardUserClient.WranglerContext.Mgmt.Project().Create(projectTemplate)
	require.NoError(pss.T(), err)

	err = projectapi.WaitForProjectFinalizerToUpdate(standardUserClient, createdProject.Name, createdProject.Namespace, 2)
	require.NoError(pss.T(), err)

	log.Info("Create a project scoped secret in the project.")
	createdProjectScopedSecret, err := secrets.CreateProjectScopedSecret(standardUserClient, pss.cluster.ID, createdProject.Name, opaqueSecretData, corev1.SecretTypeOpaque)
	require.NoError(pss.T(), err)

	log.Info("Verify that the project scoped secret has the expected label")
	err = secrets.ValidateProjectScopedSecretLabel(createdProjectScopedSecret, createdProject.Name)
	require.NoError(pss.T(), err)

	log.Info("Create five namespaces in the project.")
	namespaceList, err := namespaceapi.CreateMultipleNamespacesInProject(standardUserClient, pss.cluster.ID, createdProject.Name, 5)
	require.NoError(pss.T(), err)

	log.Info("Verify that the secret is propagated to all the namespaces in the project and the data matches the original project-scoped secret.")
	err = secrets.ValidatePropagatedNamespaceSecrets(standardUserClient, pss.cluster.ID, createdProject.Name, createdProjectScopedSecret, namespaceList)
	require.NoError(pss.T(), err)

	log.Infof("As a %v, delete the project scoped secret %s in project %s", role, createdProjectScopedSecret.Name, createdProject.Name)
	backingNamespace := fmt.Sprintf("%s-%s", pss.cluster.ID, createdProject.Name)
	err = secrets.DeleteSecret(standardUserClient, clusterapi.LocalCluster, backingNamespace, createdProjectScopedSecret.Name)
	require.NoError(pss.T(), err, "Failed to delete project scoped secret as cluster member")

	log.Info("Verify that the project scoped secret is deleted.")
	_, err = secretsapi.GetSecretByName(standardUserClient, clusterapi.LocalCluster, backingNamespace, createdProjectScopedSecret.Name, metav1.GetOptions{})
	require.Error(pss.T(), err)
	require.True(pss.T(), apierrors.IsNotFound(err), "Expected NotFound error, got: %v", err)

	log.Info("Verify that the secret is removed from all the namespaces in the project.")
	err = secrets.WaitForSecretInNamespaces(standardUserClient, pss.cluster.ID, createdProjectScopedSecret.Name, namespaceList, false)
	require.NoError(pss.T(), err, "Expected propagated secret to be deleted from all namespaces")
}

func (pss *ProjectScopedSecretTestSuite) TestProjectMemberCannotAccessOtherProjectsSecrets() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	createdProject1, _, createdProjectSecret := pss.testProjectScopedSecret(pss.cluster.ID, corev1.SecretTypeOpaque, opaqueSecretData)

	log.Info("Create another project in the cluster.")
	createdProject2, err := projectapi.CreateProject(pss.client, pss.cluster.ID)
	require.NoError(pss.T(), err)

	role := rbac.ProjectMember.String()
	log.Infof("Create a standard user and add the user to the downstream cluster as %v", role)
	newUser, standardUserClient, err := rbac.AddUserWithRoleToCluster(pss.client, rbac.StandardUser.String(), role, pss.cluster, createdProject2)
	require.NoError(pss.T(), err)

	log.Infof("As a %v, try to delete the project scoped secret %s in project %s (unauthorized access).", role, createdProjectSecret.Name, createdProject1.Name)
	backingNamespace := fmt.Sprintf("%s-%s", pss.cluster.ID, createdProject1.Name)
	err = secrets.DeleteSecret(standardUserClient, clusterapi.LocalCluster, backingNamespace, createdProjectSecret.Name)
	require.Error(pss.T(), err)
	require.True(pss.T(), apierrors.IsForbidden(err), "Expected Forbidden error, got: %v", err)

	log.Infof("As user %s, try to create a new project scoped secret in project %s (unauthorized access).", newUser.Username, createdProject1.Name)
	_, err = secrets.CreateProjectScopedSecret(standardUserClient, pss.cluster.ID, createdProject1.Name, opaqueSecretData, corev1.SecretTypeOpaque)
	require.Error(pss.T(), err)
	require.True(pss.T(), apierrors.IsForbidden(err), "Expected Forbidden error, got: %v", err)
}

func (pss *ProjectScopedSecretTestSuite) TestUserWithProjectsViewAllCannotAccessSecrets() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	createdProject, namespaceList, createdProjectSecret := pss.testProjectScopedSecret(pss.cluster.ID, corev1.SecretTypeOpaque, opaqueSecretData)

	log.Info("Create a standard user and add the user to the downstream cluster with 'View All Projects' role.")
	projectViewerRole := rbac.ProjectsView.String()
	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(pss.client, rbac.StandardUser.String(), projectViewerRole, pss.cluster, nil)
	require.NoError(pss.T(), err)

	log.Infof("As user with role %v, try to get the project scoped secret %s", projectViewerRole, createdProjectSecret.Name)
	backingNamespace := fmt.Sprintf("%s-%s", pss.cluster.ID, createdProject.Name)
	_, err = secretsapi.GetSecretByName(standardUserClient, clusterapi.LocalCluster, backingNamespace, createdProjectSecret.Name, metav1.GetOptions{})
	require.Error(pss.T(), err)
	require.True(pss.T(), apierrors.IsForbidden(err), "Expected Forbidden error when getting project scoped secret, got: %v", err)

	log.Infof("As user with role %v, try to get the secret %s in namespace %s", projectViewerRole, createdProjectSecret.Name, namespaceList[0].Name)
	_, err = secretsapi.GetSecretByName(standardUserClient, pss.cluster.ID, namespaceList[0].Name, createdProjectSecret.Name, metav1.GetOptions{})
	require.Error(pss.T(), err)
	require.True(pss.T(), apierrors.IsForbidden(err), "Expected Forbidden error when getting secret in namespace, got: %v", err)
}

func (pss *ProjectScopedSecretTestSuite) TestVerifyProjectScopedSecretRancherRestart() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	createdProject, namespaceList, createdProjectScopedSecret := pss.testProjectScopedSecret(pss.cluster.ID, corev1.SecretTypeOpaque, opaqueSecretData)

	log.Info("Restart Rancher")
	err := deployment.RestartDeployment(pss.client, clusterapi.LocalCluster, rbac.RancherDeploymentNamespace, rbac.RancherDeploymentName)
	require.NoError(pss.T(), err, "Failed to restart Rancher deployment")

	log.Info("Verify that the project scoped secret still exists after Rancher restart.")
	backingNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-%s", pss.cluster.ID, createdProject.Name)}}
	err = secrets.WaitForSecretInNamespaces(pss.client, clusterapi.LocalCluster, createdProjectScopedSecret.Name, []*corev1.Namespace{backingNamespace}, true)
	require.NoErrorf(pss.T(), err, "Project scoped secret %q not found in backing namespace %q after Rancher restart: %v", createdProjectScopedSecret.Name, backingNamespace, err)

	log.Info("Verify that the secret propagated to all the namespaces in the project still exists.")
	err = secrets.ValidatePropagatedNamespaceSecrets(pss.client, pss.cluster.ID, createdProject.Name, createdProjectScopedSecret, namespaceList)
	require.NoErrorf(pss.T(), err, "Propagated secret %q validation failed in project %q after Rancher restart: %v", createdProjectScopedSecret.Name, createdProject.Name, err)
}

func (pss *ProjectScopedSecretTestSuite) TestProjectScopedSecretWithSameNameInSameProject() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	createdProject, _, createdProjectScopedSecret := pss.testProjectScopedSecret(pss.cluster.ID, corev1.SecretTypeOpaque, opaqueSecretData)

	log.Infof("Attempt to create another project scoped secret with the same name '%s' in project '%s'", createdProjectScopedSecret.Name, createdProject.Name)
	backingNamespace := fmt.Sprintf("%s-%s", pss.cluster.ID, createdProject.Name)
	ctx, err := clusterapi.GetClusterWranglerContext(pss.client, clusterapi.LocalCluster)
	require.NoError(pss.T(), err)

	secretName := createdProjectScopedSecret.Name
	labels := map[string]string{
		secrets.ProjectScopedSecretLabel: createdProject.Name,
	}
	secretTemplate := secrets.NewSecretTemplate(secretName, backingNamespace, opaqueSecretData, corev1.SecretTypeOpaque, labels, nil)
	_, err = ctx.Core.Secret().Create(&secretTemplate)

	require.Error(pss.T(), err, "Expected error when creating project scoped secret with the same name")
	require.Contains(pss.T(), err.Error(), "already exists", "Expected conflict error due to existing secret, got: %v", err)
}

func (pss *ProjectScopedSecretTestSuite) TestProjectScopedSecretWithSameNameInDifferentProject() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	_, _, createdProjectScopedSecret := pss.testProjectScopedSecret(pss.cluster.ID, corev1.SecretTypeOpaque, opaqueSecretData)

	log.Info("Creating a second project in the cluster.")
	createdProject2, err := projectapi.CreateProject(pss.client, pss.cluster.ID)
	require.NoError(pss.T(), err)

	log.Infof("Create a project scoped secret with the same name '%s' in project '%s'", createdProjectScopedSecret.Name, createdProject2.Name)
	backingNamespace := fmt.Sprintf("%s-%s", pss.cluster.ID, createdProject2.Name)
	ctx, err := clusterapi.GetClusterWranglerContext(pss.client, clusterapi.LocalCluster)
	require.NoError(pss.T(), err)

	secretName := createdProjectScopedSecret.Name
	labels := map[string]string{
		secrets.ProjectScopedSecretLabel: createdProject2.Name,
	}
	secretTemplate := secrets.NewSecretTemplate(secretName, backingNamespace, opaqueSecretData, corev1.SecretTypeOpaque, labels, nil)
	createdProjectScopedSecret2, err := ctx.Core.Secret().Create(&secretTemplate)
	require.NoError(pss.T(), err)

	log.Info("Verify that the project scoped secret has the expected label")
	err = secrets.ValidateProjectScopedSecretLabel(createdProjectScopedSecret2, createdProject2.Name)
	require.NoError(pss.T(), err)
}

func (pss *ProjectScopedSecretTestSuite) TestProjectScopedSecrettPropagatedToNamespaceWithConflict() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create a project in the cluster.")
	createdProject, err := projectapi.CreateProject(pss.client, pss.cluster.ID)
	require.NoError(pss.T(), err)

	log.Info("Create five namespaces in the project.")
	namespaceList, err := namespaceapi.CreateMultipleNamespacesInProject(pss.client, pss.cluster.ID, createdProject.Name, 5)
	require.NoError(pss.T(), err)

	targetNamespace := namespaceList[0]
	log.Infof("Create a secret in one of the namespaces '%s'", targetNamespace.Name)
	nsData := map[string][]byte{
		"foo": []byte("bar"),
	}
	nsSecret, err := secrets.CreateSecret(pss.client, pss.cluster.ID, targetNamespace.Name, nsData, corev1.SecretTypeOpaque, nil, nil)
	require.NoError(pss.T(), err)

	log.Infof("Create project scoped secret with the same name as the namespace in project '%s'", createdProject.Name)
	backingNamespace := fmt.Sprintf("%s-%s", pss.cluster.ID, createdProject.Name)
	ctx, err := clusterapi.GetClusterWranglerContext(pss.client, clusterapi.LocalCluster)
	require.NoError(pss.T(), err)

	labels := map[string]string{
		secrets.ProjectScopedSecretLabel: createdProject.Name,
	}
	secretName := nsSecret.Name
	secretTemplate := secrets.NewSecretTemplate(secretName, backingNamespace, opaqueSecretData, corev1.SecretTypeOpaque, labels, nil)

	createdProjectScopedSecret, err := ctx.Core.Secret().Create(&secretTemplate)
	require.NoError(pss.T(), err)

	log.Info("Verify that the project scoped secret has the expected label")
	err = secrets.ValidateProjectScopedSecretLabel(createdProjectScopedSecret, createdProject.Name)
	require.NoError(pss.T(), err)

	log.Info("Verify that the secret is propagated to all the namespaces in the project and the data matches the original project-scoped secret.")
	err = secrets.ValidatePropagatedNamespaceSecrets(pss.client, pss.cluster.ID, createdProject.Name, createdProjectScopedSecret, namespaceList)
	require.NoError(pss.T(), err)
}

func TestProjectScopedSecretTestSuite(t *testing.T) {
	suite.Run(t, new(ProjectScopedSecretTestSuite))
}
