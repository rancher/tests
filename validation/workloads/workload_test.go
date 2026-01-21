//go:build (validation || infra.any || cluster.any || sanity || pit.daily || pit.harvester.daily) && !stress && !extended

package workloads

import (
	"os"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	projectsapi "github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/workloads"
	"github.com/rancher/tests/actions/workloads/cronjob"
	"github.com/rancher/tests/actions/workloads/daemonset"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/statefulset"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	appv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
)

type WorkloadTestSuite struct {
	suite.Suite
	client           *rancher.Client
	session          *session.Session
	cluster          *management.Cluster
	cattleConfig     map[string]any
	downstreamClient *v1.Client
}

func (w *WorkloadTestSuite) TearDownSuite() {
	w.session.Cleanup()
}

func (w *WorkloadTestSuite) SetupSuite() {
	w.session = session.NewSession()

	client, err := rancher.NewClient("", w.session)
	require.NoError(w.T(), err)

	w.client = client

	w.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	w.cattleConfig, err = defaults.LoadPackageDefaults(w.cattleConfig, "")
	require.NoError(w.T(), err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, w.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(w.T(), err)

	log.Info("Getting cluster name from the config file and append cluster details in connection")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(w.T(), clusterName, "Cluster name to install should be set")

	clusterID, err := clusters.GetClusterIDByName(w.client, clusterName)
	require.NoError(w.T(), err, "Error getting cluster ID")

	w.cluster, err = w.client.Management.Cluster.ByID(clusterID)
	require.NoError(w.T(), err)

	w.downstreamClient, err = w.client.Steve.ProxyDownstream(w.cluster.ID)
	require.NoError(w.T(), err)
}

func (w *WorkloadTestSuite) TestDeployments() {
	workloadTests := []struct {
		name       string
		replicas   int32
		image      string
		createFunc func(client *v1.Client, clusterID string, deployment *appv1.Deployment) (*appv1.Deployment, error)
		verifyFunc func(client *rancher.Client, clusterID, namespace, name string) error
	}{
		{"WorkloadDeploymentTest", 1, "nginx:latest", deployment.CreateDeploymentFromConfig, deployment.VerifyDeployment},
		{"WorkloadSideKickTest", 1, "redis", deployment.CreateDeploymentFromConfig, deployment.VerifyDeploymentSideKick},
		{"WorkloadUpgradeTest", 1, "nginx:latest", deployment.CreateDeploymentFromConfig, deployment.VerifyDeploymentUpgradeRollback},
		{"WorkloadPodScaleUpTest", 1, "nginx:latest", deployment.CreateDeploymentFromConfig, deployment.VerifyDeploymentPodScaleUp},
		{"WorkloadPodScaleDownTest", 3, "nginx:latest", deployment.CreateDeploymentFromConfig, deployment.VerifyDeploymentPodScaleDown},
		{"WorkloadPauseOrchestrationTest", 1, "nginx:latest", deployment.CreateDeploymentFromConfig, deployment.VerifyDeploymentOrchestration},
	}

	for _, workloadTest := range workloadTests {
		w.Suite.Run(workloadTest.name, func() {
			workloadConfigs := new(workloads.Workloads)
			operations.LoadObjectFromMap(workloads.WorkloadsConfigurationFileKey, w.cattleConfig, workloadConfigs)

			_, namespace, err := projectsapi.CreateProjectAndNamespace(w.client, w.cluster.ID)
			require.NoError(w.T(), err)

			workloadConfigs.Deployment.ObjectMeta.Namespace = namespace.Name
			workloadConfigs.Deployment.Spec.Replicas = &workloadTest.replicas
			workloadConfigs.Deployment.Spec.Template.Spec.Containers[0].Image = workloadTest.image
			workloadConfigs.Deployment.ObjectMeta.GenerateName = strings.ToLower(workloadTest.name) + "-"

			logrus.Infof("Creating deployment with name prefix: %s", workloadConfigs.Deployment.ObjectMeta.GenerateName)
			testDeployment, err := workloadTest.createFunc(w.downstreamClient, w.cluster.ID, workloadConfigs.Deployment)
			require.NoError(w.T(), err)

			logrus.Infof("Verifying deployment with name: %s", testDeployment.Name)
			err = workloadTest.verifyFunc(w.client, w.cluster.ID, testDeployment.Namespace, testDeployment.Name)
			require.NoError(w.T(), err)
		})
	}
}

func (w *WorkloadTestSuite) TestCronjobs() {
	workloadTests := []struct {
		name         string
		cronSchedule string
		createFunc   func(client *v1.Client, clusterID string, cronjob *batchv1.CronJob) (*batchv1.CronJob, error)
		verifyFunc   func(client *rancher.Client, clusterID, namespace, cronJobName string) error
	}{
		{"WorkloadCronjobTest", "* * * * *", cronjob.CreateCronJobFromConfig, cronjob.VerifyCronJob},
	}

	for _, workloadTest := range workloadTests {
		w.Suite.Run(workloadTest.name, func() {
			workloadConfigs := new(workloads.Workloads)
			operations.LoadObjectFromMap(workloads.WorkloadsConfigurationFileKey, w.cattleConfig, workloadConfigs)

			_, namespace, err := projectsapi.CreateProjectAndNamespace(w.client, w.cluster.ID)
			require.NoError(w.T(), err)

			workloadConfigs.CronJob.ObjectMeta.Namespace = namespace.Name
			workloadConfigs.CronJob.Spec.Schedule = workloadTest.cronSchedule
			workloadConfigs.CronJob.ObjectMeta.GenerateName = strings.ToLower(workloadTest.name) + "-"

			logrus.Infof("Creating cronjob with prefix: %s", workloadConfigs.CronJob.ObjectMeta.GenerateName)
			testCronjob, err := workloadTest.createFunc(w.downstreamClient, w.cluster.ID, workloadConfigs.CronJob)
			require.NoError(w.T(), err)

			logrus.Infof("Verifying cronjob with name: %s", testCronjob.Name)
			err = workloadTest.verifyFunc(w.client, w.cluster.ID, testCronjob.ObjectMeta.Namespace, testCronjob.ObjectMeta.Name)
			require.NoError(w.T(), err)
		})
	}
}

func (w *WorkloadTestSuite) TestDaemonsets() {
	workloadTests := []struct {
		name       string
		createFunc func(client *v1.Client, clusterID string, daemonset *appv1.DaemonSet) (*appv1.DaemonSet, error)
		verifyFunc func(client *rancher.Client, clusterID, namespace, daemonsetName string) error
	}{
		{"WorkloadDaemonSetTest", daemonset.CreateDaemonSetFromConfig, daemonset.VerifyDaemonset},
	}

	for _, workloadTest := range workloadTests {
		w.Suite.Run(workloadTest.name, func() {
			workloadConfigs := new(workloads.Workloads)
			operations.LoadObjectFromMap(workloads.WorkloadsConfigurationFileKey, w.cattleConfig, workloadConfigs)

			_, namespace, err := projectsapi.CreateProjectAndNamespace(w.client, w.cluster.ID)
			require.NoError(w.T(), err)

			workloadConfigs.DaemonSet.ObjectMeta.Namespace = namespace.Name
			workloadConfigs.DaemonSet.ObjectMeta.GenerateName = strings.ToLower(workloadTest.name) + "-"

			logrus.Infof("Creating daemonset with name prefix: %s", workloadConfigs.DaemonSet.ObjectMeta.GenerateName)
			testDaemonset, err := workloadTest.createFunc(w.downstreamClient, w.cluster.ID, workloadConfigs.DaemonSet)
			require.NoError(w.T(), err)

			logrus.Infof("Verifying daemonset with name: %s", testDaemonset.Name)
			err = workloadTest.verifyFunc(w.client, w.cluster.ID, testDaemonset.Namespace, testDaemonset.Name)
			require.NoError(w.T(), err)
		})
	}
}

func (w *WorkloadTestSuite) TestStatefulSets() {
	workloadTests := []struct {
		name       string
		createFunc func(client *v1.Client, clusterID string, statefulset *appv1.StatefulSet) (*appv1.StatefulSet, error)
		verifyFunc func(client *rancher.Client, clusterID, namespace, statefulsetName string) error
	}{
		{"WorkloadStatefulsetTest", statefulset.CreateStatefulSetFromConfig, statefulset.VerifyStatefulset},
	}

	for _, workloadTest := range workloadTests {
		w.Suite.Run(workloadTest.name, func() {
			workloadConfigs := new(workloads.Workloads)
			operations.LoadObjectFromMap(workloads.WorkloadsConfigurationFileKey, w.cattleConfig, workloadConfigs)

			_, namespace, err := projectsapi.CreateProjectAndNamespace(w.client, w.cluster.ID)
			require.NoError(w.T(), err)

			workloadConfigs.StatefulSet.ObjectMeta.Namespace = namespace.Name
			workloadConfigs.StatefulSet.ObjectMeta.GenerateName = strings.ToLower(workloadTest.name) + "-"

			logrus.Infof("Creating statefulset with name prefix: %s", workloadConfigs.StatefulSet.ObjectMeta.GenerateName)
			testStatefulset, err := workloadTest.createFunc(w.downstreamClient, w.cluster.ID, workloadConfigs.StatefulSet)
			require.NoError(w.T(), err)

			logrus.Infof("Verifying statefulset with name: %s", testStatefulset.Name)
			err = workloadTest.verifyFunc(w.client, w.cluster.ID, testStatefulset.Namespace, testStatefulset.Name)
			require.NoError(w.T(), err)
		})
	}
}

func TestWorkloadTestSuite(t *testing.T) {
	suite.Run(t, new(WorkloadTestSuite))
}
