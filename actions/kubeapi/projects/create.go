package projects

import (
	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateProject is a helper to create a project using wrangler context
func CreateProject(client *rancher.Client, clusterID string) (*v3.Project, error) {
	projectTemplate := NewProjectTemplate(clusterID)

	createdProject, err := client.WranglerContext.Mgmt.Project().Create(projectTemplate)
	if err != nil {
		return nil, err
	}

	err = WaitForProjectFinalizerToUpdate(client, createdProject.Name, createdProject.Namespace, 2)
	if err != nil {
		return nil, err
	}

	createdProject, err = client.WranglerContext.Mgmt.Project().Get(clusterID, createdProject.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return createdProject, nil
}

// CreateProjectAndNamespace is a helper to create a project and a namespace in the project using wrangler context
func CreateProjectAndNamespace(client *rancher.Client, clusterID string) (*v3.Project, *corev1.Namespace, error) {
	createdProject, err := CreateProject(client, clusterID)
	if err != nil {
		return nil, nil, err
	}

	createdNamespace, err := namespaceapi.CreateNamespaceUsingWrangler(client, clusterID, createdProject.Name, nil)
	if err != nil {
		return nil, nil, err
	}

	return createdProject, createdNamespace, nil
}

// CreateProjectAndNamespaceWithTemplate is a helper to create a project and a namespace in the project using a provided project template
func CreateProjectAndNamespaceWithTemplate(client *rancher.Client, clusterID string, projectTemplate *v3.Project) (*v3.Project, *corev1.Namespace, error) {
	createdProject, err := client.WranglerContext.Mgmt.Project().Create(projectTemplate)
	if err != nil {
		return nil, nil, err
	}

	err = WaitForProjectFinalizerToUpdate(client, createdProject.Name, createdProject.Namespace, 2)
	if err != nil {
		return nil, nil, err
	}

	createdProject, err = client.WranglerContext.Mgmt.Project().Get(clusterID, createdProject.Name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}

	createdNamespace, err := namespaceapi.CreateNamespaceUsingWrangler(client, clusterID, createdProject.Name, nil)
	if err != nil {
		return nil, nil, err
	}

	return createdProject, createdNamespace, nil
}
