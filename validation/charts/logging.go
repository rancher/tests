package charts

import (
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/kubectl"
	"github.com/rancher/tests/actions/charts"
)

const (
	getLogsLoggingReceiver string = `kubectl logs --namespace %s -f svc/rancher-logging-test-receiver`
)

func verifyLoggingReceiver(client *rancher.Client, clusterID string) (string, error) {
	loggingReceiverCommand := []string{
		"sh", "-c",
		fmt.Sprintf(getLogsLoggingReceiver, charts.RancherLoggingNamespace),
	}

	return kubectl.Command(client, nil, clusterID, loggingReceiverCommand, "2MB")
}
