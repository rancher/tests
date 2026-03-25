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
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/etcdsnapshot"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type S3SnapshotRestoreTestSuite struct {
	suite.Suite
	session           *session.Session
	client            *rancher.Client
	cattleConfig      map[string]any
	cluster           *v1.SteveAPIObject
	s3BucketName      string
	s3Region          string
	s3Endpoint        string
	s3CloudCredName   string
	createdTestBucket bool
	awsAccessKey      string
	awsSecretKey      string
}

func (s *S3SnapshotRestoreTestSuite) TearDownSuite() {
	if s.createdTestBucket && s.s3BucketName != "" {
		err := etcdsnapshot.DeleteS3Bucket(s.s3BucketName, s.s3Region, s.awsAccessKey, s.awsSecretKey)
		require.NoError(s.T(), err)
	}

	s.session.Cleanup()
}

type awsCredentialsConfig struct {
	SecretKey     string `json:"secretKey" yaml:"secretKey"`
	AccessKey     string `json:"accessKey" yaml:"accessKey"`
	DefaultRegion string `json:"defaultRegion" yaml:"defaultRegion"`
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

	awsCredsConfig := new(awsCredentialsConfig)
	operations.LoadObjectFromMap("awsCredentials", s.cattleConfig, awsCredsConfig)

	s.awsAccessKey = awsCredsConfig.AccessKey
	s.awsSecretKey = awsCredsConfig.SecretKey

	if rancherConfig.ClusterName == "" {
		provider := provisioning.CreateProvider(clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(s.cattleConfig)

		credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
		cloudCredential, err := provider.CloudCredFunc(standardUserClient, credentialSpec)
		require.NoError(s.T(), err)

		s.s3CloudCredName = cloudCredential.Namespace + ":" + cloudCredential.Name
		s.s3Region = awsCredsConfig.DefaultRegion
		s.s3Endpoint = fmt.Sprintf("s3.%s.amazonaws.com", s.s3Region)
		s.s3BucketName = fmt.Sprintf("snapshot-restore-s3-%d-%s", time.Now().Unix(), namegenerator.RandStringLower(5))

		err = etcdsnapshot.CreateS3Bucket(s.s3BucketName, s.s3Region, awsCredsConfig.AccessKey, awsCredsConfig.SecretKey)
		require.NoError(s.T(), err)
		s.createdTestBucket = true

		clusterConfig.ETCD = &rkev1.ETCD{
			SnapshotRetention:    5,
			SnapshotScheduleCron: "0 */5 * * *",
			S3: &rkev1.ETCDSnapshotS3{
				Bucket:              s.s3BucketName,
				CloudCredentialName: s.s3CloudCredName,
				Endpoint:            s.s3Endpoint,
				Region:              s.s3Region,
				SkipSSLVerify:       true,
			},
		}

		logrus.Infof("Provisioning RKE2 cluster with S3 snapshot config. bucket=%s region=%s endpoint=%s credential=%s", s.s3BucketName, s.s3Region, s.s3Endpoint, s.s3CloudCredName)

		s.cluster, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.RKE2ClusterType.String(), provider, *clusterConfig, machineConfigSpec, nil, false, false)
		require.NoError(s.T(), err)

		err = etcdsnapshot.VerifyS3Config(s.client, s.cluster.Name)
		require.NoError(s.T(), err)
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		s.cluster, err = client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + s.client.RancherConfig.ClusterName)
		require.NoError(s.T(), err)
	}
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
