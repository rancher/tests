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
	{
		name: "Filter by project with no namespaces - should return empty collection",
		createSecrets: func() ([]v1.Secret, []string, []string, []string) {
			// No secrets, no namespaces - testing empty project scenario
			// This simulates querying a project that exists but has no associated namespaces/secrets
			secrets := []v1.Secret{}
			expectedNames := []string{}
			allNamespaces := []string{}
			expectedNamespaces := []string{}

			return secrets, expectedNames, allNamespaces, expectedNamespaces
		},
		filter: func(namespaces []string) url.Values {
			// Generate a unique project ID to ensure it doesn't conflict with other tests
			projectID := fmt.Sprintf("p-%s", namegen.RandStringLower(6))
			return url.Values{
				"projectsornamespaces": []string{projectID},
			}
		},
		supportedWithVai: true,
	},
	{
		name: "Filter with quoted value containing dots (version string)",
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
						Labels:    map[string]string{"version": "v1.2.3-beta.1"},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name2,
						Namespace: ns,
						Labels:    map[string]string{"version": "v1.2.3"},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name3,
						Namespace: ns,
						Labels:    map[string]string{"version": "v2.0.0"},
					},
					Type: v1.SecretTypeOpaque,
				},
			}

			expectedNames := []string{name1}
			allNamespaces := []string{ns}
			expectedNamespaces := []string{ns}

			return secrets, expectedNames, allNamespaces, expectedNamespaces
		},
		filter: func(namespaces []string) url.Values {
			return url.Values{
				"filter":               []string{`metadata.labels.version="v1.2.3-beta.1"`},
				"projectsornamespaces": namespaces,
			}
		},
		supportedWithVai: true,
	},

	{
		name: "Filter with negation and quoted complex value",
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
						Labels:    map[string]string{"app": "web-server"},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name2,
						Namespace: ns,
						Labels:    map[string]string{"app": "api-gateway"},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name3,
						Namespace: ns,
						Labels:    map[string]string{"app": "api-gateway"},
					},
					Type: v1.SecretTypeOpaque,
				},
			}

			expectedNames := []string{name1}
			allNamespaces := []string{ns}
			expectedNamespaces := []string{ns}

			return secrets, expectedNames, allNamespaces, expectedNamespaces
		},
		filter: func(namespaces []string) url.Values {
			return url.Values{
				"filter":               []string{`metadata.labels.app!="api-gateway"`},
				"projectsornamespaces": namespaces,
			}
		},
		supportedWithVai: true,
	},

	{
		name: "Filter with quoted numeric string",
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
						Labels:    map[string]string{"priority": "100"},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name2,
						Namespace: ns,
						Labels:    map[string]string{"priority": "10"},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name3,
						Namespace: ns,
						Labels:    map[string]string{"priority": "1000"},
					},
					Type: v1.SecretTypeOpaque,
				},
			}

			expectedNames := []string{name1}
			allNamespaces := []string{ns}
			expectedNamespaces := []string{ns}

			return secrets, expectedNames, allNamespaces, expectedNamespaces
		},
		filter: func(namespaces []string) url.Values {
			return url.Values{
				"filter":               []string{`metadata.labels.priority="100"`},
				"projectsornamespaces": namespaces,
			}
		},
		supportedWithVai: true,
	},

	{
		name: "Filter with multiple quoted values in same query",
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
						Labels: map[string]string{
							"env":     "prod",
							"version": "1.2.3",
						},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name2,
						Namespace: ns,
						Labels: map[string]string{
							"env":     "prod",
							"version": "1.2.4",
						},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name3,
						Namespace: ns,
						Labels: map[string]string{
							"env":     "dev",
							"version": "1.2.3",
						},
					},
					Type: v1.SecretTypeOpaque,
				},
			}

			expectedNames := []string{name1}
			allNamespaces := []string{ns}
			expectedNamespaces := []string{ns}

			return secrets, expectedNames, allNamespaces, expectedNamespaces
		},
		filter: func(namespaces []string) url.Values {
			return url.Values{
				"filter":               []string{`metadata.labels.env="prod"`, `metadata.labels.version="1.2.3"`},
				"projectsornamespaces": namespaces,
			}
		},
		supportedWithVai: true,
	},
	{
		name: "Filter with substring match on quoted value",
		createSecrets: func() ([]v1.Secret, []string, []string, []string) {
			suffix := namegen.RandStringLower(randomStringLength)
			ns := fmt.Sprintf("namespace-%s", suffix)
			name1 := fmt.Sprintf("app-backend-v1.2.3-%s", namegen.RandStringLower(randomStringLength))
			name2 := fmt.Sprintf("app-frontend-v1.2.4-%s", namegen.RandStringLower(randomStringLength))
			name3 := fmt.Sprintf("config-v2.0-%s", namegen.RandStringLower(randomStringLength))

			secrets := []v1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name1,
						Namespace: ns,
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name2,
						Namespace: ns,
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name3,
						Namespace: ns,
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
				"filter":               []string{`metadata.name~"v1.2"`},
				"projectsornamespaces": namespaces,
			}
		},
		supportedWithVai: true,
	},

	{
		name: "Filter with quoted empty string value",
		createSecrets: func() ([]v1.Secret, []string, []string, []string) {
			suffix := namegen.RandStringLower(randomStringLength)
			ns := fmt.Sprintf("namespace-%s", suffix)
			name1 := fmt.Sprintf("secret1-%s", namegen.RandStringLower(randomStringLength))
			name2 := fmt.Sprintf("secret2-%s", namegen.RandStringLower(randomStringLength))

			secrets := []v1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name1,
						Namespace: ns,
						Labels:    map[string]string{"optional": ""},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name2,
						Namespace: ns,
						Labels:    map[string]string{"optional": "enabled"},
					},
					Type: v1.SecretTypeOpaque,
				},
			}

			expectedNames := []string{name1}
			allNamespaces := []string{ns}
			expectedNamespaces := []string{ns}

			return secrets, expectedNames, allNamespaces, expectedNamespaces
		},
		filter: func(namespaces []string) url.Values {
			return url.Values{
				"filter":               []string{`metadata.labels.optional=""`},
				"projectsornamespaces": namespaces,
			}
		},
		supportedWithVai: true,
	},
}
