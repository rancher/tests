package vai

import (
	"fmt"

	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type secretPageSizeTestCase struct {
	name             string
	numSecrets       int
	pageSize         int
	expectedPages    int
	expectedTotal    int
	supportedWithVai bool
}

func (s secretPageSizeTestCase) SupportedWithVai() bool {
	return s.supportedWithVai
}

// Helper function to create secrets
func createPageSizeTestSecrets(count int) ([]v1.Secret, string) {
	suffix := namegen.RandStringLower(randomStringLength)
	ns := fmt.Sprintf("pagesize-ns-%s", suffix)

	secrets := make([]v1.Secret, count)
	for i := 0; i < count; i++ {
		secrets[i] = v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("pagesize-secret%d-%s", i+1, suffix),
				Namespace: ns,
			},
		}
	}
	return secrets, ns
}

var secretPageSizeTestCases = []secretPageSizeTestCase{
	{
		name:             "Paginate 50 secrets with pagesize 10",
		numSecrets:       50,
		pageSize:         10,
		expectedPages:    5,
		expectedTotal:    50,
		supportedWithVai: true,
	},
	{
		name:             "Paginate 100 secrets with pagesize 25",
		numSecrets:       100,
		pageSize:         25,
		expectedPages:    4,
		expectedTotal:    100,
		supportedWithVai: true,
	},
	{
		name:             "Paginate 30 secrets with pagesize 50",
		numSecrets:       30,
		pageSize:         50,
		expectedPages:    1,
		expectedTotal:    30,
		supportedWithVai: true,
	},
	{
		name:             "Paginate 73 secrets with pagesize 20",
		numSecrets:       73,
		pageSize:         20,
		expectedPages:    4,
		expectedTotal:    73,
		supportedWithVai: true,
	},
	{
		name:             "Paginate 1 secret with pagesize 10",
		numSecrets:       1,
		pageSize:         10,
		expectedPages:    1,
		expectedTotal:    1,
		supportedWithVai: true,
	},
}
