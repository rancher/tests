package pods

import (
	"context"
	"strings"
	"testing"
	"time"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/rancher/tests/actions/workloads"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// VerifyReadyDaemonsetPods tries to poll the Steve API to verify the expected number of daemonset pods are in the Ready
// state
func VerifyReadyDaemonsetPods(t *testing.T, client *rancher.Client, cluster *v1.SteveAPIObject) {
	status := &provv1.ClusterStatus{}
	err := v1.ConvertToK8sType(cluster.Status, status)
	require.NoError(t, err)

	daemonsetequals := false

	err = wait.Poll(500*time.Millisecond, 5*time.Minute, func() (dameonsetequals bool, err error) {
		daemonsets, err := client.Steve.SteveType(workloads.DaemonsetSteveType).ByID(status.ClusterName)
		require.NoError(t, err)

		daemonsetsStatusType := &appv1.DaemonSetStatus{}
		err = v1.ConvertToK8sType(daemonsets.Status, daemonsetsStatusType)
		require.NoError(t, err)

		if daemonsetsStatusType.DesiredNumberScheduled == daemonsetsStatusType.NumberAvailable {
			return true, nil
		}
		return false, err
	})
	require.NoError(t, err)

	daemonsets, err := client.Steve.SteveType(workloads.DaemonsetSteveType).ByID(status.ClusterName)
	require.NoError(t, err)

	daemonsetsStatusType := &appv1.DaemonSetStatus{}
	err = v1.ConvertToK8sType(daemonsets.Status, daemonsetsStatusType)
	require.NoError(t, err)

	if daemonsetsStatusType.DesiredNumberScheduled == daemonsetsStatusType.NumberAvailable {
		daemonsetequals = true
	}

	assert.Truef(t, daemonsetequals, "Ready Daemonset Pods didn't match expected")
}

// VerifyClusterPods validates that all pods (excluding the helm pods) are in a good state.
func VerifyClusterPods(t *testing.T, client *rancher.Client, cluster *steveV1.SteveAPIObject) {
	status := &provv1.ClusterStatus{}
	err := steveV1.ConvertToK8sType(cluster.Status, status)
	assert.NoError(t, err)

	downstreamClient, err := client.Steve.ProxyDownstream(status.ClusterName)
	assert.NoError(t, err)
	assert.NotNil(t, downstreamClient)

	var podErrors []error
	steveClient := downstreamClient.SteveType(stevetypes.Pod)
	err = kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.TenMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		podErrors = []error{}

		clusterPods, err := steveClient.List(nil)
		if err != nil {
			return false, nil
		}

		for _, pod := range clusterPods.Data {
			isReady, err := pods.IsPodReady(&pod)
			if !isReady {
				return false, nil
			}

			if err != nil {
				if !strings.Contains(pod.Name, "helm") {
					podErrors = append(podErrors, err)
				} else {
					logrus.Warningf("Helm pod: %s is not ready", pod.Name)
				}
			}
		}
		return true, nil
	})

	if len(podErrors) > 0 {
		for _, err := range podErrors {
			logrus.Error(err)
		}
	}

	assert.Empty(t, podErrors, "Pod error list is not empty")
}
