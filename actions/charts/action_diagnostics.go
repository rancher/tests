package charts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
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

	// Steve schema ids are "<group>.<lowercased singular kind>" (same convention as
	// shepherd's stevetypes and neuvector's proven clusterrepo read via ProxyDownstream).
	appsSteveType        = "catalog.cattle.io.app"
	operationsSteveType  = "catalog.cattle.io.operation"
	clusterReposResource = "catalog.cattle.io.clusterrepo"
	chartRepoURLPath     = "v1/catalog.cattle.io.clusterrepos/"
	chartAppsURLPath     = "v1/catalog.cattle.io.apps/"
)

// Timing knobs are vars so the verification harness can scale them down.
// chartActionMaxElapsed is the retry budget: it gates whether another attempt may
// start. A single in-flight attempt is bounded by chartActionTimeout (the server-side
// Wait: true action timeout, also used by the payload constructors in payloads.go)
// plus slack, so legitimate slow installs are not cancelled early.
var (
	chartActionTimeout      = 600 * time.Second
	chartActionMaxAttempts  = 3
	chartActionRetrySpacing = 30 * time.Second
	chartActionMaxElapsed   = 150 * time.Second
	chartActionAttemptSlack = 30 * time.Second
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
// on server-answered transient failures (429 and 5xx responses; the observed PIT failures
// are 5xx whose raw bodies the k8s client flattens to "unknown"), emitting a lifecycle line
// per action, the response status and raw body of each failed attempt straight from the
// original rest.Result, and the downstream catalog state on final failure.
//
// repoName is the ClusterRepo the action targets (install/upgrade); it is empty for
// uninstalls, which address the App directly. targetNames lists every App the action
// applies to: the base release plus its -crd companion for charts that ship one in the
// same request, a single name for uninstalls and single-release charts. Diagnostics
// inspect every target; the returned error names the first.
func ChartActionWithRetry(ctx context.Context, client *rancher.Client, verb string, opts *PayloadOpts, repoName string, targetNames []string, req chartActionDoer) error {
	primaryTarget := opts.Name
	if len(targetNames) > 0 {
		primaryTarget = targetNames[0]
	}
	logChartActionLifecycle(verb, opts, repoName, targetNames)

	start := time.Now()
	budgetEnd := start.Add(chartActionMaxElapsed)
	attemptsMade := 0
	var lastErr error
	for attempt := 1; attempt <= chartActionMaxAttempts; attempt++ {
		attemptsMade = attempt
		// Each attempt is bounded by the server-side action timeout (Wait: true holds
		// the POST until the Helm operation settles) plus slack for a hung transport;
		// chartActionMaxElapsed below only gates whether another attempt may START.
		attemptCtx, cancel := context.WithTimeout(ctx, chartActionTimeout+chartActionAttemptSlack)
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
			time.Now().UTC().Format(time.RFC3339), verb, opts.Name, strings.Join(targetNames, ","), attempt, chartActionMaxAttempts,
			time.Since(start).Round(time.Millisecond), chartActionHTTPStatus(lastErr), lastErr, cappedDiagnosticsBody(body))

		if !isRetryableChartActionError(lastErr) || attempt == chartActionMaxAttempts {
			break
		}
		remaining := time.Until(budgetEnd)
		spacing := waitBetweenAttempts(attempt)
		if remaining <= spacing {
			// Not enough budget left for the inter-attempt delay plus a meaningful retry.
			break
		}
		if !sleepBetweenAttempts(ctx, spacing) {
			// Cancellation is the cause of failure from here on, not the stale HTTP error.
			lastErr = ctx.Err()
			break
		}
		if time.Now().After(budgetEnd) {
			// Scheduler delay can let the sleep finish past the budget; never start
			// another attempt after it has expired.
			break
		}
	}

	LogChartActionFailure(ctx, client, repoName, opts.Cluster.ID, opts.Namespace, targetNames)
	return fmt.Errorf("chart action %s %s failed after %d attempts: %w", verb, primaryTarget, attemptsMade, lastErr)
}

// waitBetweenAttempts returns the fixed delay between chart action retry attempts.
func waitBetweenAttempts(_ int) time.Duration {
	return chartActionRetrySpacing
}

// isRetryableChartActionError reports whether a chart action error is worth replaying.
// Only server-answered failures are replay candidates: 429 (rate limited) and 5xx
// responses (the observed PIT failures). Every other 4xx is deterministic and cannot
// recover on retry. Errors without an HTTP status are transport-level failures where
// the server may already have processed the request without us seeing the response;
// replaying the non-idempotent chart action could then launch a duplicate Helm
// operation, so they are terminal and left to the diagnostics bundle.
func isRetryableChartActionError(err error) bool {
	var statusErr *apierrors.StatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	code := statusErr.Status().Code
	return code == http.StatusTooManyRequests || code >= 500
}

// LogChartActionFailure dumps the catalog state around a failed chart action: the
// ClusterRepo the action targeted (when non-empty), the target Apps, and the catalog
// Operations in the chart's namespace, all through the downstream steve proxy. The bundle
// runs under a wall-clock bound because shepherd's ProxyDownstream and steve reads accept
// no context and must not delay failure propagation indefinitely. Every step logs and
// swallows its own error so diagnostics never alter the failure the caller returns.
func LogChartActionFailure(ctx context.Context, client *rancher.Client, repoName, clusterID, namespace string, targetNames []string) {
	if client == nil {
		// Diagnostics are best-effort; without a client there is nothing to read with.
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				// Swallowed: shepherd client internals can panic (multi-page
				// collections dereference an unset client, for example); diagnostics
				// must never take the test process down with them.
				logrus.Warnf("chart action diagnostics: recovered from panic: %v", r)
			}
		}()
		logDownstreamCatalogDiagnostics(client, clusterID, repoName, namespace, targetNames)
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
// empty for uninstalls), each target App's status, and the catalog operations in the chart's
// namespace through the downstream steve proxy. The ClusterRepo is read via the proxy with
// the singular steve type (the same proven pattern as neuvector's
// waitForRancherChartsRepo); the management steve client rejects the clusterrepo schema
// type with "Unknown schema type".
func logDownstreamCatalogDiagnostics(client *rancher.Client, clusterID, repoName, namespace string, targetNames []string) {
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

	// Install/upgrade requests can carry both the base release and its -crd companion,
	// so each target App is inspected, not just the primary.
	for _, targetName := range targetNames {
		// NamespacedSteveClient.ByID delegates to the embedded SteveClient.ByID, which
		// ignores the namespace, so the id must be namespace-qualified.
		app, err := proxyClient.SteveType(appsSteveType).ByID(namespace + "/" + targetName)
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
	}

	// NamespacedSteveClient.List scopes the collection URL to the namespace. Single page
	// only: shepherd's ListAll panics when pagination.next is set, because List never
	// initializes the private collection client that Next() dereferences. The recover
	// guard in LogChartActionFailure is the second line of defense for other panics.
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
		if !isChartOperation(operation, targetNames) {
			continue
		}
		logged++
		logrus.Warnf("chart action diagnostics: operation %s status: %s", operation.Name, chartOperationStatusForLog(operation))
	}
}

// isChartOperation reports whether an operation object relates to any target chart.
// Operations are generated with helm-operation-* names and carry no chart labels; the
// reliable correlation is status.releaseName, which rancher sets to the release (the App
// name) the operation acts on.
func isChartOperation(operation *steveV1.SteveAPIObject, targetNames []string) bool {
	return slices.Contains(targetNames, chartOperationReleaseName(operation))
}

// chartOperationReleaseName returns status.releaseName, or "" when absent.
func chartOperationReleaseName(operation *steveV1.SteveAPIObject) string {
	status, _ := operation.Status.(map[string]any)
	releaseName, _ := status["releaseName"].(string)
	return releaseName
}

// chartOperationStatusForLog renders an Operation status restricted to an allowlist of
// non-sensitive fields: the full status carries the helm pod's bearer token and command
// line, which must not reach Jenkins logs or archived evidence.
func chartOperationStatusForLog(operation *steveV1.SteveAPIObject) string {
	raw, err := json.Marshal(operation.Status)
	if err != nil {
		// Swallowed: diagnostics only.
		return fmt.Sprintf("status marshal: %v", err)
	}
	var allowlisted struct {
		ObservedGeneration int64  `json:"observedGeneration,omitempty"`
		Action             string `json:"action,omitempty"`
		Chart              string `json:"chart,omitempty"`
		Version            string `json:"version,omitempty"`
		ReleaseName        string `json:"releaseName,omitempty"`
		Namespace          string `json:"namespace,omitempty"`
		PodName            string `json:"podName,omitempty"`
		PodCreated         bool   `json:"podCreated,omitempty"`
		Conditions         []any  `json:"conditions,omitempty"`
	}
	if err := json.Unmarshal(raw, &allowlisted); err != nil {
		// Swallowed: diagnostics only.
		return fmt.Sprintf("status decode: %v", err)
	}
	projected, err := json.Marshal(allowlisted)
	if err != nil {
		// Swallowed: diagnostics only.
		return fmt.Sprintf("status project: %v", err)
	}
	return string(projected)
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
// re-issue the identical request. MaxRetries(0) disables client-go's internal retry
// (default 10 on Retry-After responses) so ChartActionWithRetry is the sole retry
// owner and the attempt count in the logs matches the requests actually sent.
func buildRepoActionRequest(catalogClient *catalog.Client, repoName, verb string, bodyBytes []byte) *rest.Request {
	return catalogClient.RESTClient().Post().
		AbsPath(chartRepoURLPath+repoName).Param(actionParam, verb).
		VersionedParams(&metav1.CreateOptions{}, scheme.ParameterCodec).
		Body(bodyBytes).
		MaxRetries(0)
}

// buildAppUninstallRequest mirrors shepherd's UninstallChart request shape without
// executing it. MaxRetries(0) keeps ChartActionWithRetry the sole retry owner.
func buildAppUninstallRequest(catalogClient *catalog.Client, namespace, chartName string, bodyBytes []byte) *rest.Request {
	return catalogClient.RESTClient().Post().
		Name(chartName).
		AbsPath(chartAppsURLPath+namespace).Param(actionParam, verbUninstall).
		Body(bodyBytes).
		VersionedParams(&metav1.CreateOptions{}, scheme.ParameterCodec).
		MaxRetries(0)
}

// logChartActionLifecycle emits the machine-readable lifecycle line for every chart action.
func logChartActionLifecycle(verb string, opts *PayloadOpts, repoName string, targetNames []string) {
	logrus.Infof("chart action: ts=%s verb=%s chart=%s version=%s target=%s namespace=%s cluster=%s repo=%s",
		time.Now().UTC().Format(time.RFC3339), verb, opts.Name, opts.Version, strings.Join(targetNames, ","), opts.Namespace, opts.Cluster.ID, repoName)
}
