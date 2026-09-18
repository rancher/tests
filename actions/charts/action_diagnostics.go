package charts

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	scheme "github.com/rancher/shepherd/pkg/generated/clientset/versioned/scheme"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

const (
	verbInstall   = "install"
	verbUpgrade   = "upgrade"
	verbUninstall = "uninstall"
	actionParam   = "action"

	appsSteveType        = "catalog.cattle.io.app"
	operationsSteveType  = "catalog.cattle.io.operation"
	clusterReposResource = "catalog.cattle.io.clusterrepo"
	chartRepoURLPath     = "v1/catalog.cattle.io.clusterrepos/"
	chartAppsURLPath     = "v1/catalog.cattle.io.apps/"
	operationLabelFilter = "operation.cattle.io/"

	chartActionMaxAttempts  = 3
	chartActionRetrySpacing = 30 * time.Second
	chartActionMaxElapsed   = 150 * time.Second
	diagnosticsBodyCapBytes = 4096
	diagnosticsListLimit    = 20
)

// chartActionDoer is the seam the retry wrapper executes; *rest.Request satisfies it.
type chartActionDoer interface {
	Do(context.Context) rest.Result
}

// sleepBetweenAttempts is the indirection point for sleeping between retry attempts so the
// throwaway verification harness can stub real sleeping out.
var sleepBetweenAttempts = func(d time.Duration) {
	// time.After instead of time.Sleep to stay clear of the forbidigo sleep ban.
	<-time.After(d)
}

// ChartActionWithRetry executes a chart install/upgrade/uninstall request with bounded retry
// on retryable failures (the observed PIT failures are 5xx with flattened "unknown" bodies),
// emitting a lifecycle line per action and the full diagnostics bundle on final failure.
// Caller-visible error semantics are unchanged apart from the added attempts.
func ChartActionWithRetry(ctx context.Context, client *rancher.Client, verb string, opts *PayloadOpts, repoName string, req chartActionDoer, body []byte) error {
	logChartActionLifecycle(verb, opts, repoName)

	start := time.Now()
	attemptsMade := 0
	var lastErr error
	for attempt := 1; attempt <= chartActionMaxAttempts; attempt++ {
		attemptsMade = attempt
		lastErr = req.Do(ctx).Error()
		if lastErr == nil {
			return nil
		}

		logrus.Warnf("chart action failed: ts=%s verb=%s chart=%s attempt=%d/%d elapsed=%s error=%v",
			time.Now().UTC().Format(time.RFC3339), verb, opts.Name, attempt, chartActionMaxAttempts,
			time.Since(start).Round(time.Millisecond), lastErr)

		if !isRetryableChartActionError(lastErr) {
			break
		}
		if attempt == chartActionMaxAttempts || time.Since(start) >= chartActionMaxElapsed {
			break
		}
		sleepBetweenAttempts(waitBetweenAttempts(attempt))
	}

	LogChartActionFailure(ctx, client, verb, repoName, opts.Cluster.ID, opts.Namespace, opts.Name, body)
	return fmt.Errorf("chart action %s %s failed after %d attempts: %w", verb, opts.Name, attemptsMade, lastErr)
}

// waitBetweenAttempts returns the fixed delay between chart action retry attempts.
func waitBetweenAttempts(_ int) time.Duration {
	return chartActionRetrySpacing
}

// isRetryableChartActionError reports whether a chart action error is worth retrying:
// client-side k8s status errors (4xx family) are terminal, everything else (5xx, transport)
// is retried. Uninstall no-ops (NotFound) therefore exit after a single fast attempt.
func isRetryableChartActionError(err error) bool {
	return !(apierrors.IsBadRequest(err) ||
		apierrors.IsNotFound(err) ||
		apierrors.IsConflict(err) ||
		apierrors.IsForbidden(err) ||
		apierrors.IsUnauthorized(err) ||
		apierrors.IsInvalid(err))
}

// LogChartActionFailure re-issues the failing chart action request against the raw cluster
// API to capture the HTTP status and response body that the shepherd client discards, then
// dumps the ClusterRepo and the catalog App/Operation state around the failure. Every step
// logs and swallows its own error so diagnostics never alter the failure the caller returns.
func LogChartActionFailure(ctx context.Context, client *rancher.Client, verb, repoName, clusterID, namespace, chartName string, body []byte) {
	if client == nil || body == nil {
		// Diagnostics are best-effort; without a client or request body there is nothing to probe with.
		return
	}

	probeURL := buildChartActionURL(client.RancherConfig.Host, clusterID, chartRepoURLPath, repoName, verb)
	if verb == verbUninstall {
		// Uninstall actions target the namespaced app resource, not the cluster repo.
		probeURL = buildChartActionURL(client.RancherConfig.Host, clusterID, chartAppsURLPath+namespace, chartName, verb)
	}

	status, responseBody, err := probeRequest(ctx, client.RancherConfig.Host, client.RancherConfig.AdminToken, probeURL, verb, body)
	if err != nil {
		// Swallowed: the probe is diagnostics and must not mask the original chart action failure.
		logrus.Warnf("chart action probe: transport error: %v", err)
	} else {
		logrus.Warnf("chart action probe: verb=%s status=%d body=%s", verb, status, responseBody)
	}

	logDownstreamCatalogDiagnostics(client, clusterID, namespace, chartName)
}

// logDownstreamCatalogDiagnostics dumps the rancher-charts ClusterRepo status, the target app
// status, and the catalog operations in the chart's namespace through the downstream steve
// proxy. The ClusterRepo is read via the proxy with the singular steve type (the same proven
// pattern as neuvector's waitForRancherChartsRepo); the management steve client rejects the
// clusterrepo schema type with "Unknown schema type".
func logDownstreamCatalogDiagnostics(client *rancher.Client, clusterID, namespace, chartName string) {
	proxyClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		// Swallowed: diagnostics must not alter failure propagation.
		logrus.Warnf("chart action diagnostics: downstream proxy: %v", err)
		return
	}

	repo, err := proxyClient.SteveType(clusterReposResource).ByID(catalog.RancherChartRepo)
	if err != nil {
		// Swallowed: diagnostics must not alter failure propagation.
		logrus.Warnf("chart action diagnostics: clusterrepo %s get: %v", catalog.RancherChartRepo, err)
	} else {
		repoStatus, err := json.Marshal(repo.Status)
		if err != nil {
			// Swallowed: diagnostics only.
			logrus.Warnf("chart action diagnostics: clusterrepo %s status marshal: %v", catalog.RancherChartRepo, err)
		} else {
			logrus.Warnf("chart action diagnostics: clusterrepo %s status: %s", catalog.RancherChartRepo, repoStatus)
		}
	}

	app, err := proxyClient.SteveType(appsSteveType).NamespacedSteveClient(namespace).ByID(chartName)
	if err != nil {
		// Swallowed: the app may legitimately be absent, e.g. when the install never created it.
		logrus.Warnf("chart action diagnostics: app %s/%s get: %v", namespace, chartName, err)
	} else {
		appStatus, err := json.Marshal(app.Status)
		if err != nil {
			// Swallowed: diagnostics only.
			logrus.Warnf("chart action diagnostics: app %s/%s status marshal: %v", namespace, chartName, err)
		} else {
			logrus.Warnf("chart action diagnostics: app %s/%s status: %s", namespace, chartName, appStatus)
		}
	}

	operations, err := proxyClient.SteveType(operationsSteveType).NamespacedSteveClient(namespace).List(nil)
	if err != nil {
		// Swallowed: diagnostics must not alter failure propagation.
		logrus.Warnf("chart action diagnostics: operations list %s: %v", namespace, err)
		return
	}

	logged := 0
	for i := range operations.Data {
		if logged >= diagnosticsListLimit {
			break
		}
		operation := &operations.Data[i]
		if !isChartOperation(operation, chartName) {
			continue
		}
		logged++
		operationStatus, err := json.Marshal(operation.Status)
		if err != nil {
			// Swallowed: diagnostics only.
			logrus.Warnf("chart action diagnostics: operation %s status marshal: %v", operation.Name, err)
			continue
		}
		logrus.Warnf("chart action diagnostics: operation %s status: %s", operation.Name, operationStatus)
	}
}

// isChartOperation reports whether an operation object relates to the chart, either through
// the operation label family or the chart name appearing in the object name.
func isChartOperation(operation *steveV1.SteveAPIObject, chartName string) bool {
	if strings.Contains(operation.Name, chartName) {
		return true
	}
	for labelKey := range operation.Labels {
		if strings.Contains(labelKey, operationLabelFilter) {
			return true
		}
	}
	return false
}

// buildChartActionURL returns the rancher proxied cluster API URL for a chart action,
// e.g. https://<host>/k8s/clusters/<clusterID>/v1/catalog.cattle.io.clusterrepos/<name>?action=install
func buildChartActionURL(host, clusterID, path, name, verb string) string {
	return fmt.Sprintf("https://%s/k8s/clusters/%s/%s/%s?action=%s", host, clusterID, strings.TrimSuffix(path, "/"), name, verb)
}

// probeRequest performs one raw POST against the rancher cluster API and returns the HTTP
// status code plus at most diagnosticsBodyCapBytes of the response body. Transport errors are
// returned separately so callers can skip body logging.
func probeRequest(ctx context.Context, host, adminToken, url, verb string, body []byte) (int, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, "", fmt.Errorf("probe %s %s request build failed: %w", verb, host, err)
	}
	request.Header.Set("Authorization", "Bearer "+adminToken)
	request.Header.Set("Content-Type", "application/json")

	//nolint:gosec // diagnostic probe against self-signed PIT Rancher endpoints
	httpClient := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return 0, "", fmt.Errorf("probe %s %s failed: %w", verb, host, err)
	}
	defer response.Body.Close()

	probeBody, err := io.ReadAll(io.LimitReader(response.Body, diagnosticsBodyCapBytes))
	if err != nil {
		return response.StatusCode, "", fmt.Errorf("probe %s %s body read failed: %w", verb, host, err)
	}
	return response.StatusCode, string(probeBody), nil
}

// buildRepoActionRequest mirrors shepherd's InstallChart/UpgradeChart request shape
// (clients/rancher/catalog/clusterrepo.go) without executing it, so the retry wrapper can
// re-issue the identical request.
func buildRepoActionRequest(catalogClient *catalog.Client, repoName, verb string, bodyBytes []byte) *rest.Request {
	return catalogClient.RESTClient().Post().
		AbsPath(chartRepoURLPath+repoName).Param(actionParam, verb).
		VersionedParams(&metav1.CreateOptions{}, scheme.ParameterCodec).
		Body(bodyBytes)
}

// buildAppUninstallRequest mirrors shepherd's UninstallChart request shape without executing it.
func buildAppUninstallRequest(catalogClient *catalog.Client, namespace, chartName string, bodyBytes []byte) *rest.Request {
	return catalogClient.RESTClient().Post().
		Name(chartName).
		AbsPath(chartAppsURLPath+namespace).Param(actionParam, verbUninstall).
		Body(bodyBytes).
		VersionedParams(&metav1.CreateOptions{}, scheme.ParameterCodec)
}

// marshalChartAction serializes a chart action payload. Marshal errors are impossible for the
// JSON-only payload structs used here and are ignored by design; a nil body degrades into a
// server-side error that the retry loop and diagnostics surface.
func marshalChartAction(action any) []byte {
	bodyBytes, _ := json.Marshal(action)
	return bodyBytes
}

// logChartActionLifecycle emits the machine-readable lifecycle line for every chart action.
func logChartActionLifecycle(verb string, opts *PayloadOpts, repoName string) {
	logrus.Infof("chart action: ts=%s verb=%s chart=%s version=%s namespace=%s cluster=%s repo=%s",
		time.Now().UTC().Format(time.RFC3339), verb, opts.Name, opts.Version, opts.Namespace, opts.Cluster.ID, repoName)
}
