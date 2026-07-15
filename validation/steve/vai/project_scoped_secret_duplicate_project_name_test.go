//go:build (validation || infra.any || cluster.any || extended) && !stress && !2.8 && !2.9 && !2.10 && !2.11

package vai

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

type ProjectScopedSecretDuplicateProjectNameTestSuite struct {
	suite.Suite
	client      *rancher.Client
	steveClient *steveV1.Client
	session     *session.Session
	cluster     *management.Cluster
	vaiEnabled  bool
}

func (p *ProjectScopedSecretDuplicateProjectNameTestSuite) SetupSuite() {
	testSession := session.NewSession()
	p.session = testSession

	client, err := rancher.NewClient("", p.session)
	require.NoError(p.T(), err)

	p.client = client
	p.steveClient = client.Steve

	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(p.T(), clusterName, "Cluster name to install should be set")
	require.NotEqual(p.T(), "local", clusterName, "Duplicate project-name secret tests require rancher.clusterName to point to a downstream cluster")

	clusterID, err := clusters.GetClusterIDByName(p.client, clusterName)
	require.NoError(p.T(), err, "Error getting cluster ID")
	require.NotEqual(p.T(), "local", clusterID, "Duplicate project-name secret tests require a downstream cluster, not local")

	p.cluster, err = p.client.Management.Cluster.ByID(clusterID)
	require.NoError(p.T(), err)

	enabled, err := isVaiEnabled(p.client)
	require.NoError(p.T(), err)
	p.vaiEnabled = enabled
}

func (p *ProjectScopedSecretDuplicateProjectNameTestSuite) TearDownSuite() {
	p.session.Cleanup()
}

func (p *ProjectScopedSecretDuplicateProjectNameTestSuite) ensureVaiEnabled() {
	if !p.vaiEnabled {
		err := ensureVAIState(p.client, true)
		require.NoError(p.T(), err)
		p.vaiEnabled = true
	}
}

func (p *ProjectScopedSecretDuplicateProjectNameTestSuite) setupProjectScopedSecretDuplicateProjectNameFixture(fixture *projectScopedSecretDuplicateProjectNameFixture) {
	namespaceClient := p.steveClient.SteveType("namespace")

	projectClient := p.steveClient.SteveType("management.cattle.io.project")
	projectIDs := make([]string, 0, len(projectScopedSecretDuplicateProjectNameProjects(*fixture)))
	for _, project := range projectScopedSecretDuplicateProjectNameProjects(*fixture) {
		logrus.Infof("Creating project %s in cluster namespace %s", project.Name, project.Namespace)
		_, err := projectClient.Create(&project)
		require.NoError(p.T(), err)
		projectIDs = append(projectIDs, fmt.Sprintf("%s/%s", project.Namespace, project.Name))
	}

	err := waitForResourcesCreated(projectClient, projectIDs)
	require.NoError(p.T(), err, "Not all duplicate project-name project fixtures were created successfully")

	for _, namespace := range projectScopedSecretDuplicateProjectNameNamespaces(*fixture) {
		logrus.Infof("Waiting for project backing namespace: %s", namespace)
		err = waitForNamespaceActive(namespaceClient, namespace)
		require.NoError(p.T(), err, "Namespace %s did not become active", namespace)
	}

	secretClient := p.steveClient.SteveType("secret")
	resourceIDs := make([]string, 0, len(projectScopedSecretDuplicateProjectNameSecrets(*fixture)))
	for _, secret := range projectScopedSecretDuplicateProjectNameSecrets(*fixture) {
		logrus.Infof("Creating project-scoped secret fixture %s/%s", secret.Namespace, secret.Name)
		_, err := secretClient.Create(&secret)
		require.NoError(p.T(), err)
		resourceIDs = append(resourceIDs, fmt.Sprintf("%s:%s", secret.Namespace, secret.Name))
	}

	err = waitForResourcesCreated(secretClient, resourceIDs)
	require.NoError(p.T(), err, "Not all duplicate project-name secret fixtures were created successfully")

	p.waitForProjectScopedSecretDuplicateProjectNameNames(
		*fixture,
		url.Values{
			"filter": []string{fmt.Sprintf("metadata.labels[%s]=%s", projectScopedSecretLabel, fixture.projectName)},
			"sort":   []string{"metadata.name"},
		},
		[]string{fixture.localSecretName, fixture.remoteSecretName},
	)

	p.hydrateProjectScopedSecretDuplicateProjectNameCache(*fixture)
}

func (p *ProjectScopedSecretDuplicateProjectNameTestSuite) hydrateProjectScopedSecretDuplicateProjectNameCache(fixture projectScopedSecretDuplicateProjectNameFixture) {
	logrus.Info("Hydrating VAI cache for duplicate project-name secret fixtures")

	projectClient := p.steveClient.SteveType("management.cattle.io.project")
	projectQuery := url.Values{
		"filter": []string{fmt.Sprintf("metadata.name=%s", fixture.projectName)},
		"sort":   []string{"metadata.namespace"},
	}

	err := kwait.PollUntilContextTimeout(context.Background(), time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		projectCollection, err := projectClient.List(projectQuery)
		if err != nil {
			return false, err
		}

		if len(projectCollection.Data) != 2 {
			logrus.Infof("Expected two duplicate project fixtures in Steve, got %d. Retrying...", len(projectCollection.Data))
			return false, nil
		}

		return true, nil
	})
	require.NoError(p.T(), err, "Timed out waiting for duplicate project fixtures to hydrate in Steve")

	_ = kwait.PollImmediate(time.Second, 5*time.Second, func() (bool, error) {
		return false, nil
	})
}

func (p *ProjectScopedSecretDuplicateProjectNameTestSuite) waitForProjectScopedSecretDuplicateProjectNameNames(fixture projectScopedSecretDuplicateProjectNameFixture, query url.Values, expectedNames []string) {
	secretClient := p.steveClient.SteveType("secret")
	actualNames := []string{}

	err := kwait.PollUntilContextTimeout(context.Background(), time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		secretCollection, err := secretClient.List(query)
		if err != nil {
			return false, err
		}

		actualNames = make([]string, 0, len(secretCollection.Data))
		for _, item := range secretCollection.Data {
			actualNames = append(actualNames, item.GetName())
		}

		if !reflect.DeepEqual(expectedNames, actualNames) {
			return false, nil
		}

		return true, nil
	})

	require.NoError(
		p.T(),
		err,
		"Timed out waiting for duplicate project-name query %q.\nExpected fixture names: %v\nGot fixture names: %v\nGot all response names: %v",
		query.Encode(),
		expectedNames,
		projectScopedSecretFixtureNamesFromResponse(fixture, actualNames),
		actualNames,
	)
}

func (p *ProjectScopedSecretDuplicateProjectNameTestSuite) requireQueryIncludesAndExcludes(fixture projectScopedSecretDuplicateProjectNameFixture, query url.Values, includedNames, excludedNames []string) {
	secretClient := p.steveClient.SteveType("secret")
	actualNames := []string{}

	err := kwait.PollUntilContextTimeout(context.Background(), time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		secretCollection, err := secretClient.List(query)
		if err != nil {
			return false, err
		}

		actualNames = make([]string, 0, len(secretCollection.Data))
		for _, item := range secretCollection.Data {
			actualNames = append(actualNames, item.GetName())
		}

		for _, excludedName := range excludedNames {
			if containsString(actualNames, excludedName) {
				require.FailNowf(
					p.T(),
					"Project-scoped secret cluster filter leaked a secret from a duplicate project name",
					"Query: %q\nExpected to include: %v\nExpected to exclude: %v\nGot fixture names: %v\nGot all response names: %v",
					query.Encode(),
					includedNames,
					excludedNames,
					projectScopedSecretFixtureNamesFromResponse(fixture, actualNames),
					actualNames,
				)
			}
		}

		for _, includedName := range includedNames {
			if !containsString(actualNames, includedName) {
				return false, nil
			}
		}

		return true, nil
	})

	require.NoError(
		p.T(),
		err,
		"Timed out waiting for duplicate project-name query %q.\nExpected to include: %v\nExpected to exclude: %v\nGot fixture names: %v\nGot all response names: %v",
		query.Encode(),
		includedNames,
		excludedNames,
		projectScopedSecretFixtureNamesFromResponse(fixture, actualNames),
		actualNames,
	)
}

func projectScopedSecretFixtureNamesFromResponse(fixture projectScopedSecretDuplicateProjectNameFixture, names []string) []string {
	fixtureNames := []string{}
	for _, name := range names {
		if name == fixture.localSecretName || name == fixture.remoteSecretName {
			fixtureNames = append(fixtureNames, name)
		}
	}

	return fixtureNames
}

func containsString(items []string, expected string) bool {
	for _, item := range items {
		if item == expected {
			return true
		}
	}

	return false
}

func (p *ProjectScopedSecretDuplicateProjectNameTestSuite) TestProjectScopedSecretDuplicateProjectNames() {
	p.ensureVaiEnabled()

	fixture := newProjectScopedSecretDuplicateProjectNameFixture()
	fixture.localCluster = "local"
	fixture.remoteCluster = p.cluster.ID
	p.setupProjectScopedSecretDuplicateProjectNameFixture(&fixture)

	if !p.Run("Project scoped secret direct cluster filter excludes duplicate project name from other cluster", func() {
		query := url.Values{
			"filter": []string{fmt.Sprintf("spec.clusterName=%s", fixture.localCluster)},
			"sort":   []string{"metadata.name"},
		}
		p.requireQueryIncludesAndExcludes(fixture, query, []string{fixture.localSecretName}, []string{fixture.remoteSecretName})
	}) {
		return
	}

	for _, tc := range projectScopedSecretDuplicateProjectNameTestCases {
		p.Run(tc.name, func() {
			logrus.Infof("Starting duplicate project-name case: %s", tc.name)
			logrus.Infof("Running with vai enabled: [%v]", p.vaiEnabled)

			query := tc.query(fixture)
			expectedNames := tc.expectedNames
			if tc.expectedNamesForFixture != nil {
				expectedNames = tc.expectedNamesForFixture(fixture)
			}
			p.waitForProjectScopedSecretDuplicateProjectNameNames(fixture, query, expectedNames)
		})
	}
}

func TestProjectScopedSecretDuplicateProjectNameTestSuite(t *testing.T) {
	suite.Run(t, new(ProjectScopedSecretDuplicateProjectNameTestSuite))
}
