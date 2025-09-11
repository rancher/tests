//go:build (validation || sanity) && !infra.any && !infra.aks && !infra.eks && !infra.rke2k3s && !infra.gke && !infra.rke1 && !cluster.any && !cluster.custom && !cluster.nodedriver && !extended && !stress

package rke1

import (
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/extensions/users"
	password "github.com/rancher/shepherd/extensions/users/passwordgenerator"
	"github.com/rancher/shepherd/pkg/config"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/reports"
	cis "github.com/rancher/tests/validation/provisioning/resources/cisbenchmark"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type HardenedRKE1ClusterProvisioningTestSuite struct {
	suite.Suite
	client              *rancher.Client
	session             *session.Session
	standardUserClient  *rancher.Client
	provisioningConfig  *provisioninginput.Config
	project             *management.Project
	chartInstallOptions *charts.InstallOptions
}

func (c *HardenedRKE1ClusterProvisioningTestSuite) TearDownSuite() {
	c.session.Cleanup()
}

func (c *HardenedRKE1ClusterProvisioningTestSuite) SetupSuite() {
	testSession := session.NewSession()
	c.session = testSession

	c.provisioningConfig = new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, c.provisioningConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(c.T(), err)

	c.client = client

	if c.provisioningConfig.RKE1KubernetesVersions == nil {
		rke1Versions, err := kubernetesversions.Default(c.client, extensionscluster.RKE1ClusterType.String(), nil)
		require.NoError(c.T(), err)

		c.provisioningConfig.RKE1KubernetesVersions = rke1Versions
	} else if c.provisioningConfig.RKE1KubernetesVersions[0] == "all" {
		rke1Versions, err := kubernetesversions.ListRKE1AllVersions(c.client)
		require.NoError(c.T(), err)

		c.provisioningConfig.RKE1KubernetesVersions = rke1Versions
	}

	enabled := true
	var testuser = namegen.AppendRandomString("testuser-")
	var testpassword = password.GenerateUserPassword("testpass-")
	user := &management.User{
		Username: testuser,
		Password: testpassword,
		Name:     testuser,
		Enabled:  &enabled,
	}

	newUser, err := users.CreateUserWithRole(client, user, "user")
	require.NoError(c.T(), err)

	newUser.Password = user.Password

	standardUserClient, err := client.AsUser(newUser)
	require.NoError(c.T(), err)

	c.standardUserClient = standardUserClient
}

func (c *HardenedRKE1ClusterProvisioningTestSuite) TestProvisioningRKE1HardenedCluster() {
	nodeRolesDedicated := []provisioninginput.NodePools{provisioninginput.EtcdNodePool, provisioninginput.ControlPlaneNodePool, provisioninginput.WorkerNodePool}

	tests := []struct {
		name            string
		client          *rancher.Client
		nodePools       []provisioninginput.NodePools
		scanProfileName string
	}{
		{"RKE1 CIS 1.8 Profile Hardened " + provisioninginput.StandardClientName.String(), c.standardUserClient, nodeRolesDedicated, "rke-profile-hardened-1.8"},
		{"RKE1 CIS 1.8 Profile Permissive " + provisioninginput.StandardClientName.String(), c.standardUserClient, nodeRolesDedicated, "rke-profile-permissive-1.8"},
	}
	for _, tt := range tests {
		c.Run(tt.name, func() {
			provisioningConfig := *c.provisioningConfig
			provisioningConfig.NodePools = tt.nodePools
			provisioningConfig.Hardened = true

			nodeProviders := provisioningConfig.NodeProviders[0]
			externalNodeProvider := provisioning.ExternalNodeProviderSetup(nodeProviders)

			testConfig := clusters.ConvertConfigToClusterConfig(&provisioningConfig)
			testConfig.KubernetesVersion = c.provisioningConfig.RKE1KubernetesVersions[0]

			awsEC2Configs := new(ec2.AWSEC2Configs)
			config.LoadConfig(ec2.ConfigurationFileKey, awsEC2Configs)

			clusterObject, _, err := provisioning.CreateProvisioningRKE1CustomCluster(tt.client, &externalNodeProvider, testConfig, awsEC2Configs)
			reports.TimeoutRKEReport(clusterObject, err)
			require.NoError(c.T(), err)

			provisioning.VerifyRKE1Cluster(c.T(), tt.client, testConfig, clusterObject)

			clusterMeta, err := extensionscluster.NewClusterMeta(tt.client, clusterObject.Name)
			reports.TimeoutRKEReport(clusterObject, err)
			require.NoError(c.T(), err)

			latestCISBenchmarkVersion, err := tt.client.Catalog.GetLatestChartVersion(charts.CISBenchmarkName, catalog.RancherChartRepo)
			require.NoError(c.T(), err)

			project, err := projects.GetProjectByName(tt.client, clusterMeta.ID, cis.System)
			reports.TimeoutRKEReport(clusterObject, err)
			require.NoError(c.T(), err)

			c.project = project
			require.NotEmpty(c.T(), c.project)

			c.chartInstallOptions = &charts.InstallOptions{
				Cluster:   clusterMeta,
				Version:   latestCISBenchmarkVersion,
				ProjectID: c.project.ID,
			}

			chartName := charts.CISBenchmarkName
			chartNamespace := charts.CISBenchmarkNamespace
			if provisioningConfig.Compliance {
				chartName = charts.ComplianceName
				chartNamespace = charts.ComplianceNamespace
			}
			logrus.Infof("Setting up %s on cluster (%s)", chartName, clusterMeta.Name)
			cis.SetupHardenedChart(tt.client, c.project.ClusterID, c.chartInstallOptions, chartName, chartNamespace)

			logrus.Infof("Running CIS scan on cluster (%s)", clusterMeta.Name)
			cis.RunCISScan(tt.client, c.project.ClusterID, tt.scanProfileName)
		})
	}
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestHardenedRKE1ClusterProvisioningTestSuite(t *testing.T) {
	suite.Run(t, new(HardenedRKE1ClusterProvisioningTestSuite))
}
