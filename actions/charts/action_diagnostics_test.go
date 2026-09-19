//go:build validation

package charts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	scheme "github.com/rancher/shepherd/pkg/generated/clientset/versioned/scheme"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

// newChartActionTestDoer builds a real rest client against a throwaway server so the
// wrapper is exercised against genuine rest.Result objects, including error results
// with retained raw bodies. The server is also returned so tests can simulate
// transport-level failures by closing it.
func newChartActionTestDoer(t *testing.T, handler http.Handler) (chartActionDoer, *httptest.Server) {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	config := &rest.Config{
		Host: server.URL,
		ContentConfig: rest.ContentConfig{
			GroupVersion:         &schema.GroupVersion{Group: "catalog.cattle.io", Version: "v1"},
			NegotiatedSerializer: scheme.Codecs.WithoutConversion(),
		},
	}
	client, err := rest.RESTClientFor(config)
	if err != nil {
		t.Fatalf("rest client build failed: %v", err)
	}
	return client.Post().
		AbsPath(chartRepoURLPath+"rancher-charts").
		Param(actionParam, verbInstall).
		Body([]byte(`{"chart": "rancher-monitoring"}`)), server
}

func chartActionTestOpts() *PayloadOpts {
	return &PayloadOpts{
		InstallOptions: InstallOptions{Cluster: &clusters.ClusterMeta{ID: "c-test-local"}},
		Name:           "rancher-monitoring",
		Namespace:      "cattle-monitoring-system",
	}
}

// stubChartActionTiming swaps the package timing knobs for the duration of one test and
// restores them afterwards. The attempt cap is timeout+slack per attempt; maxElapsed is
// the budget that gates whether another attempt may start.
func stubChartActionTiming(t *testing.T, spacing, maxElapsed, timeout, slack time.Duration) {
	t.Helper()

	origSpacing, origElapsed := chartActionRetrySpacing, chartActionMaxElapsed
	origTimeout, origSlack := chartActionTimeout, chartActionAttemptSlack
	chartActionRetrySpacing, chartActionMaxElapsed = spacing, maxElapsed
	chartActionTimeout, chartActionAttemptSlack = timeout, slack
	t.Cleanup(func() {
		chartActionRetrySpacing, chartActionMaxElapsed = origSpacing, origElapsed
		chartActionTimeout, chartActionAttemptSlack = origTimeout, origSlack
	})
}

func captureChartActionLogs(t *testing.T) *strings.Builder {
	t.Helper()

	var buf strings.Builder
	origOut, origLevel := logrus.StandardLogger().Out, logrus.StandardLogger().GetLevel()
	logrus.SetOutput(&buf)
	logrus.SetLevel(logrus.WarnLevel)
	t.Cleanup(func() {
		logrus.SetOutput(origOut)
		logrus.SetLevel(origLevel)
	})
	return &buf
}

func TestChartActionWithRetryCapturesStatusAndBody(t *testing.T) {
	logs := captureChartActionLogs(t)
	stubChartActionTiming(t, time.Millisecond, time.Second, time.Second, 0)

	doer, _ := newChartActionTestDoer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"kind":"Status","reason":"InternalError","message":"helm upgrade failed"}`))
	}))

	err := ChartActionWithRetry(context.Background(), nil, verbInstall, chartActionTestOpts(), "rancher-charts", []string{"rancher-monitoring"}, doer)
	if err == nil {
		t.Fatal("expected failure, got nil")
	}

	if !strings.Contains(logs.String(), "status=500") {
		t.Errorf("per-attempt log missing HTTP status, got: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "helm upgrade failed") {
		t.Errorf("per-attempt log missing raw response body, got: %s", logs.String())
	}
	if !strings.Contains(err.Error(), "install rancher-monitoring failed") {
		t.Errorf("error does not name the action target, got: %v", err)
	}
}

func TestChartActionWithRetryRecoversAfterTransientFailures(t *testing.T) {
	stubChartActionTiming(t, time.Millisecond, time.Second, time.Second, 0)

	var requests atomic.Int32
	doer, _ := newChartActionTestDoer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) <= 2 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"kind":"Status","code":500,"reason":"InternalError","message":"transient"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	err := ChartActionWithRetry(context.Background(), nil, verbInstall, chartActionTestOpts(), "rancher-charts", []string{"rancher-monitoring", "rancher-monitoring-crd"}, doer)
	if err != nil {
		t.Fatalf("expected recovery on third attempt, got: %v", err)
	}
	if got := requests.Load(); got != 3 {
		t.Errorf("expected exactly 3 requests on the recovery path, got %d", got)
	}
}

func TestChartActionWithRetryBoundsHungAttempts(t *testing.T) {
	// A hung transport is cut off by the per-attempt cap (timeout+slack = 100ms) long
	// before the server-side action timeout would answer; the failure is terminal
	// (no HTTP status) so no replay happens either.
	stubChartActionTiming(t, 10*time.Millisecond, time.Hour, 80*time.Millisecond, 20*time.Millisecond)

	var requests atomic.Int32
	doer, _ := newChartActionTestDoer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		select {
		case <-time.After(400 * time.Millisecond):
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))

	start := time.Now()
	err := ChartActionWithRetry(context.Background(), nil, verbInstall, chartActionTestOpts(), "rancher-charts", []string{"rancher-monitoring"}, doer)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected failure, got nil")
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("expected exactly 1 attempt once the attempt cap fired, got %d", got)
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("wrapper ran %s past the 100ms attempt cap", elapsed)
	}
	if !strings.Contains(err.Error(), "failed after 1 attempts") {
		t.Errorf("error does not report bounded attempts, got: %v", err)
	}
}

func TestChartActionWithRetrySlowAttemptWithinActionTimeoutSucceeds(t *testing.T) {
	// Wait:true actions legitimately block for the server-side action timeout; a
	// response arriving just under the attempt cap must not be cancelled.
	stubChartActionTiming(t, time.Millisecond, 10*time.Millisecond, 500*time.Millisecond, 100*time.Millisecond)

	doer, _ := newChartActionTestDoer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-time.After(150 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		}
	}))

	// The 10ms retry budget only gates retries; the single slow attempt itself is
	// bounded by the 600ms attempt cap and succeeds.
	err := ChartActionWithRetry(context.Background(), nil, verbInstall, chartActionTestOpts(), "rancher-charts", []string{"rancher-monitoring"}, doer)
	if err != nil {
		t.Fatalf("slow-but-legitimate attempt was cut off or failed: %v", err)
	}
}

func TestChartActionWithRetryStopsStartingAttemptsPastBudget(t *testing.T) {
	stubChartActionTiming(t, 200*time.Millisecond, 250*time.Millisecond, time.Second, 0)

	var requests atomic.Int32
	doer, _ := newChartActionTestDoer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))

	err := ChartActionWithRetry(context.Background(), nil, verbInstall, chartActionTestOpts(), "rancher-charts", []string{"rancher-monitoring"}, doer)
	if err == nil {
		t.Fatal("expected failure, got nil")
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("budget must stop attempts once it cannot fit the spacing, got %d requests", got)
	}
	if !strings.Contains(err.Error(), "failed after 2 attempts") {
		t.Errorf("error does not report budget-bounded attempts, got: %v", err)
	}
}

func TestChartActionWithRetryHonorsContextCancellationBetweenAttempts(t *testing.T) {
	stubChartActionTiming(t, 10*time.Second, time.Hour, time.Second, 0)

	var requests atomic.Int32
	doer, _ := newChartActionTestDoer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	err := ChartActionWithRetry(ctx, nil, verbInstall, chartActionTestOpts(), "rancher-charts", []string{"rancher-monitoring"}, doer)
	if err == nil {
		t.Fatal("expected failure, got nil")
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("expected exactly 1 attempt after mid-sleep cancellation, got %d", got)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancellation must be the propagated cause, got: %v", err)
	}
}

func TestChartActionWithRetryUninstallNamesTargetNotRepo(t *testing.T) {
	logs := captureChartActionLogs(t)
	stubChartActionTiming(t, time.Millisecond, time.Second, time.Second, 0)

	doer, _ := newChartActionTestDoer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"kind":"Status","reason":"NotFound","message":"apps.catalog.cattle.io not found"}`))
	}))

	err := ChartActionWithRetry(context.Background(), nil, verbUninstall, chartActionTestOpts(), "", []string{"rancher-monitoring-crd"}, doer)
	if err == nil {
		t.Fatal("expected failure, got nil")
	}
	if !strings.Contains(err.Error(), "uninstall rancher-monitoring-crd failed after 1 attempts") {
		t.Errorf("uninstall error does not name the CRD target with single attempt, got: %v", err)
	}
	if !strings.Contains(logs.String(), "target=rancher-monitoring-crd") {
		t.Errorf("failure log missing target name, got: %s", logs.String())
	}
}

func TestChartActionWithRetryDoesNotRetryClientErrors(t *testing.T) {
	logs := captureChartActionLogs(t)
	stubChartActionTiming(t, time.Millisecond, time.Second, time.Second, 0)

	var requests atomic.Int32
	doer, _ := newChartActionTestDoer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"kind":"Status","code":400,"reason":"BadRequest","message":"invalid chart values"}`))
	}))

	err := ChartActionWithRetry(context.Background(), nil, verbInstall, chartActionTestOpts(), "rancher-charts", []string{"rancher-monitoring"}, doer)
	if err == nil {
		t.Fatal("expected failure, got nil")
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("terminal client error must not be retried, got %d requests", got)
	}
	if !apierrors.IsBadRequest(err) {
		t.Errorf("wrapped API error not preserved through the retry error, got: %v", err)
	}
	if !strings.Contains(logs.String(), "status=400") {
		t.Errorf("failure log missing HTTP status, got: %s", logs.String())
	}
}

func TestChartActionWithRetryDoesNotRetryTransportErrors(t *testing.T) {
	stubChartActionTiming(t, time.Millisecond, time.Second, time.Second, 0)

	// A closed server means the request never gets an HTTP answer: the server may have
	// processed an attempt without the client seeing a response, so the wrapper must
	// treat the unknown-state failure as terminal rather than replay the action.
	var requests atomic.Int32
	doer, server := newChartActionTestDoer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
	}))
	server.Close()

	err := ChartActionWithRetry(context.Background(), nil, verbInstall, chartActionTestOpts(), "rancher-charts", []string{"rancher-monitoring"}, doer)
	if err == nil {
		t.Fatal("expected failure, got nil")
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("transport failure must not be replayed, got %d handler calls", got)
	}
	if !strings.Contains(err.Error(), "failed after 1 attempts") {
		t.Errorf("transport failure must stop after one attempt, got: %v", err)
	}
}

func TestLogChartActionFailureNilClientIsNoop(t *testing.T) {
	// Must not panic or log diagnostics when there is no client to read with.
	LogChartActionFailure(context.Background(), nil, "rancher-charts", "c-local", "cattle-monitoring-system", []string{"rancher-monitoring"})
}

func TestChartOperationStatusForLogRedactsSensitiveFields(t *testing.T) {
	operation := &v1.SteveAPIObject{
		Status: map[string]any{
			"action":      "install",
			"chart":       "rancher-monitoring",
			"version":     "102.0.0",
			"releaseName": "rancher-monitoring",
			"namespace":   "cattle-monitoring-system",
			"podName":     "helm-operation-abc123",
			"podCreated":  true,
			"token":       "kubeconfig-u-abcdef-secrettoken",
			"command":     []any{"helm", "upgrade", "--set", "token=secret"},
		},
	}
	operation.Name = "helm-operation-abc123"

	logged := chartOperationStatusForLog(operation)
	for _, secret := range []string{"secrettoken", "token=", "helm\", \"upgrade"} {
		if strings.Contains(logged, secret) {
			t.Errorf("operation status log leaked sensitive material %q: %s", secret, logged)
		}
	}
	for _, want := range []string{"install", "rancher-monitoring", "helm-operation-abc123"} {
		if !strings.Contains(logged, want) {
			t.Errorf("operation status log missing diagnostic field %q: %s", want, logged)
		}
	}
}

func TestIsChartOperationMatchesAnyTarget(t *testing.T) {
	operation := &v1.SteveAPIObject{
		Status: map[string]any{"releaseName": "rancher-monitoring-crd"},
	}
	operation.Name = "helm-operation-xyz789"

	if !isChartOperation(operation, []string{"rancher-monitoring", "rancher-monitoring-crd"}) {
		t.Error("companion CRD release must match the target set")
	}
	if isChartOperation(operation, []string{"rancher-monitoring"}) {
		t.Error("non-target release must not match")
	}
}

func TestChartActionHTTPStatus(t *testing.T) {
	doer, _ := newChartActionTestDoer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))

	result := doer.Do(context.Background())
	err := result.Error()
	if err == nil {
		t.Fatal("expected error from 502 handler")
	}
	if got := chartActionHTTPStatus(err); got != http.StatusBadGateway {
		t.Errorf("chartActionHTTPStatus = %d, want %d", got, http.StatusBadGateway)
	}
	if got := chartActionHTTPStatus(nil); got != 0 {
		t.Errorf("chartActionHTTPStatus(nil) = %d, want 0", got)
	}
}
