//go:build validation || recurring

package rke2

import (
	"os"
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevestates"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/steve"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	actionsDefaults "github.com/rancher/tests/actions/config/defaults"
	configDefaults "github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/reports"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

const (
	localCluster          = "local"
	templateTestConfigKey = "templateTest"
)

type templateTest struct {
	client             *rancher.Client
	standardUserClient *rancher.Client
	session            *session.Session
	templateConfig     *provisioninginput.TemplateConfig
	cloudCredentials   *v1.SteveAPIObject
	cattleConfig       map[string]any
}

func templateSetup(t *testing.T) templateTest {
	var r templateTest
	testSession := session.NewSession()
	r.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	r.cattleConfig, err = configDefaults.LoadPackageDefaults(r.cattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, r.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	r.templateConfig = new(provisioninginput.TemplateConfig)
	operations.LoadObjectFromMap(templateTestConfigKey, r.cattleConfig, r.templateConfig)

	provider := provisioning.CreateProvider(r.templateConfig.TemplateProvider)
	cloudCredentialConfig := cloudcredentials.LoadCloudCredential(r.templateConfig.TemplateProvider)
	r.cloudCredentials, err = provider.CloudCredFunc(client, cloudCredentialConfig)
	require.NoError(t, err)

	r.standardUserClient, _, _, err = standard.CreateStandardUser(r.client)
	require.NoError(t, err)

	return r
}

func TestTemplate(t *testing.T) {
	t.Parallel()
	r := templateSetup(t)

	tests := []struct {
		name   string
		client *rancher.Client
	}{
		{"RKE2_Template|etcd|cp|worker", r.standardUserClient},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			r.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := steve.CreateAndWaitForResource(r.client, namespaces.FleetLocal+"/"+localCluster, stevetypes.ClusterRepo, r.templateConfig.Repo, stevestates.Active, 5*time.Second, defaults.FiveMinuteTimeout)
			require.NoError(t, err)

			k8sversions, err := kubernetesversions.Default(r.client, actionsDefaults.RKE2, nil)
			require.NoError(t, err)

			clusterName := namegenerator.AppendRandomString(actionsDefaults.RKE2 + "-template")

			logrus.Infof("Provisioning template cluster (%s)", clusterName)
			err = charts.InstallTemplateChart(r.client, r.templateConfig.Repo.ObjectMeta.Name, r.templateConfig.TemplateName, clusterName, k8sversions[0], r.cloudCredentials)
			require.NoError(t, err)

			_, cluster, err := clusters.GetProvisioningClusterByName(r.client, clusterName, namespaces.FleetDefault)
			reports.TimeoutClusterReport(cluster, err)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(r.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(tt.client, cluster)
			if err != nil {
				logrus.Warningf("Deployment verification received an error: %v", err)
			}

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(r.client, cluster)
			require.NoError(t, err)
		})
	}
}
