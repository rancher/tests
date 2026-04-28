package projects

import (
	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateProjectWithTemplate creates a project using wrangler context with a provided project template
func CreateProjectWithTemplate(client *rancher.Client, clusterID string, projectTemplate *v3.Project) (*v3.Project, error) {
	createdProject, err := client.WranglerContext.Mgmt.Project().Create(projectTemplate)
	if err != nil {
		return nil, err
	}

	if err = WaitForProjectFinalizerToUpdate(client, createdProject.Name, createdProject.Namespace, 2); err != nil {
		return nil, err
	}

	return client.WranglerContext.Mgmt.Project().Get(clusterID, createdProject.Name, metav1.GetOptions{})
}

// CreateProject creates a project with default test template using wrangler context
func CreateProject(client *rancher.Client, clusterID string) (*v3.Project, error) {
	return CreateProjectWithTemplate(client, clusterID, NewProjectTemplate(clusterID))
}

// CreateProjectAndNamespace is a helper to create a project and a namespace in the project using wrangler context
func CreateProjectAndNamespace(client *rancher.Client, clusterID string) (*v3.Project, *corev1.Namespace, error) {
	createdProject, err := CreateProject(client, clusterID)
	if err != nil {
		return nil, nil, err
	}

	createdNamespace, err := namespaceapi.CreateNamespace(client, clusterID, createdProject.Name, namegen.AppendRandomString("testns"), "", nil, nil)
	if err != nil {
		return nil, nil, err
	}

	return createdProject, createdNamespace, nil
}

// CreateProjectAndNamespaceWithTemplate creates a project from template and a namespace in the project
func CreateProjectAndNamespaceWithTemplate(client *rancher.Client, clusterID string, projectTemplate *v3.Project) (*v3.Project, *corev1.Namespace, error) {
	createdProject, err := CreateProjectWithTemplate(client, clusterID, projectTemplate)
	if err != nil {
		return nil, nil, err
	}

	createdNamespace, err := namespaceapi.CreateNamespace(client, clusterID, createdProject.Name, namegen.AppendRandomString("testns"), "", nil, nil)
	if err != nil {
		return nil, nil, err
	}

	return createdProject, createdNamespace, nil
}
