package secrets

import (
	"context"
	"fmt"
	"reflect"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// VerifyPropagatedNamespaceSecrets verifies that secrets propagated to project namespaces match the original project-scoped secret
func VerifyPropagatedNamespaceSecrets(client *rancher.Client, clusterID, projectID string, projectScopedSecret *corev1.Secret, namespaceList []*corev1.Namespace) error {
	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		for _, ns := range namespaceList {
			nsSecret, err := GetSecretByName(client, clusterID, ns.Name, projectScopedSecret.Name, metav1.GetOptions{})
			if err != nil {
				return false, nil
			}

			if nsSecret.Labels[ProjectScopedSecretLabel] != projectID {
				return false, nil
			}

			if nsSecret.Annotations == nil || nsSecret.Annotations[ProjectScopedSecretCopyAnnotation] != "true" {
				return false, nil
			}

			if !reflect.DeepEqual(projectScopedSecret.Data, nsSecret.Data) {
				return false, nil
			}
		}
		return true, nil
	})
}

// VerifyProjectScopedSecretLabel ensures the project-scoped secret has the correct label
func VerifyProjectScopedSecretLabel(projectScopedSecret *corev1.Secret, expectedProjectID string) error {
	actualLabel, labelExists := projectScopedSecret.Labels[ProjectScopedSecretLabel]
	if !labelExists || actualLabel != expectedProjectID {
		return fmt.Errorf("project scoped secret missing or incorrect label '%s=%s'", ProjectScopedSecretLabel, expectedProjectID)
	}
	return nil
}
