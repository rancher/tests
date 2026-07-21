package snapshot

import (
	"fmt"
	"os"
	"testing"
	"time"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/etcdsnapshot"
	"github.com/rancher/tests/actions/logging"
	projectsapi "github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/workloads"
	"github.com/rancher/tests/actions/workloads/deployment"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	ContainerImage        = "nginx"
	WindowsContainerImage = "mcr.microsoft.com/windows/servercore/iis"
)

type snapshotTest struct {
	suite.Suite
	Client            *rancher.Client
	Session           *session.Session
	CattleConfig      map[string]any
	ClusterConfig     *clusters.ClusterConfig
	rancherConfig     *rancher.Config
	WorkloadsConfig   *workloads.Workloads
	WorkloadClient    *v1.Client
	Cluster           *v1.SteveAPIObject
	S3BucketName      string
	S3Region          string
	S3Endpoint        string
	S3CloudCredName   string
	CreatedTestBucket bool
	AWSAccessKey      string
	AWSSecretKey      string
}

type awsCredentialsConfig struct {
	SecretKey     string `json:"secretKey" yaml:"secretKey"`
	AccessKey     string `json:"accessKey" yaml:"accessKey"`
	DefaultRegion string `json:"defaultRegion" yaml:"defaultRegion"`
}

func Setup(t *testing.T, clusterType string, isS3, isWindows bool) *snapshotTest {
	s := &snapshotTest{}

	testSession := session.NewSession()
	s.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	s.Client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(s.Client)
	require.NoError(t, err)

	s.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	s.CattleConfig, err = defaults.LoadPackageDefaults(s.CattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, s.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.CattleConfig, clusterConfig)

	s.ClusterConfig = clusterConfig

	awsEC2Configs := new(ec2.AWSEC2Configs)
	operations.LoadObjectFromMap(ec2.ConfigurationFileKey, s.CattleConfig, awsEC2Configs)

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, s.CattleConfig, rancherConfig)

	s.rancherConfig = rancherConfig

	awsCredsConfig := new(awsCredentialsConfig)
	operations.LoadObjectFromMap("awsCredentials", s.CattleConfig, awsCredsConfig)

	s.AWSAccessKey = awsCredsConfig.AccessKey
	s.AWSSecretKey = awsCredsConfig.SecretKey

	workloadConfigs := new(workloads.Workloads)
	operations.LoadObjectFromMap(workloads.WorkloadsConfigurationFileKey, s.CattleConfig, workloadConfigs)

	s.WorkloadsConfig = workloadConfigs

	if rancherConfig.ClusterName == "" {
		provider := provisioning.CreateProvider(clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(s.CattleConfig)

		if isS3 {
			credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
			cloudCredential, err := provider.CloudCredFunc(standardUserClient, credentialSpec)
			require.NoError(t, err)

			s.S3CloudCredName = cloudCredential.Namespace + ":" + cloudCredential.Name
			s.S3Region = awsCredsConfig.DefaultRegion
			s.S3Endpoint = fmt.Sprintf("s3.%s.amazonaws.com", s.S3Region)
			s.S3BucketName = fmt.Sprintf("snapshot-restore-s3-%d-%s", time.Now().Unix(), namegenerator.RandStringLower(5))

			err = etcdsnapshot.CreateS3Bucket(s.S3BucketName, s.S3Region, awsCredsConfig.AccessKey, awsCredsConfig.SecretKey)
			require.NoError(t, err)

			s.CreatedTestBucket = true

			clusterConfig.ETCD = &rkev1.ETCD{
				SnapshotRetention:    5,
				SnapshotScheduleCron: "0 */5 * * *",
				S3: &rkev1.ETCDSnapshotS3{
					Bucket:              s.S3BucketName,
					CloudCredentialName: s.S3CloudCredName,
					Endpoint:            s.S3Endpoint,
					Region:              s.S3Region,
					SkipSSLVerify:       true,
				},
			}
		}

		if isWindows {
			nodeRolesStandard := []provisioninginput.MachinePools{
				provisioninginput.EtcdMachinePool,
				provisioninginput.ControlPlaneMachinePool,
				provisioninginput.WorkerMachinePool,
				provisioninginput.WindowsMachinePool,
			}

			nodeRolesStandard[0].MachinePoolConfig.Quantity = 1
			nodeRolesStandard[1].MachinePoolConfig.Quantity = 1
			nodeRolesStandard[2].MachinePoolConfig.Quantity = 1
			nodeRolesStandard[3].MachinePoolConfig.Quantity = 1

			logrus.Info("Provisioning RKE2 windows cluster")
			s.Cluster, err = resources.ProvisionRKE2K3SCluster(t, standardUserClient, defaults.RKE2, provider, *clusterConfig, machineConfigSpec, awsEC2Configs, true, true)
			require.NoError(t, err)
		} else {
			logrus.Infof("Provisioning %s cluster", clusterType)
			s.Cluster, err = resources.ProvisionRKE2K3SCluster(t, standardUserClient, clusterType, provider, *clusterConfig, machineConfigSpec, nil, false, false)
			require.NoError(t, err)
		}
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		s.Cluster, err = s.Client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + s.rancherConfig.ClusterName)
		require.NoError(t, err)
	}

	clusterStatus := &provv1.ClusterStatus{}
	err = v1.ConvertToK8sType(s.Cluster.Status, clusterStatus)
	require.NoError(t, err)

	s.WorkloadClient, err = s.Client.Steve.ProxyDownstream(clusterStatus.ClusterName)
	require.NoError(t, err)

	return s
}

// CreateSnapshotDeployment is a helper function to create a deployment on a downstream cluster.
func CreateSnapshotDeployment(client *rancher.Client, workloadClient *v1.Client, clusterID, deploymentName string, workloadsConfig *workloads.Workloads) error {
	_, namespace, err := projectsapi.CreateProjectAndNamespace(client, clusterID)
	if err != nil {
		return err
	}

	deploymentConfig := workloadsConfig.Deployment.DeepCopy()
	deploymentConfig.ObjectMeta.Namespace = namespace.Name
	deploymentConfig.ObjectMeta.Name = deploymentName
	deploymentConfig.ObjectMeta.GenerateName = ""

	if workloadsConfig.IsWindows {
		deploymentConfig.Spec.Template.Spec.Containers[0].Image = WindowsContainerImage
	} else {
		deploymentConfig.Spec.Template.Spec.Containers[0].Image = ContainerImage
	}

	createdDeployment, err := deployment.CreateDeploymentFromConfig(workloadClient, clusterID, deploymentConfig)
	if err != nil {
		return err
	}

	err = deployment.VerifyDeployment(client, clusterID, createdDeployment.Namespace, createdDeployment.Name)
	if err != nil {
		return err
	}

	return nil
}
