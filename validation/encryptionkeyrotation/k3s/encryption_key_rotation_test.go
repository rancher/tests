//go:build validation || recurring || proxy || ipv6 || dualstack

package encryptionkeyrotation

import (
	"os"
	"testing"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	snapshot "github.com/rancher/shepherd/extensions/etcdsnapshot"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/encryptionkeyrotation"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type EncryptionKeyRotationTestSuite struct {
	suite.Suite
	session      *session.Session
	client       *rancher.Client
	cattleConfig map[string]any
	cluster      *v1.SteveAPIObject
}

func (e *EncryptionKeyRotationTestSuite) TearDownSuite() {
	e.session.Cleanup()
}

func (e *EncryptionKeyRotationTestSuite) SetupSuite() {
	testSession := session.NewSession()
	e.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(e.T(), err)

	e.client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(e.client)
	require.NoError(e.T(), err)

	e.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	e.cattleConfig, err = defaults.LoadPackageDefaults(e.cattleConfig, "")
	require.NoError(e.T(), err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, e.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(e.T(), err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, e.cattleConfig, clusterConfig)

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, e.cattleConfig, rancherConfig)

	if rancherConfig.ClusterName == "" {
		provider := provisioning.CreateProvider(clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(e.cattleConfig)

		logrus.Info("Provisioning K3S cluster")
		e.cluster, err = resources.ProvisionRKE2K3SCluster(e.T(), standardUserClient, defaults.K3S, provider, *clusterConfig, machineConfigSpec, nil, true, false)
		require.NoError(e.T(), err)
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		e.cluster, err = e.client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + e.client.RancherConfig.ClusterName)
		require.NoError(e.T(), err)
	}
}

func (e *EncryptionKeyRotationTestSuite) TestEncryptionKeyRotation() {
	tests := []struct {
		name    string
		cluster *v1.SteveAPIObject
	}{
		{"K3S_Encryption_Key_Rotation", e.cluster},
	}

	for _, tt := range tests {
		var err error
		e.Run(tt.name, func() {
			logrus.Infof("Creating snapshot on cluster (%s)", tt.cluster.Name)
			_, err := snapshot.CreateRKE2K3SSnapshot(e.client, tt.cluster.Name)
			require.NoError(e.T(), err)

			logrus.Infof("Enabling secrets encryption on cluster (%s)", tt.cluster.Name)
			err = encryptionkeyrotation.EnableSecretsEncryption(e.client, tt.cluster.Name)
			require.NoError(e.T(), err)

			logrus.Infof("Performing encryption key rotation on cluster (%s)", tt.cluster.Name)
			err = encryptionkeyrotation.RotateEncryptionKey(e.client, tt.cluster.Name)
			require.NoError(e.T(), err)

			clusterStatus := &provv1.ClusterStatus{}
			err = steveV1.ConvertToK8sType(tt.cluster.Status, clusterStatus)
			require.NoError(e.T(), err)

			logrus.Infof("Verifying encryption key rotated on cluster (%s)", tt.cluster.Name)
			err = encryptionkeyrotation.VerifyEncryptionKeyRotation(e.client, clusterStatus, defaults.K3S)
			require.NoError(e.T(), err)

			logrus.Infof("Verifying the cluster is ready (%s)", tt.cluster.Name)
			err = provisioning.VerifyClusterReady(e.client, tt.cluster)
			require.NoError(e.T(), err)

			logrus.Infof("Verifying cluster deployments (%s)", tt.cluster.Name)
			err = deployment.VerifyClusterDeployments(e.client, tt.cluster)
			require.NoError(e.T(), err)

			logrus.Infof("Verifying cluster pods (%s)", tt.cluster.Name)
			err = pods.VerifyClusterPods(e.client, tt.cluster)
			require.NoError(e.T(), err)
		})

		params := provisioning.GetProvisioningSchemaParams(e.client, e.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestEncryptionKeyRotationTestSuite(t *testing.T) {
	suite.Run(t, new(EncryptionKeyRotationTestSuite))
}
