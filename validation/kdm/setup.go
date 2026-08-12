package kdm

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/kdm"
	"github.com/rancher/tests/actions/logging"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	tfpConfig "github.com/rancher/tfp-automation/config"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type kdmTest struct {
	suite.Suite
	Session            *session.Session
	Client             *rancher.Client
	StandardUserClient *rancher.Client
	CattleConfig       map[string]any
	ClusterConfig      *clusters.ClusterConfig
	rancherConfig      *rancher.Config
	Cluster            *v1.SteveAPIObject
}

func Setup(t *testing.T, clusterType string) *kdmTest {
	k := &kdmTest{}

	testSession := session.NewSession()
	k.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	k.Client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(k.Client)
	require.NoError(t, err)

	k.StandardUserClient = standardUserClient

	k.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))
	_, _, _, standaloneConfig := tfpConfig.LoadTFPConfigs(k.CattleConfig)

	k.CattleConfig, err = defaults.LoadPackageDefaults(k.CattleConfig, "")
	require.NoError(t, err)

	setting, err := client.Management.Setting.ByID("rke-metadata-config")
	require.NoError(t, err)

	rawKDM := setting.Value

	if strings.HasPrefix(rawKDM, "{") {
		var m map[string]string

		err := json.Unmarshal([]byte(rawKDM), &m)
		require.NoError(t, err)

		rawKDM = m["url"]
	}

	kdmURL := rawKDM

	kdmVersions, err := kdm.VerifyKDMUrl(kdmURL, standaloneConfig.RancherTagVersion)
	require.NoError(t, err)

	rke2Versions, err := kubernetesversions.ListRKE2AllVersions(k.Client)
	require.NoError(t, err)

	k3sVersions, err := kubernetesversions.ListK3SAllVersions(k.Client)
	require.NoError(t, err)

	err = kdm.VerifyKDMVersions(kdmVersions, rke2Versions, defaults.RKE2)
	require.NoError(t, err)

	err = kdm.VerifyKDMVersions(kdmVersions, k3sVersions, defaults.K3S)
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, k.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	k.CattleConfig, err = defaults.SetK8sDefault(k.Client, clusterType, k.CattleConfig)
	require.NoError(t, err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, k.CattleConfig, clusterConfig)

	k.ClusterConfig = clusterConfig

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, k.CattleConfig, rancherConfig)

	k.rancherConfig = rancherConfig

	return k
}
