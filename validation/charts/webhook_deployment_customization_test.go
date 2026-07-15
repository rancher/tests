//go:build !sanity && !extended && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package charts

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	chartactions "github.com/rancher/tests/actions/charts"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/yaml"
)

const webhookCustomizationTimeout = 3 * time.Minute

var (
	deploymentGVR = appsv1.SchemeGroupVersion.WithResource("deployments")
	configMapGVR  = corev1.SchemeGroupVersion.WithResource("configmaps")
	pdbGVR        = policyv1.SchemeGroupVersion.WithResource("poddisruptionbudgets")
)

type WebhookDeploymentCustomizationTestSuite struct {
	suite.Suite
	client      *rancher.Client
	session     *session.Session
	clusterName string
}

func (w *WebhookDeploymentCustomizationTestSuite) TearDownSuite() {
	w.session.Cleanup()
}

func (w *WebhookDeploymentCustomizationTestSuite) SetupSuite() {
	testSession := session.NewSession()
	w.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(w.T(), err)

	w.client = client
	w.clusterName = client.RancherConfig.ClusterName
}

func (w *WebhookDeploymentCustomizationTestSuite) TestWebhookDeploymentCustomization() {
	tests := []struct {
		cluster string
	}{
		{localCluster},
		{w.clusterName},
	}

	for _, tt := range tests {
		clusterID, err := clusters.GetClusterIDByName(w.client, tt.cluster)
		require.NoError(w.T(), err)

		w.Run("Scale replicas on "+tt.cluster, func() {
			customization := map[string]any{
				"replicaCount": int64(2),
			}

			w.applyWebhookCustomization(clusterID, customization)
			w.requireDeployment(clusterID, func(deployment *unstructured.Unstructured) error {
				replicas, found, err := unstructured.NestedInt64(deployment.Object, "spec", "replicas")
				if err != nil {
					return err
				}
				if !found || replicas != 2 {
					return fmt.Errorf("expected replicas 2, got %d", replicas)
				}
				return nil
			})
			w.requireAppliedWebhookCustomization(clusterID, customization)
			w.requireDownstreamWebhookValues(clusterID, map[string]any{"replicaCount": int64(2)})
		})

		w.Run("Append tolerations on "+tt.cluster, func() {
			expectedToleration := map[string]any{
				"key":      "dedicated",
				"operator": "Equal",
				"value":    "rancher",
				"effect":   "NoSchedule",
			}
			customization := map[string]any{
				"appendTolerations": []any{expectedToleration},
			}

			w.applyWebhookCustomization(clusterID, customization)
			w.requireDeployment(clusterID, func(deployment *unstructured.Unstructured) error {
				tolerations, found, err := unstructured.NestedSlice(deployment.Object, "spec", "template", "spec", "tolerations")
				if err != nil {
					return err
				}
				if !found || !sliceContainsMap(tolerations, expectedToleration) {
					return fmt.Errorf("expected webhook toleration %v in %v", expectedToleration, tolerations)
				}
				return nil
			})
			w.requireAppliedWebhookCustomization(clusterID, customization)
			w.requireDownstreamWebhookValues(clusterID, map[string]any{"tolerations": []any{expectedToleration}})
		})

		w.Run("Override resource requirements on "+tt.cluster, func() {
			expectedResources := map[string]any{
				"requests": map[string]any{
					"cpu":    "100m",
					"memory": "128Mi",
				},
				"limits": map[string]any{
					"cpu":    "500m",
					"memory": "512Mi",
				},
			}
			customization := map[string]any{
				"overrideResourceRequirements": expectedResources,
			}

			w.applyWebhookCustomization(clusterID, customization)
			w.requireDeployment(clusterID, func(deployment *unstructured.Unstructured) error {
				containers, found, err := unstructured.NestedSlice(deployment.Object, "spec", "template", "spec", "containers")
				if err != nil {
					return err
				}
				if !found || len(containers) == 0 {
					return fmt.Errorf("webhook deployment has no containers")
				}
				container, ok := containers[0].(map[string]any)
				if !ok {
					return fmt.Errorf("unexpected container payload %T", containers[0])
				}
				resources, ok := container["resources"].(map[string]any)
				if !ok || !quantitiesEqual(resources, expectedResources) {
					return fmt.Errorf("expected resources %v, got %v", expectedResources, container["resources"])
				}
				return nil
			})
			w.requireAppliedWebhookCustomization(clusterID, customization)
			w.requireDownstreamWebhookValues(clusterID, map[string]any{"resources": expectedResources})
		})

		w.Run("Override affinity on "+tt.cluster, func() {
			expectedAffinity := map[string]any{
				"podAntiAffinity": map[string]any{
					"preferredDuringSchedulingIgnoredDuringExecution": []any{
						map[string]any{
							"weight": int64(100),
							"podAffinityTerm": map[string]any{
								"labelSelector": map[string]any{
									"matchExpressions": []any{
										map[string]any{
											"key":      "app",
											"operator": "In",
											"values":   []any{chartactions.RancherWebhookName},
										},
									},
								},
								"topologyKey": "kubernetes.io/hostname",
							},
						},
					},
				},
			}
			customization := map[string]any{
				"replicaCount":     int64(2),
				"overrideAffinity": expectedAffinity,
			}

			w.applyWebhookCustomization(clusterID, customization)
			w.requireDeployment(clusterID, func(deployment *unstructured.Unstructured) error {
				affinity, found, err := unstructured.NestedMap(deployment.Object, "spec", "template", "spec", "affinity")
				if err != nil {
					return err
				}
				if !found || !containsMap(affinity, expectedAffinity) {
					return fmt.Errorf("expected affinity %v, got %v", expectedAffinity, affinity)
				}
				return nil
			})
			w.requireAppliedWebhookCustomization(clusterID, customization)
			w.requireDownstreamWebhookValues(clusterID, map[string]any{"affinity": expectedAffinity})
		})

		w.Run("Create pod disruption budget with maxUnavailable on "+tt.cluster, func() {
			customization := map[string]any{
				"replicaCount": int64(2),
				"podDisruptionBudget": map[string]any{
					"maxUnavailable": "1",
				},
			}

			w.applyWebhookCustomization(clusterID, customization)
			// The rendered PDB is validated with minAvailable below. Stored Helm values
			// from older runs can retain minAvailable and mask maxUnavailable rendering.
			w.requireAppliedWebhookCustomization(clusterID, customization)
			w.requireDownstreamWebhookValues(clusterID, map[string]any{
				"podDisruptionBudget": map[string]any{
					"enabled":        true,
					"maxUnavailable": "1",
				},
			})
		})

		w.Run("Create pod disruption budget with minAvailable on "+tt.cluster, func() {
			customization := map[string]any{
				"replicaCount": int64(2),
				"podDisruptionBudget": map[string]any{
					"minAvailable": "1",
				},
			}

			w.applyWebhookCustomization(clusterID, customization)
			w.requirePodDisruptionBudget(clusterID, "minAvailable", "1")
			w.requireAppliedWebhookCustomization(clusterID, customization)
			w.requireDownstreamWebhookValues(clusterID, map[string]any{
				"podDisruptionBudget": map[string]any{
					"enabled":      true,
					"minAvailable": "1",
				},
			})
		})

		w.Run("Clear webhook deployment customization on "+tt.cluster, func() {
			w.clearWebhookCustomization(clusterID)
			w.requireDeployment(clusterID, func(deployment *unstructured.Unstructured) error {
				replicas, found, err := unstructured.NestedInt64(deployment.Object, "spec", "replicas")
				if err != nil {
					return err
				}
				if !found || replicas != 1 {
					return fmt.Errorf("expected replicas 1 after clearing customization, got %d", replicas)
				}
				return nil
			})
			w.requireNoPodDisruptionBudget(clusterID)
			w.requireAppliedWebhookCustomization(clusterID, nil)
			w.requireDownstreamWebhookValues(clusterID, map[string]any{
				"replicaCount": int64(1),
				"podDisruptionBudget": map[string]any{
					"enabled": false,
				},
			})
		})
	}
}

func (w *WebhookDeploymentCustomizationTestSuite) applyWebhookCustomization(clusterID string, customization map[string]any) {
	w.clearWebhookCustomization(clusterID)
	w.patchWebhookCustomization(clusterID, customization)
}

func (w *WebhookDeploymentCustomizationTestSuite) patchWebhookCustomization(clusterID string, customization map[string]any) {
	patch := map[string]any{
		"spec": map[string]any{
			"webhookDeploymentCustomization": customization,
		},
	}
	patchBytes, err := json.Marshal(patch)
	require.NoError(w.T(), err)

	_, err = w.client.WranglerContext.Mgmt.Cluster().Patch(clusterID, types.MergePatchType, patchBytes)
	require.NoError(w.T(), err)
}

func (w *WebhookDeploymentCustomizationTestSuite) clearWebhookCustomization(clusterID string) {
	patchBytes := []byte(`{"spec":{"webhookDeploymentCustomization":null}}`)

	_, err := w.client.WranglerContext.Mgmt.Cluster().Patch(clusterID, types.MergePatchType, patchBytes)
	require.NoError(w.T(), err)
}

func (w *WebhookDeploymentCustomizationTestSuite) requireDeployment(clusterID string, verify func(*unstructured.Unstructured) error) {
	dynamicClient, err := w.client.GetDownStreamClusterClient(clusterID)
	require.NoError(w.T(), err)

	err = wait.PollUntilContextTimeout(context.Background(), 5*time.Second, webhookCustomizationTimeout, true, func(ctx context.Context) (bool, error) {
		deployment, err := dynamicClient.Resource(deploymentGVR).Namespace(chartactions.RancherWebhookNamespace).Get(ctx, chartactions.RancherWebhookName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		return verify(deployment) == nil, nil
	})
	require.NoError(w.T(), err)
}

func (w *WebhookDeploymentCustomizationTestSuite) requirePodDisruptionBudget(clusterID, field, expected string) {
	dynamicClient, err := w.client.GetDownStreamClusterClient(clusterID)
	require.NoError(w.T(), err)

	lastObserved := "not found"
	err = wait.PollUntilContextTimeout(context.Background(), 5*time.Second, webhookCustomizationTimeout, true, func(ctx context.Context) (bool, error) {
		pdb, err := dynamicClient.Resource(pdbGVR).Namespace(chartactions.RancherWebhookNamespace).Get(ctx, chartactions.RancherWebhookName, metav1.GetOptions{})
		if err != nil {
			if lastObserved == "not found" || apierrors.IsNotFound(err) {
				lastObserved = err.Error()
			}
			return false, nil
		}
		spec, _, _ := unstructured.NestedMap(pdb.Object, "spec")
		lastObserved = fmt.Sprintf("%v", spec)
		value, found, err := unstructured.NestedFieldNoCopy(pdb.Object, "spec", field)
		if err != nil {
			return false, err
		}
		if !found {
			oppositeField := oppositePDBField(field)
			if oppositeField != "" {
				oppositeValue, oppositeFound, err := unstructured.NestedFieldNoCopy(pdb.Object, "spec", oppositeField)
				if err != nil {
					return false, err
				}
				if oppositeFound {
					return false, fmt.Errorf("expected PDB spec.%s=%q, but spec.%s is still set to %v; last observed spec: %s", field, expected, oppositeField, oppositeValue, lastObserved)
				}
			}
		}
		return found && intOrStringEqual(value, expected), nil
	})
	require.NoError(w.T(), err, "expected PDB spec.%s=%q, last observed spec: %s", field, expected, lastObserved)
}

func (w *WebhookDeploymentCustomizationTestSuite) requireNoPodDisruptionBudget(clusterID string) {
	dynamicClient, err := w.client.GetDownStreamClusterClient(clusterID)
	require.NoError(w.T(), err)

	err = wait.PollUntilContextTimeout(context.Background(), 5*time.Second, webhookCustomizationTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := dynamicClient.Resource(pdbGVR).Namespace(chartactions.RancherWebhookNamespace).Get(ctx, chartactions.RancherWebhookName, metav1.GetOptions{})
		return apierrors.IsNotFound(err), nil
	})
	require.NoError(w.T(), err)
}

func (w *WebhookDeploymentCustomizationTestSuite) requireAppliedWebhookCustomization(clusterID string, expected map[string]any) {
	err := wait.PollUntilContextTimeout(context.Background(), 5*time.Second, webhookCustomizationTimeout, true, func(ctx context.Context) (bool, error) {
		cluster, err := w.client.Steve.SteveType("management.cattle.io.cluster").ByID(clusterID)
		if err != nil {
			return false, nil
		}

		status, ok := cluster.Status.(map[string]any)
		if !ok {
			return false, nil
		}
		applied, _ := status["appliedWebhookDeploymentCustomization"].(map[string]any)
		if expected == nil {
			return len(applied) == 0, nil
		}
		return containsMap(applied, expected), nil
	})
	require.NoError(w.T(), err)
}

func (w *WebhookDeploymentCustomizationTestSuite) requireDownstreamWebhookValues(clusterID string, expectedValues map[string]any) {
	if clusterID == localCluster {
		return
	}

	dynamicClient, err := w.client.GetDownStreamClusterClient(clusterID)
	require.NoError(w.T(), err)

	err = wait.PollUntilContextTimeout(context.Background(), 5*time.Second, webhookCustomizationTimeout, true, func(ctx context.Context) (bool, error) {
		configMap, err := dynamicClient.Resource(configMapGVR).Namespace(chartactions.RancherWebhookNamespace).Get(ctx, "rancher-config", metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		rawValues, found, err := unstructured.NestedString(configMap.Object, "data", chartactions.RancherWebhookName)
		if err != nil || !found {
			return false, err
		}

		values := map[string]any{}
		if err = yaml.Unmarshal([]byte(rawValues), &values); err != nil {
			return false, err
		}
		return containsMap(values, expectedValues), nil
	})
	require.NoError(w.T(), err)
}

func sliceContainsMap(items []any, expected map[string]any) bool {
	for _, item := range items {
		actual, ok := item.(map[string]any)
		if ok && containsMap(actual, expected) {
			return true
		}
	}
	return false
}

func containsMap(actual, expected map[string]any) bool {
	if expected == nil {
		return true
	}
	if actual == nil {
		return false
	}
	for key, expectedValue := range expected {
		actualValue, ok := actual[key]
		if !ok {
			return false
		}
		if !containsValue(actualValue, expectedValue) {
			return false
		}
	}
	return true
}

func containsValue(actual, expected any) bool {
	switch expectedTyped := expected.(type) {
	case map[string]any:
		actualTyped, ok := actual.(map[string]any)
		return ok && containsMap(actualTyped, expectedTyped)
	case []any:
		actualTyped, ok := actual.([]any)
		if !ok || len(actualTyped) < len(expectedTyped) {
			return false
		}
		for i := range expectedTyped {
			if !containsValue(actualTyped[i], expectedTyped[i]) {
				return false
			}
		}
		return true
	case int64:
		return numericEqual(actual, expectedTyped)
	default:
		return reflect.DeepEqual(actual, expected)
	}
}

func numericEqual(actual any, expected int64) bool {
	switch actualTyped := actual.(type) {
	case int64:
		return actualTyped == expected
	case int:
		return int64(actualTyped) == expected
	case int32:
		return int64(actualTyped) == expected
	case float64:
		return int64(actualTyped) == expected
	default:
		return false
	}
}

func intOrStringEqual(actual any, expected string) bool {
	switch actualTyped := actual.(type) {
	case string:
		return actualTyped == expected
	case int64:
		return fmt.Sprintf("%d", actualTyped) == expected
	case int:
		return fmt.Sprintf("%d", actualTyped) == expected
	case int32:
		return fmt.Sprintf("%d", actualTyped) == expected
	case float64:
		return fmt.Sprintf("%.0f", actualTyped) == expected
	default:
		return false
	}
}

func oppositePDBField(field string) string {
	switch field {
	case "minAvailable":
		return "maxUnavailable"
	case "maxUnavailable":
		return "minAvailable"
	default:
		return ""
	}
}

func quantitiesEqual(actual, expected map[string]any) bool {
	for section, expectedSection := range expected {
		actualQuantities, ok := actual[section].(map[string]any)
		if !ok {
			return false
		}
		expectedQuantities, ok := expectedSection.(map[string]any)
		if !ok {
			return false
		}
		for name, expectedQuantity := range expectedQuantities {
			actualQuantity, ok := actualQuantities[name].(string)
			if !ok {
				return false
			}
			actualParsed := resource.MustParse(actualQuantity)
			expectedParsed := resource.MustParse(expectedQuantity.(string))
			if actualParsed.Cmp(expectedParsed) != 0 {
				return false
			}
		}
	}
	return true
}

func TestWebhookDeploymentCustomizationTestSuite(t *testing.T) {
	suite.Run(t, new(WebhookDeploymentCustomizationTestSuite))
}
