package projects

import (
	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	extprojectapi "github.com/rancher/shepherd/extensions/kubeapi/projects"
)

// UpdateProjectNamespaceFinalizer is a helper to update the finalizer in a project
func UpdateProjectNamespaceFinalizer(client *rancher.Client, existingProject *v3.Project, finalizer []string) (*v3.Project, error) {
	updatedProject := existingProject.DeepCopy()
	updatedProject.ObjectMeta.Finalizers = finalizer

	return extprojectapi.UpdateProject(client, updatedProject)
}

// UpdateProjectContainerResourceLimit is a helper to update the container default resource limit in a project
func UpdateProjectContainerResourceLimit(client *rancher.Client, existingProject *v3.Project, cpuLimit, cpuReservation, memoryLimit, memoryReservation string) (*v3.Project, error) {
	updatedProject := existingProject.DeepCopy()
	updatedProject.Spec.ContainerDefaultResourceLimit.LimitsCPU = cpuLimit
	updatedProject.Spec.ContainerDefaultResourceLimit.RequestsCPU = cpuReservation
	updatedProject.Spec.ContainerDefaultResourceLimit.LimitsMemory = memoryLimit
	updatedProject.Spec.ContainerDefaultResourceLimit.RequestsMemory = memoryReservation

	return extprojectapi.UpdateProject(client, updatedProject)
}
