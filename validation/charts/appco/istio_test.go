package appco

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	extencharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/kubectl"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/kubeapi/namespaces"
	"github.com/rancher/tests/actions/workloads/job"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	istioSecret = "application-collection"
	//Get it from env var
	username = ""
	//Get it from env var
	accessToken = ""
	pilotImage  = "dp.apps.rancher.io/containers/pilot:1.25.3"
)

type IstioTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *clusters.ClusterMeta
}

func (i *IstioTestSuite) TearDownSuite() {
	i.session.Cleanup()
}

func (i *IstioTestSuite) SetupSuite() {
	testSession := session.NewSession()
	i.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(i.T(), err)

	i.client = client

	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(i.T(), clusterName, "Cluster name to install is not set")

	cluster, err := clusters.NewClusterMeta(client, clusterName)
	require.NoError(i.T(), err)

	i.cluster = cluster

	i.T().Logf("Creating %s namespace", charts.RancherIstioNamespace)
	_, err = namespaces.CreateNamespace(i.client, i.cluster.ID, namegen.AppendRandomString("testns"), charts.RancherIstioNamespace, "{}", map[string]string{}, map[string]string{})
	require.NoError(i.T(), err)

	i.T().Logf("Creating %s secret", istioSecret)
	secretCommand := strings.Split(fmt.Sprintf("kubectl create secret docker-registry %s --docker-server=dp.apps.rancher.io --docker-username=%s --docker-password=%s -n %s", istioSecret, username, accessToken, charts.RancherIstioNamespace), " ")
	logCmd, err := kubectl.Command(client, nil, i.cluster.ID, secretCommand, "")
	require.NoError(i.T(), err)
	require.Equal(i.T(), fmt.Sprintf("secret/%s created\n", istioSecret), logCmd)

	i.T().Log("Creating job to pull pilot image")
	container := corev1.Container{
		Name:            namegen.AppendRandomString("pilot"),
		Image:           pilotImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		VolumeMounts:    nil,
	}

	podTemplate := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:    make(map[string]string),
			Namespace: charts.RancherIstioNamespace,
		},
		Spec: corev1.PodSpec{
			Containers:    []corev1.Container{container},
			RestartPolicy: corev1.RestartPolicyNever,
			Volumes:       nil,
			ImagePullSecrets: []corev1.LocalObjectReference{
				corev1.LocalObjectReference{
					Name: istioSecret,
				}},
			NodeSelector: nil,
		},
	}

	_, err = job.CreateJob(i.client, i.cluster.ID, charts.RancherIstioNamespace, podTemplate, false)
	require.NoError(i.T(), err)
}

func (i *IstioTestSuite) TestIstioSideCarInstallation() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	i.T().Log("Building SideCar AppCo command")
	appCoCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`helm registry login dp.apps.rancher.io -u %s -p %s && helm install %s oci://dp.apps.rancher.io/charts/istio -n %s --set global.imagePullSecrets={%s}`, username, accessToken, charts.RancherIstioName, charts.RancherIstioNamespace, istioSecret),
	}

	i.T().Log("Running SideCar AppCo command")
	logCmd, err := kubectl.Command(client, nil, i.cluster.ID, appCoCommand, "1MB")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))

	i.T().Log("Checking if the istio chart is installed")
	istioChart, err := extencharts.GetChartStatus(client, i.cluster.ID, charts.RancherIstioNamespace, charts.RancherIstioName)
	require.NoError(i.T(), err)
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	helmUninstallCommand := fmt.Sprintf(`helm uninstall %s -n %s`, charts.RancherIstioName, charts.RancherIstioNamespace)
	deleteConfigurationCommand := `kubectl delete mutatingwebhookconfiguration istio-sidecar-injector`
	deleteCustomDefinationCommand := `kubectl delete $(kubectl get CustomResourceDefinition -l='app.kubernetes.io/part-of=istio' -o name -A)`

	i.T().Log("Building uninstall command")
	uninstallCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`%s && %s && %s`, helmUninstallCommand, deleteConfigurationCommand, deleteCustomDefinationCommand),
	}

	i.T().Log("Running uninstall command")
	_, err = kubectl.Command(client, nil, i.cluster.ID, uninstallCommand, "2MB")
	require.NoError(i.T(), err)

	i.T().Log("Checking if the istio chart is uninstalled")
	istioChart, err = extencharts.GetChartStatus(client, i.cluster.ID, charts.RancherIstioNamespace, charts.RancherIstioName)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestIstioAmbientInstallation() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	ambientCommand := `--set cni.enabled=true,ztunnel.enabled=true --set istiod.cni.enabled=true --set cni.profile=ambient,istiod.profile=ambient,ztunnel.profile=ambient`
	i.T().Log("Building Ambient AppCo command")
	appCoCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`helm registry login dp.apps.rancher.io -u %s -p %s && helm install %s oci://dp.apps.rancher.io/charts/istio -n %s --set global.imagePullSecrets={%s} %s`, username, accessToken, charts.RancherIstioName, charts.RancherIstioNamespace, istioSecret, ambientCommand),
	}

	i.T().Log("Running Ambient AppCo command")
	logCmd, err := kubectl.Command(client, nil, i.cluster.ID, appCoCommand, "1MB")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))

	i.T().Log("Checking if the istio chart is installed")
	istioChart, err := extencharts.GetChartStatus(client, i.cluster.ID, charts.RancherIstioNamespace, charts.RancherIstioName)
	require.NoError(i.T(), err)
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	helmUninstallCommand := fmt.Sprintf(`helm uninstall %s -n %s`, charts.RancherIstioName, charts.RancherIstioNamespace)
	deleteConfigurationCommand := `kubectl delete mutatingwebhookconfiguration istio-sidecar-injector`
	deleteCustomDefinationCommand := `kubectl delete $(kubectl get CustomResourceDefinition -l='app.kubernetes.io/part-of=istio' -o name -A)`

	i.T().Log("Building uninstall command")
	uninstallCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`%s && %s && %s`, helmUninstallCommand, deleteConfigurationCommand, deleteCustomDefinationCommand),
	}

	i.T().Log("Running uninstall command")
	_, err = kubectl.Command(client, nil, i.cluster.ID, uninstallCommand, "2MB")
	require.NoError(i.T(), err)

	i.T().Log("Checking if the istio chart is uninstalled")
	istioChart, err = extencharts.GetChartStatus(client, i.cluster.ID, charts.RancherIstioNamespace, charts.RancherIstioName)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestIstioGatewayStandaloneInstallation() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	gatewayCommand := `--set base.enabled=false,istiod.enabled=false --set gateway.enabled=true`
	i.T().Log("Building Gateway AppCo command")
	appCoCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`helm registry login dp.apps.rancher.io -u %s -p %s && helm install %s oci://dp.apps.rancher.io/charts/istio -n %s --set global.imagePullSecrets={%s} %s`, username, accessToken, charts.RancherIstioName, charts.RancherIstioNamespace, istioSecret, gatewayCommand),
	}

	i.T().Log("Running Gateway AppCo command")
	logCmd, err := kubectl.Command(client, nil, i.cluster.ID, appCoCommand, "1MB")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))

	i.T().Log("Checking if the istio chart is installed")
	istioChart, err := extencharts.GetChartStatus(client, i.cluster.ID, charts.RancherIstioNamespace, charts.RancherIstioName)
	require.NoError(i.T(), err)
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	helmUninstallCommand := fmt.Sprintf(`helm uninstall %s -n %s`, charts.RancherIstioName, charts.RancherIstioNamespace)
	deleteConfigurationCommand := `kubectl delete mutatingwebhookconfiguration istio-sidecar-injector`
	deleteCustomDefinationCommand := `kubectl delete $(kubectl get CustomResourceDefinition -l='app.kubernetes.io/part-of=istio' -o name -A)`

	i.T().Log("Building uninstall command")
	uninstallCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`%s && %s && %s`, helmUninstallCommand, deleteConfigurationCommand, deleteCustomDefinationCommand),
	}

	i.T().Log("Running uninstall command")
	_, err = kubectl.Command(client, nil, i.cluster.ID, uninstallCommand, "2MB")
	require.NoError(i.T(), err)

	i.T().Log("Checking if the istio chart is uninstalled")
	istioChart, err = extencharts.GetChartStatus(client, i.cluster.ID, charts.RancherIstioNamespace, charts.RancherIstioName)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestIstioGatewayDifferentNamespaceInstallation() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	gatewayCommand := `--set gateway.enabled=true,gateway.namespaceOverride=default`
	i.T().Log("Building Gateway AppCo command")
	appCoCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`helm registry login dp.apps.rancher.io -u %s -p %s && helm install %s oci://dp.apps.rancher.io/charts/istio -n %s --set global.imagePullSecrets={%s} %s`, username, accessToken, charts.RancherIstioName, charts.RancherIstioNamespace, istioSecret, gatewayCommand),
	}

	i.T().Log("Running Gateway AppCo command")
	logCmd, err := kubectl.Command(client, nil, i.cluster.ID, appCoCommand, "1MB")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))

	i.T().Log("Checking if the istio chart is installed")
	istioChart, err := extencharts.GetChartStatus(client, i.cluster.ID, charts.RancherIstioNamespace, charts.RancherIstioName)
	require.NoError(i.T(), err)
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	helmUninstallCommand := fmt.Sprintf(`helm uninstall %s -n %s`, charts.RancherIstioName, charts.RancherIstioNamespace)
	deleteConfigurationCommand := `kubectl delete mutatingwebhookconfiguration istio-sidecar-injector`
	deleteCustomDefinationCommand := `kubectl delete $(kubectl get CustomResourceDefinition -l='app.kubernetes.io/part-of=istio' -o name -A)`

	i.T().Log("Building uninstall command")
	uninstallCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`%s && %s && %s`, helmUninstallCommand, deleteConfigurationCommand, deleteCustomDefinationCommand),
	}

	i.T().Log("Running uninstall command")
	_, err = kubectl.Command(client, nil, i.cluster.ID, uninstallCommand, "2MB")
	require.NoError(i.T(), err)

	i.T().Log("Checking if the istio chart is uninstalled")
	istioChart, err = extencharts.GetChartStatus(client, i.cluster.ID, charts.RancherIstioNamespace, charts.RancherIstioName)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestIstioCanaryUpgrade() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	i.T().Log("Building SideCar AppCo command")
	appCoCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`helm registry login dp.apps.rancher.io -u %s -p %s && helm install %s oci://dp.apps.rancher.io/charts/istio -n %s --set global.imagePullSecrets={%s}`, username, accessToken, charts.RancherIstioName, charts.RancherIstioNamespace, istioSecret),
	}

	i.T().Log("Running SideCar AppCo command")
	logCmd, err := kubectl.Command(client, nil, i.cluster.ID, appCoCommand, "1MB")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))

	i.T().Log("Checking if the istio chart is installed")
	istioChart, err := extencharts.GetChartStatus(client, i.cluster.ID, charts.RancherIstioNamespace, charts.RancherIstioName)
	require.NoError(i.T(), err)
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Building Canary upgrade command")
	canaryUpgradeCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`helm registry login dp.apps.rancher.io -u %s -p %s && helm upgrade %s oci://dp.apps.rancher.io/charts/istio -n %s --set global.imagePullSecrets={%s} --set istiod.revision=canary,base.defaultRevision=canary`, username, accessToken, charts.RancherIstioName, charts.RancherIstioNamespace, istioSecret),
	}

	i.T().Log("Running Canary upgrade command")
	logCmd, err = kubectl.Command(client, nil, i.cluster.ID, canaryUpgradeCommand, "2MB")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))

	i.T().Log("Checking if the istio chart is installed")
	istioChart, err = extencharts.GetChartStatus(client, i.cluster.ID, charts.RancherIstioNamespace, charts.RancherIstioName)
	require.NoError(i.T(), err)
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Verifying if istio-ingress gateway is using the canary revision")
	getCanaryCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`kubectl -n %s get pod -o jsonpath='{.items..metadata.name}'`, charts.RancherIstioNamespace),
	}

	appName := "istiod-canary"
	logCmd, err = kubectl.Command(client, nil, i.cluster.ID, getCanaryCommand, "2MB")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, appName))

	helmUninstallCommand := fmt.Sprintf(`helm uninstall %s -n %s`, charts.RancherIstioName, charts.RancherIstioNamespace)
	deleteConfigurationCommand := `kubectl delete mutatingwebhookconfiguration istio-sidecar-injector`
	deleteCustomDefinationCommand := `kubectl delete $(kubectl get CustomResourceDefinition -l='app.kubernetes.io/part-of=istio' -o name -A)`

	i.T().Log("Building uninstall command")
	uninstallCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`%s && %s && %s`, helmUninstallCommand, deleteConfigurationCommand, deleteCustomDefinationCommand),
	}

	i.T().Log("Running uninstall command")
	_, err = kubectl.Command(client, nil, i.cluster.ID, uninstallCommand, "2MB")
	require.NoError(i.T(), err)

	i.T().Log("Checking if the istio chart is uninstalled")
	istioChart, err = extencharts.GetChartStatus(client, i.cluster.ID, charts.RancherIstioNamespace, charts.RancherIstioName)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)

}

func (i *IstioTestSuite) TestIstioInPlaceUpgrade() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	i.T().Log("Building SideCar AppCo command")
	appCoCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`helm registry login dp.apps.rancher.io -u %s -p %s && helm install %s oci://dp.apps.rancher.io/charts/istio -n %s --set global.imagePullSecrets={%s}`, username, accessToken, charts.RancherIstioName, charts.RancherIstioNamespace, istioSecret),
	}

	i.T().Log("Running SideCar AppCo command")
	logCmd, err := kubectl.Command(client, nil, i.cluster.ID, appCoCommand, "1MB")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))

	i.T().Log("Checking if the istio chart is installed")
	istioChart, err := extencharts.GetChartStatus(client, i.cluster.ID, charts.RancherIstioNamespace, charts.RancherIstioName)
	require.NoError(i.T(), err)
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Building In Place upgrade command")
	canaryUpgradeCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`helm registry login dp.apps.rancher.io -u %s -p %s && helm upgrade %s oci://dp.apps.rancher.io/charts/istio -n %s --set global.imagePullSecrets={%s}`, username, accessToken, charts.RancherIstioName, charts.RancherIstioNamespace, istioSecret),
	}

	i.T().Log("Running In Place upgrade command")
	logCmd, err = kubectl.Command(client, nil, i.cluster.ID, canaryUpgradeCommand, "2MB")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))

	i.T().Log("Checking if the istio chart is installed")
	istioChart, err = extencharts.GetChartStatus(client, i.cluster.ID, charts.RancherIstioNamespace, charts.RancherIstioName)
	require.NoError(i.T(), err)
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	helmUninstallCommand := fmt.Sprintf(`helm uninstall %s -n %s`, charts.RancherIstioName, charts.RancherIstioNamespace)
	deleteConfigurationCommand := `kubectl delete mutatingwebhookconfiguration istio-sidecar-injector`
	deleteCustomDefinationCommand := `kubectl delete $(kubectl get CustomResourceDefinition -l='app.kubernetes.io/part-of=istio' -o name -A)`

	i.T().Log("Building uninstall command")
	uninstallCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`%s && %s && %s`, helmUninstallCommand, deleteConfigurationCommand, deleteCustomDefinationCommand),
	}

	i.T().Log("Running uninstall command")
	_, err = kubectl.Command(client, nil, i.cluster.ID, uninstallCommand, "2MB")
	require.NoError(i.T(), err)

	i.T().Log("Checking if the istio chart is uninstalled")
	istioChart, err = extencharts.GetChartStatus(client, i.cluster.ID, charts.RancherIstioNamespace, charts.RancherIstioName)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)
}

func TestIstioTestSuite(t *testing.T) {
	suite.Run(t, new(IstioTestSuite))
}
