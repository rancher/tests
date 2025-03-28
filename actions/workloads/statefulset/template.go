package statefulset

import (
	"fmt"

	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewStatefulsetTemplate is a constructor that creates the template for a StatefulSet
func NewStatefulsetTemplate(namespaceName string, podTemplate corev1.PodTemplateSpec, replicas int32) *appv1.StatefulSet {
	statefulsetName := namegen.AppendRandomString("teststatefulset")

	labels := map[string]string{
		"workload.user.cattle.io/workloadselector": fmt.Sprintf("apps.statefulset-%v-%v", namespaceName, statefulsetName),
	}
	podTemplate.ObjectMeta.Labels = labels

	return &appv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      statefulsetName,
			Namespace: namespaceName,
		},
		Spec: appv1.StatefulSetSpec{
			Replicas: &replicas,
			Template: podTemplate,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
		},
	}
}
