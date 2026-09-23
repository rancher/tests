//go:build os

package os

import (
	"math/rand"
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	shepherdclusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/networking"
	projectsapi "github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type nodeDriverTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func nodeDriverSetup(t *testing.T) nodeDriverTest {
	var r nodeDriverTest
	testSession := session.NewSession()
	r.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)
	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	r.cattleConfig, err = defaults.LoadPackageDefaults(r.cattleConfig, "")
	require.NoError(t, err)

	r.cattleConfig, err = defaults.LoadSecretsManagerDefaults(r.cattleConfig)
	require.NoError(t, err)

	err = defaults.VerifyCattleConfig(r.cattleConfig, nil)
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, r.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	r.standardUserClient, _, _, err = standard.CreateStandardUser(r.client)
	require.NoError(t, err)

	return r
}

func TestNodeDriver(t *testing.T) {
	t.Parallel()
	r := nodeDriverSetup(t)
	t.Cleanup(r.session.Cleanup)

	tests := []struct {
		name    string
		k8sType string
		client  *rancher.Client
	}{
		{"OS_RKE2_Node_Driver", defaults.RKE2, r.standardUserClient},
		{"OS_K3S_Node_Driver", defaults.K3S, r.standardUserClient},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			testSession := session.NewSession()
			t.Cleanup(func() {
				logrus.Infof("Running cleanup (%s)", tt.name)
				testSession.Cleanup()
			})

			testClient, err := tt.client.WithSession(testSession)
			require.NoError(t, err)

			adminClient, err := r.client.WithSession(testSession)
			require.NoError(t, err)

			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)

			versions, err := kubernetesversions.Default(adminClient, tt.k8sType, nil)
			require.NoError(t, err)
			clusterConfig.KubernetesVersion = versions[0]

			if clusterConfig.MixedArchitecture && len(clusterConfig.MachinePools) == 1 && clusterConfig.MachinePools[0].MachinePoolConfig.Worker && clusterConfig.MachinePools[0].MachinePoolConfig.Etcd {
				t.Skip("skipping all-roles pool: mixed architecture requires a dedicated worker pool")
			}

			require.NotNil(t, clusterConfig.Provider)

			provider := provisioning.CreateProvider(clusterConfig.Provider)
			credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
			machineConfigSpec := provider.LoadMachineConfigFunc(r.cattleConfig)

			logrus.Info("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningCluster(testClient, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(adminClient, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(testClient, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(adminClient, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying service account token secret (%s)", cluster.Name)
			err = clusters.VerifyServiceAccountTokenSecret(adminClient, cluster.Name)
			require.NoError(t, err)

			workloadConfigs := new(workloads.Workloads)
			operations.LoadObjectFromMap(workloads.WorkloadsConfigurationFileKey, r.cattleConfig, workloadConfigs)

			logrus.Infof("Creating workloads (%s)", cluster.Name)
			createdWorkloads, err := workloads.CreateWorkloads(adminClient, cluster.Name, *workloadConfigs)
			require.NoError(t, err)

			logrus.Infof("Verifying workloads (%s)", cluster.Name)
			_, err = workloads.VerifyWorkloads(adminClient, cluster.Name, *createdWorkloads)
			require.NoError(t, err)

			clusterID, err := shepherdclusters.GetClusterIDByName(adminClient, cluster.Name)
			require.NoError(t, err)

			downstreamClient, err := clusters.ProxyDownstreamWithRetry(adminClient, clusterID)
			require.NoError(t, err)

			_, namespace, err := projectsapi.CreateProjectAndNamespace(adminClient, clusterID)
			require.NoError(t, err)

			connectivityWorkloadConfigs := new(workloads.Workloads)
			operations.LoadObjectFromMap(workloads.WorkloadsConfigurationFileKey, r.cattleConfig, connectivityWorkloadConfigs)

			logrus.Infof("Verifying pod connectivity (%s)", cluster.Name)
			err = networking.VerifyPodConnectivity(adminClient, downstreamClient, clusterID, namespace.Name, "pod-connectivity-", connectivityWorkloadConfigs)
			require.NoError(t, err)

			hostPort := rand.Intn(55283) + 10251
			logrus.Infof("Verifying host port connectivity (%s)", cluster.Name)
			err = networking.VerifyHostPortConnectivity(adminClient, downstreamClient, clusterID, namespace.Name, hostPort, "/", connectivityWorkloadConfigs)
			require.NoError(t, err)

			nodePort := rand.Intn(2767) + 30000
			logrus.Infof("Verifying node port connectivity (%s)", cluster.Name)
			err = networking.VerifyNodePortConnectivity(adminClient, downstreamClient, clusterID, namespace.Name, nodePort, "/", connectivityWorkloadConfigs)
			require.NoError(t, err)
		})

		params := provisioning.GetProvisioningSchemaParams(tt.client, r.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}
