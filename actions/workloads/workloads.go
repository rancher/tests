package workloads

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/session"
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
	kwait "k8s.io/apimachinery/pkg/util/wait"
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
	IsWindows   bool               `json:"isWindows,omitempty" yaml:"isWindows,omitempty"`
}

// CreateWorkloads creates a variety of workloads on a cluster
func CreateWorkloads(client *rancher.Client, clusterName string, workloads Workloads) (*Workloads, error) {
	clusterID := clusterName
	isV3ID, err := regexp.MatchString(v3IDRegex, clusterName)
	if err != nil {
		return nil, err
	}

	if !isV3ID {
		clusterID, err = clusters.GetClusterIDByName(client, clusterName)
		if err != nil {
			return nil, err
		}
	}

	client, err = client.ReLogin()
	if err != nil {
		return nil, err
	}

	logrus.Debugf("Creating a namespace on %s", clusterName)
	// creator's cluster-owner RBAC binding can take a moment to propagate after cluster creation, so retry on forbidden errors
	var namespace *corev1.Namespace
	var pollErr error
	err = kwait.PollUntilContextTimeout(context.TODO(), 10*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		_, namespace, pollErr = projectsapi.CreateProjectAndNamespace(client, clusterID)
		if pollErr != nil {
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		return nil, pollErr
	}

	workloadSession := client.Session.NewSession()
	provisioningClient, err := client.WithSession(workloadSession)
	if err != nil {
		return nil, err
	}
	provisioningClient.Steve.Ops.Session = session.NewSession()

	workloadTypes := make([]string, 0, 6)
	if workloads.Deployment != nil {
		workloadTypes = append(workloadTypes, stevetypes.Deployment)
	}
	if workloads.DaemonSet != nil {
		workloadTypes = append(workloadTypes, "apps.daemonset")
	}
	if workloads.CronJob != nil {
		workloadTypes = append(workloadTypes, "batch.cronjob")
	}
	if workloads.Job != nil {
		workloadTypes = append(workloadTypes, "batch.job")
	}
	if workloads.Pod != nil {
		workloadTypes = append(workloadTypes, stevetypes.Pod)
	}
	if workloads.StatefulSet != nil {
		workloadTypes = append(workloadTypes, "apps.statefulset")
	}

	var downstreamClient *v1.Client
	err = kwait.PollUntilContextTimeout(context.Background(), 5*time.Second, time.Minute, true, func(ctx context.Context) (bool, error) {
		refreshedClient, refreshErr := provisioningClient.ReLogin()
		if refreshErr != nil {
			pollErr = refreshErr
			return false, nil
		}

		downstreamClient, pollErr = refreshedClient.Steve.ProxyDownstream(clusterID)
		if pollErr != nil {
			return false, nil
		}

		for _, workloadType := range workloadTypes {
			_, pollErr = downstreamClient.SteveType(workloadType).NamespacedSteveClient(namespace.Name).List(nil)
			if pollErr != nil {
				return false, nil
			}
		}

		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("workload schemas did not become ready on cluster %s: %w", clusterName, pollErr)
	}

	if workloads.Deployment != nil {
		logrus.Debugf("Creating a deployment on cluster: %s", clusterName)
		workloads.Deployment.ObjectMeta.Namespace = namespace.Name
		workloads.Deployment, err = deployment.CreateDeploymentFromConfig(downstreamClient, clusterID, workloads.Deployment)
		if err != nil {
			return nil, err
		}
		registerWorkloadCleanup(workloadSession, client, clusterID, "apps.deployment", workloads.Deployment.Namespace, workloads.Deployment.Name)
	}

	if workloads.DaemonSet != nil {
		logrus.Debugf("Creating a daemonset on cluster: %s", clusterName)
		workloads.DaemonSet.ObjectMeta.Namespace = namespace.Name
		workloads.DaemonSet, err = daemonset.CreateDaemonSetFromConfig(downstreamClient, clusterID, workloads.DaemonSet)
		if err != nil {
			return nil, err
		}
		registerWorkloadCleanup(workloadSession, client, clusterID, "apps.daemonset", workloads.DaemonSet.Namespace, workloads.DaemonSet.Name)
	}

	if workloads.CronJob != nil {
		logrus.Debugf("Creating a cronjob on cluster: %s", clusterName)
		workloads.CronJob.ObjectMeta.Namespace = namespace.Name
		workloads.CronJob, err = cronjob.CreateCronJobFromConfig(downstreamClient, clusterID, workloads.CronJob)
		if err != nil {
			return nil, err
		}
		registerWorkloadCleanup(workloadSession, client, clusterID, "batch.cronjob", workloads.CronJob.Namespace, workloads.CronJob.Name)
	}

	if workloads.Job != nil {
		logrus.Debugf("Creating a job on cluster: %s", clusterName)
		workloads.Job.ObjectMeta.Namespace = namespace.Name
		workloads.Job, err = job.CreateJobFromConfig(downstreamClient, clusterID, workloads.Job)
		if err != nil {
			return nil, err
		}
		registerWorkloadCleanup(workloadSession, client, clusterID, "batch.job", workloads.Job.Namespace, workloads.Job.Name)
	}

	if workloads.Pod != nil {
		logrus.Debugf("Creating a pod on cluster: %s", clusterName)
		workloads.Pod.ObjectMeta.Namespace = namespace.Name
		workloads.Pod, err = pods.CreatePodFromConfig(downstreamClient, clusterID, workloads.Pod)
		if err != nil {
			return nil, err
		}
		registerWorkloadCleanup(workloadSession, client, clusterID, stevetypes.Pod, workloads.Pod.Namespace, workloads.Pod.Name)
	}

	if workloads.StatefulSet != nil {
		logrus.Debugf("Creating a statefulset on cluster: %s", clusterName)
		workloads.StatefulSet.ObjectMeta.Namespace = namespace.Name
		workloads.StatefulSet, err = statefulset.CreateStatefulSetFromConfig(downstreamClient, clusterID, workloads.StatefulSet)
		if err != nil {
			return nil, err
		}
		registerWorkloadCleanup(workloadSession, client, clusterID, "apps.statefulset", workloads.StatefulSet.Namespace, workloads.StatefulSet.Name)
	}

	return &workloads, nil
}

func registerWorkloadCleanup(cleanupSession *session.Session, client *rancher.Client, clusterID, resourceType, namespace, name string) {
	cleanupSession.RegisterCleanupFunc(func() error {
		adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
		if err != nil {
			return err
		}

		steveClient, err := adminClient.Steve.ProxyDownstream(clusterID)
		if err != nil {
			return err
		}

		workloadID := namespace + "/" + name
		workload, err := steveClient.SteveType(resourceType).ByID(workloadID)
		if err != nil {
			if strings.Contains(err.Error(), "404 Not Found") {
				return nil
			}

			return err
		}

		return steveClient.SteveType(resourceType).Delete(workload)
	})
}
