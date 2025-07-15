package vai

import (
	"fmt"
	"net/url"

	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var vaiOnlySecretFilterCases = []secretFilterTestCase{
	{
		name: "Filter by project-scoped-secret-copy annotation",
		createSecrets: func() ([]v1.Secret, []string, []string, []string) {
			suffix := namegen.RandStringLower(randomStringLength)
			ns := fmt.Sprintf("namespace-%s", suffix)
			name1 := fmt.Sprintf("secret1-%s", namegen.RandStringLower(randomStringLength))
			name2 := fmt.Sprintf("secret2-%s", namegen.RandStringLower(randomStringLength))
			name3 := fmt.Sprintf("secret3-%s", namegen.RandStringLower(randomStringLength))

			projectID := "local:p-test123"

			secrets := []v1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name1,
						Namespace: ns,
						Annotations: map[string]string{
							"management.cattle.io/project-scoped-secret-copy": projectID,
						},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name2,
						Namespace: ns,
						Annotations: map[string]string{
							"management.cattle.io/project-scoped-secret-copy": projectID,
						},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name3,
						Namespace: ns,
						// No project annotation - this secret should NOT be returned
					},
					Type: v1.SecretTypeOpaque,
				},
			}

			expectedNames := []string{name1, name2}
			allNamespaces := []string{ns}
			expectedNamespaces := []string{ns}

			return secrets, expectedNames, allNamespaces, expectedNamespaces
		},
		filter: func(namespaces []string) url.Values {
			return url.Values{
				"filter":               []string{`metadata.annotations[management.cattle.io/project-scoped-secret-copy]="local:p-test123"`},
				"projectsornamespaces": namespaces,
			}
		},
		supportedWithVai: true,
	},
	{
		name: "Filter by project-scoped-secret-copy annotation - different projects",
		createSecrets: func() ([]v1.Secret, []string, []string, []string) {
			suffix := namegen.RandStringLower(randomStringLength)
			ns := fmt.Sprintf("namespace-%s", suffix)
			name1 := fmt.Sprintf("secret1-%s", namegen.RandStringLower(randomStringLength))
			name2 := fmt.Sprintf("secret2-%s", namegen.RandStringLower(randomStringLength))
			name3 := fmt.Sprintf("secret3-%s", namegen.RandStringLower(randomStringLength))

			secrets := []v1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name1,
						Namespace: ns,
						Annotations: map[string]string{
							"management.cattle.io/project-scoped-secret-copy": "local:p-project1",
						},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name2,
						Namespace: ns,
						Annotations: map[string]string{
							"management.cattle.io/project-scoped-secret-copy": "local:p-project2",
						},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name3,
						Namespace: ns,
						Annotations: map[string]string{
							"management.cattle.io/project-scoped-secret-copy": "local:p-project1",
						},
					},
					Type: v1.SecretTypeOpaque,
				},
			}

			// Only expecting secrets from project1
			expectedNames := []string{name1, name3}
			allNamespaces := []string{ns}
			expectedNamespaces := []string{ns}

			return secrets, expectedNames, allNamespaces, expectedNamespaces
		},
		filter: func(namespaces []string) url.Values {
			return url.Values{
				"filter":               []string{`metadata.annotations[management.cattle.io/project-scoped-secret-copy]="local:p-project1"`},
				"projectsornamespaces": namespaces,
			}
		},
		supportedWithVai: true,
	},
	{
		name: "Filter by project-scoped-secret-copy annotation with negation",
		createSecrets: func() ([]v1.Secret, []string, []string, []string) {
			suffix := namegen.RandStringLower(randomStringLength)
			ns := fmt.Sprintf("namespace-%s", suffix)
			name1 := fmt.Sprintf("secret1-%s", namegen.RandStringLower(randomStringLength))
			name2 := fmt.Sprintf("secret2-%s", namegen.RandStringLower(randomStringLength))
			name3 := fmt.Sprintf("secret3-%s", namegen.RandStringLower(randomStringLength))

			secrets := []v1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name1,
						Namespace: ns,
						Annotations: map[string]string{
							"management.cattle.io/project-scoped-secret-copy": "local:p-exclude",
						},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name2,
						Namespace: ns,
						Annotations: map[string]string{
							"management.cattle.io/project-scoped-secret-copy": "local:p-include",
						},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name3,
						Namespace: ns,
						// No annotation - this should be included in results
					},
					Type: v1.SecretTypeOpaque,
				},
			}

			// Expecting all secrets EXCEPT the one with "local:p-exclude"
			expectedNames := []string{name2, name3}
			allNamespaces := []string{ns}
			expectedNamespaces := []string{ns}

			return secrets, expectedNames, allNamespaces, expectedNamespaces
		},
		filter: func(namespaces []string) url.Values {
			return url.Values{
				"filter":               []string{`metadata.annotations[management.cattle.io/project-scoped-secret-copy]!="local:p-exclude"`},
				"projectsornamespaces": namespaces,
			}
		},
		supportedWithVai: true,
	},
}
