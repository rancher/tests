package monitoring

import (
	"net"
	"strconv"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/kubectl"
	corev1 "k8s.io/api/core/v1"
)

// ProbeMode selects how the webhook receiver NodePort is probed.
type ProbeMode string

const (
	// ProbeModeRunner probes the receiver with an HTTP request made by the go test runner itself.
	ProbeModeRunner ProbeMode = "runner"

	// ProbeModeInCluster probes the receiver from inside the target cluster via the Rancher proxy.
	ProbeModeInCluster ProbeMode = "in-cluster"
)

// ProbeModeForAddressType maps the selected node address type to the probe mode used to reach it.
// Runner-side HTTP only makes sense for runner-reachable public addresses, so ExternalIP maps to
// ProbeModeRunner; internal or unknown address types are probed in-cluster via the Rancher proxy.
func ProbeModeForAddressType(addressType corev1.NodeAddressType) ProbeMode {
	if addressType == corev1.NodeExternalIP {
		return ProbeModeRunner
	}

	return ProbeModeInCluster
}

// JoinNodeAddressPort combines a node address and a port for use in a URL or host:port
// pair. IPv6 literal addresses are bracketed (net.JoinHostPort semantics), which plain
// fmt.Sprintf("%s:%d", ...) interpolation does not do.
func JoinNodeAddressPort(address string, port int32) string {
	return net.JoinHostPort(address, strconv.Itoa(int(port)))
}

// WebhookReceiverProbeURL builds the http URL probed against the webhook receiver NodePort.
// IPv6 literal addresses are bracketed (JoinNodeAddressPort), and a leading '/' in path
// is stripped so the URL never contains a double slash.
func WebhookReceiverProbeURL(address string, port int32, path string) string {
	return "http://" + JoinNodeAddressPort(address, port) + "/" + strings.TrimLeft(path, "/")
}

// IsReachableHTTPStatus reports whether probe output counts as success. It mirrors shepherd
// GetExternalIngressResponse success semantics (HTTP 200 only) and tolerates the trailing
// newline pod log output usually carries.
func IsReachableHTTPStatus(status string) bool {
	return strings.TrimSpace(status) == "200"
}

// ProbeHTTPInCluster curls probeURL from inside the target cluster and returns the raw log
// output (the curl-written HTTP status code). Redirects are followed (-L) so the reported
// status is the final response — Traefik answers GET /dashboard with a 302 to /dashboard/,
// and the runner-side Go client follows redirects automatically, so the in-cluster probe
// must match that behavior instead of failing on the healthy redirect. The command runs in
// a short-lived shell-image job on the target cluster through the Rancher proxy (the same
// mechanism DeleteMonitoringResources uses for teardown), so the go test runner needs no
// route into the node subnet.
func ProbeHTTPInCluster(client *rancher.Client, clusterID, probeURL string) (string, error) {
	return kubectl.Command(client, nil, clusterID, []string{"curl", "-sSL", "-o", "/dev/null", "-w", "%{http_code}", probeURL}, "")
}
