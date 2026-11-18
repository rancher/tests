package daemonsets

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extdaemonsetsapi "github.com/rancher/shepherd/extensions/kubeapi/workloads/daemonsets"
	extworkloads "github.com/rancher/shepherd/extensions/workloads"
	deploymentapi "github.com/rancher/tests/actions/kubeapi/workloads/deployments"
	appv1 "k8s.io/api/apps/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// CreateDaemonset is a helper to create a daemonset using wrangler context with the specified parameters. If waitForReady is true, it will wait for the DaemonSet to be ready after creation.
func CreateDaemonset(client *rancher.Client, clusterID, namespaceName string, imageName string, replicaCount int, secretName, configMapName string, useEnvVars, useVolumes, waitForReady bool) (*appv1.DaemonSet, error) {
	if imageName == "" {
		imageName = deploymentapi.NginxImageName
	}

	deploymentTemplate, err := deploymentapi.CreateDeployment(client, clusterID, namespaceName, imageName, replicaCount, secretName, configMapName, useEnvVars, useVolumes, false, true)
	if err != nil {
		return nil, err
	}

	daemonsetTemplate := extworkloads.NewDaemonSetTemplate(deploymentTemplate.Name, namespaceName, deploymentTemplate.Spec.Template, true, nil)
	createdDaemonset, err := extdaemonsetsapi.CreateDaemonSetWithTemplate(client, clusterID, daemonsetTemplate, waitForReady)
	if err != nil {
		return nil, err
	}

	return createdDaemonset, nil
}

// RestartDaemonSet triggers a rollout restart of a daemon set by updating an annotation and waits for it to be ready
func RestartDaemonSet(client *rancher.Client, clusterID, namespaceName, daemonSetName string) error {
	daemonSet, err := extdaemonsetsapi.GetDaemonSetByName(client, clusterID, namespaceName, daemonSetName)
	if err != nil {
		return fmt.Errorf("error fetching daemon set %s in namespace %s: %w", daemonSetName, namespaceName, err)
	}

	if daemonSet.Spec.Template.Annotations == nil {
		daemonSet.Spec.Template.Annotations = map[string]string{}
	}

	daemonSet.Spec.Template.Annotations[deploymentapi.RestartAnnotation] = time.Now().UTC().Format(time.RFC3339Nano)

	updated, err := extdaemonsetsapi.UpdateDaemonSet(client, clusterID, daemonSet, false)
	if err != nil {
		return fmt.Errorf("error restarting daemon set %s in namespace %s: %w", daemonSetName, namespaceName, err)
	}

	var lastStatus appv1.DaemonSetStatus

	var lastErr error

	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.FiveMinuteTimeout, true,
		func(context.Context) (bool, error) {
			current, err := extdaemonsetsapi.GetDaemonSetByName(client, clusterID, namespaceName, daemonSetName)
			if err != nil {
				lastErr = err

				return false, nil
			}

			lastErr = nil
			lastStatus = current.Status

			return lastStatus.ObservedGeneration >= updated.Generation && lastStatus.UpdatedNumberScheduled == lastStatus.DesiredNumberScheduled &&
				lastStatus.NumberAvailable == lastStatus.DesiredNumberScheduled, nil
		})
	if pollErr != nil {
		return fmt.Errorf("daemon set %s in namespace %s never finished rolling out after the restart (last status: %+v, last read error: %v): %w",
			daemonSetName, namespaceName, lastStatus, lastErr, pollErr)
	}

	return nil
}
