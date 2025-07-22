//go:build (validation || infra.any || cluster.any || extended) && !stress && !2.8 && !2.9 && !2.10 && !2.11

package vai

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/interoperability/vai/database"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	coreV1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

type VaiTestSuite struct {
	suite.Suite
	client       *rancher.Client
	steveClient  *steveV1.Client
	session      *session.Session
	cluster      *management.Cluster
	vaiEnabled   bool
	testData     *TestData
	dbCollection *database.SnapshotCollection
	dbExtractor  *database.Extractor
	dbQuery      *database.Query
}

type TestData struct {
	SecretName    string
	NamespaceName string
}

func (v *VaiTestSuite) SetupSuite() {
	testSession := session.NewSession()
	v.session = testSession

	client, err := rancher.NewClient("", v.session)
	require.NoError(v.T(), err)

	v.client = client
	v.steveClient = client.Steve

	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(v.T(), clusterName, "Cluster name to install should be set")

	clusterID, err := clusters.GetClusterIDByName(v.client, clusterName)
	require.NoError(v.T(), err, "Error getting cluster ID")

	v.cluster, err = v.client.Management.Cluster.ByID(clusterID)
	require.NoError(v.T(), err)

	enabled, err := isVaiEnabled(v.client)
	require.NoError(v.T(), err)
	v.vaiEnabled = enabled
	v.dbExtractor = database.NewExtractor(v.client)
	v.dbQuery = database.NewQuery()
}

func (v *VaiTestSuite) TearDownSuite() {
	if v.dbCollection != nil {
		v.dbCollection.Cleanup()
	}

	v.session.Cleanup()
}

func (v *VaiTestSuite) ensureVaiEnabled() {
	if !v.vaiEnabled {
		err := ensureVAIState(v.client, true)
		require.NoError(v.T(), err)
		v.vaiEnabled = true
	}
}

func (v *VaiTestSuite) ensureVaiDisabled() {
	if v.vaiEnabled {
		err := ensureVAIState(v.client, false)
		require.NoError(v.T(), err)
		v.vaiEnabled = false
	}
}

func (v *VaiTestSuite) setupTestResources() {
	v.T().Log("Setting up test resources...")

	v.testData = &TestData{
		SecretName:    fmt.Sprintf("db-secret-%s", namegen.RandStringLower(randomStringLength)),
		NamespaceName: fmt.Sprintf("db-namespace-%s", namegen.RandStringLower(randomStringLength)),
	}

	v.T().Logf("Creating namespace: %s", v.testData.NamespaceName)
	namespace := &coreV1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: v.testData.NamespaceName},
	}
	namespaceClient := v.steveClient.SteveType("namespace")
	_, err := namespaceClient.Create(namespace)
	require.NoError(v.T(), err)

	v.T().Logf("Creating secret: %s", v.testData.SecretName)
	secret := &coreV1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      v.testData.SecretName,
			Namespace: "default",
		},
		Type: coreV1.SecretTypeOpaque,
		Data: map[string][]byte{
			"test": []byte("data"),
		},
	}
	secretClient := v.steveClient.SteveType("secret")
	_, err = secretClient.Create(secret)
	require.NoError(v.T(), err)

	v.T().Log("Accessing metrics endpoint to ensure tables are created")
	metricsClient := v.client.Steve.SteveType("metrics.k8s.io.nodemetrics")
	_, err = metricsClient.List(nil)
	if err != nil {
		v.T().Logf("Warning: Failed to access metrics endpoint: %v", err)
	}

	v.T().Log("Listing resources to hydrate VAI cache...")
	_, err = v.client.Steve.SteveType("namespace").List(nil)
	require.NoError(v.T(), err)
	_, err = v.client.Steve.SteveType("secret").List(nil)
	require.NoError(v.T(), err)
	v.T().Log("Waiting 15s to ensure resources are cached...")
	_ = kwait.PollImmediate(time.Second, 15*time.Second, func() (bool, error) {
		return false, nil
	})

	v.T().Log("Test resources setup completed")
}

func (v *VaiTestSuite) extractAllVAIDatabases() {
	collection, err := v.dbExtractor.ExtractAll()
	require.NoError(v.T(), err)
	v.dbCollection = collection
}

func (v *VaiTestSuite) checkDBFilesExist() {
	v.T().Log("Checking if VAI database files exist in pods...")
	for podName, snapshot := range v.dbCollection.Snapshots {
		v.T().Run(fmt.Sprintf("Pod %s", podName), func(t *testing.T) {
			count, err := v.dbQuery.ExecuteCount(snapshot,
				"SELECT COUNT(*) FROM sqlite_master WHERE type='table'")
			require.NoError(t, err, "Should be able to query database")
			require.True(t, count > 0, "Database should contain tables")

			t.Logf("Database file for pod %s is valid with %d tables", podName, count)
		})
	}
}

func (v *VaiTestSuite) checkSecretInVAIDatabase() {
	v.T().Log("Checking if secret exists in VAI databases...")
	require.NotNil(v.T(), v.testData, "Test data should be set up")

	secretFoundCount := 0
	for podName, snapshot := range v.dbCollection.Snapshots {
		v.T().Run(fmt.Sprintf("Pod %s", podName), func(t *testing.T) {
			tableCount, err := v.dbQuery.ExecuteCount(snapshot,
				`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name = ?`,
				"_v1_Secret_fields")
			if err != nil {
				t.Logf("Error checking table: %v", err)
				return
			}
			if tableCount == 0 {
				t.Log("Secret table does not exist")
				return
			}

			count, err := v.dbQuery.ExecuteCount(snapshot,
				`SELECT COUNT(*) FROM "_v1_Secret_fields" WHERE "metadata.name" = ?`,
				v.testData.SecretName)

			if err != nil {
				t.Logf("Error querying secret: %v", err)
				return
			}

			if count > 0 {
				t.Logf("Secret %s found in pod %s", v.testData.SecretName, podName)
				secretFoundCount++
			} else {
				t.Logf("Secret %s not found in pod %s", v.testData.SecretName, podName)
			}
		})
	}

	v.T().Logf("Secret found in %d out of %d pods", secretFoundCount, len(v.dbCollection.Snapshots))
	assert.Greater(v.T(), secretFoundCount, 0, "Secret should be found in at least one pod")
}

func (v *VaiTestSuite) checkNamespaceInAllVAIDatabases() {
	v.T().Log("Checking if namespace exists in all VAI databases...")
	require.NotNil(v.T(), v.testData, "Test data should be set up")

	namespaceFoundCount := 0
	totalPods := len(v.dbCollection.Snapshots)

	for podName, snapshot := range v.dbCollection.Snapshots {
		v.T().Run(fmt.Sprintf("Pod %s", podName), func(t *testing.T) {
			tableCount, err := v.dbQuery.ExecuteCount(snapshot,
				`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name = ?`,
				"_v1_Namespace_fields")
			if err != nil || tableCount == 0 {
				t.Logf("Namespace table does not exist or error: %v", err)
				return
			}

			count, err := v.dbQuery.ExecuteCount(snapshot,
				`SELECT COUNT(*) FROM "_v1_Namespace_fields" WHERE "metadata.name" = ?`,
				v.testData.NamespaceName)

			if err != nil {
				t.Logf("Error querying namespace: %v", err)
				return
			}

			if count > 0 {
				t.Logf("Namespace %s found in pod %s", v.testData.NamespaceName, podName)
				namespaceFoundCount++
			} else {
				t.Logf("Namespace %s not found in pod %s", v.testData.NamespaceName, podName)
			}
		})
	}

	v.T().Logf("Namespace found in %d out of %d pods", namespaceFoundCount, totalPods)
	assert.Equal(v.T(), totalPods, namespaceFoundCount,
		"Namespace should be found in all pods' databases")
}

func (v *VaiTestSuite) checkMetricTablesInVAIDatabase() {
	v.T().Log("Checking if metric tables exist in VAI databases...")

	expectedTables := []string{
		"metrics.k8s.io_v1beta1_NodeMetrics",
		"metrics.k8s.io_v1beta1_NodeMetrics_fields",
		"metrics.k8s.io_v1beta1_NodeMetrics_indices",
	}

	podsWithAllTables := 0

	for podName, snapshot := range v.dbCollection.Snapshots {
		v.T().Run(fmt.Sprintf("Pod %s", podName), func(t *testing.T) {
			result, err := v.dbQuery.Execute(snapshot,
				`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
			require.NoError(t, err)

			tableMap := make(map[string]bool)
			for _, row := range result.Rows {
				if name, ok := row["name"].(string); ok {
					tableMap[name] = true
				}
			}

			foundCount := 0
			for _, expectedTable := range expectedTables {
				if tableMap[expectedTable] {
					foundCount++
					t.Logf("Found table %s", expectedTable)
				}
			}

			if foundCount == 0 {
				t.Log("No metric tables found (this is acceptable)")
			} else if foundCount == len(expectedTables) {
				t.Logf("All %d metric tables present", foundCount)
				podsWithAllTables++
			} else {
				t.Errorf("Found only %d/%d metric tables", foundCount, len(expectedTables))
			}
		})
	}

	v.T().Logf("Summary: %d pods have all metric tables", podsWithAllTables)
	require.Greater(v.T(), podsWithAllTables, 0,
		"At least one pod must have all metric tables")
}

func (v *VaiTestSuite) runSecretFilterTestCases(testCases []secretFilterTestCase) {
	secretClient := v.steveClient.SteveType("secret")
	namespaceClient := v.steveClient.SteveType("namespace")

	for _, tc := range testCases {
		v.Run(tc.name, func() {
			logrus.Infof("Starting case: %s", tc.name)
			logrus.Infof("Running with vai enabled: [%v]", v.vaiEnabled)

			secrets, expectedNames, allNamespaces, expectedNamespaces := tc.createSecrets()

			for _, ns := range allNamespaces {
				logrus.Infof("Creating namespace: %s", ns)
				_, err := namespaceClient.Create(&coreV1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: ns,
					},
				})
				require.NoError(v.T(), err)
			}

			for _, ns := range allNamespaces {
				err := waitForNamespaceActive(namespaceClient, ns)
				require.NoError(v.T(), err, "Namespace %s did not become active", ns)
			}

			resourceIDs := make([]string, 0, len(secrets))
			createdSecrets := make([]steveV1.SteveAPIObject, len(secrets))
			for i, secret := range secrets {
				created, err := secretClient.Create(&secret)
				require.NoError(v.T(), err)
				createdSecrets[i] = *created
				resourceIDs = append(resourceIDs, fmt.Sprintf("%s:%s", secret.Namespace, secret.Name))
			}

			err := waitForResourcesCreated(secretClient, resourceIDs)
			require.NoError(v.T(), err, "Not all secrets were created successfully")

			filterValues := tc.filter(expectedNamespaces)

			secretCollection, err := secretClient.List(filterValues)
			require.NoError(v.T(), err)

			var actualNames []string
			for _, item := range secretCollection.Data {
				actualNames = append(actualNames, item.GetName())
			}

			require.Equal(v.T(), len(expectedNames), len(actualNames), "Number of returned secrets doesn't match expected")
			for _, expectedName := range expectedNames {
				require.Contains(v.T(), actualNames, expectedName, fmt.Sprintf("Expected secret %s not found in actual secrets", expectedName))
			}
		})
	}
}

func (v *VaiTestSuite) runPodFilterTestCases(testCases []podFilterTestCase) {
	podClient := v.steveClient.SteveType("pod")
	namespaceClient := v.steveClient.SteveType("namespace")

	for _, tc := range testCases {
		v.Run(tc.name, func() {
			logrus.Infof("Starting case: %s", tc.name)
			logrus.Infof("Running with vai enabled: [%v]", v.vaiEnabled)

			pods, expectedNames, allNamespaces, expectedNamespaces := tc.createPods()

			for _, ns := range allNamespaces {
				logrus.Infof("Creating namespace: %s", ns)
				_, err := namespaceClient.Create(&coreV1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: ns,
					},
				})
				require.NoError(v.T(), err)
			}

			for _, ns := range allNamespaces {
				err := waitForNamespaceActive(namespaceClient, ns)
				require.NoError(v.T(), err, "Namespace %s did not become active", ns)
			}

			resourceIDs := make([]string, 0, len(pods))
			createdPods := make([]steveV1.SteveAPIObject, len(pods))
			for i, pod := range pods {
				created, err := podClient.Create(&pod)
				require.NoError(v.T(), err)
				createdPods[i] = *created
				resourceIDs = append(resourceIDs, fmt.Sprintf("%s:%s", pod.Namespace, pod.Name))
			}

			err := waitForResourcesCreated(podClient, resourceIDs)
			require.NoError(v.T(), err, "Not all pods were created successfully")

			filterValues := tc.filter(expectedNamespaces)

			podCollection, err := podClient.List(filterValues)
			require.NoError(v.T(), err)

			var actualNames []string
			for _, item := range podCollection.Data {
				actualNames = append(actualNames, item.GetName())
			}

			if tc.expectFound {
				require.Equal(v.T(), len(expectedNames), len(actualNames), "Number of returned pods doesn't match expected")
				for _, expectedName := range expectedNames {
					require.Contains(v.T(), actualNames, expectedName, fmt.Sprintf("Expected pod %s not found in actual pods", expectedName))
				}
			} else {
				require.Empty(v.T(), actualNames, "Expected no pods to be found, but some were returned")
			}
		})
	}
}

func (v *VaiTestSuite) runSecretSortTestCases(testCases []secretSortTestCase) {
	secretClient := v.steveClient.SteveType("secret")
	namespaceClient := v.steveClient.SteveType("namespace")

	for _, tc := range testCases {
		v.Run(tc.name, func() {
			logrus.Infof("Starting case: %s", tc.name)
			logrus.Infof("Running with vai enabled: [%v]", v.vaiEnabled)

			secrets, sortedNames, namespaces := tc.createSecrets(tc.sort)

			for _, ns := range namespaces {
				_, err := namespaceClient.Create(&coreV1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: ns,
					},
				})
				require.NoError(v.T(), err)
			}

			for _, ns := range namespaces {
				err := waitForNamespaceActive(namespaceClient, ns)
				require.NoError(v.T(), err, "Namespace %s did not become active", ns)
			}

			resourceIDs := make([]string, 0, len(secrets))

			for _, secret := range secrets {
				_, err := secretClient.Create(&secret)
				require.NoError(v.T(), err)
				resourceIDs = append(resourceIDs, fmt.Sprintf("%s:%s", secret.Namespace, secret.Name))
			}

			err := waitForResourcesCreated(secretClient, resourceIDs)
			require.NoError(v.T(), err, "Not all secrets were created successfully")

			sortValues := tc.sort()
			sortValues.Add("projectsornamespaces", strings.Join(namespaces, ","))

			secretCollection, err := waitForResourceCount(secretClient, sortValues, len(secrets))
			require.NoError(v.T(), err, "Failed to retrieve all secrets")

			var actualNames []string
			for _, item := range secretCollection.Data {
				actualNames = append(actualNames, item.GetName())
			}

			require.Equal(v.T(), len(sortedNames), len(actualNames), "Number of returned secrets doesn't match expected")
			for i, expectedName := range sortedNames {
				require.Equal(v.T(), expectedName, actualNames[i], fmt.Sprintf("Secret at position %d doesn't match expected order", i))
			}
		})
	}
}

func (v *VaiTestSuite) runSecretLimitTestCases(testCases []secretLimitTestCase) {
	for _, tc := range testCases {
		v.Run(tc.name, func() {
			logrus.Infof("Starting case: %s", tc.name)
			logrus.Infof("Running with vai enabled: [%v]", v.vaiEnabled)

			secrets, ns := tc.createSecrets()

			namespaceClient := v.steveClient.SteveType("namespace")
			_, err := namespaceClient.Create(&coreV1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: ns,
				},
			})
			require.NoError(v.T(), err)

			err = waitForNamespaceActive(namespaceClient, ns)
			require.NoError(v.T(), err, "Namespace %s did not become active", ns)

			secretClient := v.steveClient.SteveType("secret").NamespacedSteveClient(ns)

			resourceIDs := make([]string, 0, len(secrets))
			for _, secret := range secrets {
				_, err := secretClient.Create(&secret)
				require.NoError(v.T(), err)
				resourceIDs = append(resourceIDs, secret.Name)
			}

			err = waitForResourcesCreated(secretClient, resourceIDs)
			require.NoError(v.T(), err, "Not all secrets were created successfully")

			logrus.Infof("Test expects to create %d secrets with limit=%d", len(secrets), tc.limit)

			var retrievedSecrets []coreV1.Secret
			var continueToken string
			requestCount := 0
			paginationWorked := true

			for {
				requestCount++
				params := url.Values{}
				params.Set("limit", fmt.Sprintf("%d", tc.limit))
				if continueToken != "" {
					params.Set("continue", continueToken)
				}

				logrus.Infof("Request %d: limit=%d, continue=%s", requestCount, tc.limit, continueToken)

				secretCollection, err := secretClient.List(params)
				require.NoError(v.T(), err)

				logrus.Infof("Response %d: returned %d items", requestCount, len(secretCollection.Data))

				if secretCollection.Pagination != nil {
					logrus.Infof("  Pagination exists: Next=%s", secretCollection.Pagination.Next)
				} else {
					logrus.Info("  Pagination is nil")
				}

				remainingItems := tc.expectedTotal - len(retrievedSecrets)
				expectedInThisPage := tc.limit
				if remainingItems < tc.limit {
					expectedInThisPage = remainingItems
				}

				if len(secretCollection.Data) != expectedInThisPage {
					logrus.Errorf("PAGINATION BROKEN: Expected %d items in this page but got %d (remaining: %d, limit: %d)",
						expectedInThisPage, len(secretCollection.Data), remainingItems, tc.limit)
					paginationWorked = false
				}

				if remainingItems > tc.limit && len(secretCollection.Data) > tc.limit {
					logrus.Errorf("PAGINATION BROKEN: Received %d items but limit was %d (remaining: %d)",
						len(secretCollection.Data), tc.limit, remainingItems)
					paginationWorked = false
				}

				if requestCount == 1 && len(secretCollection.Data) == tc.expectedTotal && tc.expectedTotal > tc.limit {
					logrus.Errorf("PAGINATION BROKEN: Got all %d items in first request despite limit=%d",
						tc.expectedTotal, tc.limit)
					paginationWorked = false
				}

				for _, obj := range secretCollection.Data {
					var secret coreV1.Secret
					err := steveV1.ConvertToK8sType(obj.JSONResp, &secret)
					require.NoError(v.T(), err)
					retrievedSecrets = append(retrievedSecrets, secret)
				}

				if secretCollection.Pagination == nil || secretCollection.Pagination.Next == "" {
					logrus.Info("No more pages, ending pagination")

					if requestCount == 1 && tc.expectedTotal > tc.limit {
						logrus.Errorf("PAGINATION BROKEN: Only made 1 request for %d items with limit %d",
							tc.expectedTotal, tc.limit)
						paginationWorked = false
					}
					break
				}

				nextURL, err := url.Parse(secretCollection.Pagination.Next)
				require.NoError(v.T(), err)
				oldToken := continueToken
				continueToken = nextURL.Query().Get("continue")
				logrus.Infof("Continue token changed from '%s' to '%s'", oldToken, continueToken)
			}

			logrus.Infof("Pagination complete: made %d requests, retrieved %d total secrets",
				requestCount, len(retrievedSecrets))

			require.Equal(v.T(), tc.expectedTotal, len(retrievedSecrets), "Number of retrieved secrets doesn't match expected")

			expectedSecrets := make(map[string]bool)
			for _, secret := range secrets {
				expectedSecrets[secret.Name] = false
			}

			for _, secret := range retrievedSecrets {
				_, ok := expectedSecrets[secret.Name]
				require.True(v.T(), ok, "Unexpected secret: %s", secret.Name)
				expectedSecrets[secret.Name] = true
			}

			for name, found := range expectedSecrets {
				require.True(v.T(), found, "Expected secret not found: %s", name)
			}

			expectedRequests := (tc.expectedTotal + tc.limit - 1) / tc.limit
			if tc.expectedTotal <= tc.limit {
				expectedRequests = 1
			}

			if paginationWorked {
				require.Equal(v.T(), expectedRequests, requestCount,
					"Pagination request count mismatch: expected %d requests for %d items with limit %d, but made %d requests",
					expectedRequests, tc.expectedTotal, tc.limit, requestCount)

				logrus.Infof("✓ Pagination worked correctly: Made %d requests for %d items with limit %d",
					requestCount, tc.expectedTotal, tc.limit)
			} else {
				require.Fail(v.T(), "Pagination using limit/continue is not working - limit parameter is being ignored")
			}
		})
	}
}

func (v *VaiTestSuite) checkVaiDescription() {
	const expectedVAIDescription = "Improve performance by enabling SQLite-backed caching. This also enables server-side pagination and other scaling based performance improvements."

	feature, err := v.steveClient.SteveType("management.cattle.io.feature").ByID("ui-sql-cache")
	require.NoError(v.T(), err)

	status := feature.Status.(map[string]interface{})
	description := status["description"].(string)

	assert.Equal(v.T(), expectedVAIDescription, description,
		"VAI description should not have changed")
}

func (v *VaiTestSuite) runTimestampTestCases(testCases []timestampTestCase) {
	for _, tc := range testCases {
		v.Run(tc.name, func() {
			logrus.Infof("Starting timestamp test: %s", tc.name)
			logrus.Infof("Running with VAI enabled: [%v]", v.vaiEnabled)

			resource, ns, name := tc.createResource()

			if ns != "" && tc.namespaced {
				namespaceClient := v.steveClient.SteveType("namespace")
				_, err := namespaceClient.Create(&coreV1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: ns,
					},
				})
				if err != nil && !strings.Contains(err.Error(), "already exists") {
					require.NoError(v.T(), err)
				}

				err = waitForNamespaceActive(namespaceClient, ns)
				require.NoError(v.T(), err, "Namespace %s did not become active", ns)
			}

			var steveClient interface{}
			if tc.namespaced && ns != "" {
				steveClient = v.steveClient.SteveType(tc.resourceType).NamespacedSteveClient(ns)
			} else {
				steveClient = v.steveClient.SteveType(tc.resourceType)
			}

			switch client := steveClient.(type) {
			case *steveV1.SteveClient:
				_, err := client.Create(resource)
				require.NoError(v.T(), err, "Failed to create %s", tc.resourceType)
			case *steveV1.NamespacedSteveClient:
				_, err := client.Create(resource)
				require.NoError(v.T(), err, "Failed to create %s", tc.resourceType)
			default:
				require.Fail(v.T(), "Unexpected client type: %T", client)
			}

			err := waitForResourcesCreated(steveClient, []string{name})
			require.NoError(v.T(), err, "Resource %s/%s not created", tc.resourceType, name)

			logrus.Info("Waiting 5 seconds for initial age...")
			_ = kwait.PollImmediate(time.Second, 5*time.Second, func() (bool, error) {
				return false, nil
			})

			ages := make([]string, 0, tc.checkCount)
			ageDurations := make([]time.Duration, 0, tc.checkCount)
			ageIndices := make([]int, 0, tc.checkCount)

			for i := 0; i < tc.checkCount; i++ {
				if i > 0 {
					logrus.Infof("Waiting %v before check %d...", tc.waitBetweenChecks, i+1)
					_ = kwait.PollImmediate(time.Second, tc.waitBetweenChecks, func() (bool, error) {
						return false, nil
					})

				}

				var collection *steveV1.SteveCollection
				var err error

				switch client := steveClient.(type) {
				case *steveV1.SteveClient:
					collection, err = client.List(nil)
				case *steveV1.NamespacedSteveClient:
					collection, err = client.List(nil)
				}
				require.NoError(v.T(), err)

				foundInList := false
				for _, item := range collection.Data {
					if item.Name == name {
						foundInList = true

						metadata, ok := item.JSONResp["metadata"].(map[string]interface{})
						require.True(v.T(), ok, "metadata not found in list response")

						fields, ok := metadata["fields"].([]interface{})
						require.True(v.T(), ok, "metadata.fields not found in list response")
						require.Greater(v.T(), len(fields), 0, "metadata.fields is empty")

						if i == 0 {
							logrus.Infof("Fields for %s: %v", tc.resourceType, fields)
						}

						ageIndex, ageStr, err := findAgeFieldIndex(fields, name)
						require.NoError(v.T(), err, "Failed to find age field")

						age, err := parseAge(ageStr)
						require.NoError(v.T(), err, "Failed to parse age: %s", ageStr)

						ages = append(ages, ageStr)
						ageDurations = append(ageDurations, age)
						ageIndices = append(ageIndices, ageIndex)

						logrus.Infof("Check %d - %s Age: %s (at index %d)",
							i+1, tc.resourceType, ageStr, ageIndex)
						break
					}
				}
				require.True(v.T(), foundInList, "Resource not found in list response")
			}

			for i := 1; i < len(ageIndices); i++ {
				require.Equal(v.T(), ageIndices[0], ageIndices[i],
					"Age field index should be consistent: check 1 had index %d, check %d had index %d",
					ageIndices[0], i+1, ageIndices[i])
			}

			logrus.Info("Verifying Age values are increasing...")
			for i := 1; i < len(ageDurations); i++ {
				require.Greater(v.T(), ageDurations[i], ageDurations[i-1],
					"Age should increase: check %d (%v) should be > check %d (%v)",
					i+1, ages[i], i, ages[i-1])
			}

			if tc.checkCount > 1 {
				actualIncrease := ageDurations[len(ageDurations)-1] - ageDurations[0]
				logrus.Infof("Total age increase: %v over %d checks", actualIncrease, tc.checkCount)
			}

			logrus.Infof("✓ %s timestamp test passed: Age field correctly updates over time", tc.resourceType)
		})
	}
}

func parseAge(age string) (time.Duration, error) {
	age = strings.TrimSpace(age)

	if age == "0s" || age == "0" {
		return 0, nil
	}

	if strings.HasSuffix(age, "s") || strings.HasSuffix(age, "m") || strings.HasSuffix(age, "h") {
		return time.ParseDuration(age)
	}

	if strings.HasSuffix(age, "d") {
		days := strings.TrimSuffix(age, "d")
		var d int
		fmt.Sscanf(days, "%d", &d)
		return time.Duration(d) * 24 * time.Hour, nil
	}

	if num, err := strconv.Atoi(age); err == nil {
		return time.Duration(num) * time.Second, nil
	}

	return 0, fmt.Errorf("unable to parse age: %s", age)
}

func isLikelyAgeField(s string) bool {
	if s == "" || s == "<none>" || s == "none" {
		return false
	}

	if strings.Contains(s, "=") ||
		strings.Contains(s, "/") ||
		strings.Contains(s, ":") ||
		strings.Contains(s, ".") && !strings.HasSuffix(s, "s") {
		return false
	}

	_, err := parseAge(s)
	return err == nil
}

func findAgeFieldIndex(fields []interface{}, resourceName string) (int, string, error) {
	candidates := []struct {
		index int
		value string
	}{}

	for i := len(fields) - 1; i >= 0; i-- {
		if fieldStr, ok := fields[i].(string); ok && isLikelyAgeField(fieldStr) {
			candidates = append(candidates, struct {
				index int
				value string
			}{i, fieldStr})
		}
	}

	if len(candidates) == 1 {
		return candidates[0].index, candidates[0].value, nil
	}

	for _, candidate := range candidates {
		if candidate.index > 0 &&
			(strings.HasSuffix(candidate.value, "s") ||
				strings.HasSuffix(candidate.value, "m") ||
				strings.HasSuffix(candidate.value, "h") ||
				strings.HasSuffix(candidate.value, "d")) {
			return candidate.index, candidate.value, nil
		}
	}

	for _, candidate := range candidates {
		if candidate.index > 0 && candidate.value != resourceName {
			return candidate.index, candidate.value, nil
		}
	}

	return -1, "", fmt.Errorf("could not find age field in: %v", fields)
}

func (v *VaiTestSuite) runSecretPageSizeTestCases(testCases []secretPageSizeTestCase) {
	for _, tc := range testCases {
		v.Run(tc.name, func() {
			logrus.Infof("Starting case: %s", tc.name)
			logrus.Infof("Running with vai enabled: [%v]", v.vaiEnabled)

			secrets, _ := createPageSizeTestSecrets(tc.numSecrets)

			_, ns, err := projects.CreateProjectAndNamespaceUsingWrangler(v.client, v.cluster.ID)
			require.NoError(v.T(), err)

			for i := range secrets {
				secrets[i].Namespace = ns.Name
			}

			secretClient := v.steveClient.SteveType("secret").NamespacedSteveClient(ns.Name)

			resourceIDs := make([]string, 0, len(secrets))
			for _, secret := range secrets {
				_, err := secretClient.Create(&secret)
				require.NoError(v.T(), err)
				resourceIDs = append(resourceIDs, secret.Name)
			}

			err = waitForResourcesCreated(secretClient, resourceIDs)
			require.NoError(v.T(), err, "Not all secrets were created successfully")

			var retrievedSecrets []coreV1.Secret
			var currentPage = 1

			for {
				params := url.Values{}
				params.Set("pagesize", fmt.Sprintf("%d", tc.pageSize))
				params.Set("page", fmt.Sprintf("%d", currentPage))

				secretCollection, err := secretClient.List(params)
				require.NoError(v.T(), err)

				if currentPage == 1 && secretCollection.Pagination != nil {
					logrus.Infof("First page retrieved %d items", len(secretCollection.Data))
				}

				require.LessOrEqual(v.T(), len(secretCollection.Data), tc.pageSize,
					"Page %d has more items than expected pageSize", currentPage)

				for _, obj := range secretCollection.Data {
					var secret coreV1.Secret
					err := steveV1.ConvertToK8sType(obj.JSONResp, &secret)
					require.NoError(v.T(), err)
					retrievedSecrets = append(retrievedSecrets, secret)
				}

				if len(secretCollection.Data) == 0 ||
					(len(secretCollection.Data) < tc.pageSize && currentPage > 1) {
					break
				}

				if len(retrievedSecrets) >= tc.expectedTotal {
					break
				}

				currentPage++
			}

			logrus.Infof("✓ Retrieved %d secrets across %d pages (expected %d pages with pagesize %d)",
				len(retrievedSecrets), currentPage, tc.expectedPages, tc.pageSize)

			require.Equal(v.T(), tc.expectedTotal, len(retrievedSecrets), "Number of retrieved secrets doesn't match expected")

			expectedSecrets := make(map[string]int)
			for _, secret := range secrets {
				expectedSecrets[secret.Name] = 0
			}

			for _, secret := range retrievedSecrets {
				count, ok := expectedSecrets[secret.Name]
				require.True(v.T(), ok, "Unexpected secret: %s", secret.Name)
				expectedSecrets[secret.Name] = count + 1
			}

			for name, count := range expectedSecrets {
				require.Equal(v.T(), 1, count, "Secret %s was found %d times, expected exactly 1", name, count)
			}

			if tc.expectedPages > 1 {
				params := url.Values{}
				params.Set("pagesize", fmt.Sprintf("%d", tc.pageSize))
				params.Set("page", fmt.Sprintf("%d", tc.expectedPages))

				secretCollection, err := secretClient.List(params)
				require.NoError(v.T(), err)

				expectedLastPageSize := tc.expectedTotal % tc.pageSize
				if expectedLastPageSize == 0 {
					expectedLastPageSize = tc.pageSize
				}
				require.Equal(v.T(), expectedLastPageSize, len(secretCollection.Data),
					"Last page doesn't have expected number of items")
			}

			params := url.Values{}
			params.Set("pagesize", fmt.Sprintf("%d", tc.pageSize))
			params.Set("page", fmt.Sprintf("%d", tc.expectedPages+1))

			secretCollection, err := secretClient.List(params)
			require.NoError(v.T(), err)
			require.Equal(v.T(), 0, len(secretCollection.Data), "Out of bounds page should return empty list")
		})
	}
}

func (v *VaiTestSuite) TestVaiDisabled() {
	v.ensureVaiDisabled()

	v.Run("SecretFilters", func() {
		unsupportedWithVai := filterTestCases(secretFilterTestCases, false)
		v.runSecretFilterTestCases(unsupportedWithVai)
	})

	v.Run("PodFilters", func() {
		unsupportedWithVai := filterTestCases(podFilterTestCases, false)
		v.runPodFilterTestCases(unsupportedWithVai)
	})

	v.Run("SecretSorting", func() {
		unsupportedWithVai := filterTestCases(secretSortTestCases, false)
		v.runSecretSortTestCases(unsupportedWithVai)
	})

	v.Run("SecretLimit", func() {
		unsupportedWithVai := filterTestCases(secretLimitTestCases, false)
		v.runSecretLimitTestCases(unsupportedWithVai)
	})

	v.Run("NormalOperations", func() {
		pods, err := v.client.Steve.SteveType("pod").List(nil)
		require.NoError(v.T(), err)
		require.NotEmpty(v.T(), pods.Data, "Should be able to list pods even with VAI disabled")
	})
}

func (v *VaiTestSuite) TestVaiEnabled() {
	v.ensureVaiEnabled()

	v.Run("SetupTestResources", v.setupTestResources)
	v.Run("ExtractAllDatabases", v.extractAllVAIDatabases)

	v.Run("DatabaseTests", func() {
		v.Run("CheckDBFilesExist", v.checkDBFilesExist)
		v.Run("CheckSecretInDB", v.checkSecretInVAIDatabase)
		v.Run("CheckNamespaceInAllVAIDatabases", v.checkNamespaceInAllVAIDatabases)
		v.Run("CheckMetricTablesInVAIDatabase", v.checkMetricTablesInVAIDatabase)
	})

	v.Run("SecretFilters", func() {
		supportedWithVai := filterTestCases(secretFilterTestCases, true)
		v.runSecretFilterTestCases(supportedWithVai)
	})

	v.Run("VaiOnlySecretFilters", func() {
		supportedWithVai := filterTestCases(vaiOnlySecretFilterCases, true)
		v.runSecretFilterTestCases(supportedWithVai)
	})

	v.Run("PodFilters", func() {
		supportedWithVai := filterTestCases(podFilterTestCases, true)
		v.runPodFilterTestCases(supportedWithVai)
	})

	v.Run("SecretSorting", func() {
		supportedWithVai := filterTestCases(secretSortTestCases, true)
		v.runSecretSortTestCases(supportedWithVai)
	})

	v.Run("VaiOnlySecretSorting", func() {
		supportedWithVai := filterTestCases(vaiOnlySecretSortCases, true)
		v.runSecretSortTestCases(supportedWithVai)
	})

	v.Run("CheckVaiDescription", v.checkVaiDescription)

	v.Run("TimestampCacheTests", func() {
		supportedWithVai := filterTestCases(timestampTestCases, v.vaiEnabled)
		v.runTimestampTestCases(supportedWithVai)
	})

	v.Run("SecretPageSize", func() {
		supportedWithVai := filterTestCases(secretPageSizeTestCases, true)
		v.runSecretPageSizeTestCases(supportedWithVai)
	})

}

func TestVaiTestSuite(t *testing.T) {
	suite.Run(t, new(VaiTestSuite))
}
