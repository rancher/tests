package appco

import (
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	extencharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/kubectl"
	"github.com/rancher/tests/actions/charts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	expectedDeployLog                                  = "deployed"
	istioCanaryRevisionApp                             = "istiod-canary"
	rancherIstioSecretName                      string = `application-collection`
	istioAmbientModeSet                         string = `--set cni.enabled=true,ztunnel.enabled=true --set istiod.cni.enabled=false --set cni.profile=ambient,istiod.profile=ambient,ztunnel.profile=ambient`
	istioGatewayModeSet                         string = `--set base.enabled=false,istiod.enabled=false --set gateway.enabled=true,gateway.namespaceOverride=default`
	istioGatewayDiffNamespaceModeSet            string = `--set gateway.enabled=true,gateway.namespaceOverride=default`
	istioCanaryUpgradeSet                       string = `--set istiod.revision=canary,base.defaultRevision=canary,gateway.namespaceOverride=default`
	createIstioSecretCommand                    string = `kubectl create secret docker-registry %s --docker-server=dp.apps.rancher.io --docker-username=%s --docker-password=%s -n %s`
	watchAndwaitInstallIstioAppCoCommand        string = `helm registry login dp.apps.rancher.io -u %s -p %s && helm install %s oci://dp.apps.rancher.io/charts/istio -n %s --set global.imagePullSecrets={%s} %s`
	watchAndwaitUpgradeIstioAppCoUpgradeCommand string = `helm registry login dp.apps.rancher.io -u %s -p %s && helm upgrade %s oci://dp.apps.rancher.io/charts/istio -n %s --set global.imagePullSecrets={%s} %s`
	getPodsMetadataNameCommand                  string = `kubectl -n %s get pod -o jsonpath='{.items..metadata.name}'`
	logBufferSize                               string = `2MB`
)

func createIstioSecret(client *rancher.Client, clusterID string, appCoUsername string, appCoToken string) (string, error) {
	secretCommand := strings.Split(fmt.Sprintf(createIstioSecretCommand, rancherIstioSecretName, appCoUsername, appCoToken, charts.RancherIstioNamespace), " ")
	return kubectl.Command(client, nil, clusterID, secretCommand, "")
}

func watchAndwaitInstallIstioAppCo(client *rancher.Client, clusterID string, appCoUsername string, appCoToken string, sets string) (*extencharts.ChartStatus, string, error) {
	istioAppCoCommand := []string{
		"sh", "-c",
		fmt.Sprintf(watchAndwaitInstallIstioAppCoCommand, appCoUsername, appCoToken, charts.RancherIstioName, charts.RancherIstioNamespace, rancherIstioSecretName, sets),
	}

	logCmd, err := kubectl.Command(client, nil, clusterID, istioAppCoCommand, logBufferSize)

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

func watchAndwaitUpgradeIstioAppCo(client *rancher.Client, clusterID string, appCoUsername string, appCoToken string, sets string) (*extencharts.ChartStatus, string, error) {
	istioAppCoCommand := []string{
		"sh", "-c",
		fmt.Sprintf(watchAndwaitUpgradeIstioAppCoUpgradeCommand, appCoUsername, appCoToken, charts.RancherIstioName, charts.RancherIstioNamespace, rancherIstioSecretName, sets),
	}

	logCmd, err := kubectl.Command(client, nil, clusterID, istioAppCoCommand, logBufferSize)
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

func verifyCanaryRevision(client *rancher.Client, clusterID string) (string, error) {
	getCanaryCommand := []string{
		"sh", "-c",
		fmt.Sprintf(getPodsMetadataNameCommand, charts.RancherIstioNamespace),
	}

	return kubectl.Command(client, nil, clusterID, getCanaryCommand, logBufferSize)
}
