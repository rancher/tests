//go:build validation || recurring

package rke2

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/users"
	password "github.com/rancher/shepherd/extensions/users/passwordgenerator"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/reports"
	cis "github.com/rancher/tests/validation/provisioning/resources/cisbenchmark"
	packageDefaults "github.com/rancher/tests/validation/provisioning/rke2/defaults"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

type hardenedTest struct {
	client              *rancher.Client
	session             *session.Session
	standardUserClient  *rancher.Client
	cattleConfig        map[string]any
	project             *management.Project
	chartInstallOptions *charts.InstallOptions
}

func hardenedSetup(t *testing.T) hardenedTest {
	var r hardenedTest
	testSession := session.NewSession()
	r.session = testSession

	client, err := rancher.NewClient("", testSession)
	assert.NoError(t, err)

	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	r.cattleConfig, err = packageDefaults.LoadPackageDefaults(r.cattleConfig, "")
	assert.NoError(t, err)

	r.cattleConfig, err = defaults.SetK8sDefault(client, "rke2", r.cattleConfig)
	assert.NoError(t, err)

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
	assert.NoError(t, err)

	newUser.Password = user.Password

	standardUserClient, err := client.AsUser(newUser)
	assert.NoError(t, err)

	r.standardUserClient = standardUserClient

	return r
}

func TestHardened(t *testing.T) {
	t.Parallel()
	r := hardenedSetup(t)
	nodeRolesStandard := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	tests := []struct {
		name            string
		client          *rancher.Client
		machinePools    []provisioninginput.MachinePools
		scanProfileName string
	}{
		{"RKE2_CIS_1.9_Profile|3_etcd|2_cp|3_worker", r.client, nodeRolesStandard, "rke2-cis-1.9-profile"},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Info("Running cleanup")
			r.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)
			clusterConfig.MachinePools = tt.machinePools
			clusterConfig.Hardened = true

			externalNodeProvider := provisioning.ExternalNodeProviderSetup(clusterConfig.NodeProvider)

			awsEC2Configs := new(ec2.AWSEC2Configs)
			operations.LoadObjectFromMap(ec2.ConfigurationFileKey, r.cattleConfig, awsEC2Configs)

			clusterObject, err := provisioning.CreateProvisioningCustomCluster(tt.client, &externalNodeProvider, clusterConfig, awsEC2Configs)
			reports.TimeoutClusterReport(clusterObject, err)
			assert.NoError(t, err)

			provisioning.VerifyCluster(t, tt.client, clusterConfig, clusterObject)

			cluster, err := extensionscluster.NewClusterMeta(tt.client, clusterObject.Name)
			reports.TimeoutClusterReport(clusterObject, err)
			assert.NoError(t, err)

			latestCISBenchmarkVersion, err := tt.client.Catalog.GetLatestChartVersion(charts.CISBenchmarkName, catalog.RancherChartRepo)
			assert.NoError(t, err)

			project, err := projects.GetProjectByName(tt.client, cluster.ID, cis.System)
			reports.TimeoutClusterReport(clusterObject, err)
			assert.NoError(t, err)

			r.project = project
			assert.NotEmpty(t, r.project)

			r.chartInstallOptions = &charts.InstallOptions{
				Cluster:   cluster,
				Version:   latestCISBenchmarkVersion,
				ProjectID: r.project.ID,
			}

			cis.SetupCISBenchmarkChart(tt.client, r.project.ClusterID, r.chartInstallOptions, charts.CISBenchmarkNamespace)
			cis.RunCISScan(tt.client, r.project.ClusterID, tt.scanProfileName)
		})

		params := provisioning.GetCustomSchemaParams(tt.client, r.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}
