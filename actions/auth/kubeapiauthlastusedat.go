package auth

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	clusterv3 "github.com/rancher/rancher/pkg/apis/cluster.cattle.io/v3"
	extapi "github.com/rancher/rancher/pkg/apis/ext.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	exttokenapi "github.com/rancher/shepherd/extensions/kubeapi/tokens"
	"github.com/rancher/shepherd/extensions/kubeapi/workloads/daemonsets"
	"github.com/rancher/shepherd/extensions/kubeconfig"
	"github.com/rancher/shepherd/extensions/kubectl"
	"github.com/rancher/tests/actions/kubeapi/tokens"
	"github.com/rancher/tests/actions/kubeapi/tokens/exttokens"
	"github.com/rancher/tests/actions/kubeapi/workloads/pods"
	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

const (
	reviewLogBuffer        = "256KB"
	reviewStartTolerance   = 10
	reviewMarker           = "REVIEW"
	reviewDone             = "DONE"
	reviewNode             = "NODE"
	reviewLate             = "LATE"
	restartedAtAnnotation  = "kubectl.kubernetes.io/restartedAt"
	clusterAuthTokenGroup  = "cluster.cattle.io"
	clusterAuthTokenPlural = "clusterauthtokens"
	patchVerb              = "patch"
)

var kubeAPIAuthErrorMarkers = []string{"error updating clusterAuthToken", "forbidden", "conflict"}

// ReviewPlan describes the TokenReviews to send for one token
type ReviewPlan struct {
	Token      string
	Count      int
	Interval   time.Duration
	Parallel   bool
	Bursts     int
	BurstPause time.Duration
}

// ReviewRun holds what a review job sent together with the node it ran on
type ReviewRun struct {
	Node     string
	Requests [][]Request
}

// NewClusterScopedExtCredential mints an ext token scoped to the cluster so that kube-api-auth gets a cluster auth token for it
func NewClusterScopedExtCredential(client *rancher.Client, clusterID string) (*Credential, error) {
	token := exttokens.NewExtTokenObject("", lastUsedAtExtTokenTTL)
	token.Spec.ClusterName = clusterID

	created, err := exttokenapi.CreateExtToken(client, token)
	if err != nil {
		return nil, fmt.Errorf("minting an ext token scoped to cluster %s: %w", clusterID, err)
	}

	if created.Status.BearerToken == "" {
		return nil, fmt.Errorf("Rancher returned ext token %s without its bearer token, which is only handed back at creation", created.Name)
	}

	return NewCredentialFromBearer(tokens.Ext, created.Status.BearerToken)
}

// SendTokenReviews posts the planned TokenReviews to kube-api-auth from a control plane node, all starting at the given time or at once when it is zero
func SendTokenReviews(client *rancher.Client, clusterID string, startAt time.Time, plans ...ReviewPlan) (*ReviewRun, error) {
	command := []string{"sh", "-c", reviewScript(startAt, plans)}

	output, err := kubectl.CommandOnControlPlane(client, clusterID, command, reviewLogBuffer)
	if err != nil {
		return nil, fmt.Errorf("sending TokenReviews to %s: %w", KubeAPIAuthEndpoint, err)
	}

	return parseReviewOutput(output, len(plans))
}

// ReviewOnCadence sends one TokenReview every interval until the span has passed, in jobs short enough for the node job to outlive
func ReviewOnCadence(client *rancher.Client, clusterID, token string, interval, span time.Duration) ([]Request, error) {
	started := time.Now()
	sent := []Request{}

	for span-time.Since(started) >= interval {
		chunk := min(span-time.Since(started), 3*defaults.TenSecondTimeout)
		plan := ReviewPlan{Token: token, Count: max(int(chunk/interval), 1), Interval: interval}

		run, err := SendTokenReviews(client, clusterID, time.Time{}, plan)
		if err != nil {
			return sent, err
		}

		sent = append(sent, run.Requests[0]...)
	}

	return sent, nil
}

func reviewScript(startAt time.Time, plans []ReviewPlan) string {
	var script strings.Builder

	start := int64(0)
	if !startAt.IsZero() {
		start = startAt.Unix()
	}

	script.WriteString(fmt.Sprintf("START=%d\n", start))
	script.WriteString(`wait_start() { while [ $((START - $(date +%s))) -gt 1 ]; do sleep 1; done; while [ "$(date +%s)" -lt "$START" ]; do :; done; }` + "\n")
	script.WriteString(fmt.Sprintf(`review() { out=$(curl -s -m 30 -w ' %%{http_code}' -X POST %s -H 'Content-Type: application/json' --data-binary "$2"); `+
		`case "$out" in *'"authenticated":true'*) ok=1;; *) ok=0;; esac; echo "%s $1 $(date +%%s) ${out##* } $ok"; }`+"\n",
		KubeAPIAuthEndpoint, reviewMarker))
	script.WriteString(fmt.Sprintf("echo %s $(hostname)\n", reviewNode))
	script.WriteString(fmt.Sprintf(`wait_start; if [ "$START" -gt 0 ] && [ "$(date +%%s)" -gt "$((START+%d))" ]; then echo %s; fi`+"\n", reviewStartTolerance, reviewLate))

	for index, plan := range plans {
		script.WriteString(fmt.Sprintf("P%d=%s\n", index, shellQuote(NewTokenReviewPayload(TokenReviewAPIV1, TokenReviewKind, plan.Token))))

		if plan.Parallel {
			script.WriteString(fmt.Sprintf(`( b=0; while [ $b -lt %d ]; do i=0; while [ $i -lt %d ]; do review %d "$P%d" & i=$((i+1)); done; wait; b=$((b+1)); `+
				`if [ $b -lt %d ]; then sleep %s; fi; done ) &`+"\n",
				max(plan.Bursts, 1), plan.Count, index, index, max(plan.Bursts, 1), shellSeconds(plan.BurstPause)))

			continue
		}

		script.WriteString(fmt.Sprintf(`( i=0; while [ $i -lt %d ]; do review %d "$P%d"; i=$((i+1)); if [ $i -lt %d ]; then sleep %s; fi; done ) &`+"\n",
			plan.Count, index, index, plan.Count, shellSeconds(plan.Interval)))
	}

	script.WriteString(fmt.Sprintf("wait\necho %s\n", reviewDone))

	return script.String()
}

func shellSeconds(duration time.Duration) string {
	return strconv.FormatFloat(duration.Seconds(), 'f', -1, 64)
}

func parseReviewOutput(output string, plans int) (*ReviewRun, error) {
	run := &ReviewRun{Requests: make([][]Request, plans)}
	done := false

	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		switch fields[0] {
		case reviewDone:
			done = true
		case reviewNode:
			if len(fields) == 2 {
				run.Node = fields[1]
			}
		case reviewLate:
			return nil, fmt.Errorf("the job began more than %ds after its start time, so the stored values it planned for had aged past their offsets", reviewStartTolerance)
		case reviewMarker:
			if len(fields) != 5 {
				return nil, fmt.Errorf("a review line has %d fields where 5 were expected: %q", len(fields), line)
			}

			index, indexErr := strconv.Atoi(fields[1])
			epoch, epochErr := strconv.ParseInt(fields[2], 10, 64)
			status, statusErr := strconv.Atoi(fields[3])

			if indexErr != nil || epochErr != nil || statusErr != nil || index < 0 || index >= plans {
				return nil, fmt.Errorf("a review line does not parse: %q", line)
			}

			run.Requests[index] = append(run.Requests[index], Request{At: time.Unix(epoch, 0), StatusCode: status, Authenticated: fields[4] == "1"})
		}
	}

	if !done {
		return nil, fmt.Errorf("the review job ended before it finished, so the output holds only part of what it sent: %s", output)
	}

	for _, requests := range run.Requests {
		sort.Slice(requests, func(i, j int) bool { return requests[i].At.Before(requests[j].At) })
	}

	return run, nil
}

// VerifyNoKubeAPIAuthErrors fails when any kube-api-auth pod logged a failed write, a forbidden or a conflict since the baseline
func VerifyNoKubeAPIAuthErrors(client *rancher.Client, clusterID string, baseline map[string]string) error {
	current, err := KubeAPIAuthLogs(client, clusterID)
	if err != nil {
		return fmt.Errorf("reading what %v logged: %w", KubeAPIAuthDaemonSet, err)
	}

	for pod, logs := range current {
		appended := logsSince(baseline[pod], logs)

		for _, marker := range kubeAPIAuthErrorMarkers {
			if strings.Contains(strings.ToLower(appended), strings.ToLower(marker)) {
				return fmt.Errorf("%s on %s logged %q", KubeAPIAuthDaemonSet, pod, marker)
			}
		}
	}

	return nil
}

// VerifyKubeAPIAuthRunsOnEveryControlPlaneNode fails unless one kube-api-auth pod is running for each control plane node
func VerifyKubeAPIAuthRunsOnEveryControlPlaneNode(client *rancher.Client, clusterID string) error {
	wanted, err := ControlPlaneNodeCount(client, clusterID)
	if err != nil {
		return fmt.Errorf("counting the control plane nodes: %w", err)
	}

	podList, err := pods.GetPodsByLabelSelector(client, clusterID, KubeAPIAuthNamespace, kubeAPIAuthAppLabel)
	if err != nil {
		return fmt.Errorf("listing the %s pods on cluster %s: %w", KubeAPIAuthDaemonSet, clusterID, err)
	}

	running := 0

	for _, pod := range podList {
		if pod.Status.Phase == corev1.PodRunning {
			running++
		}
	}

	if running != wanted {
		return fmt.Errorf("%d %s pods are running for %d control plane nodes", running, KubeAPIAuthDaemonSet, wanted)
	}

	return nil
}

// VerifyKubeAPIAuthCanPatchClusterAuthTokens fails unless the kube-api-auth service account may patch cluster auth tokens
func VerifyKubeAPIAuthCanPatchClusterAuthTokens(client *rancher.Client, clusterID string) error {
	daemonSet, err := daemonsets.GetDaemonSetByName(client, clusterID, KubeAPIAuthNamespace, KubeAPIAuthDaemonSet)
	if err != nil {
		return fmt.Errorf("reading the %s daemon set on cluster %s: %w", KubeAPIAuthDaemonSet, clusterID, err)
	}

	serviceAccount := daemonSet.Spec.Template.Spec.ServiceAccountName
	if serviceAccount == "" {
		return fmt.Errorf("the %s daemon set runs under no service account of its own", KubeAPIAuthDaemonSet)
	}

	kubeConfig, err := kubeconfig.GetKubeconfig(client, clusterID)
	if err != nil {
		return fmt.Errorf("getting a kubeconfig for cluster %s: %w", clusterID, err)
	}

	restConfig, err := (*kubeConfig).ClientConfig()
	if err != nil {
		return fmt.Errorf("building a client for cluster %s: %w", clusterID, err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("building a clientset for cluster %s: %w", clusterID, err)
	}

	review, err := clientset.AuthorizationV1().SubjectAccessReviews().Create(context.Background(), &authzv1.SubjectAccessReview{
		Spec: authzv1.SubjectAccessReviewSpec{
			User: fmt.Sprintf("system:serviceaccount:%s:%s", KubeAPIAuthNamespace, serviceAccount),
			ResourceAttributes: &authzv1.ResourceAttributes{
				Verb: patchVerb, Group: clusterAuthTokenGroup, Resource: clusterAuthTokenPlural, Namespace: KubeAPIAuthNamespace,
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("asking cluster %s whether %s may patch %s: %w", clusterID, serviceAccount, clusterAuthTokenPlural, err)
	}

	if !review.Status.Allowed {
		return fmt.Errorf("service account %s may not %s %s, so a write of lastUsedAt would be forbidden", serviceAccount, patchVerb, clusterAuthTokenPlural)
	}

	return nil
}

// RestartKubeAPIAuth rolls the kube-api-auth daemon set the way a rollout restart does and waits for it to be ready
func RestartKubeAPIAuth(client *rancher.Client, clusterID string) error {
	daemonSet, err := daemonsets.GetDaemonSetByName(client, clusterID, KubeAPIAuthNamespace, KubeAPIAuthDaemonSet)
	if err != nil {
		return fmt.Errorf("reading the %s daemon set on cluster %s: %w", KubeAPIAuthDaemonSet, clusterID, err)
	}

	if daemonSet.Spec.Template.Annotations == nil {
		daemonSet.Spec.Template.Annotations = map[string]string{}
	}

	daemonSet.Spec.Template.Annotations[restartedAtAnnotation] = time.Now().UTC().Format(time.RFC3339)

	if _, err := daemonsets.UpdateDaemonSet(client, clusterID, daemonSet, true); err != nil {
		return fmt.Errorf("restarting the %s daemon set on cluster %s: %w", KubeAPIAuthDaemonSet, clusterID, err)
	}

	return nil
}

// VerifyOnlyLastUsedAtChanged fails when a cluster auth token differs from its earlier read in anything but lastUsedAt
func VerifyOnlyLastUsedAtChanged(before, after *clusterv3.ClusterAuthToken) error {
	comparable := func(token *clusterv3.ClusterAuthToken) *clusterv3.ClusterAuthToken {
		copied := token.DeepCopy()
		copied.LastUsedAt = nil
		copied.ResourceVersion = ""
		copied.Generation = 0
		copied.ManagedFields = nil

		return copied
	}

	if !equality.Semantic.DeepEqual(comparable(before), comparable(after)) {
		return fmt.Errorf("cluster auth token %s changed in more than its lastUsedAt: %s", before.Name, changedFields(comparable(before), comparable(after)))
	}

	return nil
}

func changedFields(before, after any) string {
	flatten := func(object any) map[string]any {
		fields := map[string]any{}

		converted, err := runtime.DefaultUnstructuredConverter.ToUnstructured(object)
		if err != nil {
			return fields
		}

		for key, value := range converted {
			if nested, ok := value.(map[string]any); ok && key == "metadata" {
				for nestedKey, nestedValue := range nested {
					fields[key+"."+nestedKey] = nestedValue
				}

				continue
			}

			fields[key] = value
		}

		return fields
	}

	beforeFields, afterFields := flatten(before), flatten(after)
	changed := []string{}

	for key, value := range beforeFields {
		if !equality.Semantic.DeepEqual(value, afterFields[key]) {
			changed = append(changed, key)
		}
	}

	for key := range afterFields {
		if _, seen := beforeFields[key]; !seen {
			changed = append(changed, key)
		}
	}

	sort.Strings(changed)

	return strings.Join(changed, ", ")
}

// VerifyOnlyExtLastUsedAtChanged fails when an ext token or its backing secret differ from their earlier read in anything but lastUsedAt
func VerifyOnlyExtLastUsedAtChanged(beforeToken, afterToken *extapi.Token, beforeSecret, afterSecret *corev1.Secret) error {
	comparableToken := func(token *extapi.Token) *extapi.Token {
		copied := token.DeepCopy()
		copied.Status.LastUsedAt = nil
		copied.ResourceVersion = ""
		copied.ManagedFields = nil

		return copied
	}

	comparableSecret := func(secret *corev1.Secret) *corev1.Secret {
		copied := secret.DeepCopy()
		delete(copied.Data, "last-used-at")
		copied.ResourceVersion = ""
		copied.ManagedFields = nil

		return copied
	}

	if !equality.Semantic.DeepEqual(comparableToken(beforeToken), comparableToken(afterToken)) {
		return fmt.Errorf("ext token %s changed in more than its lastUsedAt: %s", beforeToken.Name, changedFields(comparableToken(beforeToken), comparableToken(afterToken)))
	}

	if !equality.Semantic.DeepEqual(comparableSecret(beforeSecret), comparableSecret(afterSecret)) {
		return fmt.Errorf("the secret backing ext token %s changed in more than its lastUsedAt: %s", beforeToken.Name, changedFields(comparableSecret(beforeSecret), comparableSecret(afterSecret)))
	}

	return nil
}

// ClusterAuthTarget returns the stored lastUsedAt kube-api-auth keeps for this credential's token on the cluster
func (c *Credential) ClusterAuthTarget(clusterID string) tokens.Target {
	return tokens.Target{Kind: tokens.ClusterAuth, Name: c.Name, ClusterID: clusterID}
}

// NewClusterAuthCredential mints a token of the kind scoped to the cluster and waits for its cluster auth token to reach the cluster
func NewClusterAuthCredential(client *rancher.Client, clusterID string, kind tokens.Kind) (*Credential, error) {
	var credential *Credential

	switch kind {
	case tokens.V3:
		bearer, err := NewClusterScopedToken(client, clusterID, time.Hour)
		if err != nil {
			return nil, fmt.Errorf("minting a cluster scoped v3 token: %w", err)
		}

		credential, err = NewCredentialFromBearer(tokens.V3, bearer)
		if err != nil {
			return nil, fmt.Errorf("building the v3 credential: %w", err)
		}
	case tokens.Ext:
		var err error

		credential, err = NewClusterScopedExtCredential(client, clusterID)
		if err != nil {
			return nil, fmt.Errorf("minting a cluster scoped ext token: %w", err)
		}
	default:
		return nil, fmt.Errorf("cannot mint a token of kind %q", kind)
	}

	if err := WaitForClusterAuthToken(client, clusterID, credential.Name); err != nil {
		return nil, fmt.Errorf("waiting for the cluster auth token %s: %w", credential.Name, err)
	}

	return credential, nil
}

// WaitForClusterAuthTokenEnabled polls until the cluster auth token of the credential is enabled or disabled as given
func WaitForClusterAuthTokenEnabled(client *rancher.Client, clusterID string, credential *Credential, enabled bool) error {
	var last bool

	var lastErr error

	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true,
		func(context.Context) (bool, error) {
			clusterAuthToken, err := tokens.GetClusterAuthToken(client, clusterID, credential.Name)
			if err != nil {
				lastErr = err

				return false, nil
			}

			last = clusterAuthToken.Enabled

			return last == enabled, nil
		})
	if pollErr != nil {
		return fmt.Errorf("cluster auth token %s stays enabled=%t where enabled=%t was expected (last read error: %v): %w", credential.Name, last, enabled, lastErr, pollErr)
	}

	return nil
}

// ReviewAcrossDisable reviews the token every interval while it is disabled part way through, and returns the reviews with the time the disable reached the cluster
func ReviewAcrossDisable(client *rancher.Client, clusterID string, credential *Credential, count int, interval, disableAfter time.Duration) ([]Request, time.Time, error) {
	type outcome struct {
		requests []Request
		err      error
	}

	finished := make(chan outcome, 1)

	go func() {
		requests, err := ReviewOnCadence(client, clusterID, credential.Bearer, interval, time.Duration(count)*interval)
		finished <- outcome{requests, err}
	}()

	<-time.After(disableAfter)

	disableErr := SetCredentialEnabled(client, credential, false)
	if disableErr == nil {
		disableErr = WaitForClusterAuthTokenEnabled(client, clusterID, credential, false)
	}

	disabledAt := time.Now()
	result := <-finished

	if disableErr != nil {
		return nil, disabledAt, disableErr
	}

	if result.err != nil {
		return nil, disabledAt, result.err
	}

	return result.requests, disabledAt, nil
}

// ReviewAlongsideRequests reviews the token against kube-api-auth while also using it through the Rancher proxy, and returns both sets
func ReviewAlongsideRequests(client *rancher.Client, requester *Requester, clusterID string, credential *Credential, interval, span time.Duration) ([]Request, []Request, error) {
	type outcome struct {
		requests []Request
		err      error
	}

	reviewed := make(chan outcome, 1)

	go func() {
		requests, err := ReviewOnCadence(client, clusterID, credential.Bearer, interval, span)
		reviewed <- outcome{requests, err}
	}()

	proxied, proxyErr := requester.WithCluster(clusterID).Repeat(credential, ProxyPath, interval, span)
	result := <-reviewed

	if proxyErr != nil {
		return result.requests, proxied, proxyErr
	}

	return result.requests, proxied, result.err
}

// VerifyAllReviewed fails unless every TokenReview was answered with a 200 that authenticated the token
func VerifyAllReviewed(requests []Request) error {
	for _, request := range requests {
		if request.StatusCode != http.StatusOK || !request.Authenticated {
			return fmt.Errorf("the review sent at %s was answered %d, authenticated=%t", request.At.UTC().Format(time.RFC3339), request.StatusCode, request.Authenticated)
		}
	}

	return nil
}

// VerifyWritesAreCopies fails unless every upstream write holds a value the cluster auth token held at some point
func VerifyWritesAreCopies(upstream, clusterAuth []tokens.Write) error {
	held := map[int64]struct{}{}
	for _, write := range clusterAuth {
		held[write.Time.Unix()] = struct{}{}
	}

	for _, write := range upstream {
		if _, found := held[write.Time.Unix()]; !found {
			return fmt.Errorf("the upstream token holds %s, which the cluster auth token never held", write.Time.Format(time.RFC3339))
		}
	}

	return nil
}
