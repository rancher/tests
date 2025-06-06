package projects

import (
	"context"
	"sort"
	"strings"

	"github.com/rancher/norman/types"
	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	"github.com/rancher/tests/actions/kubeapi/namespaces"
	projectsapi "github.com/rancher/tests/actions/kubeapi/projects"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// GetProjectByName is a helper function that returns the project by name in a specific cluster.
func GetProjectByName(client *rancher.Client, clusterID, projectName string) (*management.Project, error) {
	var project *management.Project

	adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
	if err != nil {
		return project, err
	}

	projectsList, err := adminClient.Management.Project.List(&types.ListOpts{
		Filters: map[string]interface{}{
			"clusterId": clusterID,
		},
	})
	if err != nil {
		return project, err
	}

	for i, p := range projectsList.Data {
		if p.Name == projectName {
			project = &projectsList.Data[i]
			break
		}
	}

	return project, nil
}

// GetProjectList is a helper function that returns all the project in a specific cluster
func GetProjectList(client *rancher.Client, clusterID string) (*management.ProjectCollection, error) {
	var projectsList *management.ProjectCollection

	projectsList, err := client.Management.Project.List(&types.ListOpts{
		Filters: map[string]interface{}{
			"clusterId": clusterID,
		},
	})
	if err != nil {
		return projectsList, err
	}

	return projectsList, nil
}

// ListProjectNames is a helper which returns a sorted list of project names
func ListProjectNames(client *rancher.Client, clusterID string) ([]string, error) {
	projectList, err := GetProjectList(client, clusterID)
	if err != nil {
		return nil, err
	}

	projectNames := make([]string, len(projectList.Data))

	for idx, project := range projectList.Data {
		projectNames[idx] = project.Name
	}
	sort.Strings(projectNames)
	return projectNames, nil
}

// CreateProjectAndNamespace is a helper to create a project (norman) and a namespace in the project
func CreateProjectAndNamespace(client *rancher.Client, clusterID string) (*management.Project, *corev1.Namespace, error) {
	createdProject, err := client.Management.Project.Create(NewProjectConfig(clusterID))
	if err != nil {
		return nil, nil, err
	}

	namespaceName := namegen.AppendRandomString("testns")
	projectName := strings.Split(createdProject.ID, ":")[1]

	createdNamespace, err := namespaces.CreateNamespace(client, clusterID, projectName, namespaceName, "{}", map[string]string{}, map[string]string{})
	if err != nil {
		return nil, nil, err
	}

	return createdProject, createdNamespace, nil
}

// CreateProjectAndNamespaceUsingWrangler is a helper to create a project (wrangler context) and a namespace in the project
func CreateProjectAndNamespaceUsingWrangler(client *rancher.Client, clusterID string) (*v3.Project, *corev1.Namespace, error) {
	createdProject, err := CreateProjectUsingWrangler(client, clusterID)
	if err != nil {
		return nil, nil, err
	}

	createdNamespace, err := CreateNamespaceUsingWrangler(client, clusterID, createdProject.Name, nil)
	if err != nil {
		return nil, nil, err
	}

	return createdProject, createdNamespace, nil
}

// CreateProjectUsingWrangler is a helper to create a project using wrangler context
func CreateProjectUsingWrangler(client *rancher.Client, clusterID string) (*v3.Project, error) {
	projectTemplate := projectsapi.NewProjectTemplate(clusterID)
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

// CreateNamespaceUsingWrangler is a helper to create a namespace in the project using wrangler context
func CreateNamespaceUsingWrangler(client *rancher.Client, clusterID, projectName string, labels map[string]string) (*corev1.Namespace, error) {
	namespaceName := namegen.AppendRandomString("testns")
	annotations := map[string]string{
		"field.cattle.io/projectId": clusterID + ":" + projectName,
	}

	ctx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	createdNamespace, err := ctx.Core.Namespace().Create(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        namespaceName,
			Annotations: annotations,
			Labels:      labels,
		},
	})
	if err != nil {
		return nil, err
	}

	err = WaitForProjectIDUpdate(client, clusterID, projectName, createdNamespace.Name)
	if err != nil {
		return nil, err
	}

	return createdNamespace, nil
}

// WaitForProjectFinalizerToUpdate is a helper to wait for project finalizer to update to match the expected finalizer count
func WaitForProjectFinalizerToUpdate(client *rancher.Client, projectName string, projectNamespace string, finalizerCount int) error {
	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TenSecondTimeout, false, func(ctx context.Context) (done bool, pollErr error) {
		project, pollErr := client.WranglerContext.Mgmt.Project().Get(projectNamespace, projectName, metav1.GetOptions{})
		if pollErr != nil {
			return false, pollErr
		}

		if len(project.Finalizers) == finalizerCount {
			return true, nil
		}
		return false, pollErr
	})

	if err != nil {
		return err
	}

	return nil
}

// WaitForProjectIDUpdate is a helper that waits for the project-id annotation and label to be updated in a specified namespace
func WaitForProjectIDUpdate(client *rancher.Client, clusterID, projectName, namespaceName string) error {
	expectedAnnotations := map[string]string{
		projectsapi.ProjectIDAnnotation: clusterID + ":" + projectName,
	}

	expectedLabels := map[string]string{
		projectsapi.ProjectIDAnnotation: projectName,
	}

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, pollErr error) {
		namespace, pollErr := namespaces.GetNamespaceByName(client, clusterID, namespaceName)
		if pollErr != nil {
			return false, pollErr
		}

		for key, expectedValue := range expectedAnnotations {
			if actualValue, ok := namespace.Annotations[key]; !ok || actualValue != expectedValue {
				return false, nil
			}
		}

		for key, expectedValue := range expectedLabels {
			if actualValue, ok := namespace.Labels[key]; !ok || actualValue != expectedValue {
				return false, nil
			}
		}

		return true, nil
	})

	if err != nil {
		return err
	}

	return nil
}

// UpdateProjectNamespaceFinalizer is a helper to update the finalizer in a project
func UpdateProjectNamespaceFinalizer(client *rancher.Client, existingProject *v3.Project, finalizer []string) (*v3.Project, error) {
	updatedProject := existingProject.DeepCopy()
	updatedProject.ObjectMeta.Finalizers = finalizer

	updatedProject, err := projectsapi.UpdateProject(client, existingProject, updatedProject)
	if err != nil {
		return nil, err
	}

	return updatedProject, nil
}
