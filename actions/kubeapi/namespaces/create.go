package namespaces

import (
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	extnamespaceapi "github.com/rancher/shepherd/extensions/kubeapi/namespaces"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateNamespace creates a namespace using wrangler context in a project in a specific cluster and waits for project ID propagation.
func CreateNamespace(client *rancher.Client, clusterID, projectName, namespaceName, containerDefaultResourceLimit string, labels, annotations map[string]string) (*corev1.Namespace, error) {
	if annotations == nil {
		annotations = make(map[string]string)
	}

	if containerDefaultResourceLimit != "" {
		annotations[ContainerDefaultResourceLimitAnnotation] = containerDefaultResourceLimit
	}

	if projectName != "" {
		annotationValue := clusterID + ":" + projectName
		annotations[ProjectIDAnnotation] = annotationValue
	}

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        namespaceName,
			Annotations: annotations,
			Labels:      labels,
		},
	}

	createdNamespace, err := extnamespaceapi.CreateNamespace(client, clusterID, namespace)
	if err != nil {
		return nil, err
	}

	if projectName != "" {
		err = WaitForProjectIDUpdate(client, clusterID, projectName, namespaceName)
		if err != nil {
			return nil, err
		}
	}

	return createdNamespace, nil
}

// CreateMultipleNamespacesInProject creates multiple namespaces in the specified project using wrangler context
func CreateMultipleNamespacesInProject(client *rancher.Client, clusterID, projectID string, count int) ([]*corev1.Namespace, error) {
	var createdNamespaces []*corev1.Namespace

	for i := 0; i < count; i++ {
		ns, err := CreateNamespace(client, clusterID, projectID, namegen.AppendRandomString("testns"), "", nil, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create namespace %d/%d: %w", i+1, count, err)
		}

		createdNamespaces = append(createdNamespaces, ns)
	}

	return createdNamespaces, nil
}
