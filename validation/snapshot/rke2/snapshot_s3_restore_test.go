//go:build (validation || extended || infra.any || cluster.any) && !sanity && !stress

package rke2

import (
	"fmt"
	"os"
	"testing"
	"time"

	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/cloudcredentials"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/etcdsnapshot"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	s3resources "github.com/rancher/tests/validation/provisioning/resources/s3Bucket"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	s3Region   = "us-east-2"
	s3Endpoint = "s3.us-east-2.amazonaws.com"
)

type S3SnapshotRestoreTestSuite struct {
	suite.Suite
	session      *session.Session
	client       *rancher.Client
	cattleConfig map[string]any
	cluster      *v1.SteveAPIObject
	clusterConfig *clusters.ClusterConfig
	bucketName    string
}

func (s *S3SnapshotRestoreTestSuite) TearDownSuite() {
	if s.bucketName != "" {
		err := s3resources.DeleteBucket(s.bucketName, s3Region)
		require.NoError(s.T(), err)
	}

	s.session.Cleanup()
}

func (s *S3SnapshotRestoreTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err)

	s.client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(s.client)
	require.NoError(s.T(), err)

	s.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	s.cattleConfig, err = defaults.LoadPackageDefaults(s.cattleConfig, "")
	require.NoError(s.T(), err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, s.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(s.T(), err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, clusterConfig)

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, s.cattleConfig, rancherConfig)

	provider := provisioning.CreateProvider(s.clusterConfig.Provider)

	if rancherConfig.ClusterName == "" {
		provider := provisioning.CreateProvider(clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(s.cattleConfig)

		logrus.Info("Provisioning RKE2 cluster")
		s.cluster, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.RKE2ClusterType.String(), provider, *clusterConfig, machineConfigSpec, nil, false, false)
		require.NoError(s.T(), err)

		err = etcdsnapshot.VerifyS3Config(s.client, s.cluster.Name)
		require.NoError(s.T(), err)
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		s.cluster, err = client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + s.client.RancherConfig.ClusterName)
		require.NoError(s.T(), err)
	}

	s.bucketName = fmt.Sprintf("rke2-s3-restore-%d", time.Now().Unix())
	err = s3resources.CreateBucket(s.bucketName, s3Region)
	require.NoError(s.T(), err)

	err = s.configureClusterS3(provider)
	require.NoError(s.T(), err)

	err = etcdsnapshot.VerifyS3Config(s.client, s.cluster.Name)
	require.NoError(s.T(), err)
}

func (s *S3SnapshotRestoreTestSuite) configureClusterS3(provider provisioning.Provider) error {
	credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))

	cloudCredential, err := provider.CloudCredFunc(s.client, credentialSpec)
	if err != nil {
		return err
	}

	cluster, err := s.client.Steve.SteveType(stevetypes.Provisioning).ByID(s.cluster.ID)
	if err != nil {
		return err
	}

	spec, ok := cluster.Object["spec"].(map[string]interface{})
	if !ok || spec == nil {
		spec = map[string]interface{}{}
		cluster.Object["spec"] = spec
	}

	rkeConfig, ok := spec["rkeConfig"].(map[string]interface{})
	if !ok || rkeConfig == nil {
		rkeConfig = map[string]interface{}{}
		spec["rkeConfig"] = rkeConfig
	}

	etcdConfig := &rkev1.ETCD{
		S3: &rkev1.ETCDSnapshotS3{
			Bucket:              s.bucketName,
			CloudCredentialName: cloudCredential.Name,
			Endpoint:            s3Endpoint,
			Region:              s3Region,
			SkipSSLVerify:       true,
		},
	}

	rkeConfig["etcd"] = map[string]interface{}{
		"s3": map[string]interface{}{
			"bucket":              etcdConfig.S3.Bucket,
			"cloudCredentialName": etcdConfig.S3.CloudCredentialName,
			"endpoint":            etcdConfig.S3.Endpoint,
			"region":              etcdConfig.S3.Region,
			"skipSSLVerify":       etcdConfig.S3.SkipSSLVerify,
		},
	}

	updatedCluster, err := s.client.Steve.SteveType(stevetypes.Provisioning).Update(cluster, cluster.Object)
	if err != nil {
		return err
	}

	s.cluster = updatedCluster
	logrus.Infof("Configured S3 backup target for cluster %s with bucket %s", s.cluster.Name, s.bucketName)

	return nil
}

func (s *S3SnapshotRestoreTestSuite) TestS3SnapshotRestore() {
	snapshotRestoreNone := &etcdsnapshot.Config{
		UpgradeKubernetesVersion: "",
		SnapshotRestore:          "none",
		RecurringRestores:        1,
	}

	tests := []struct {
		name         string
		etcdSnapshot *etcdsnapshot.Config
		cluster      *v1.SteveAPIObject
	}{
		{"RKE2_S3_Restore", snapshotRestoreNone, s.cluster},
	}

	for _, tt := range tests {
		var err error
		s.Run(tt.name, func() {
			cluster, err := s.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.cluster.ID)
			require.NoError(s.T(), err)

			err = etcdsnapshot.CreateAndValidateSnapshotRestore(s.client, cluster.Name, tt.etcdSnapshot, containerImage)
			require.NoError(s.T(), err)
		})

		params := provisioning.GetProvisioningSchemaParams(s.client, s.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestS3SnapshotRestoreTestSuite(t *testing.T) {
	suite.Run(t, new(S3SnapshotRestoreTestSuite))
}
