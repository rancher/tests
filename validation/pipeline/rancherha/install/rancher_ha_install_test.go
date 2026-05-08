//go:build rancherinstall

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/interoperability/qainfraautomation"
	qaconfig "github.com/rancher/tests/interoperability/qainfraautomation/config"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RancherHAInstallTestSuite struct {
	suite.Suite
	session        *session.Session
	infraCfg       *qaconfig.Config
	kubeconfigPath string
	fqdn           string
}

func (s *RancherHAInstallTestSuite) SetupSuite() {
	s.session = session.NewSession()

	cattleConfig := config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	s.infraCfg = new(qaconfig.Config)
	operations.LoadObjectFromMap(qaconfig.ConfigurationFileKey, cattleConfig, s.infraCfg)

	rancherInstallCfg := s.infraCfg.RancherInstall
	require.NotNil(s.T(), rancherInstallCfg, "qaInfraAutomation.rancherInstall config is required")

	if rancherInstallCfg.ExistingKubeconfig != "" {
		logrus.Infof("[rancherha] using existing kubeconfig: %s", rancherInstallCfg.ExistingKubeconfig)
		s.kubeconfigPath = rancherInstallCfg.ExistingKubeconfig
	} else {
		result := s.provisionCluster()
		s.kubeconfigPath = result.KubeconfigPath
		s.fqdn = result.FQDN

		if result.FQDN != "" {
			logrus.Infof("[rancherha] using Route53 FQDN as hostname: %s", result.FQDN)
		}
	}

	absKubeconfig, err := filepath.Abs(s.kubeconfigPath)
	require.NoError(s.T(), err, "failed to resolve absolute path for kubeconfig %s", s.kubeconfigPath)
	prevKubeconfig := os.Getenv("KUBECONFIG")
	os.Setenv("KUBECONFIG", absKubeconfig)
	logrus.Infof("[rancherha] KUBECONFIG set to %s", absKubeconfig)
	s.T().Cleanup(func() {
		if prevKubeconfig != "" {
			os.Setenv("KUBECONFIG", prevKubeconfig)
		} else {
			os.Unsetenv("KUBECONFIG")
		}
	})
}

func (s *RancherHAInstallTestSuite) provisionCluster() qainfraautomation.StandaloneClusterResult {
	s.T().Helper()

	require.NotNil(s.T(), s.infraCfg.StandaloneCluster,
		"qaInfraAutomation.standaloneCluster config is required when existingKubeconfig is not set")

	k8sVersion := s.infraCfg.StandaloneCluster.KubernetesVersion
	require.NotEmpty(s.T(), k8sVersion, "qaInfraAutomation.standaloneCluster.kubernetesVersion is required")

	var clusterType string
	switch {
	case strings.Contains(k8sVersion, "+k3s"):
		clusterType = "k3s"
	case strings.Contains(k8sVersion, "+rke2"):
		clusterType = "rke2"
	default:
		s.T().Fatalf("cannot determine cluster type from kubernetesVersion %q: must contain +k3s or +rke2", k8sVersion)
	}

	switch clusterType {
	case "rke2":
		logrus.Info("[rancherha] provisioning AWS RKE2 standalone cluster")
		return qainfraautomation.ProvisionAWSRKE2Cluster(
			s.T(),
			s.infraCfg,
			s.infraCfg.StandaloneCluster,
		)
	default:
		logrus.Info("[rancherha] provisioning AWS K3s standalone cluster")
		return qainfraautomation.ProvisionAWSK3SCluster(
			s.T(),
			s.infraCfg,
			s.infraCfg.StandaloneCluster,
		)
	}
}

func (s *RancherHAInstallTestSuite) TestInstallRancherHA() {
	s.T().Log("Installing Rancher via Ansible playbook")
	result := qainfraautomation.InstallRancher(
		s.T(),
		s.infraCfg,
		s.kubeconfigPath,
		s.fqdn,
	)

	logrus.Infof("[rancherha] Rancher installed at https://%s", result.FQDN)
	logrus.Infof("[rancherha] Admin token: %s", result.AdminToken)

	s.T().Log("Validating Rancher API access")
	insecure := true
	cleanup := s.infraCfg.RancherInstall.Cleanup == nil || *s.infraCfg.RancherInstall.Cleanup
	rancherCfg := &rancher.Config{
		Host:       result.FQDN,
		AdminToken: result.AdminToken,
		Insecure:   &insecure,
		Cleanup:    &cleanup,
	}

	adminClient, err := rancher.NewClientForConfig(result.AdminToken, rancherCfg, s.session)
	require.NoError(s.T(), err, "failed to create rancher client")

	s.T().Log("Validating local cluster is accessible")
	_, err = adminClient.Steve.SteveType("provisioning.cattle.io.cluster").ByID("fleet-local/local")
	require.NoError(s.T(), err, "failed to access local cluster via Rancher API")

	s.T().Log("Rancher HA installation completed and validated successfully")
}

func (s *RancherHAInstallTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func TestRancherHAInstallTestSuite(t *testing.T) {
	suite.Run(t, new(RancherHAInstallTestSuite))
}
