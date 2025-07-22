package vai

import (
	"fmt"
	"net/url"

	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var vaiOnlySecretSortCases = []secretSortTestCase{
	{
		name: "Sort by label should include secrets without that label in results",
		createSecrets: func(sortFunc func() url.Values) ([]v1.Secret, []string, []string) {
			suffix := namegen.RandStringLower(randomStringLength)
			ns := fmt.Sprintf("namespace-%s", suffix)

			name1 := "secret-no-priority-1"
			name2 := "secret-high-priority-2"
			name3 := "secret-low-priority-3"
			name4 := "secret-no-priority-4"

			secrets := []v1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name1,
						Namespace: ns,
						// No labels
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name2,
						Namespace: ns,
						Labels:    map[string]string{"priority": "high"},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name3,
						Namespace: ns,
						Labels:    map[string]string{"priority": "low"},
					},
					Type: v1.SecretTypeOpaque,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name4,
						Namespace: ns,
						// No labels
					},
					Type: v1.SecretTypeOpaque,
				},
			}

			// For ASC NULLS LAST: labeled secrets first (alphabetically), then unlabeled secrets last
			expectedOrder := []string{name2, name3, name1, name4} // "high", "low", null, null

			return secrets, expectedOrder, []string{ns}
		},
		sort: func() url.Values {
			return url.Values{"sort": []string{"metadata.labels.priority"}}
		},
		supportedWithVai: true,
	},
}
