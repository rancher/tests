//go:build (validation || infra.any || cluster.any || extended) && !stress && !2.8 && !2.9 && !2.10

package vai

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
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

			var retrievedSecrets []coreV1.Secret
			var continueToken string
			for {
				params := url.Values{}
				params.Set("limit", fmt.Sprintf("%d", tc.limit))
				if continueToken != "" {
					params.Set("continue", continueToken)
				}

				secretCollection, err := secretClient.List(params)
				require.NoError(v.T(), err)

				for _, obj := range secretCollection.Data {
					var secret coreV1.Secret
					err := steveV1.ConvertToK8sType(obj.JSONResp, &secret)
					require.NoError(v.T(), err)
					retrievedSecrets = append(retrievedSecrets, secret)
				}

				if secretCollection.Pagination == nil || secretCollection.Pagination.Next == "" {
					break
				}
				nextURL, err := url.Parse(secretCollection.Pagination.Next)
				require.NoError(v.T(), err)
				continueToken = nextURL.Query().Get("continue")
			}

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

	v.Run("PodFilters", func() {
		supportedWithVai := filterTestCases(podFilterTestCases, true)
		v.runPodFilterTestCases(supportedWithVai)
	})

	v.Run("SecretSorting", func() {
		supportedWithVai := filterTestCases(secretSortTestCases, true)
		v.runSecretSortTestCases(supportedWithVai)
	})

	v.Run("SecretLimit", func() {
		supportedWithVai := filterTestCases(secretLimitTestCases, true)
		v.runSecretLimitTestCases(supportedWithVai)
	})
}

func TestVaiTestSuite(t *testing.T) {
	suite.Run(t, new(VaiTestSuite))
}
