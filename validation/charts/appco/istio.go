package appco

import (
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	extencharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/kubectl"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/kubeapi/namespaces"
	"github.com/rancher/tests/actions/workloads/job"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	RancherIstioSecret string = "application-collection"
	RancherPilotImage  string = "dp.apps.rancher.io/containers/pilot:1.25.3"
)

func CreateIstioNamespace(client *rancher.Client, clusterID string) error {
	namespace, err := namespaces.GetNamespaceByName(client, clusterID, charts.RancherIstioNamespace)
	if err != nil && !strings.Contains(err.Error(), "not found") {
		return err
	}
	if namespace != nil {
		return nil
	}

	_, err = namespaces.CreateNamespace(client, clusterID, namegen.AppendRandomString("testns"), charts.RancherIstioNamespace, "{}", map[string]string{}, map[string]string{})
	return err
}

func CreateIstioSecret(client *rancher.Client, clusterID string, appCoUsername string, appCoToken string) (string, error) {
	secretCommand := strings.Split(fmt.Sprintf("kubectl create secret docker-registry %s --docker-server=dp.apps.rancher.io --docker-username=%s --docker-password=%s -n %s", RancherIstioSecret, appCoUsername, appCoToken, charts.RancherIstioNamespace), " ")
	logCmd, err := kubectl.Command(client, nil, clusterID, secretCommand, "")
	return logCmd, err
}

func CreatePilotJob(client *rancher.Client, clusterID string) error {
	container := corev1.Container{
		Name:            namegen.AppendRandomString("pilot"),
		Image:           RancherPilotImage,
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
					Name: RancherIstioSecret,
				}},
			NodeSelector: nil,
		},
	}

	_, err := job.CreateJob(client, clusterID, charts.RancherIstioNamespace, podTemplate, false)
	return err
}

func InstallIstioAppCo(client *rancher.Client, clusterID string, appCoUsername string, appCoToken string, sets string) (*extencharts.ChartStatus, string, error) {
	istioAppCoCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`helm registry login dp.apps.rancher.io -u %s -p %s && helm install %s oci://dp.apps.rancher.io/charts/istio -n %s --set global.imagePullSecrets={%s} %s`, appCoUsername, appCoToken, charts.RancherIstioName, charts.RancherIstioNamespace, RancherIstioSecret, sets),
	}

	logCmd, err := kubectl.Command(client, nil, clusterID, istioAppCoCommand, "1MB")

	if err != nil {
		return nil, logCmd, err
	}

	istioChart, err := extencharts.GetChartStatus(client, clusterID, charts.RancherIstioNamespace, charts.RancherIstioName)
	return istioChart, logCmd, err
}

func UpgradeIstioAppCo(client *rancher.Client, clusterID string, appCoUsername string, appCoToken string, sets string) (*extencharts.ChartStatus, string, error) {
	istioAppCoCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`helm registry login dp.apps.rancher.io -u %s -p %s && helm upgrade %s oci://dp.apps.rancher.io/charts/istio -n %s --set global.imagePullSecrets={%s} %s`, appCoUsername, appCoToken, charts.RancherIstioName, charts.RancherIstioNamespace, RancherIstioSecret, sets),
	}

	logCmd, err := kubectl.Command(client, nil, clusterID, istioAppCoCommand, "1MB")

	if err != nil {
		return nil, logCmd, err
	}

	istioChart, err := extencharts.GetChartStatus(client, clusterID, charts.RancherIstioNamespace, charts.RancherIstioName)
	return istioChart, logCmd, err
}

func UninstallIstioAppCo(client *rancher.Client, clusterID string) (*extencharts.ChartStatus, error) {
	helmUninstallCommand := fmt.Sprintf(`helm uninstall %s -n %s`, charts.RancherIstioName, charts.RancherIstioNamespace)
	deleteConfigurationCommand := `kubectl delete mutatingwebhookconfiguration istio-sidecar-injector`
	deleteCustomDefinationCommand := `kubectl delete $(kubectl get CustomResourceDefinition -l='app.kubernetes.io/part-of=istio' -o name -A)`

	uninstallCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`%s && %s && %s`, helmUninstallCommand, deleteConfigurationCommand, deleteCustomDefinationCommand),
	}

	_, err := kubectl.Command(client, nil, clusterID, uninstallCommand, "2MB")

	if err != nil {
		return nil, err
	}

	istioChart, err := extencharts.GetChartStatus(client, clusterID, charts.RancherIstioNamespace, charts.RancherIstioName)
	return istioChart, err
}
