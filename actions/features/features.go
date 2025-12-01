package features

import (
	"context"
	"strings"
	"time"

	"github.com/rancher/norman/types"
	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/sirupsen/logrus"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	rancherPodProfix           = "rancher"
	ClusterAutoscaling         = "cluster-autoscaling"
	AutoscalerRepositoryEnvVar = "CATTLE_CLUSTER_AUTOSCALER_CHART_REPOSITORY"
	AutoscalerImageEnvVar      = "CATTLE_CLUSTER_AUTOSCALER_IMAGE"
	local                      = "local"
	rancherDeploymentID        = "cattle-system/rancher"
)

func UpdateFeatureFlag(client *rancher.Client, name string, value bool) error {
	featureOpts := &types.ListOpts{Filters: map[string]interface{}{
		"name": name,
	}}

	features, err := client.Management.Feature.List(featureOpts)
	if err != nil {
		return err
	}

	for _, feature := range features.Data {
		if *feature.Value != value {
			feature.Value = &value
		} else {
			logrus.Warningf("Feature: %s is already %v", name, value)
			return nil
		}

		logrus.Debugf("Updating: %s state to %v", feature.Name, *feature.Value)
		client.Management.Feature.Update(&feature, &feature)
	}

	cluster, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(namespaces.FleetLocal + "/local")
	if err != nil {
		return err
	}

	status := &provv1.ClusterStatus{}
	err = steveV1.ConvertToK8sType(cluster.Status, status)
	if err != nil {
		return err
	}

	downstreamClient, err := client.Steve.ProxyDownstream(status.ClusterName)
	if err != nil {
		return err
	}

	logrus.Debug("Waiting for rancher deployment to restart")
	restarted := false
	steveClient := downstreamClient.SteveType(stevetypes.Pod)
	err = kwait.PollUntilContextTimeout(context.TODO(), 10*time.Second, defaults.FiveMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		clusterPods, err := steveClient.List(nil)
		if err != nil {
			restarted = true
			return false, nil
		}

		for _, pod := range clusterPods.Data {
			isReady, _ := pods.IsPodReady(&pod)
			if !isReady {
				if strings.Contains(pod.Name, rancherPodProfix) {
					restarted = true
				}

				return false, nil
			}
		}

		if !restarted {
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		logrus.Warning("Rancher restart was not observed")
	}

	return nil
}

func IsEnabled(client *rancher.Client, name string) (bool, error) {
	featureOpts := &types.ListOpts{Filters: map[string]interface{}{
		"name": name,
	}}

	features, err := client.Management.Feature.List(featureOpts)
	if err != nil {
		return false, err
	}

	enabled := false
	for _, feature := range features.Data {
		if *feature.Value == true {
			enabled = true
		}
	}

	return enabled, nil
}

func ConfigureAutoscaler(client *rancher.Client, repository, image string) error {
	downstreamClient, err := client.Steve.ProxyDownstream(local)
	if err != nil {
		return err
	}

	rancherDeployment, err := downstreamClient.SteveType(stevetypes.Deployment).ByID(rancherDeploymentID)
	if err != nil {
		return err
	}

	deploymentSpec := &appv1.DeploymentSpec{}
	err = steveV1.ConvertToK8sType(rancherDeployment.Spec, deploymentSpec)
	if err != nil {
		return err
	}

	updateDeployment := *rancherDeployment

	for i, container := range deploymentSpec.Template.Spec.Containers {
		if container.Name == "rancher" {
			createRepositoryEnvVar := true
			createImageEnvVar := true
			for _, envVar := range container.Env {
				if envVar.Name == AutoscalerRepositoryEnvVar && envVar.Value == repository {
					createRepositoryEnvVar = false
					logrus.Warningf("Rancher deployment env var: %s is already configured", AutoscalerRepositoryEnvVar)
				} else if envVar.Name == AutoscalerImageEnvVar && envVar.Value == image {
					logrus.Warningf("Rancher deployment env var: %s is already configured", AutoscalerImageEnvVar)
					createImageEnvVar = false
				}
			}

			var repositoryVar corev1.EnvVar
			if createRepositoryEnvVar {
				repositoryVar = corev1.EnvVar{
					Name:      AutoscalerRepositoryEnvVar,
					Value:     repository,
					ValueFrom: nil,
				}

				deploymentSpec.Template.Spec.Containers[i].Env = append(deploymentSpec.Template.Spec.Containers[i].Env, repositoryVar)
			}

			var imageVar corev1.EnvVar
			if createImageEnvVar {
				imageVar = corev1.EnvVar{
					Name:      AutoscalerImageEnvVar,
					Value:     image,
					ValueFrom: nil,
				}

				deploymentSpec.Template.Spec.Containers[i].Env = append(deploymentSpec.Template.Spec.Containers[i].Env, imageVar)
			}
		}
	}

	updateDeployment.Spec = deploymentSpec

	rancherDeployment, err = downstreamClient.SteveType(stevetypes.Deployment).Update(rancherDeployment, updateDeployment)
	if err != nil {
		return err
	}

	err = kwait.PollUntilContextTimeout(context.TODO(), 10*time.Second, defaults.FiveMinuteTimeout, false, func(ctx context.Context) (done bool, err error) {
		deploymentResp, err := downstreamClient.SteveType(stevetypes.Deployment).ByID(rancherDeploymentID)
		if err != nil {
			return false, nil
		}

		deployment := &appv1.Deployment{}
		err = steveV1.ConvertToK8sType(deploymentResp.JSONResp, deployment)
		if err != nil {

			return false, nil
		}

		if *deployment.Spec.Replicas == deployment.Status.AvailableReplicas {
			return true, nil
		}

		return false, nil
	})

	return nil
}
