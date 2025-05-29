package appco

import (
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	extencharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/kubectl"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/charts"
	kubeapiNamespaces "github.com/rancher/tests/actions/kubeapi/namespaces"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	RancherIstioSecret string = "application-collection"
)

func createIstioNamespace(client *rancher.Client, clusterID string) error {
	_, err := kubeapiNamespaces.CreateNamespace(client, clusterID, namegen.AppendRandomString("testns"), charts.RancherIstioNamespace, "{}", map[string]string{}, map[string]string{})
	return err
}

func createIstioSecret(client *rancher.Client, clusterID string, appCoUsername string, appCoToken string) (string, error) {
	secretCommand := strings.Split(fmt.Sprintf("kubectl create secret docker-registry %s --docker-server=dp.apps.rancher.io --docker-username=%s --docker-password=%s -n %s", RancherIstioSecret, appCoUsername, appCoToken, charts.RancherIstioNamespace), " ")
	logCmd, err := kubectl.Command(client, nil, clusterID, secretCommand, "")
	return logCmd, err
}

func installIstioAppCo(client *rancher.Client, clusterID string, appCoUsername string, appCoToken string, sets string) (*extencharts.ChartStatus, string, error) {
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
