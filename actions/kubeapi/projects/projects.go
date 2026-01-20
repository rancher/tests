package projects

import (
	"context"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	DummyFinalizer                                = "dummy"
	SystemProjectLabel                            = "authz.management.cattle.io/system-project"
	ContainerDefaultLimitAnnotation               = "field.cattle.io/containerDefaultResourceLimit"
	ResourceQuotaAnnotation                       = "field.cattle.io/resourceQuota"
	ResourceQuotaStatusAnnotation                 = "cattle.io/status"
	Projects                                      = "projects"
	ExistingPodResourceQuotaKey                   = "pods"
	ExtendedPodResourceQuotaKey                   = "count/pods"
	ExtendedJobResourceQuotaKey                   = "count/jobs.batch"
	ExtendedEphemeralStorageResourceQuotaKey      = "ephemeral-storage"
	ExtendedEphemeralStorageResourceQuotaRequest  = "requests.ephemeral-storage"
	ExtendedEphemeralStorageResourceQuotaLimit    = "limits.ephemeral-storage"
	ExceedededResourceQuotaErrorMessage           = "exceeded quota"
	NamespaceQuotaExceedsProjectQuotaErrorMessage = "namespace default quota limit exceeds project limit"
	GroupName                                     = "management.cattle.io"
	Version                                       = "v3"
)

// NewProjectTemplate is a constructor that creates a public API project template
func NewProjectTemplate(clusterID string) *v3.Project {
	project := &v3.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:       namegen.AppendRandomString("testproject"),
			Namespace:  clusterID,
			Finalizers: []string{},
		},
		Spec: v3.ProjectSpec{
			ClusterName: clusterID,
			DisplayName: "test-" + namegen.AppendRandomString(""),
			ResourceQuota: &v3.ProjectResourceQuota{
				Limit: v3.ResourceQuotaLimit{
					Pods: "",
				},
			},
			NamespaceDefaultResourceQuota: &v3.NamespaceResourceQuota{
				Limit: v3.ResourceQuotaLimit{
					Pods: "",
				},
			},
			ContainerDefaultResourceLimit: &v3.ContainerResourceLimit{
				RequestsCPU:    "",
				RequestsMemory: "",
				LimitsCPU:      "",
				LimitsMemory:   "",
			},
		},
	}
	return project
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

// ApplyProjectAndNamespaceResourceQuotas applies quotas for project and namespace
func ApplyProjectAndNamespaceResourceQuotas(project *v3.Project, projectExisting *v3.ResourceQuotaLimit, projectExtended map[string]string, namespaceExisting *v3.ResourceQuotaLimit, namespaceExtended map[string]string) {
	if projectExisting != nil {
		project.Spec.ResourceQuota.Limit = *projectExisting
	}
	if len(projectExtended) > 0 {
		if project.Spec.ResourceQuota.Limit.Extended == nil {
			project.Spec.ResourceQuota.Limit.Extended = map[string]string{}
		}
		for k, v := range projectExtended {
			project.Spec.ResourceQuota.Limit.Extended[k] = v
		}
	}

	if namespaceExisting != nil {
		project.Spec.NamespaceDefaultResourceQuota.Limit = *namespaceExisting
	}
	if len(namespaceExtended) > 0 {
		if project.Spec.NamespaceDefaultResourceQuota.Limit.Extended == nil {
			project.Spec.NamespaceDefaultResourceQuota.Limit.Extended = map[string]string{}
		}
		for k, v := range namespaceExtended {
			project.Spec.NamespaceDefaultResourceQuota.Limit.Extended[k] = v
		}
	}
}

// ApplyProjectContainerDefaultLimits applies container default CPU/memory limits and requests.
func ApplyProjectContainerDefaultLimits(project *v3.Project, cpuLimit, cpuRequest, memoryLimit, memoryRequest string) {
	project.Spec.ContainerDefaultResourceLimit.LimitsCPU = cpuLimit
	project.Spec.ContainerDefaultResourceLimit.RequestsCPU = cpuRequest
	project.Spec.ContainerDefaultResourceLimit.LimitsMemory = memoryLimit
	project.Spec.ContainerDefaultResourceLimit.RequestsMemory = memoryRequest
}
