package vai

import (
	"fmt"
	"net/url"

	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type secretFilterTestCase struct {
	name             string
	createSecrets    func() ([]v1.Secret, []string, []string, []string)
	filter           func(namespaces []string) url.Values
	supportedWithVai bool
}

func (s secretFilterTestCase) SupportedWithVai() bool {
	return s.supportedWithVai
}

var secretFilterTestCases = []secretFilterTestCase{
	{
		name: "Filter with negation and namespace",
		createSecrets: func() ([]v1.Secret, []string, []string, []string) {
			suffix := namegen.RandStringLower(randomStringLength)
			ns := fmt.Sprintf("namespace-%s", suffix)

			name1 := fmt.Sprintf("secret1-%s", namegen.RandStringLower(randomStringLength))
			name2 := fmt.Sprintf("secret2-%s", namegen.RandStringLower(randomStringLength))
			name3 := fmt.Sprintf("config3")

			secrets := []v1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{Name: name1, Namespace: ns},
					Type:       v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: name2, Namespace: ns},
					Type:       v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: name3, Namespace: ns},
					Type:       v1.SecretTypeOpaque,
				},
			}

			expectedNames := []string{name1, name2}
			allNamespaces := []string{ns}
			expectedNamespaces := []string{ns}

			return secrets, expectedNames, allNamespaces, expectedNamespaces
		},
		filter: func(namespaces []string) url.Values {
			return url.Values{
				"filter":               []string{"metadata.name!=config3"},
				"projectsornamespaces": namespaces,
			}
		},
		supportedWithVai: true,
	},
	{
		name: "Filter by namespace",
		createSecrets: func() ([]v1.Secret, []string, []string, []string) {
			suffix := namegen.RandStringLower(randomStringLength)
			ns1 := fmt.Sprintf("namespace1-%s", suffix)
			ns2 := fmt.Sprintf("namespace2-%s", suffix)
			name1 := fmt.Sprintf("secret1-%s", namegen.RandStringLower(randomStringLength))
			name2 := fmt.Sprintf("secret2-%s", namegen.RandStringLower(randomStringLength))

			secrets := []v1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{Name: name1, Namespace: ns1},
					Type:       v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: name2, Namespace: ns2},
					Type:       v1.SecretTypeOpaque,
				},
			}

			expectedNames := []string{name1}
			allNamespaces := []string{ns1, ns2}
			expectedNamespaces := []string{ns1}

			return secrets, expectedNames, allNamespaces, expectedNamespaces
		},
		filter: func(namespaces []string) url.Values {
			return url.Values{
				"projectsornamespaces": []string{namespaces[0]},
			}
		},
		supportedWithVai: true,
	},
	{
		name: "Filter by name and namespace",
		createSecrets: func() ([]v1.Secret, []string, []string, []string) {
			suffix := namegen.RandStringLower(randomStringLength)
			ns := fmt.Sprintf("namespace-%s", suffix)
			name1 := fmt.Sprintf("secret1")
			name2 := fmt.Sprintf("config2-%s", namegen.RandStringLower(randomStringLength))

			secrets := []v1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{Name: name1, Namespace: ns},
					Type:       v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: name2, Namespace: ns},
					Type:       v1.SecretTypeOpaque,
				},
			}

			expectedNames := []string{name1}
			allNamespaces := []string{ns}
			expectedNamespaces := []string{ns}

			return secrets, expectedNames, allNamespaces, expectedNamespaces
		},
		filter: func(namespaces []string) url.Values {
			return url.Values{
				"filter":               []string{"metadata.name=secret1"},
				"projectsornamespaces": namespaces,
			}
		},
		supportedWithVai: true,
	},
	{
		name: "Filter by single label",
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
						Labels:    map[string]string{"key1": "value1"},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name2,
						Namespace: ns,
						Labels:    map[string]string{"key1": "value2"},
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
				"filter":               []string{"metadata.labels.key1=value1"},
				"projectsornamespaces": namespaces,
			}
		},
		supportedWithVai: true,
	},
	{
		name: "Filter by multiple labels",
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
						Labels:    map[string]string{"key1": "value1", "key2": "value2"},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name2,
						Namespace: ns,
						Labels:    map[string]string{"key1": "value1"},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name3,
						Namespace: ns,
						Labels:    map[string]string{"key2": "value2"},
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
				"filter":               []string{"metadata.labels.key1=value1", "metadata.labels.key2=value2"},
				"projectsornamespaces": namespaces,
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
