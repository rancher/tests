package workloads

import (
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/tests/actions/workloads/cronjob"
	"github.com/rancher/tests/actions/workloads/daemonset"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/job"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/rancher/tests/actions/workloads/statefulset"
	"github.com/sirupsen/logrus"
)

// VerifyWorkloads verifies a variety of workloads on a cluster
func VerifyWorkloads(client *rancher.Client, clusterName string, workloads Workloads) (*Workloads, error) {
	client, err := client.ReLogin()
	if err != nil {
		return nil, err
	}

	clusterID, err := clusters.GetClusterIDByName(client, clusterName)
	if err != nil {
		return nil, err
	}

	if workloads.Deployment != nil {
		logrus.Debugf("Verifying deployment scale up on cluster: %s", clusterID)
		err = deployment.VerifyDeploymentPodScaleUp(client, clusterID, workloads.Deployment.Namespace, workloads.Deployment.Name)
		if err != nil {
			logrus.Warningf("Deployment scale up verification failed: %s, attempting to continue with other verifications", err)
		}

		logrus.Debugf("Verifying deployment scale down on cluster: %s", clusterID)
		err = deployment.VerifyDeploymentPodScaleDown(client, clusterID, workloads.Deployment.Namespace, workloads.Deployment.Name)
		if err != nil {
			logrus.Warningf("Deployment scale down verification failed: %s, attempting to continue with other verifications", err)
		}

		logrus.Debugf("Verifying deployment upgrade rollback on cluster: %s", clusterID)
		err = deployment.VerifyDeploymentUpgradeRollback(client, clusterID, workloads.Deployment.Namespace, workloads.Deployment.Name)
		if err != nil {
			logrus.Warningf("Deployment upgrade rollback verification failed: %s, attempting to continue with other verifications", err)
		}

		logrus.Debugf("Verifying deployment orchestration on cluster: %s", clusterID)
		err = deployment.VerifyDeploymentOrchestration(client, clusterID, workloads.Deployment.Namespace, workloads.Deployment.Name)
		if err != nil {
			logrus.Warningf("Deployment orchestration verification failed: %s, attempting to continue with other verifications", err)
		}

		logrus.Debugf("Verifying deployment sidekick on cluster: %s", clusterID)
		err = deployment.VerifyDeploymentSideKick(client, clusterID, workloads.Deployment.Namespace, workloads.Deployment.Name)
		if err != nil {
			logrus.Warningf("Deployment sidekick verification failed: %s, attempting to continue with other verifications", err)
		}

	}

	if workloads.DaemonSet != nil {
		logrus.Debugf("Verifying daemonset on cluster: %s", clusterName)
		err = daemonset.VerifyDaemonset(client, clusterID, workloads.DaemonSet.Namespace, workloads.DaemonSet.Name)
		if err != nil {
			logrus.Warningf("Daemonset verification failed: %s, attempting to continue with other verifications", err)
		}
	}

	if workloads.CronJob != nil {
		logrus.Debugf("Verifying cronjob on cluster: %s", clusterName)
		err = cronjob.VerifyCronJob(client, clusterID, workloads.CronJob.Namespace, workloads.CronJob.Name)
		if err != nil {
			logrus.Warningf("Cronjob verification failed: %s, attempting to continue with other verifications", err)
		}
	}

	if workloads.Job != nil {
		logrus.Debugf("Verifying job on cluster: %s", clusterName)
		err = job.VerifyJob(client, clusterID, workloads.Job.Namespace, workloads.Job.Name)
		if err != nil {
			logrus.Warningf("Job verification failed: %s, attempting to continue with other verifications", err)
		}
	}

	if workloads.StatefulSet != nil {
		logrus.Debugf("Verifying statefulset on cluster: %s", clusterName)
		err = statefulset.VerifyStatefulset(client, clusterID, workloads.StatefulSet.Namespace, workloads.StatefulSet.Name)
		if err != nil {
			logrus.Warningf("Statefulset verification failed: %s, attempting to continue with other verifications", err)
		}
	}

	cluster, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(namespaces.FleetDefault + "/" + clusterName)
	if err != nil {
		return nil, err
	}

	err = pods.VerifyClusterPods(client, cluster)

	return &workloads, err

}
