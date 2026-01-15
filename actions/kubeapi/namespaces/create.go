package namespaces

import (
	"context"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/unstructured"
	"github.com/rancher/shepherd/pkg/api/scheme"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/wait"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeUnstructured "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
)

// CreateNamespace is a helper function that uses the dynamic client to create a namespace on a project.
// It registers a delete function with a wait.WatchWait to ensure the namspace is deleted cleanly.
func CreateNamespace(client *rancher.Client, clusterID, projectName, namespaceName, containerDefaultResourceLimit string, labels, annotations map[string]string) (*corev1.Namespace, error) {
	if annotations == nil {
		annotations = make(map[string]string)
	}

	if containerDefaultResourceLimit != "" {
		annotations["field.cattle.io/containerDefaultResourceLimit"] = containerDefaultResourceLimit
	}

	if projectName != "" {
		annotationValue := clusterID + ":" + projectName
		annotations["field.cattle.io/projectId"] = annotationValue
	}

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        namespaceName,
			Annotations: annotations,
			Labels:      labels,
		},
	}

	dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return nil, err
	}

	adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
	if err != nil {
		return nil, err
	}

	adminDynamicClient, err := adminClient.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return nil, err
	}

	namespaceResource := dynamicClient.Resource(NamespaceGroupVersionResource).Namespace("")

	unstructuredResp, err := namespaceResource.Create(context.TODO(), unstructured.MustToUnstructured(namespace), metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	clusterRoleResource := adminDynamicClient.Resource(rbacv1.SchemeGroupVersion.WithResource("clusterroles"))

	clusterRoleWatch, err := clusterRoleResource.Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + fmt.Sprintf("%s-namespaces-edit", projectName),
		TimeoutSeconds: &defaults.WatchTimeoutSeconds,
	})

	if err != nil {
		return nil, err
	}

	err = wait.WatchWait(clusterRoleWatch, func(event watch.Event) (ready bool, err error) {
		clusterRole := &rbacv1.ClusterRole{}
		err = scheme.Scheme.Convert(event.Object.(*kubeUnstructured.Unstructured), clusterRole, event.Object.(*kubeUnstructured.Unstructured).GroupVersionKind())

		if err != nil {
			return false, err
		}

		for _, rule := range clusterRole.Rules {
			for _, resourceName := range rule.ResourceNames {
				if resourceName == namespaceName {
					return true, nil
				}
			}
		}
		return false, nil
	})

	if err != nil {
		return nil, err
	}

	client.Session.RegisterCleanupFunc(func() error {
		err := namespaceResource.Delete(context.TODO(), unstructuredResp.GetName(), metav1.DeleteOptions{})
		if errors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}

		adminNamespaceResource := adminDynamicClient.Resource(NamespaceGroupVersionResource).Namespace("")
		watchInterface, err := adminNamespaceResource.Watch(context.TODO(), metav1.ListOptions{
			FieldSelector:  "metadata.name=" + unstructuredResp.GetName(),
			TimeoutSeconds: &defaults.WatchTimeoutSeconds,
		})

		if err != nil {
			return err
		}

		return wait.WatchWait(watchInterface, func(event watch.Event) (ready bool, err error) {
			if event.Type == watch.Deleted {
				return true, nil
			}
			return false, nil
		})
	})

	newNamespace := &corev1.Namespace{}
	err = scheme.Scheme.Convert(unstructuredResp, newNamespace, unstructuredResp.GroupVersionKind())
	if err != nil {
		return nil, err
	}
	return newNamespace, nil
}

// CreateNamespaceUsingWrangler is a helper to create a namespace in the project using wrangler context
func CreateNamespaceUsingWrangler(client *rancher.Client, clusterID, projectName string, labels map[string]string) (*corev1.Namespace, error) {
	namespaceName := namegen.AppendRandomString("testns")
	annotations := map[string]string{
		ProjectIDAnnotation: clusterID + ":" + projectName,
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

// CreateMultipleNamespacesInProject creates multiple namespaces in the specified project using wrangler context
func CreateMultipleNamespacesInProject(client *rancher.Client, clusterID, projectID string, count int) ([]*corev1.Namespace, error) {
	var createdNamespaces []*corev1.Namespace

	for i := 0; i < count; i++ {
		ns, err := CreateNamespaceUsingWrangler(client, clusterID, projectID, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create namespace %d/%d: %w", i+1, count, err)
		}

		createdNamespaces = append(createdNamespaces, ns)
	}

	return createdNamespaces, nil
}
