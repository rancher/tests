package appco

import (
	"fmt"
	"strings"
	"time"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	extencharts "github.com/rancher/shepherd/extensions/charts"
	extensionsfleet "github.com/rancher/shepherd/extensions/fleet"
	"github.com/rancher/shepherd/extensions/kubectl"
	"github.com/rancher/shepherd/extensions/workloads"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/workloads/job"
	"github.com/rancher/tests/interoperability/fleet"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
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
	exampleAppProjectName                              = "demo-project"
	pilotImage                                  string = `dp.apps.rancher.io/containers/pilot:1.26.1-1.2`
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

	istioChart, err := watchAndwaitIstioAppCo(client, clusterID)
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

	istioChart, err := watchAndwaitIstioAppCo(client, clusterID)
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

func watchAndwaitCreateFleetGitRepo(client *rancher.Client, clusterName string, clusterID string) (*v1.SteveAPIObject, error) {
	secretName, err := createFleetSecret(client)
	if err != nil {
		return nil, err
	}

	fleetGitRepo := &v1alpha1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fleet.FleetMetaName + namegenerator.RandStringLower(5),
			Namespace: fleet.Namespace,
		},
		Spec: v1alpha1.GitRepoSpec{
			Repo:   fleet.ExampleRepo,
			Branch: fleet.BranchName,
			Paths:  []string{"appco"},
			Targets: []v1alpha1.GitTarget{
				{
					ClusterName: clusterName,
				},
			},
			HelmSecretName: secretName,
		},
	}

	logrus.Info("Creating a fleet git repo")
	repoObject, err := extensionsfleet.CreateFleetGitRepo(client, fleetGitRepo)
	if err != nil {
		return nil, err
	}

	logrus.Info("Verify git repo")
	backoff := kwait.Backoff{
		Duration: 1 * time.Second,
		Factor:   1.1,
		Jitter:   0.1,
		Steps:    20,
	}

	err = kwait.ExponentialBackoff(backoff, func() (finished bool, err error) {
		client, err = client.ReLogin()
		if err != nil {
			return false, err
		}

		gitRepo, err := client.Steve.SteveType(extensionsfleet.FleetGitRepoResourceType).ByID(repoObject.ID)
		if err != nil {
			return false, err
		}
		gitStatus := &v1alpha1.GitRepoStatus{}
		err = steveV1.ConvertToK8sType(gitRepo.Status, gitStatus)
		if err != nil {
			return false, err
		}

		if gitStatus.Summary.Modified > 0 || gitStatus.Summary.NotReady > 0 {
			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return nil, err
	}

	return repoObject, nil
}

func watchAndwaitIstioAppCo(client *rancher.Client, clusterID string) (*extencharts.ChartStatus, error) {
	err := extencharts.WatchAndWaitDeployments(client, clusterID, charts.RancherIstioNamespace, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	istioChart, err := extencharts.GetChartStatus(client, clusterID, charts.RancherIstioNamespace, charts.RancherIstioName)
	if err != nil {
		return nil, err
	}

	return istioChart, err
}

func createFleetSecret(client *rancher.Client) (string, error) {
	keyData := map[string][]byte{
		corev1.BasicAuthUsernameKey: []byte(*AppCoUsername),
		corev1.BasicAuthPasswordKey: []byte(*AppCoAccessToken),
	}

	secretName := namegenerator.AppendRandomString("fleet-appco-secret")
	secretTemplate := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: fleet.Namespace,
		},
		Data: keyData,
		Type: corev1.SecretTypeBasicAuth,
	}

	secretResp, err := client.WranglerContext.Core.Secret().Create(&secretTemplate)

	if err != nil {
		return "", err
	}

	return secretResp.Name, nil
}

func pullPilotImage(client *rancher.Client, clusterID string) error {
	container := workloads.NewContainer(namegenerator.RandStringLower(3), pilotImage, corev1.PullIfNotPresent, nil, nil, nil, nil, nil)
	podTemplate := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: make(map[string]string),
		},
		Spec: corev1.PodSpec{
			Containers:       []corev1.Container{container},
			RestartPolicy:    corev1.RestartPolicyNever,
			Volumes:          nil,
			ImagePullSecrets: []corev1.LocalObjectReference{corev1.LocalObjectReference{Name: rancherIstioSecretName}},
			NodeSelector:     nil,
		},
	}

	_, err := job.CreateJob(client, clusterID, charts.RancherIstioNamespace, podTemplate, true)
	return err
}
