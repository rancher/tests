package charts

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/ingresses"
	"github.com/rancher/tests/actions/charts"
	appv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubewait "k8s.io/apimachinery/pkg/util/wait"

	log "github.com/sirupsen/logrus"
)

const (
	// Project that example app and charts are installed in
	exampleAppProjectName = "demo-project"
	// Namespace that example app objects are installed in
	exampleAppNamespaceName = "demo-namespace"

	// Example app port and path to be checked
	exampleAppPort            = "31380"
	exampleAppProductPagePath = "productpage"

	// Example app different review bodies to be checked
	firstReviewBodyPart  = `<small>Reviewer1</small></blockquote>`
	secondReviewBodyPart = `<fontcolor="black"><!--fullstars:-->`
	thirdReviewBodyPart  = `<fontcolor="red"><!--fullstars:-->`
)

var (
	// Rancher istio chart kiali path
	kialiPath = "api/v1/namespaces/istio-system/services/http:kiali:20001/proxy/console/"
	// Rancher istio chart tracing path
	tracingPath = "api/v1/namespaces/istio-system/services/http:tracing:16686/proxy/jaeger/search"
)

// chartInstallOptions is a private struct that has istio and monitoring charts install options
type chartInstallOptions struct {
	monitoring *charts.InstallOptions
	istio      *charts.InstallOptions
}

// chartFeatureOptions is a private struct that has istio and monitoring charts feature options
type chartFeatureOptions struct {
	monitoring *charts.RancherMonitoringOpts
	istio      *charts.RancherIstioOpts
}

// getChartCaseEndpointUntilBodyHas is a private helper function
// that awaits the body of the response until the desired string is found
func getChartCaseEndpointUntilBodyHas(client *rancher.Client, host, path, bodyPart string) (found bool, err error) {
	trimAllSpaces := func(str string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, str)
	}

	err = kubewait.PollUntilContextTimeout(context.TODO(), 500*time.Millisecond, 2*time.Minute, true, func(context.Context) (ongoing bool, err error) {
		bodyString, err := ingresses.GetExternalIngressResponse(client, host, path, false)
		if err != nil {
			return ongoing, err
		}

		trimmedBody := trimAllSpaces(bodyString)
		if strings.Contains(trimmedBody, bodyPart) {
			found = true
			return !ongoing, nil
		}

		return
	})
	if err != nil {
		return false, err
	}

	return
}

// listIstioDeployments lists istio deployments in the downstream cluster using the dynamic client.
// It returns whatever deployments it can find, even if fewer than expected.
func listIstioDeployments(client *rancher.Client, clusterID string) (deploymentSpecList []*appv1.DeploymentSpec, err error) {
	deploymentSpecList, err = listIstioDeploymentsOnce(client, clusterID)
	if err != nil {
		return nil, err
	}
	if len(deploymentSpecList) < 2 {
		log.Infof("listIstioDeployments: found %d istio deployments (expected >= 2)", len(deploymentSpecList))
	}
	return deploymentSpecList, nil
}

func listIstioDeploymentsOnce(client *rancher.Client, clusterID string) (deploymentSpecList []*appv1.DeploymentSpec, err error) {
	adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
	if err != nil {
		return nil, err
	}
	adminDynamicClient, err := adminClient.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return nil, err
	}

	deploymentGVR := schema.GroupVersionResource{
		Group:    "apps",
		Version:  "v1",
		Resource: "deployments",
	}

	deployments, err := adminDynamicClient.Resource(deploymentGVR).Namespace(charts.RancherIstioNamespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	for _, unstructuredDeployment := range deployments.Items {
		labels := unstructuredDeployment.GetLabels()

		if hasIstioLabel(labels) {
			deployment := &appv1.Deployment{}
			err := steveV1.ConvertToK8sType(unstructuredDeployment.Object, deployment)
			if err != nil {
				return deploymentSpecList, err
			}

			deploymentSpecList = append(deploymentSpecList, &deployment.Spec)
		}
	}

	return deploymentSpecList, nil
}

// hasIstioLabel checks if the deployment labels indicate it is an istio component.
// Supports multiple label patterns across different rancher-istio chart versions:
//   - Legacy: operator.istio.io/version
//   - Modern: istio.io/version
//   - Common: app.kubernetes.io/name matching istiod or istio-ingressgateway
//   - Rancher chart: release=rancher-istio (for add-on deployments like tracing)
func hasIstioLabel(labels map[string]string) bool {
	if _, ok := labels["operator.istio.io/version"]; ok {
		return true
	}
	if _, ok := labels["istio.io/version"]; ok {
		return true
	}
	if name, ok := labels["app.kubernetes.io/name"]; ok {
		if name == "istiod" || name == "istio-ingressgateway" {
			return true
		}
	}
	if release, ok := labels["release"]; ok {
		if release == "rancher-istio" {
			return true
		}
	}
	return false
}
