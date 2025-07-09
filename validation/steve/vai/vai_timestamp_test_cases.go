package vai

import (
	"fmt"
	"time"

	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type timestampTestCase struct {
	name              string
	resourceType      string
	namespaced        bool
	createResource    func() (interface{}, string, string)
	waitBetweenChecks time.Duration
	checkCount        int
	supportedWithVai  bool
}

func (t timestampTestCase) SupportedWithVai() bool {
	return t.supportedWithVai
}

var timestampTestCases = []timestampTestCase{
	{
		name:         "Pod Age field updates correctly",
		resourceType: "pod",
		namespaced:   true,
		createResource: func() (interface{}, string, string) {
			suffix := namegen.RandStringLower(randomStringLength)
			ns := fmt.Sprintf("timestamp-ns-%s", suffix)
			name := fmt.Sprintf("test-pod-%s", suffix)
			pod := v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: ns,
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "test-container",
							Image: "nginx:alpine",
						},
					},
				},
			}
			return pod, ns, name
		},
		waitBetweenChecks: 5 * time.Second,
		checkCount:        3,
		supportedWithVai:  true,
	},
	{
		name:         "Deployment Age field updates correctly",
		resourceType: "apps.deployment",
		namespaced:   true,
		createResource: func() (interface{}, string, string) {
			suffix := namegen.RandStringLower(randomStringLength)
			ns := fmt.Sprintf("timestamp-ns-%s", suffix)
			name := fmt.Sprintf("test-deployment-%s", suffix)
			replicas := int32(1)
			deployment := appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: ns,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": name,
						},
					},
					Template: v1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": name,
							},
						},
						Spec: v1.PodSpec{
							Containers: []v1.Container{
								{
									Name:  "test-container",
									Image: "nginx:alpine",
								},
							},
						},
					},
				},
			}
			return deployment, ns, name
		},
		waitBetweenChecks: 5 * time.Second,
		checkCount:        3,
		supportedWithVai:  true,
	},
	{
		name:         "Service Age field updates correctly",
		resourceType: "service",
		namespaced:   true,
		createResource: func() (interface{}, string, string) {
			suffix := namegen.RandStringLower(randomStringLength)
			ns := fmt.Sprintf("timestamp-ns-%s", suffix)
			name := fmt.Sprintf("test-service-%s", suffix)
			service := v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: ns,
				},
				Spec: v1.ServiceSpec{
					Selector: map[string]string{
						"app": name,
					},
					Ports: []v1.ServicePort{
						{
							Port: 80,
						},
					},
				},
			}
			return service, ns, name
		},
		waitBetweenChecks: 5 * time.Second,
		checkCount:        3,
		supportedWithVai:  true,
	},
	{
		name:         "ConfigMap Age field updates correctly",
		resourceType: "configmap",
		namespaced:   true,
		createResource: func() (interface{}, string, string) {
			suffix := namegen.RandStringLower(randomStringLength)
			ns := fmt.Sprintf("timestamp-ns-%s", suffix)
			name := fmt.Sprintf("test-cm-%s", suffix)
			cm := v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: ns,
				},
				Data: map[string]string{
					"test": "data",
				},
			}
			return cm, ns, name
		},
		waitBetweenChecks: 5 * time.Second,
		checkCount:        3,
		supportedWithVai:  true,
	},
}
