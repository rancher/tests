//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package nodeannotations

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rancher/rancher/tests/v2/actions/rbac"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/util/wait"
)

type NodeAnnotationsTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (na *NodeAnnotationsTestSuite) SetupSuite() {
	log.Info("Setting up test suite")
	na.session = session.NewSession()
	client, err := rancher.NewClient("", na.session)
	require.NoError(na.T(), err)
	na.client = client

	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(na.T(), clusterName, "Cluster name to install should be set")

	clusterID, err := clusters.GetClusterIDByName(na.client, clusterName)
	require.NoError(na.T(), err)
	na.cluster, err = na.client.Management.Cluster.ByID(clusterID)
	require.NoError(na.T(), err)
	log.Info("Test suite setup completed")
}

func (na *NodeAnnotationsTestSuite) TearDownSuite() {
	log.Info("Cleaning up test suite")
	na.session.Cleanup()
}

func (na *NodeAnnotationsTestSuite) getTestNode() (string, error) {
	steveNodes, err := na.client.Steve.SteveType("node").List(nil)
	if err != nil {
		return "", fmt.Errorf("failed to list nodes via Steve: %v", err)
	}

	if len(steveNodes.Data) == 0 {
		return "", fmt.Errorf("no nodes found")
	}

	nodeName := steveNodes.Data[0].Name
	if nodeName == "" {
		nodeName = steveNodes.Data[0].ID
	}
	log.Infof("✅ Selected test node: %s", nodeName)

	return nodeName, nil
}

func (na *NodeAnnotationsTestSuite) updateNodeAnnotationsV1Complete(client *rancher.Client, nodeName string, annotations map[string]string) error {
	log.Infof("🔧 Updating annotations via v1 API with complete payload for node %s", nodeName)

	nodeObject, err := client.Steve.SteveType("node").ByID(nodeName)
	if err != nil {
		return fmt.Errorf("failed to get node object for '%s': %v", nodeName, err)
	}

	metadata, ok := nodeObject.JSONResp["metadata"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("failed to get metadata from node object")
	}

	metadata["annotations"] = annotations
	log.Infof("📊 Total annotations to set: %d", len(annotations))

	completePayload := map[string]interface{}{
		"id":         nodeObject.JSONResp["id"],
		"type":       nodeObject.JSONResp["type"],
		"apiVersion": nodeObject.JSONResp["apiVersion"],
		"kind":       nodeObject.JSONResp["kind"],
		"metadata":   metadata,
		"spec":       nodeObject.JSONResp["spec"],
	}

	log.Info("📤 Sending complete payload to v1/Steve API...")

	_, err = client.Steve.SteveType("node").Update(nodeObject, completePayload)
	if err != nil {
		log.Errorf("❌ V1 API update failed: %v", err)
		return err
	}

	log.Info("✅ V1 API update succeeded!")
	return nil
}

func (na *NodeAnnotationsTestSuite) verifyAnnotationState(nodeName string, key string, expectedValue *string) error {
	return wait.PollImmediate(1*time.Second, 30*time.Second, func() (bool, error) {
		nodeObject, err := na.client.Steve.SteveType("node").ByID(nodeName)
		if err != nil {
			log.Errorf("Error getting node %s: %v", nodeName, err)
			return false, err
		}

		annotations := make(map[string]string)
		if metadata, ok := nodeObject.JSONResp["metadata"].(map[string]interface{}); ok {
			if annos, ok := metadata["annotations"].(map[string]interface{}); ok {
				for k, v := range annos {
					if strVal, ok := v.(string); ok {
						annotations[k] = strVal
					}
				}
			}
		}

		actualValue, exists := annotations[key]
		if expectedValue == nil {
			if !exists {
				log.Infof("Successfully verified annotation %s is deleted", key)
				return true, nil
			}
			log.Infof("Waiting for annotation %s to be deleted (current value: %s)", key, actualValue)
			return false, nil
		}

		if exists {
			if actualValue == *expectedValue {
				log.Infof("Successfully verified annotation %s=%s", key, actualValue)
				return true, nil
			}
			log.Infof("Waiting for annotation %s to be updated (current: %s, expected: %s)",
				key, actualValue, *expectedValue)
		} else {
			log.Infof("Waiting for annotation %s to be created with value %s", key, *expectedValue)
		}
		return false, nil
	})
}

func (na *NodeAnnotationsTestSuite) TestRBACAnnotations() {
	subSession := na.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		globalRole string
		role       string
		canModify  bool
	}{
		{rbac.StandardUser.String(), rbac.ClusterOwner.String(), true},
		{rbac.StandardUser.String(), rbac.ClusterMember.String(), false},
	}

	nodeName, err := na.getTestNode()
	require.NoError(na.T(), err)

	for _, tt := range tests {
		tt := tt
		na.Run(fmt.Sprintf("Testing annotations with role %s", tt.role), func() {
			_, standardUserClient, err := rbac.AddUserWithRoleToCluster(
				na.client,
				tt.globalRole,
				tt.role,
				na.cluster,
				nil,
			)
			require.NoError(na.T(), err)

			steveClient, err := standardUserClient.Steve.ProxyDownstream(na.cluster.ID)
			require.NoError(na.T(), err)

			annotationKey := namegen.AppendRandomString("test-rbac-")
			annotationValue := namegen.AppendRandomString("test-value-")

			var updateErr error
			retryCount := 3

			for i := 0; i < retryCount; i++ {
				nodeObject, err := steveClient.SteveType("node").ByID(nodeName)
				if err != nil {
					updateErr = err
					break
				}

				annotations := make(map[string]string)
				if metadata, ok := nodeObject.JSONResp["metadata"].(map[string]interface{}); ok {
					if annos, ok := metadata["annotations"].(map[string]interface{}); ok {
						for k, v := range annos {
							if strVal, ok := v.(string); ok {
								annotations[k] = strVal
							}
						}
					}
				}

				annotations[annotationKey] = annotationValue

				metadata, _ := nodeObject.JSONResp["metadata"].(map[string]interface{})
				metadata["annotations"] = annotations

				completePayload := map[string]interface{}{
					"id":         nodeObject.JSONResp["id"],
					"type":       nodeObject.JSONResp["type"],
					"apiVersion": nodeObject.JSONResp["apiVersion"],
					"kind":       nodeObject.JSONResp["kind"],
					"metadata":   metadata,
					"spec":       nodeObject.JSONResp["spec"],
				}

				_, updateErr = steveClient.SteveType("node").Update(nodeObject, completePayload)

				if updateErr == nil || !strings.Contains(updateErr.Error(), "409") {
					break
				}

				if i < retryCount-1 {
					log.Infof("Got 409 conflict, retrying... (attempt %d/%d)", i+2, retryCount)
					_ = wait.PollImmediate(500*time.Millisecond, 500*time.Millisecond, func() (bool, error) {
						return false, nil
					})
				}
			}

			if tt.canModify {
				require.NoError(na.T(), updateErr, "update should succeed for %s", tt.role)

				err = na.verifyAnnotationStateProxied(steveClient, nodeName, annotationKey, &annotationValue)
				require.NoError(na.T(), err)
			} else {
				require.Error(na.T(), updateErr)
				errorLower := strings.ToLower(updateErr.Error())
				isValidError := strings.Contains(updateErr.Error(), "403") ||
					strings.Contains(errorLower, "forbidden") ||
					strings.Contains(errorLower, "not updatable") ||
					strings.Contains(errorLower, "cannot update") ||
					strings.Contains(errorLower, "permission denied")

				assert.True(na.T(), isValidError,
					"Expected permission denied error, got: %v", updateErr)
			}
		})
	}
}

func (na *NodeAnnotationsTestSuite) verifyAnnotationStateProxied(steveClient *v1.Client, nodeName string, key string, expectedValue *string) error {
	return wait.PollImmediate(1*time.Second, 30*time.Second, func() (bool, error) {
		nodeObject, err := steveClient.SteveType("node").ByID(nodeName)
		if err != nil {
			log.Errorf("Error getting node %s: %v", nodeName, err)
			return false, err
		}

		annotations := make(map[string]string)
		if metadata, ok := nodeObject.JSONResp["metadata"].(map[string]interface{}); ok {
			if annos, ok := metadata["annotations"].(map[string]interface{}); ok {
				for k, v := range annos {
					if strVal, ok := v.(string); ok {
						annotations[k] = strVal
					}
				}
			}
		}

		actualValue, exists := annotations[key]
		if expectedValue == nil {
			if !exists {
				log.Infof("Successfully verified annotation %s is deleted", key)
				return true, nil
			}
			log.Infof("Waiting for annotation %s to be deleted (current value: %s)", key, actualValue)
			return false, nil
		}

		if exists {
			if actualValue == *expectedValue {
				log.Infof("Successfully verified annotation %s=%s", key, actualValue)
				return true, nil
			}
			log.Infof("Waiting for annotation %s to be updated (current: %s, expected: %s)",
				key, actualValue, *expectedValue)
		} else {
			log.Infof("Waiting for annotation %s to be created with value %s", key, *expectedValue)
		}
		return false, nil
	})
}

func (na *NodeAnnotationsTestSuite) TestNodeAnnotationsWithTableTests() {
	subSession := na.session.NewSession()
	defer subSession.Cleanup()

	nodeName, err := na.getTestNode()
	require.NoError(na.T(), err)
	log.Infof("Starting table tests on node: %s", nodeName)

	updateKey1 := namegen.AppendRandomString("test-multi-update-1")
	updateKey2 := namegen.AppendRandomString("test-multi-update-2")
	mixedKey1 := namegen.AppendRandomString("test-mixed-1")
	mixedKey2 := namegen.AppendRandomString("test-mixed-2")
	cycleKey := namegen.AppendRandomString("test-cycle")
	multiEditKey := namegen.AppendRandomString("test-multi-edit")

	createSetupFunc := func(annotations map[string]string) func() {
		return func() {
			log.Info("Setting up initial annotations")

			nodeObject, err := na.client.Steve.SteveType("node").ByID(nodeName)
			require.NoError(na.T(), err)

			initialAnnotations := make(map[string]string)
			if metadata, ok := nodeObject.JSONResp["metadata"].(map[string]interface{}); ok {
				if annos, ok := metadata["annotations"].(map[string]interface{}); ok {
					for k, v := range annos {
						if strVal, ok := v.(string); ok {
							initialAnnotations[k] = strVal
						}
					}
				}
			}

			for k, v := range annotations {
				log.Infof("Setting up initial annotation %s=%s", k, v)
				initialAnnotations[k] = v
			}

			err = na.updateNodeAnnotationsV1Complete(na.client, nodeName, initialAnnotations)
			require.NoError(na.T(), err)

			for k, v := range annotations {
				err = na.verifyAnnotationState(nodeName, k, &v)
				require.NoError(na.T(), err)
			}
		}
	}

	tests := []struct {
		name          string
		annotations   map[string]string
		operation     string
		expectedError bool
		setup         func()
	}{
		{
			name: "Single Simple Annotation",
			annotations: map[string]string{
				namegen.AppendRandomString("test-single"): "simple-value",
			},
			operation: "add",
		},
		{
			name: "Multiple Annotations",
			annotations: map[string]string{
				namegen.AppendRandomString("test-multi-1"): "value1",
				namegen.AppendRandomString("test-multi-2"): "value2",
				namegen.AppendRandomString("test-multi-3"): "value3",
			},
			operation: "add",
		},
		{
			name: "Update Existing Annotation",
			annotations: map[string]string{
				updateKey1: "updated-value",
			},
			operation: "update",
			setup: createSetupFunc(map[string]string{
				updateKey1: "initial-value",
			}),
		},
		{
			name: "Delete Existing Annotation",
			annotations: map[string]string{
				updateKey2: "to-be-deleted",
			},
			operation: "delete",
			setup: createSetupFunc(map[string]string{
				updateKey2: "to-be-deleted",
			}),
		},
		{
			name: "Long Annotation Value",
			annotations: map[string]string{
				namegen.AppendRandomString("test-long"): strings.Repeat("very-long-value", 50),
			},
			operation: "add",
		},
		{
			name: "Special Characters",
			annotations: map[string]string{
				namegen.AppendRandomString("test-special"): "value with spaces!@#$%^&*()",
			},
			operation: "add",
		},
		{
			name: "Bulk Operation",
			annotations: func() map[string]string {
				bulk := make(map[string]string)
				bulkPrefix := namegen.AppendRandomString("test-bulk")
				for i := 0; i < 50; i++ {
					key := fmt.Sprintf("%s-%d", bulkPrefix, i)
					bulk[key] = fmt.Sprintf("bulk-value-%d", i)
				}
				return bulk
			}(),
			operation: "add",
		},
		{
			name: "Update Multiple Simultaneously",
			annotations: map[string]string{
				mixedKey1: "update-value-1",
				mixedKey2: "update-value-2",
			},
			operation: "update",
			setup: createSetupFunc(map[string]string{
				mixedKey1: "initial-value-1",
				mixedKey2: "initial-value-2",
			}),
		},
		{
			name: "Unicode Test 1",
			annotations: map[string]string{
				namegen.AppendRandomString("test-unicode-1"): "うしぼう",
			},
			operation:     "add",
			expectedError: false,
		},
		{
			name: "Unicode Test 2",
			annotations: map[string]string{
				namegen.AppendRandomString("test-unicode-2"): "🐮🤠🚜🌾",
			},
			operation:     "add",
			expectedError: false,
		},
		{
			name: "Add-Delete-Add Cycle",
			annotations: map[string]string{
				cycleKey: "final-value",
			},
			operation: "add",
			setup: func() {
				log.Infof("Starting Add-Delete-Add cycle for key: %s", cycleKey)
				initialValue := "initial-value"

				log.Infof("Adding initial value: %s", initialValue)
				initialAdd := createSetupFunc(map[string]string{
					cycleKey: initialValue,
				})
				initialAdd()

				err := na.verifyAnnotationState(nodeName, cycleKey, &initialValue)
				require.NoError(na.T(), err)

				log.Info("Deleting annotation")
				nodeObject, err := na.client.Steve.SteveType("node").ByID(nodeName)
				require.NoError(na.T(), err)

				deleteAnnotations := make(map[string]string)
				if metadata, ok := nodeObject.JSONResp["metadata"].(map[string]interface{}); ok {
					if annos, ok := metadata["annotations"].(map[string]interface{}); ok {
						for k, v := range annos {
							if k != cycleKey {
								if strVal, ok := v.(string); ok {
									deleteAnnotations[k] = strVal
								}
							}
						}
					}
				}

				err = na.updateNodeAnnotationsV1Complete(na.client, nodeName, deleteAnnotations)
				require.NoError(na.T(), err)

				err = na.verifyAnnotationState(nodeName, cycleKey, nil)
				require.NoError(na.T(), err)
			},
		},
		{
			name: "Multiple Edit Cycles",
			annotations: map[string]string{
				multiEditKey: "final-value",
			},
			operation: "update",
			setup: func() {
				log.Infof("Starting multiple edit cycles for key: %s", multiEditKey)
				values := []string{"first-value", "second-value", "third-value"}

				for i, value := range values {
					log.Infof("Edit cycle %d: setting value to %s", i+1, value)
					editStep := createSetupFunc(map[string]string{
						multiEditKey: value,
					})
					editStep()
					err := na.verifyAnnotationState(nodeName, multiEditKey, &value)
					require.NoError(na.T(), err)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		na.Run(tt.name, func() {
			log.Infof("Starting test: %s", tt.name)
			if tt.setup != nil {
				tt.setup()
			}

			nodeObject, err := na.client.Steve.SteveType("node").ByID(nodeName)
			require.NoError(na.T(), err)

			updatedAnnotations := make(map[string]string)
			if metadata, ok := nodeObject.JSONResp["metadata"].(map[string]interface{}); ok {
				if annos, ok := metadata["annotations"].(map[string]interface{}); ok {
					for k, v := range annos {
						if strVal, ok := v.(string); ok {
							updatedAnnotations[k] = strVal
						}
					}
				}
			}

			switch tt.operation {
			case "add", "update":
				for k, v := range tt.annotations {
					log.Infof("Setting annotation %s=%s", k, v)
					updatedAnnotations[k] = v
				}
			case "delete":
				for k := range tt.annotations {
					log.Infof("Deleting annotation %s", k)
					delete(updatedAnnotations, k)
				}
			}

			err = na.updateNodeAnnotationsV1Complete(na.client, nodeName, updatedAnnotations)

			if tt.expectedError {
				require.Error(na.T(), err)
			} else {
				require.NoError(na.T(), err)

				if tt.operation != "delete" {
					for k, v := range tt.annotations {
						err = na.verifyAnnotationState(nodeName, k, &v)
						require.NoError(na.T(), err)
					}
				} else {
					for k := range tt.annotations {
						err = na.verifyAnnotationState(nodeName, k, nil)
						require.NoError(na.T(), err)
					}
				}
			}
		})
	}
}

func TestNodeAnnotationsTestSuite(t *testing.T) {
	suite.Run(t, new(NodeAnnotationsTestSuite))
}
