package namespaceformatter

import (
	"context"
	"fmt"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	"github.com/rancher/shepherd/pkg/session"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/namespaces"
	rbac "github.com/rancher/tests/actions/rbac"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	allowedSystemNamespaceAnnotation = "management.cattle.io/system-namespace"
	allowedProjectIDAnnotation       = "field.cattle.io/projectId"
	allowedFleetManagedLabel         = "fleet.cattle.io/managed"
	fleetLocalNamespace              = "fleet-local"
)

type NamespaceFormatterTestSuite struct {
	suite.Suite
	client    *rancher.Client
	session   *session.Session
	clusterID string
}

func (nf *NamespaceFormatterTestSuite) TearDownSuite() {
	nf.session.Cleanup()
}

func (nf *NamespaceFormatterTestSuite) SetupSuite() {
	nf.session = session.NewSession()

	client, err := rancher.NewClient("", nf.session)
	require.NoError(nf.T(), err)
	nf.client = client

	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(nf.T(), clusterName, "Cluster name to install should be set")

	nf.clusterID, err = clusters.GetClusterIDByName(nf.client, clusterName)
	require.NoError(nf.T(), err, "Error getting cluster ID")
}

func (nf *NamespaceFormatterTestSuite) TestImplicitNamespaceAccessRedactsMetadata() {
	standardUser, standardUserClient, err := rbac.SetupUser(nf.client, rbac.StandardUser.String())
	require.NoError(nf.T(), err)

	crtb, err := rbacapi.CreateClusterRoleTemplateBinding(nf.client, nf.clusterID, standardUser.ID, rbac.ManageNodes.String())
	require.NoError(nf.T(), err)
	nf.session.RegisterCleanupFunc(func() error {
		err := nf.client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	})

	standardUserClient, err = standardUserClient.ReLogin()
	require.NoError(nf.T(), err)

	userContext, err := extclusterapi.GetClusterWranglerContext(standardUserClient, nf.clusterID)
	require.NoError(nf.T(), err)
	_, err = userContext.Core.Namespace().Get(fleetLocalNamespace, metav1.GetOptions{})
	require.Error(nf.T(), err)
	require.Truef(nf.T(), apierrors.IsForbidden(err), "expected direct namespace access to be forbidden, got %v", err)

	userSteveClient, err := standardUserClient.Steve.ProxyDownstream(nf.clusterID)
	require.NoError(nf.T(), err)
	_, err = waitForSteveNodes(userSteveClient)
	require.NoError(nf.T(), err)

	adminSteveClient, err := nf.client.Steve.ProxyDownstream(nf.clusterID)
	require.NoError(nf.T(), err)
	adminNamespace, err := adminSteveClient.SteveType(namespaces.NamespaceSteveType).ByID(fleetLocalNamespace)
	require.NoError(nf.T(), err)
	assertAdminNamespaceHasMetadataToRedact(nf.T(), adminNamespace)
	expectedLabels, expectedAnnotations := allowedNamespaceMetadata(nf.T(), adminNamespace)

	limitedNamespace, err := waitForSteveNamespace(userSteveClient, fleetLocalNamespace)
	require.NoError(nf.T(), err)
	assertLimitedNamespaceHasOnlyAllowedMetadata(nf.T(), limitedNamespace, fleetLocalNamespace, expectedLabels, expectedAnnotations)
}

func waitForSteveNodes(steveClient *steveV1.Client) (*steveV1.SteveCollection, error) {
	var nodeList *steveV1.SteveCollection
	var lastErr error

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(context.Context) (bool, error) {
		var err error
		nodeList, err = steveClient.SteveType("node").ListAll(nil)
		if err != nil {
			lastErr = err
			return false, nil
		}

		return len(nodeList.Data) > 0, nil
	})
	if err != nil {
		return nil, fmt.Errorf("timed out waiting for Steve node access: %w; last error: %v", err, lastErr)
	}

	return nodeList, nil
}

func waitForSteveNamespace(steveClient *steveV1.Client, namespaceName string) (*steveV1.SteveAPIObject, error) {
	var namespace *steveV1.SteveAPIObject
	var lastErr error

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(context.Context) (bool, error) {
		namespaceList, err := steveClient.SteveType(namespaces.NamespaceSteveType).ListAll(nil)
		if err != nil {
			lastErr = err
			return false, nil
		}

		for index := range namespaceList.Data {
			if namespaceList.Data[index].Name == namespaceName {
				namespace = &namespaceList.Data[index]
				return true, nil
			}
		}

		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("timed out waiting for Steve namespace %q in list: %w; last error: %v", namespaceName, err, lastErr)
	}

	return namespace, nil
}

func assertAdminNamespaceHasMetadataToRedact(t *testing.T, namespace *steveV1.SteveAPIObject) {
	metadata := rawMap(t, namespace.JSONResp["metadata"], "metadata")
	labels := rawMap(t, metadata["labels"], "metadata.labels")
	annotations := rawMap(t, metadata["annotations"], "metadata.annotations")

	for key := range labels {
		if key != allowedFleetManagedLabel {
			return
		}
	}
	for key := range annotations {
		if key != allowedSystemNamespaceAnnotation && key != allowedProjectIDAnnotation {
			return
		}
	}

	require.Fail(t, "expected admin namespace metadata to include at least one non-allowlisted label or annotation")
}

func allowedNamespaceMetadata(t *testing.T, namespace *steveV1.SteveAPIObject) (map[string]any, map[string]any) {
	metadata := rawMap(t, namespace.JSONResp["metadata"], "metadata")
	labels := rawMap(t, metadata["labels"], "metadata.labels")
	annotations := rawMap(t, metadata["annotations"], "metadata.annotations")

	allowedLabels := map[string]any{}
	if value, ok := labels[allowedFleetManagedLabel]; ok {
		allowedLabels[allowedFleetManagedLabel] = value
	}

	allowedAnnotations := map[string]any{}
	for _, key := range []string{allowedSystemNamespaceAnnotation, allowedProjectIDAnnotation} {
		if value, ok := annotations[key]; ok {
			allowedAnnotations[key] = value
		}
	}

	return allowedLabels, allowedAnnotations
}

func assertLimitedNamespaceHasOnlyAllowedMetadata(t *testing.T, namespace *steveV1.SteveAPIObject, namespaceName string, expectedLabels, expectedAnnotations map[string]any) {
	metadata := rawMap(t, namespace.JSONResp["metadata"], "metadata")
	labels := rawMap(t, metadata["labels"], "metadata.labels")
	annotations := rawMap(t, metadata["annotations"], "metadata.annotations")
	status := rawMap(t, namespace.JSONResp["status"], "status")

	assert.Equal(t, namespaceName, metadata["name"])
	assert.NotEmpty(t, metadata["resourceVersion"])
	assert.NotContains(t, metadata, "uid")
	assert.NotContains(t, metadata, "creationTimestamp")
	assert.NotContains(t, metadata, "managedFields")

	assert.Equal(t, "Active", status["phase"])

	assert.Equal(t, expectedAnnotations, annotations)
	assert.Equal(t, expectedLabels, labels)

	for key := range annotations {
		assert.Contains(t, []string{allowedSystemNamespaceAnnotation, allowedProjectIDAnnotation}, key)
	}
	for key := range labels {
		assert.Contains(t, []string{allowedFleetManagedLabel}, key)
	}
}

func rawMap(t *testing.T, raw any, field string) map[string]any {
	if raw == nil {
		return map[string]any{}
	}

	typed, ok := raw.(map[string]any)
	require.Truef(t, ok, "expected %s to be a map, got %T", field, raw)

	return typed
}

func TestNamespaceFormatterTestSuite(t *testing.T) {
	suite.Run(t, new(NamespaceFormatterTestSuite))
}
