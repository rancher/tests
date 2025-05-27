package appco

import (
	"context"
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	extencharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/kubectl"
	"github.com/rancher/shepherd/pkg/api/steve/catalog/types"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/wait"
	"github.com/rancher/tests/actions/charts"
	kubeapiNamespaces "github.com/rancher/tests/actions/kubeapi/namespaces"
	"github.com/rancher/tests/actions/namespaces"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

const (
	RancherIstioSecret string = "application-collection"
)

func createIstioNamespace(client *rancher.Client, clusterID string) error {
	namespace, err := kubeapiNamespaces.GetNamespaceByName(client, clusterID, charts.RancherIstioNamespace)
	if err != nil && !strings.Contains(err.Error(), "not found") {
		return err
	}
	if namespace != nil {
		return nil
	}

	_, err = kubeapiNamespaces.CreateNamespace(client, clusterID, namegen.AppendRandomString("testns"), charts.RancherIstioNamespace, "{}", map[string]string{}, map[string]string{})
	return err
}

func createIstioSecret(client *rancher.Client, clusterID string, appCoUsername string, appCoToken string) (string, error) {
	secretCommand := strings.Split(fmt.Sprintf("kubectl create secret docker-registry %s --docker-server=dp.apps.rancher.io --docker-username=%s --docker-password=%s -n %s", RancherIstioSecret, appCoUsername, appCoToken, charts.RancherIstioNamespace), " ")
	logCmd, err := kubectl.Command(client, nil, clusterID, secretCommand, "")
	return logCmd, err
}

func installIstioAppCo(client *rancher.Client, clusterID string, appCoUsername string, appCoToken string, sets string) (*extencharts.ChartStatus, string, error) {

	client.Session.RegisterCleanupFunc(func() error {
		logrus.Infof("Uninstalling Istio AppCo")
		istioChart, err := uninstallIstioAppCo(client, clusterID)
		if err != nil {
			return err
		}
		if istioChart == nil || !istioChart.IsAlreadyInstalled {
			return fmt.Errorf("Istio is still installed")
		}
		return nil
	})

	istioAppCoCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`helm registry login dp.apps.rancher.io -u %s -p %s && helm install %s oci://dp.apps.rancher.io/charts/istio -n %s --set global.imagePullSecrets={%s} %s`, appCoUsername, appCoToken, charts.RancherIstioName, charts.RancherIstioNamespace, RancherIstioSecret, sets),
	}

	logCmd, err := kubectl.Command(client, nil, clusterID, istioAppCoCommand, "2MB")

	if err != nil {
		return nil, logCmd, err
	}

	err = extencharts.WatchAndWaitDeployments(client, clusterID, charts.RancherIstioNamespace, metav1.ListOptions{})
	if err != nil {
		return nil, logCmd, err
	}

	istioChart, err := extencharts.GetChartStatus(client, clusterID, charts.RancherIstioNamespace, charts.RancherIstioName)
	if err != nil {
		return nil, logCmd, err
	}

	return istioChart, logCmd, err
}

func upgradeIstioAppCo(client *rancher.Client, clusterID string, appCoUsername string, appCoToken string, sets string) (*extencharts.ChartStatus, string, error) {
	istioAppCoCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`helm registry login dp.apps.rancher.io -u %s -p %s && helm upgrade %s oci://dp.apps.rancher.io/charts/istio -n %s --set global.imagePullSecrets={%s} %s`, appCoUsername, appCoToken, charts.RancherIstioName, charts.RancherIstioNamespace, RancherIstioSecret, sets),
	}

	logCmd, err := kubectl.Command(client, nil, clusterID, istioAppCoCommand, "2MB")
	if err != nil {
		return nil, logCmd, err
	}

	err = extencharts.WatchAndWaitDeployments(client, clusterID, charts.RancherIstioNamespace, metav1.ListOptions{})
	if err != nil {
		return nil, logCmd, err
	}

	istioChart, err := extencharts.GetChartStatus(client, clusterID, charts.RancherIstioNamespace, charts.RancherIstioName)
	if err != nil {
		return nil, logCmd, err
	}

	return istioChart, logCmd, err
}

func newChartUninstallAction() *types.ChartUninstallAction {
	return &types.ChartUninstallAction{
		DisableHooks: false,
		DryRun:       false,
		KeepHistory:  false,
		Timeout:      nil,
		Description:  "",
	}
}

func uninstallIstioAppCo(client *rancher.Client, clusterID string) (*extencharts.ChartStatus, error) {

	catalogClient, err := client.GetClusterCatalogClient(clusterID)
	if err != nil {
		return nil, err
	}

	defaultChartUninstallAction := newChartUninstallAction()

	err = catalogClient.UninstallChart(charts.RancherIstioName, charts.RancherIstioNamespace, defaultChartUninstallAction)
	if err != nil {
		return nil, err
	}

	watchAppInterface, err := catalogClient.Apps(charts.RancherIstioNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + charts.RancherIstioName,
		TimeoutSeconds: &defaults.WatchTimeoutSeconds,
	})
	if err != nil {
		return nil, err
	}

	err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
		if event.Type == watch.Error {
			return false, fmt.Errorf("there was an error uninstalling rancher istio chart")
		} else if event.Type == watch.Deleted {
			return true, nil
		}
		return false, nil
	})

	steveclient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return nil, err
	}

	namespaceClient := steveclient.SteveType(namespaces.NamespaceSteveType)

	namespace, err := namespaceClient.ByID(charts.RancherIstioNamespace)
	if err != nil {
		return nil, err
	}

	err = namespaceClient.Delete(namespace)
	if err != nil {
		return nil, err
	}

	istioChart, err := extencharts.GetChartStatus(client, clusterID, charts.RancherIstioNamespace, charts.RancherIstioName)
	return istioChart, err
}
