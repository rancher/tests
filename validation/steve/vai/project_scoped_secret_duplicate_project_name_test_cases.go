package vai

import (
	"fmt"
	"net/url"
	"sort"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	coreV1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	projectScopedSecretLabel        = "management.cattle.io/project-scoped-secret"
	projectScopedSecretClusterLabel = "management.cattle.io/project-scoped-secret-cluster"
)

type projectScopedSecretDuplicateProjectNameFixture struct {
	namespace        string
	localCluster     string
	remoteCluster    string
	missingCluster   string
	projectName      string
	localDisplay     string
	remoteDisplay    string
	localSecretName  string
	remoteSecretName string
}

type projectScopedSecretDuplicateProjectNameTestCase struct {
	name                    string
	query                   func(projectScopedSecretDuplicateProjectNameFixture) url.Values
	expectedNames           []string
	expectedNamesForFixture func(projectScopedSecretDuplicateProjectNameFixture) []string
}

func newProjectScopedSecretDuplicateProjectNameFixture() projectScopedSecretDuplicateProjectNameFixture {
	suffix := namegen.RandStringLower(randomStringLength)
	namespace := fmt.Sprintf("pss-duplicate-%s", suffix)

	return projectScopedSecretDuplicateProjectNameFixture{
		namespace:        namespace,
		missingCluster:   namespace + "-missing",
		projectName:      fmt.Sprintf("duplicate-custom-project-%s", suffix),
		localDisplay:     "local-display",
		remoteDisplay:    "remote-display",
		localSecretName:  fmt.Sprintf("local-secret-%s", suffix),
		remoteSecretName: fmt.Sprintf("remote-secret-%s", suffix),
	}
}

func projectScopedSecretDuplicateProjectNameNamespaces(fixture projectScopedSecretDuplicateProjectNameFixture) []string {
	return []string{
		fmt.Sprintf("%s-%s", fixture.localCluster, fixture.projectName),
		fmt.Sprintf("%s-%s", fixture.remoteCluster, fixture.projectName),
	}
}

func projectScopedSecretDuplicateProjectNameProjects(fixture projectScopedSecretDuplicateProjectNameFixture) []v3.Project {
	return []v3.Project{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fixture.projectName,
				Namespace: fixture.localCluster,
			},
			Spec: v3.ProjectSpec{
				ClusterName: fixture.localCluster,
				DisplayName: fixture.localDisplay,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fixture.projectName,
				Namespace: fixture.remoteCluster,
			},
			Spec: v3.ProjectSpec{
				ClusterName: fixture.remoteCluster,
				DisplayName: fixture.remoteDisplay,
			},
		},
	}
}

func projectScopedSecretDuplicateProjectNameSecrets(fixture projectScopedSecretDuplicateProjectNameFixture) []coreV1.Secret {
	return []coreV1.Secret{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fixture.localSecretName,
				Namespace: fmt.Sprintf("%s-%s", fixture.localCluster, fixture.projectName),
				Labels: map[string]string{
					projectScopedSecretLabel:        fixture.projectName,
					projectScopedSecretClusterLabel: fixture.localCluster,
				},
			},
			Type: coreV1.SecretTypeOpaque,
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fixture.remoteSecretName,
				Namespace: fmt.Sprintf("%s-%s", fixture.remoteCluster, fixture.projectName),
				Labels: map[string]string{
					projectScopedSecretLabel:        fixture.projectName,
					projectScopedSecretClusterLabel: fixture.remoteCluster,
				},
			},
			Type: coreV1.SecretTypeOpaque,
		},
	}
}

func projectScopedSecretNamesSortedByCluster(fixture projectScopedSecretDuplicateProjectNameFixture, descending bool) []string {
	secrets := []struct {
		name        string
		clusterName string
	}{
		{name: fixture.localSecretName, clusterName: fixture.localCluster},
		{name: fixture.remoteSecretName, clusterName: fixture.remoteCluster},
	}

	sort.Slice(secrets, func(firstIndex, secondIndex int) bool {
		if secrets[firstIndex].clusterName == secrets[secondIndex].clusterName {
			return secrets[firstIndex].name < secrets[secondIndex].name
		}

		if descending {
			return secrets[firstIndex].clusterName > secrets[secondIndex].clusterName
		}
		return secrets[firstIndex].clusterName < secrets[secondIndex].clusterName
	})

	names := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		names = append(names, secret.name)
	}

	return names
}

var projectScopedSecretDuplicateProjectNameTestCases = []projectScopedSecretDuplicateProjectNameTestCase{
	{
		name: "Project scoped secret filter keeps duplicate project names isolated",
		query: func(f projectScopedSecretDuplicateProjectNameFixture) url.Values {
			return url.Values{
				"filter": []string{
					fmt.Sprintf("metadata.labels[%s]=%s", projectScopedSecretLabel, f.projectName),
					fmt.Sprintf("spec.clusterName=%s", f.localCluster),
				},
				"sort": []string{"metadata.name"},
			}
		},
		expectedNamesForFixture: func(f projectScopedSecretDuplicateProjectNameFixture) []string {
			return []string{f.localSecretName}
		},
	},
	{
		name: "Project scoped secret filters remote cluster only",
		query: func(f projectScopedSecretDuplicateProjectNameFixture) url.Values {
			return url.Values{
				"filter": []string{
					fmt.Sprintf("metadata.labels[%s]=%s", projectScopedSecretLabel, f.projectName),
					fmt.Sprintf("spec.clusterName=%s", f.remoteCluster),
				},
				"sort": []string{"metadata.name"},
			}
		},
		expectedNamesForFixture: func(f projectScopedSecretDuplicateProjectNameFixture) []string {
			return []string{f.remoteSecretName}
		},
	},
	{
		name: "Project scoped secret cluster filter misses unknown cluster",
		query: func(f projectScopedSecretDuplicateProjectNameFixture) url.Values {
			return url.Values{
				"filter": []string{
					fmt.Sprintf("metadata.labels[%s]=%s", projectScopedSecretLabel, f.projectName),
					fmt.Sprintf("spec.clusterName=%s", f.missingCluster),
				},
				"sort": []string{"metadata.name"},
			}
		},
		expectedNames: []string{},
	},
	{
		name: "Project scoped secret filters local display name only",
		query: func(f projectScopedSecretDuplicateProjectNameFixture) url.Values {
			return url.Values{
				"filter": []string{
					fmt.Sprintf("metadata.labels[%s]=%s", projectScopedSecretLabel, f.projectName),
					fmt.Sprintf("spec.displayName=%s", f.localDisplay),
				},
				"sort": []string{"metadata.name"},
			}
		},
		expectedNamesForFixture: func(f projectScopedSecretDuplicateProjectNameFixture) []string {
			return []string{f.localSecretName}
		},
	},
	{
		name: "Project scoped secret filters remote display name only",
		query: func(f projectScopedSecretDuplicateProjectNameFixture) url.Values {
			return url.Values{
				"filter": []string{
					fmt.Sprintf("metadata.labels[%s]=%s", projectScopedSecretLabel, f.projectName),
					fmt.Sprintf("spec.displayName=%s", f.remoteDisplay),
				},
				"sort": []string{"metadata.name"},
			}
		},
		expectedNamesForFixture: func(f projectScopedSecretDuplicateProjectNameFixture) []string {
			return []string{f.remoteSecretName}
		},
	},
	{
		name: "Project scoped secret duplicate project label alone lists both",
		query: func(f projectScopedSecretDuplicateProjectNameFixture) url.Values {
			return url.Values{
				"filter": []string{fmt.Sprintf("metadata.labels[%s]=%s", projectScopedSecretLabel, f.projectName)},
				"sort":   []string{"metadata.name"},
			}
		},
		expectedNamesForFixture: func(f projectScopedSecretDuplicateProjectNameFixture) []string {
			return []string{f.localSecretName, f.remoteSecretName}
		},
	},
	{
		name: "Project scoped secret project label plus cluster label isolates local",
		query: func(f projectScopedSecretDuplicateProjectNameFixture) url.Values {
			return url.Values{
				"filter": []string{
					fmt.Sprintf("metadata.labels[%s]=%s", projectScopedSecretLabel, f.projectName),
					fmt.Sprintf("metadata.labels[%s]=%s", projectScopedSecretClusterLabel, f.localCluster),
				},
				"sort": []string{"metadata.name"},
			}
		},
		expectedNamesForFixture: func(f projectScopedSecretDuplicateProjectNameFixture) []string {
			return []string{f.localSecretName}
		},
	},
	{
		name: "Project scoped secret sorts by derived cluster ascending",
		query: func(f projectScopedSecretDuplicateProjectNameFixture) url.Values {
			return url.Values{
				"filter": []string{fmt.Sprintf("metadata.labels[%s]=%s", projectScopedSecretLabel, f.projectName)},
				"sort":   []string{"spec.clusterName,metadata.name"},
			}
		},
		expectedNamesForFixture: func(f projectScopedSecretDuplicateProjectNameFixture) []string {
			return projectScopedSecretNamesSortedByCluster(f, false)
		},
	},
	{
		name: "Project scoped secret sorts by derived cluster descending",
		query: func(f projectScopedSecretDuplicateProjectNameFixture) url.Values {
			return url.Values{
				"filter": []string{fmt.Sprintf("metadata.labels[%s]=%s", projectScopedSecretLabel, f.projectName)},
				"sort":   []string{"-spec.clusterName,metadata.name"},
			}
		},
		expectedNamesForFixture: func(f projectScopedSecretDuplicateProjectNameFixture) []string {
			return projectScopedSecretNamesSortedByCluster(f, true)
		},
	},
	{
		name: "Project scoped secret sorts by derived display name",
		query: func(f projectScopedSecretDuplicateProjectNameFixture) url.Values {
			return url.Values{
				"filter": []string{fmt.Sprintf("metadata.labels[%s]=%s", projectScopedSecretLabel, f.projectName)},
				"sort":   []string{"spec.displayName,metadata.name"},
			}
		},
		expectedNamesForFixture: func(f projectScopedSecretDuplicateProjectNameFixture) []string {
			return []string{f.localSecretName, f.remoteSecretName}
		},
	},
	{
		name: "Project scoped secret paginates after derived cluster sort",
		query: func(f projectScopedSecretDuplicateProjectNameFixture) url.Values {
			return url.Values{
				"filter":   []string{fmt.Sprintf("metadata.labels[%s]=%s", projectScopedSecretLabel, f.projectName)},
				"sort":     []string{"spec.clusterName,metadata.name"},
				"pagesize": []string{"1"},
				"page":     []string{"2"},
			}
		},
		expectedNamesForFixture: func(f projectScopedSecretDuplicateProjectNameFixture) []string {
			return []string{projectScopedSecretNamesSortedByCluster(f, false)[1]}
		},
	},
}
