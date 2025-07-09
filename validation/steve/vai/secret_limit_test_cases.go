package vai

import (
	"fmt"

	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type secretLimitTestCase struct {
	name             string
	createSecrets    func() ([]v1.Secret, string)
	limit            int
	expectedTotal    int
	supportedWithVai bool
}

func (s secretLimitTestCase) SupportedWithVai() bool {
	return s.supportedWithVai
}

var secretLimitTestCases = []secretLimitTestCase{
	{
		name: "Paginate 50 secrets with limit 10",
		createSecrets: func() ([]v1.Secret, string) {
			suffix := namegen.RandStringLower(randomStringLength)
			ns := fmt.Sprintf("pagination-ns-%s", suffix)
			numSecrets := 50
			secrets := make([]v1.Secret, numSecrets)
			for i := 0; i < numSecrets; i++ {
				secrets[i] = v1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("pagination-secret%d-%s", i+1, suffix),
						Namespace: ns,
					},
				}
			}
			return secrets, ns
		},
		limit:            10,
		expectedTotal:    50,
		supportedWithVai: false,
	},
	{
		name: "Paginate 100 secrets with limit 25",
		createSecrets: func() ([]v1.Secret, string) {
			suffix := namegen.RandStringLower(randomStringLength)
			ns := fmt.Sprintf("pagination-ns-%s", suffix)
			numSecrets := 100
			secrets := make([]v1.Secret, numSecrets)
			for i := 0; i < numSecrets; i++ {
				secrets[i] = v1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("pagination-secret%d-%s", i+1, suffix),
						Namespace: ns,
					},
				}
			}
			return secrets, ns
		},
		limit:            25,
		expectedTotal:    100,
		supportedWithVai: false,
	},
	{
		name: "Paginate 75 secrets with limit 15",
		createSecrets: func() ([]v1.Secret, string) {
			suffix := namegen.RandStringLower(randomStringLength)
			ns := fmt.Sprintf("pagination-ns-%s", suffix)
			numSecrets := 75
			secrets := make([]v1.Secret, numSecrets)
			for i := 0; i < numSecrets; i++ {
				secrets[i] = v1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("pagination-secret%d-%s", i+1, suffix),
						Namespace: ns,
					},
				}
			}
			return secrets, ns
		},
		limit:            15,
		expectedTotal:    75,
		supportedWithVai: false,
	},
}
