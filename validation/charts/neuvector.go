package charts

import (
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults/stevestates"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/ingresses"
	"github.com/rancher/tests/actions/charts"
	"github.com/sirupsen/logrus"
)

const (
	neuVectorWebUIService         = "neuvector-service-webui"
	neuVectorControllerService    = "neuvector-svc-controller"
	neuVectorAdmissionWebhookSvc  = "neuvector-svc-admission-webhook"
	neuVectorWebUIPort            = "8443"
	neuVectorWebUIServiceProxyFmt = "k8s/clusters/%s/api/v1/namespaces/%s/services/https:%s:%s/proxy/"
)

// expectedNeuVectorServices is the list of service names that must be present for NeuVector to be operational.
var expectedNeuVectorServices = []string{
	neuVectorWebUIService,
	neuVectorControllerService,
	neuVectorAdmissionWebhookSvc,
}

// verifyNeuVectorServicesActive checks that all expected NeuVector services exist and are active
// in the NeuVector namespace.
func verifyNeuVectorServicesActive(client *rancher.Client, clusterID string) error {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	for _, svcName := range expectedNeuVectorServices {
		svcID := charts.NeuVectorNamespace + "/" + svcName
		logrus.Infof("Verifying NeuVector service [%s] is active", svcID)

		svc, err := steveClient.SteveType(stevetypes.Service).ByID(svcID)
		if err != nil {
			return fmt.Errorf("failed to get NeuVector service [%s]: %w", svcName, err)
		}

		if svc.ObjectMeta.State == nil || svc.ObjectMeta.State.Error || svc.ObjectMeta.State.Name != stevestates.Active {
			return fmt.Errorf("NeuVector service [%s] is not active (state: %v)", svcName, svc.ObjectMeta.State)
		}
	}

	return nil
}

// verifyNeuVectorWebUIAccessible verifies that the NeuVector manager web UI is reachable
// through the Rancher service proxy endpoint.
func verifyNeuVectorWebUIAccessible(client *rancher.Client, clusterID string) (string, error) {
	proxyPath := fmt.Sprintf(neuVectorWebUIServiceProxyFmt, clusterID, charts.NeuVectorNamespace, neuVectorWebUIService, neuVectorWebUIPort)

	logrus.Infof("Verifying NeuVector manager UI is accessible at path [%s]", proxyPath)
	resp, err := ingresses.GetExternalIngressResponse(client, client.RancherConfig.Host, proxyPath, true)
	if err != nil {
		return resp, fmt.Errorf("NeuVector manager UI is not accessible via service proxy: %w", err)
	}
	logrus.Infof("NeuVector manager UI responded successfully: %s", resp)

	return resp, nil
}
