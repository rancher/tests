package projects

import (
	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateProject creates a project with default test template using wrangler context
func CreateProject(client *rancher.Client, clusterID string) (*v3.Project, error) {
	return CreateProjectWithTemplate(client, clusterID, NewProjectTemplate(clusterID))
}

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

// CreateProjectWithTemplateAndNamespace creates a project from template and a namespace in the project
func CreateProjectWithTemplateAndNamespace(client *rancher.Client, clusterID string, projectTemplate *v3.Project) (*v3.Project, *corev1.Namespace, error) {
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

func CreateProjectWithQuotasAndNamespace(client *rancher.Client, clusterID string, namespacePodLimit, projectPodLimit string) (*v3.Project, *corev1.Namespace, error) {
	projectTemplate := NewProjectTemplate(clusterID)
	projectTemplate.Spec.NamespaceDefaultResourceQuota.Limit.Pods = namespacePodLimit
	projectTemplate.Spec.ResourceQuota.Limit.Pods = projectPodLimit

	return CreateProjectWithTemplateAndNamespace(client, clusterID, projectTemplate)
}

// CreateProjectWithLimitsAndNamespace creates a project with container default resource limits and a namespace in the project
func CreateProjectWithLimitsAndNamespace(client *rancher.Client, clusterID string, cpuLimit, cpuReservation, memoryLimit, memoryReservation string) (*v3.Project, *corev1.Namespace, error) {
	projectTemplate := NewProjectTemplate(clusterID)
	projectTemplate.Spec.ContainerDefaultResourceLimit.LimitsCPU = cpuLimit
	projectTemplate.Spec.ContainerDefaultResourceLimit.RequestsCPU = cpuReservation
	projectTemplate.Spec.ContainerDefaultResourceLimit.LimitsMemory = memoryLimit
	projectTemplate.Spec.ContainerDefaultResourceLimit.RequestsMemory = memoryReservation

	return CreateProjectWithTemplateAndNamespace(client, clusterID, projectTemplate)
}
