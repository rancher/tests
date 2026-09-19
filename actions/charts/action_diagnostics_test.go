package charts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rancher/shepherd/extensions/clusters"
	scheme "github.com/rancher/shepherd/pkg/generated/clientset/versioned/scheme"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

// newChartActionTestDoer builds a real rest client against a throwaway server so the
// wrapper is exercised against genuine rest.Result objects, including error results
// with retained raw bodies.
func newChartActionTestDoer(t *testing.T, handler http.Handler) chartActionDoer {
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
		AbsPath(chartRepoURLPath + "rancher-charts").
		Param(actionParam, verbInstall).
		Body([]byte(`{"chart": "rancher-monitoring"}`))
}

func chartActionTestOpts() *PayloadOpts {
	return &PayloadOpts{
		InstallOptions: InstallOptions{Cluster: &clusters.ClusterMeta{ID: "c-test-local"}},
		Name:           "rancher-monitoring",
		Namespace:      "cattle-monitoring-system",
	}
}

// stubChartActionTiming swaps the package timing knobs for the duration of one test and
// restores them afterwards.
func stubChartActionTiming(t *testing.T, spacing, maxElapsed time.Duration) {
	t.Helper()

	origSpacing, origElapsed := chartActionRetrySpacing, chartActionMaxElapsed
	chartActionRetrySpacing, chartActionMaxElapsed = spacing, maxElapsed
	t.Cleanup(func() {
		chartActionRetrySpacing, chartActionMaxElapsed = origSpacing, origElapsed
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
	stubChartActionTiming(t, time.Millisecond, time.Second)

	doer := newChartActionTestDoer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"kind":"Status","reason":"InternalError","message":"helm upgrade failed"}`))
	}))

	err := ChartActionWithRetry(context.Background(), nil, verbInstall, chartActionTestOpts(), "rancher-charts", "rancher-monitoring", doer)
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

func TestChartActionWithRetryStopsAtElapsedDeadline(t *testing.T) {
	stubChartActionTiming(t, 10*time.Millisecond, 100*time.Millisecond)

	var requests atomic.Int32
	// Each handler call blocks well past the 100ms loop deadline, so only the first
	// attempt may start; its per-attempt context must cut the hung request off.
	doer := newChartActionTestDoer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		select {
		case <-time.After(400 * time.Millisecond):
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))

	start := time.Now()
	err := ChartActionWithRetry(context.Background(), nil, verbInstall, chartActionTestOpts(), "rancher-charts", "rancher-monitoring", doer)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected failure, got nil")
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("expected exactly 1 attempt once the elapsed budget was spent, got %d", got)
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("wrapper ran %s past the 100ms deadline bound", elapsed)
	}
	if !strings.Contains(err.Error(), "failed after 1 attempts") {
		t.Errorf("error does not report bounded attempts, got: %v", err)
	}
}

func TestChartActionWithRetryHonorsContextCancellationBetweenAttempts(t *testing.T) {
	stubChartActionTiming(t, 10*time.Second, time.Hour)

	var requests atomic.Int32
	doer := newChartActionTestDoer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	err := ChartActionWithRetry(ctx, nil, verbInstall, chartActionTestOpts(), "rancher-charts", "rancher-monitoring", doer)
	if err == nil {
		t.Fatal("expected failure, got nil")
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("expected exactly 1 attempt after mid-sleep cancellation, got %d", got)
	}
}

func TestChartActionWithRetryUninstallNamesTargetNotRepo(t *testing.T) {
	logs := captureChartActionLogs(t)
	stubChartActionTiming(t, time.Millisecond, time.Second)

	doer := newChartActionTestDoer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"kind":"Status","reason":"NotFound","message":"apps.catalog.cattle.io not found"}`))
	}))

	err := ChartActionWithRetry(context.Background(), nil, verbUninstall, chartActionTestOpts(), "", "rancher-monitoring-crd", doer)
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

func TestLogChartActionFailureNilClientIsNoop(t *testing.T) {
	// Must not panic or log diagnostics when there is no client to read with.
	LogChartActionFailure(context.Background(), nil, "rancher-charts", "c-local", "cattle-monitoring-system", "rancher-monitoring")
}

func TestChartActionHTTPStatus(t *testing.T) {
	doer := newChartActionTestDoer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
