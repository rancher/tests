package charts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
)

// Timing knobs are vars so the verification harness can scale them down.
var (
	chartActionMaxAttempts  = 3
	chartActionRetrySpacing = 30 * time.Second
	chartActionMaxElapsed   = 150 * time.Second
	diagnosticsBodyCapBytes = 4096
	diagnosticsBundleLimit  = 60 * time.Second
	diagnosticsListLimit    = 20
)

// chartActionDoer is the seam the retry wrapper executes; *rest.Request satisfies it.
type chartActionDoer interface {
	Do(context.Context) rest.Result
}

// sleepBetweenAttempts is the indirection point for sleeping between retry attempts so the
// throwaway verification harness can stub real sleeping out. It reports whether the full
// delay elapsed; false means ctx was cancelled and no further attempt should start.
var sleepBetweenAttempts = func(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// ChartActionWithRetry executes a chart install/upgrade/uninstall request with bounded retry
// on retryable failures (the observed PIT failures are 5xx whose raw bodies the k8s client
// flattens to "unknown"), emitting a lifecycle line per action, the response status and raw
// body of each failed attempt straight from the original rest.Result, and the downstream
// catalog state on final failure.
//
// repoName is the ClusterRepo the action targets (install/upgrade); it is empty for
// uninstalls, which address the App directly. targetName is the catalog App the action
// applies to: the chart name for installs/upgrades and base uninstalls, "<chart>-crd" for
// CRD cleanup uninstalls. Diagnostics, lifecycle logs, and the returned error all name
// targetName, never repoName.
func ChartActionWithRetry(ctx context.Context, client *rancher.Client, verb string, opts *PayloadOpts, repoName, targetName string, req chartActionDoer) error {
	logChartActionLifecycle(verb, opts, repoName, targetName)

	start := time.Now()
	deadline := start.Add(chartActionMaxElapsed)
	attemptsMade := 0
	var lastErr error
	for attempt := 1; attempt <= chartActionMaxAttempts; attempt++ {
		attemptsMade = attempt
		// Bound every attempt by the overall deadline so a hung request cannot push the
		// loop past chartActionMaxElapsed.
		attemptCtx, cancel := context.WithTimeout(ctx, time.Until(deadline))
		result := req.Do(attemptCtx)
		cancel()
		lastErr = result.Error()
		if lastErr == nil {
			return nil
		}

		// The k8s client flattens non-Status 5xx bodies to "unknown"; Result.Raw retains
		// the original response, so the status and body come from the failed attempt
		// itself rather than a re-issued mutating request.
		body, _ := result.Raw()
		logrus.Warnf("chart action failed: ts=%s verb=%s chart=%s target=%s attempt=%d/%d elapsed=%s status=%d error=%v body=%s",
			time.Now().UTC().Format(time.RFC3339), verb, opts.Name, targetName, attempt, chartActionMaxAttempts,
			time.Since(start).Round(time.Millisecond), chartActionHTTPStatus(lastErr), lastErr, cappedDiagnosticsBody(body))

		if !isRetryableChartActionError(lastErr) || attempt == chartActionMaxAttempts {
			break
		}
		remaining := time.Until(deadline)
		spacing := waitBetweenAttempts(attempt)
		if remaining <= spacing {
			// Not enough budget left for the inter-attempt delay plus a meaningful retry.
			break
		}
		if !sleepBetweenAttempts(ctx, spacing) {
			break
		}
	}

	LogChartActionFailure(ctx, client, repoName, opts.Cluster.ID, opts.Namespace, targetName)
	return fmt.Errorf("chart action %s %s failed after %d attempts: %w", verb, targetName, attemptsMade, lastErr)
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

// LogChartActionFailure dumps the catalog state around a failed chart action: the
// ClusterRepo the action targeted (when non-empty), the target App, and the catalog
// Operations in the chart's namespace, all through the downstream steve proxy. The bundle
// runs under a wall-clock bound because shepherd's ProxyDownstream and steve reads accept
// no context and must not delay failure propagation indefinitely. Every step logs and
// swallows its own error so diagnostics never alter the failure the caller returns.
func LogChartActionFailure(ctx context.Context, client *rancher.Client, repoName, clusterID, namespace, targetName string) {
	if client == nil {
		// Diagnostics are best-effort; without a client there is nothing to read with.
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		logDownstreamCatalogDiagnostics(client, clusterID, repoName, namespace, targetName)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		logrus.Warnf("chart action diagnostics: abandoned: %v", ctx.Err())
	case <-time.After(diagnosticsBundleLimit):
		// Swallowed: a wedged downstream tunnel must not block failure propagation. The
		// goroutine is one-shot diagnostics; it exits with the process once its own
		// calls fail or the connection drops.
		logrus.Warnf("chart action diagnostics: bundle timed out after %s", diagnosticsBundleLimit)
	}
}

// logDownstreamCatalogDiagnostics dumps the ClusterRepo named by repoName (when non-empty;
// empty for uninstalls), the target App status, and the catalog operations in the chart's
// namespace through the downstream steve proxy. The ClusterRepo is read via the proxy with
// the singular steve type (the same proven pattern as neuvector's
// waitForRancherChartsRepo); the management steve client rejects the clusterrepo schema
// type with "Unknown schema type".
func logDownstreamCatalogDiagnostics(client *rancher.Client, clusterID, repoName, namespace, targetName string) {
	proxyClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		// Swallowed: diagnostics must not alter failure propagation.
		logrus.Warnf("chart action diagnostics: downstream proxy: %v", err)
		return
	}

	if repoName != "" {
		repo, err := proxyClient.SteveType(clusterReposResource).ByID(repoName)
		if err != nil {
			// Swallowed: diagnostics must not alter failure propagation.
			logrus.Warnf("chart action diagnostics: clusterrepo %s get: %v", repoName, err)
		} else {
			repoStatus, err := json.Marshal(repo.Status)
			if err != nil {
				// Swallowed: diagnostics only.
				logrus.Warnf("chart action diagnostics: clusterrepo %s status marshal: %v", repoName, err)
			} else {
				logrus.Warnf("chart action diagnostics: clusterrepo %s status: %s", repoName, repoStatus)
			}
		}
	}

	app, err := proxyClient.SteveType(appsSteveType).NamespacedSteveClient(namespace).ByID(targetName)
	if err != nil {
		// Swallowed: the app may legitimately be absent, e.g. when the install never created it.
		logrus.Warnf("chart action diagnostics: app %s/%s get: %v", namespace, targetName, err)
	} else {
		appStatus, err := json.Marshal(app.Status)
		if err != nil {
			// Swallowed: diagnostics only.
			logrus.Warnf("chart action diagnostics: app %s/%s status marshal: %v", namespace, targetName, err)
		} else {
			logrus.Warnf("chart action diagnostics: app %s/%s status: %s", namespace, targetName, appStatus)
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
		if !isChartOperation(operation, targetName) {
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

// chartActionHTTPStatus extracts the HTTP status code the server returned from a chart
// action error, or 0 for transport-level failures that never got a response.
func chartActionHTTPStatus(err error) int32 {
	var statusErr *apierrors.StatusError
	if errors.As(err, &statusErr) {
		return statusErr.Status().Code
	}
	return 0
}

// cappedDiagnosticsBody truncates a raw response body to diagnosticsBodyCapBytes for logging.
func cappedDiagnosticsBody(body []byte) string {
	if len(body) <= diagnosticsBodyCapBytes {
		return string(body)
	}
	return string(body[:diagnosticsBodyCapBytes]) + "...(truncated)"
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

// logChartActionLifecycle emits the machine-readable lifecycle line for every chart action.
func logChartActionLifecycle(verb string, opts *PayloadOpts, repoName, targetName string) {
	logrus.Infof("chart action: ts=%s verb=%s chart=%s version=%s target=%s namespace=%s cluster=%s repo=%s",
		time.Now().UTC().Format(time.RFC3339), verb, opts.Name, opts.Version, targetName, opts.Namespace, opts.Cluster.ID, repoName)
}
