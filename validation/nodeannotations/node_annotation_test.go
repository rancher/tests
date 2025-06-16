//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package nodeannotations

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rancher/norman/types"
	"github.com/rancher/rancher/tests/v2/actions/rbac"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	log "github.com/sirupsen/logrus"
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

func (na *NodeAnnotationsTestSuite) getTestNode() (string, *management.Node, error) {
	nodeClient := na.client.Management.Node
	nodes, err := nodeClient.List(&types.ListOpts{})
	if err != nil {
		return "", nil, err
	}
	if len(nodes.Data) == 0 {
		return "", nil, fmt.Errorf("no nodes found in cluster")
	}
	nodeID := nodes.Data[0].ID
	log.Infof("Selected test node: %s", nodeID)
	currentNode, err := nodeClient.ByID(nodeID)
	return nodeID, currentNode, err
}

func (na *NodeAnnotationsTestSuite) verifyAnnotationState(nodeID string, key string, expectedValue *string) error {
	return wait.PollImmediate(1*time.Second, 30*time.Second, func() (bool, error) {
		node, err := na.client.Management.Node.ByID(nodeID)
		if err != nil {
			log.Errorf("Error getting node %s: %v", nodeID, err)
			return false, err
		}

		actualValue, exists := node.Annotations[key]
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

	nodeID, currentNode, err := na.getTestNode()
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

			nodeClient := standardUserClient.Management.Node
			var nodeList *management.NodeCollection
			err = wait.PollImmediate(1*time.Second, 30*time.Second, func() (bool, error) {
				nodeList, err = nodeClient.List(nil)
				if err != nil {
					return false, nil
				}
				return true, nil
			})
			require.NoError(na.T(), err, "Failed to list nodes")
			require.NotNil(na.T(), nodeList, "Node list should not be nil")

			annotationKey := namegen.AppendRandomString("test-rbac")
			annotationValue := namegen.AppendRandomString("test-value")

			annotations := make(map[string]string)
			if currentNode.Annotations != nil {
				for k, v := range currentNode.Annotations {
					annotations[k] = v
				}
			}

			annotations[annotationKey] = annotationValue
			updatePayload := map[string]interface{}{
				"annotations": annotations,
			}

			var updateErr error
			err = wait.PollImmediate(500*time.Millisecond, 10*time.Second, func() (bool, error) {
				_, updateErr = nodeClient.Update(currentNode, updatePayload)
				if updateErr == nil {
					return true, nil
				}
				if !strings.Contains(updateErr.Error(), "401") {
					return true, nil
				}
				log.Debugf("Got 401 error, retrying node update...")
				return false, nil
			})

			if err != nil && updateErr != nil {
				log.Debugf("Polling timed out with error: %v, last update error: %v", err, updateErr)
			}

			if tt.canModify {
				require.NoError(na.T(), err, "polling should not timeout")
				require.NoError(na.T(), updateErr, "update should succeed")
				err = na.verifyAnnotationState(nodeID, annotationKey, &annotationValue)
				require.NoError(na.T(), err)

				delete(annotations, annotationKey)
				_, err = na.client.Management.Node.Update(currentNode, map[string]interface{}{
					"annotations": annotations,
				})
				require.NoError(na.T(), err)
				err = na.verifyAnnotationState(nodeID, annotationKey, nil)
				require.NoError(na.T(), err)
			} else {
				require.Error(na.T(), updateErr)
				require.True(na.T(),
					strings.Contains(updateErr.Error(), "403") ||
						strings.Contains(strings.ToLower(updateErr.Error()), "forbidden"),
					"Expected forbidden error, got: %v", updateErr)
			}
		})
	}
}

func (na *NodeAnnotationsTestSuite) TestNodeAnnotationsWithTableTests() {
	subSession := na.session.NewSession()
	defer subSession.Cleanup()

	nodeID, currentNode, err := na.getTestNode()
	require.NoError(na.T(), err)
	log.Infof("Starting table tests on node: %s", nodeID)

	updateKey1 := namegen.AppendRandomString("test-multi-update-1")
	updateKey2 := namegen.AppendRandomString("test-multi-update-2")
	mixedKey1 := namegen.AppendRandomString("test-mixed-1")
	mixedKey2 := namegen.AppendRandomString("test-mixed-2")
	cycleKey := namegen.AppendRandomString("test-cycle")
	multiEditKey := namegen.AppendRandomString("test-multi-edit")

	createSetupFunc := func(annotations map[string]string) func() {
		return func() {
			log.Info("Setting up initial annotations")
			initialAnnotations := make(map[string]string)
			for k, v := range currentNode.Annotations {
				initialAnnotations[k] = v
			}
			for k, v := range annotations {
				log.Infof("Setting up initial annotation %s=%s", k, v)
				initialAnnotations[k] = v
			}
			_, err := na.client.Management.Node.Update(currentNode, map[string]interface{}{
				"annotations": initialAnnotations,
			})
			require.NoError(na.T(), err)

			for k, v := range annotations {
				err = na.verifyAnnotationState(nodeID, k, &v)
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

				err := na.verifyAnnotationState(nodeID, cycleKey, &initialValue)
				require.NoError(na.T(), err)

				log.Info("Deleting annotation")
				deleteStep := createSetupFunc(map[string]string{})
				deleteStep()

				err = na.verifyAnnotationState(nodeID, cycleKey, nil)
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
					err := na.verifyAnnotationState(nodeID, multiEditKey, &value)
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

			freshNode, err := na.client.Management.Node.ByID(nodeID)
			require.NoError(na.T(), err)

			updatedAnnotations := make(map[string]string)
			if freshNode.Annotations != nil {
				for k, v := range freshNode.Annotations {
					updatedAnnotations[k] = v
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

			_, err = na.client.Management.Node.Update(freshNode, map[string]interface{}{
				"annotations": updatedAnnotations,
			})

			if tt.expectedError {
				require.Error(na.T(), err)
			} else {
				require.NoError(na.T(), err)

				if tt.operation != "delete" {
					for k, v := range tt.annotations {
						err = na.verifyAnnotationState(nodeID, k, &v)
						require.NoError(na.T(), err)
					}
				} else {
					for k := range tt.annotations {
						err = na.verifyAnnotationState(nodeID, k, nil)
						require.NoError(na.T(), err)
					}
				}
			}
		})
	}

	log.Info("Starting cleanup of test annotations")
	finalNode, err := na.client.Management.Node.ByID(nodeID)
	require.NoError(na.T(), err)

	cleanAnnotations := make(map[string]string)
	if finalNode.Annotations != nil {
		for k, v := range finalNode.Annotations {
			if !strings.Contains(k, "test-") {
				cleanAnnotations[k] = v
			} else {
				log.Infof("Removing test annotation: %s", k)
			}
		}
	}

	_, err = na.client.Management.Node.Update(finalNode, map[string]interface{}{
		"annotations": cleanAnnotations,
	})
	require.NoError(na.T(), err)
	log.Info("Cleanup completed successfully")
}

func TestNodeAnnotationsTestSuite(t *testing.T) {
	suite.Run(t, new(NodeAnnotationsTestSuite))
}
