package uiplugins

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rancher/shepherd/clients/rancher/catalog"
	"github.com/rancher/shepherd/pkg/api/steve/catalog/types"
	"github.com/sirupsen/logrus"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	// installChartRetries is how many times an extension chart install is retried
	// when the catalog API reports a transient server-side failure.
	installChartRetries = 3
	// installChartRetryDelay is the quiet period between install retries.
	installChartRetryDelay = 5 * time.Second
)

// isTransientInstallError reports whether err is a server-side catalog failure
// worth retrying: an HTTP 500/503/504/429 (or the catalog API's generic
// "an error on the server (unknown)" StatusError) rather than a deterministic
// validation/render error in the request itself.
func isTransientInstallError(err error) bool {
	if err == nil {
		return false
	}
	if k8sErrors.IsInternalError(err) || k8sErrors.IsServerTimeout(err) ||
		k8sErrors.IsServiceUnavailable(err) || k8sErrors.IsTooManyRequests(err) ||
		k8sErrors.IsTimeout(err) {
		return true
	}
	// The catalog API surfaces unhandled controller errors as a generic
	// "an error on the server (unknown)" StatusError with no actionable reason.
	status, ok := err.(k8sErrors.APIStatus)
	if !ok {
		return false
	}
	return status.Status().Reason == metav1.StatusReasonUnknown
}

// installFailureContext gathers whatever the cluster currently reports for the
// failing extension so the surfaced error names a real condition instead of the
// opaque "unknown" server reason. Missing objects are skipped, not fatal.
func installFailureContext(catalogClient *catalog.Client, chartName, repoName string) string {
	var parts []string

	if app, getErr := catalogClient.Apps(extensionNamespace).Get(context.TODO(), chartName, metav1.GetOptions{}); getErr == nil {
		parts = append(parts, fmt.Sprintf("app state=%q error=%t", app.Status.Summary.State, app.Status.Summary.Error))
	}

	if repo, getErr := catalogClient.ClusterRepos().Get(context.TODO(), repoName, metav1.GetOptions{}); getErr == nil {
		for _, cond := range repo.Status.Conditions {
			if cond.Message != "" {
				parts = append(parts, fmt.Sprintf("repo condition %s=%s: %s", cond.Type, cond.Status, cond.Message))
			}
		}
	}

	if len(parts) == 0 {
		return "no additional install context available"
	}
	return "context: " + strings.Join(parts, "; ")
}

// installChartWithRetry runs the catalog chart-install action, retrying transient
// server-side errors, and on exhaustion returns an error enriched with the
// extension App state and ClusterRepo conditions so the real cause is visible.
func installChartWithRetry(catalogClient *catalog.Client, action *types.ChartInstallAction, repoName, chartName string) error {
	var lastErr error

	for attempt := 1; attempt <= installChartRetries; attempt++ {
		lastErr = catalogClient.InstallChart(action, repoName)
		if lastErr == nil {
			return nil
		}
		if !isTransientInstallError(lastErr) {
			return lastErr
		}

		logrus.Warnf("UI plugin %s install attempt %d/%d failed with transient catalog error: %v", chartName, attempt, installChartRetries, lastErr)
		if attempt < installChartRetries {
			_ = kwait.PollUntilContextTimeout(context.TODO(), installChartRetryDelay, installChartRetryDelay+time.Second, false, func(context.Context) (bool, error) {
				return true, nil
			})
		}
	}

	return fmt.Errorf("install UI plugin %s: %w; %s", chartName, lastErr, installFailureContext(catalogClient, chartName, repoName))
}
