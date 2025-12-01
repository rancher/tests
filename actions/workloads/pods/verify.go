package pods

import (
	"context"
	"slices"
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
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	Webhook      = "rancher-webhook"
	SUC          = "system-upgrade-controller"
	Fleet        = "fleet-agent"
	ClusterAgent = "cattle-cluster-agent"
	helmPrefix   = "helm"
)

// VerifyReadyDaemonsetPods tries to poll the Steve API to verify the expected number of daemonset pods are in the Ready
// state
func VerifyReadyDaemonsetPods(t *testing.T, client *rancher.Client, cluster *v1.SteveAPIObject) {
	status := &provv1.ClusterStatus{}
	err := v1.ConvertToK8sType(cluster.Status, status)
	require.NoError(t, err)

	daemonsetequals := false

	err = kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.TenMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
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
	require.NoError(t, err)

	downstreamClient, err := client.Steve.ProxyDownstream(status.ClusterName)
	require.NoError(t, err)
	require.NotNil(t, downstreamClient)

	var podErrors []error
	steveClient := downstreamClient.SteveType(stevetypes.Pod)
	deploymentClient := downstreamClient.SteveType(stevetypes.Deployment)
	err = kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.TenMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		clusterDeployments, err := deploymentClient.List(nil)
		if err != nil {
			return false, nil
		}

		requiredDeployments := []string{ClusterAgent, Webhook, Fleet, SUC}
		requiredDeploymentCount := 0
		for _, deployment := range clusterDeployments.Data {
			if slices.Contains(requiredDeployments, deployment.Name) {
				logrus.Tracef("Deployment: %s exists", deployment.Name)
				requiredDeploymentCount += 1
			}
		}
		if requiredDeploymentCount != len(requiredDeployments) {
			return false, nil
		}

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
				if !strings.Contains(pod.Name, helmPrefix) {
					podErrors = append(podErrors, err)
				} else {
					logrus.Warningf("Helm pod: %s is not ready", pod.Name)
				}
			}
		}

		return true, nil
	})
	assert.NoError(t, err)

	if len(podErrors) > 0 {
		for _, err := range podErrors {
			logrus.Error(err)
		}
	}

	require.Empty(t, podErrors, "Pod error list is not empty")
}
