package projects

import (
	"sort"
	"strings"

	"github.com/rancher/norman/types"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	corev1 "k8s.io/api/core/v1"
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

	projectName := strings.Split(createdProject.ID, ":")[1]

	createdNamespace, err := namespaceapi.CreateNamespace(client, clusterID, projectName, namegen.AppendRandomString("testns"), "", nil, nil)
	if err != nil {
		return nil, nil, err
	}

	return createdProject, createdNamespace, nil
}
