package workloads

import (
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	projectsapi "github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/workloads/cronjob"
	"github.com/rancher/tests/actions/workloads/daemonset"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/job"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/rancher/tests/actions/workloads/statefulset"
	"github.com/sirupsen/logrus"
	appv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	WorkloadsConfigurationFileKey = "workloadConfigs"
	DaemonsetSteveType            = "apps.daemonset"
)

type Workloads struct {
	Deployment  *appv1.Deployment  `json:"deployment,omitempty" yaml:"deployment,omitempty"`
	DaemonSet   *appv1.DaemonSet   `json:"daemonset,omitempty" yaml:"daemonset,omitempty"`
	CronJob     *batchv1.CronJob   `json:"cronjob,omitempty" yaml:"cronjob,omitempty"`
	Job         *batchv1.Job       `json:"job,omitempty" yaml:"job,omitempty"`
	StatefulSet *appv1.StatefulSet `json:"statefulset,omitempty" yaml:"statefulset,omitempty"`
	Pod         *corev1.Pod        `json:"pod,omitempty" yaml:"pod,omitempty"`
}

// CreateWorkloads creates a variety of workloads on a cluster
func CreateWorkloads(client *rancher.Client, clusterName string, workloads Workloads) (*Workloads, error) {
	clusterID, err := clusters.GetClusterIDByName(client, clusterName)
	if err != nil {
		return nil, err
	}

	logrus.Debugf("Creating a namespace on %s", clusterName)
	_, namespace, err := projectsapi.CreateProjectAndNamespace(client, clusterID)
	if err != nil {
		return nil, err
	}

	client, err = client.ReLogin()
	if err != nil {
		return nil, err
	}

	downstreamClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return nil, err
	}

	if workloads.Deployment != nil {
		logrus.Debugf("Creating a deployment on cluster: %s", clusterName)
		workloads.Deployment.ObjectMeta.Namespace = namespace.Name
		workloads.Deployment, err = deployment.CreateDeploymentFromConfig(downstreamClient, clusterID, workloads.Deployment)
		if err != nil {
			return nil, err
		}
	}

	if workloads.DaemonSet != nil {
		logrus.Debugf("Creating a daemonset on cluster: %s", clusterName)
		workloads.DaemonSet.ObjectMeta.Namespace = namespace.Name
		workloads.DaemonSet, err = daemonset.CreateDaemonSetFromConfig(downstreamClient, clusterID, workloads.DaemonSet)
		if err != nil {
			return nil, err
		}
	}

	if workloads.CronJob != nil {
		logrus.Debugf("Creating a cronjob on cluster: %s", clusterName)
		workloads.CronJob.ObjectMeta.Namespace = namespace.Name
		workloads.CronJob, err = cronjob.CreateCronJobFromConfig(downstreamClient, clusterID, workloads.CronJob)
		if err != nil {
			return nil, err
		}
	}

	if workloads.Job != nil {
		logrus.Debugf("Creating a job on cluster: %s", clusterName)
		workloads.Job.ObjectMeta.Namespace = namespace.Name
		workloads.Job, err = job.CreateJobFromConfig(downstreamClient, clusterID, workloads.Job)
		if err != nil {
			return nil, err
		}
	}

	if workloads.Pod != nil {
		logrus.Debugf("Creating a pod on cluster: %s", clusterName)
		workloads.Pod.ObjectMeta.Namespace = namespace.Name
		workloads.Pod, err = pods.CreatePodFromConfig(downstreamClient, clusterID, workloads.Pod)
		if err != nil {
			return nil, err
		}
	}

	if workloads.StatefulSet != nil {
		logrus.Debugf("Creating a statefulset on cluster: %s", clusterName)
		workloads.StatefulSet.ObjectMeta.Namespace = namespace.Name
		workloads.StatefulSet, err = statefulset.CreateStatefulSetFromConfig(downstreamClient, clusterID, workloads.StatefulSet)
		if err != nil {
			return nil, err
		}
	}

	return &workloads, nil
}
