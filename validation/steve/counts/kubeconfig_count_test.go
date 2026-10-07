//go:build !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package counts

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	extclusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	extkubeconfigs "github.com/rancher/shepherd/extensions/kubeapi/kubeconfigs"
	"github.com/rancher/shepherd/pkg/session"
	kubeconfigactions "github.com/rancher/tests/actions/kubeapi/kubeconfigs"
	"github.com/rancher/tests/actions/rbac"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	countSchemaID      = "count"
	kubeconfigSchemaID = "ext.cattle.io.kubeconfig"
)

type countQuery struct {
	name   string
	values url.Values
}

var countQueries = []countQuery{
	{
		name: "plain count request",
	},
	{
		name: "dashboard count request",
		values: url.Values{
			"exclude": []string{"metadata.managedFields"},
		},
	},
}

type steveCountResponse struct {
	Data []struct {
		Counts map[string]struct {
			Summary *struct {
				Count int `json:"count"`
			} `json:"summary"`
		} `json:"counts"`
	} `json:"data"`
}

type resourceCountObservation struct {
	listNames []string
	counts    map[string]int
}

type resourceCountTestCase struct {
	name          string
	client        *rancher.Client
	exactNames    []string
	includedNames []string
	excludedNames []string
	minimumCount  int
}

type ownedKubeconfig struct {
	client *rancher.Client
	name   string
}

type KubeconfigCountTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (k *KubeconfigCountTestSuite) SetupSuite() {
	k.session = session.NewSession()

	client, err := rancher.NewClient("", k.session)
	require.NoError(k.T(), err)
	k.client = client

	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(k.T(), clusterName, "Cluster name should be set")

	clusterID, err := extclusters.GetClusterIDByName(client, clusterName)
	require.NoError(k.T(), err, "Error getting cluster ID")

	k.cluster, err = client.Management.Cluster.ByID(clusterID)
	require.NoError(k.T(), err, "Error getting cluster")
}

func (k *KubeconfigCountTestSuite) TearDownSuite() {
	k.session.Cleanup()
}

func (k *KubeconfigCountTestSuite) TestKubeconfigCountsRespectOwnership() {
	testSession := k.session.NewSession()
	defer testSession.Cleanup()

	adminClient, err := k.client.WithSession(testSession)
	require.NoError(k.T(), err)

	_, additionalAdminClient, err := rbac.SetupUser(adminClient, rbac.Admin.String())
	require.NoError(k.T(), err, "Failed to create an additional global admin")

	_, baseMemberClient, err := rbac.AddUserWithRoleToCluster(
		adminClient,
		rbac.BaseUser.String(),
		rbac.ClusterMember.String(),
		k.cluster,
		nil,
	)
	require.NoError(k.T(), err, "Failed to create a User-Base cluster member")

	_, standardMemberClient, err := rbac.AddUserWithRoleToCluster(
		adminClient,
		rbac.StandardUser.String(),
		rbac.ClusterMember.String(),
		k.cluster,
		nil,
	)
	require.NoError(k.T(), err, "Failed to create a Standard User cluster member")

	_, memberWithoutKubeconfigsClient, err := rbac.AddUserWithRoleToCluster(
		adminClient,
		rbac.BaseUser.String(),
		rbac.ClusterMember.String(),
		k.cluster,
		nil,
	)
	require.NoError(k.T(), err, "Failed to create a cluster member with no kubeconfigs")

	_, standardUserClient, err := rbac.SetupUser(adminClient, rbac.StandardUser.String())
	require.NoError(k.T(), err, "Failed to create a Standard User with no cluster access")

	_, baseUserClient, err := rbac.SetupUser(adminClient, rbac.BaseUser.String())
	require.NoError(k.T(), err, "Failed to create a User-Base user with no cluster access")

	createdKubeconfigs := []ownedKubeconfig{}
	defer func() {
		for index := len(createdKubeconfigs) - 1; index >= 0; index-- {
			fixture := createdKubeconfigs[index]
			err := fixture.client.WranglerContext.Ext.Kubeconfig().Delete(fixture.name, &metav1.DeleteOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				k.T().Errorf("Failed to clean up kubeconfig %q: %v", fixture.name, err)
			}
		}
	}()

	createKubeconfig := func(client *rancher.Client, clusterID string) string {
		created, err := kubeconfigactions.CreateKubeconfig(client, []string{clusterID}, "", nil)
		require.NoError(k.T(), err, "Failed to create kubeconfig as user %q", client.UserID)
		require.NotEmpty(k.T(), created.Name)

		createdKubeconfigs = append(createdKubeconfigs, ownedKubeconfig{
			client: client,
			name:   created.Name,
		})
		require.Equal(k.T(), client.UserID, created.Labels[kubeconfigactions.UserIDLabel])
		return created.Name
	}

	adminKubeconfigNames := []string{
		createKubeconfig(adminClient, "local"),
		createKubeconfig(additionalAdminClient, k.cluster.ID),
	}
	baseMemberKubeconfigNames := []string{
		createKubeconfig(baseMemberClient, k.cluster.ID),
		createKubeconfig(baseMemberClient, k.cluster.ID),
	}
	standardMemberKubeconfigNames := []string{
		createKubeconfig(standardMemberClient, k.cluster.ID),
	}

	allFixtureNames := combineNames(
		adminKubeconfigNames,
		baseMemberKubeconfigNames,
		standardMemberKubeconfigNames,
	)

	initialCases := []resourceCountTestCase{
		{
			name:          "configured admin sees every fixture",
			client:        adminClient,
			includedNames: allFixtureNames,
			minimumCount:  len(allFixtureNames),
		},
		{
			name:          "additional global admin sees every fixture",
			client:        additionalAdminClient,
			includedNames: allFixtureNames,
			minimumCount:  len(allFixtureNames),
		},
		{
			name:          "User-Base cluster member sees two owned kubeconfigs only",
			client:        baseMemberClient,
			exactNames:    baseMemberKubeconfigNames,
			excludedNames: namesExcept(allFixtureNames, baseMemberKubeconfigNames),
		},
		{
			name:          "Standard User cluster member sees one owned kubeconfig only",
			client:        standardMemberClient,
			exactNames:    standardMemberKubeconfigNames,
			excludedNames: namesExcept(allFixtureNames, standardMemberKubeconfigNames),
		},
		{
			name:          "cluster member without owned kubeconfigs sees zero",
			client:        memberWithoutKubeconfigsClient,
			exactNames:    []string{},
			excludedNames: allFixtureNames,
		},
		{
			name:          "Standard User with no cluster access sees zero",
			client:        standardUserClient,
			exactNames:    []string{},
			excludedNames: allFixtureNames,
		},
		{
			name:          "User-Base with no cluster access sees zero",
			client:        baseUserClient,
			exactNames:    []string{},
			excludedNames: allFixtureNames,
		},
	}

	k.runKubeconfigCountCases("after creation", initialCases)

	deletedName := baseMemberKubeconfigNames[0]
	err = extkubeconfigs.DeleteKubeconfig(baseMemberClient, deletedName, true)
	require.NoError(k.T(), err, "Failed to delete owned kubeconfig %q", deletedName)

	remainingBaseMemberNames := baseMemberKubeconfigNames[1:]
	remainingFixtureNames := namesExcept(allFixtureNames, []string{deletedName})
	afterDeletionCases := []resourceCountTestCase{
		{
			name:          "configured admin list and count reflect deletion",
			client:        adminClient,
			includedNames: remainingFixtureNames,
			excludedNames: []string{deletedName},
			minimumCount:  len(remainingFixtureNames),
		},
		{
			name:          "additional global admin list and count reflect deletion",
			client:        additionalAdminClient,
			includedNames: remainingFixtureNames,
			excludedNames: []string{deletedName},
			minimumCount:  len(remainingFixtureNames),
		},
		{
			name:          "member count decrements after deleting one of two",
			client:        baseMemberClient,
			exactNames:    remainingBaseMemberNames,
			excludedNames: append(namesExcept(remainingFixtureNames, remainingBaseMemberNames), deletedName),
		},
		{
			name:          "other member count remains isolated",
			client:        standardMemberClient,
			exactNames:    standardMemberKubeconfigNames,
			excludedNames: append(namesExcept(remainingFixtureNames, standardMemberKubeconfigNames), deletedName),
		},
		{
			name:          "member without kubeconfigs remains at zero",
			client:        memberWithoutKubeconfigsClient,
			exactNames:    []string{},
			excludedNames: append(remainingFixtureNames, deletedName),
		},
		{
			name:          "Standard User remains at zero",
			client:        standardUserClient,
			exactNames:    []string{},
			excludedNames: allFixtureNames,
		},
		{
			name:          "User-Base remains at zero",
			client:        baseUserClient,
			exactNames:    []string{},
			excludedNames: allFixtureNames,
		},
	}

	k.runKubeconfigCountCases("after deletion", afterDeletionCases)
}

func (k *KubeconfigCountTestSuite) runKubeconfigCountCases(phase string, testCases []resourceCountTestCase) {
	var convergedObservations []resourceCountObservation
	converged := assert.EventuallyWithT(k.T(), func(collect *assert.CollectT) {
		observations := make([]resourceCountObservation, len(testCases))
		for index, testCase := range testCases {
			observation, err := observeResourceCount(testCase.client, kubeconfigSchemaID, "kubeconfigs")
			if !assert.NoError(collect, err, "%s: %s", phase, testCase.name) {
				continue
			}
			observations[index] = observation
			assertResourceCountObservation(collect, phase, "kubeconfig", testCase, observation)
		}
		convergedObservations = observations
	}, defaults.OneMinuteTimeout, defaults.FiveSecondTimeout, "%s kubeconfig counts did not converge", phase)
	if !converged {
		return
	}

	for index, testCase := range testCases {
		testCase := testCase
		observation := convergedObservations[index]
		k.Run(fmt.Sprintf("%s/%s", phase, testCase.name), func() {
			assertResourceCountObservation(k.T(), phase, "kubeconfig", testCase, observation)
		})
	}
}

func observeResourceCount(client *rancher.Client, schemaID, resourceName string) (resourceCountObservation, error) {
	listNames, err := listAllSteveResourceNames(client, schemaID)
	if err != nil {
		return resourceCountObservation{}, fmt.Errorf("failed to list %s as user %q: %w", resourceName, client.UserID, err)
	}

	observation := resourceCountObservation{
		listNames: listNames,
		counts:    make(map[string]int, len(countQueries)),
	}

	for _, query := range countQueries {
		count, err := getSteveResourceCount(client, schemaID, query.values)
		if err != nil {
			return resourceCountObservation{}, fmt.Errorf("%s as user %q failed: %w", query.name, client.UserID, err)
		}
		observation.counts[query.name] = count
	}

	return observation, nil
}

func listAllSteveResourceNames(client *rancher.Client, schemaID string) ([]string, error) {
	collection, err := client.Steve.SteveType(schemaID).List(nil)
	if err != nil {
		return nil, err
	}

	names := collection.Names()
	seenNextURLs := map[string]struct{}{}
	for collection.Pagination != nil && collection.Pagination.Next != "" {
		nextURL := collection.Pagination.Next
		if _, seen := seenNextURLs[nextURL]; seen {
			return nil, fmt.Errorf("Steve %q pagination repeated next URL %q", schemaID, nextURL)
		}
		seenNextURLs[nextURL] = struct{}{}

		nextCollection := &steveV1.SteveCollection{}
		if err := client.Steve.Ops.DoNext(nextURL, nextCollection); err != nil {
			return nil, fmt.Errorf("failed to get next Steve %q page: %w", schemaID, err)
		}
		names = append(names, nextCollection.Names()...)
		collection = nextCollection
	}

	sort.Strings(names)
	return names, nil
}

func getSteveResourceCount(client *rancher.Client, schemaID string, values url.Values) (int, error) {
	countEndpoint, err := client.Steve.Ops.GetCollectionURL(countSchemaID, http.MethodGet)
	if err != nil {
		return 0, fmt.Errorf("failed to discover Steve count endpoint: %w", err)
	}

	parsedEndpoint, err := url.Parse(countEndpoint)
	if err != nil {
		return 0, fmt.Errorf("failed to parse Steve count endpoint %q: %w", countEndpoint, err)
	}

	query := parsedEndpoint.Query()
	for key, queryValues := range values {
		for _, value := range queryValues {
			query.Add(key, value)
		}
	}
	parsedEndpoint.RawQuery = query.Encode()

	response := steveCountResponse{}
	if err := client.Steve.Ops.DoGet(parsedEndpoint.String(), nil, &response); err != nil {
		return 0, fmt.Errorf("failed to get Steve counts: %w", err)
	}
	if len(response.Data) != 1 {
		return 0, fmt.Errorf("expected one Steve count object, got %d", len(response.Data))
	}

	resourceCount, found := response.Data[0].Counts[schemaID]
	if !found {
		return 0, fmt.Errorf("Steve count response has no %q entry", schemaID)
	}
	if resourceCount.Summary == nil {
		return 0, fmt.Errorf("Steve count response has no summary for %q", schemaID)
	}

	return resourceCount.Summary.Count, nil
}

func assertResourceCountObservation(t assert.TestingT, phase, resourceName string, testCase resourceCountTestCase, observation resourceCountObservation) {
	if testCase.exactNames != nil {
		assert.Equal(
			t,
			sortedNames(testCase.exactNames),
			observation.listNames,
			"%s: %s: Steve list returned unexpected %ss",
			phase,
			testCase.name,
			resourceName,
		)
	}

	for _, expectedName := range testCase.includedNames {
		assert.Contains(t, observation.listNames, expectedName, "%s: %s: expected owned/admin-visible %s", phase, testCase.name, resourceName)
	}
	for _, unexpectedName := range testCase.excludedNames {
		assert.NotContains(t, observation.listNames, unexpectedName, "%s: %s: unexpected %s was visible", phase, testCase.name, resourceName)
	}
	if testCase.minimumCount > 0 {
		assert.GreaterOrEqual(t, len(observation.listNames), testCase.minimumCount, "%s: %s: Steve list omitted %s fixtures", phase, testCase.name, resourceName)
	}

	for _, query := range countQueries {
		count, found := observation.counts[query.name]
		if !assert.True(t, found, "%s: %s: missing result for %s", phase, testCase.name, query.name) {
			continue
		}
		assert.Equal(
			t,
			len(observation.listNames),
			count,
			"%s: %s: %s must match the caller-visible Steve list",
			phase,
			testCase.name,
			query.name,
		)
		if testCase.exactNames != nil {
			assert.Equal(t, len(testCase.exactNames), count, "%s: %s: %s returned an unexpected exact count", phase, testCase.name, query.name)
		}
		if testCase.minimumCount > 0 {
			assert.GreaterOrEqual(t, count, testCase.minimumCount, "%s: %s: %s omitted fixtures", phase, testCase.name, query.name)
		}
	}
}

func combineNames(groups ...[]string) []string {
	combined := []string{}
	for _, group := range groups {
		combined = append(combined, group...)
	}
	return sortedNames(combined)
}

func namesExcept(allNames, excludedNames []string) []string {
	excluded := make(map[string]struct{}, len(excludedNames))
	for _, name := range excludedNames {
		excluded[name] = struct{}{}
	}

	result := []string{}
	for _, name := range allNames {
		if _, found := excluded[name]; !found {
			result = append(result, name)
		}
	}
	return sortedNames(result)
}

func sortedNames(names []string) []string {
	result := append([]string(nil), names...)
	sort.Strings(result)
	return result
}

func TestKubeconfigCountTestSuite(t *testing.T) {
	suite.Run(t, new(KubeconfigCountTestSuite))
}
