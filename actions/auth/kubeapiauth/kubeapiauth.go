package kubeapiauth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/kubectl"
	authactions "github.com/rancher/tests/actions/auth"
	tokenapi "github.com/rancher/tests/actions/tokens"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

// TokenReviewPod is a long-lived host network pod on one control plane node that sends TokenReviews to the kube-api-auth pod on that node
type TokenReviewPod struct {
	Node       string
	Name       string
	restConfig *rest.Config
}

// CreateTokenReviewPods starts one host network pod on every control plane node of the cluster and waits until each is running
func CreateTokenReviewPods(client *rancher.Client, clusterID string) ([]*TokenReviewPod, error) {
	downstream, err := client.WranglerContext.DownStreamClusterWranglerContext(clusterID)
	if err != nil {
		return nil, fmt.Errorf("reaching cluster %s: %w", clusterID, err)
	}

	nodeList, err := downstream.Core.Node().List(metav1.ListOptions{LabelSelector: authactions.ControlPlaneNodeLabel})
	if err != nil {
		return nil, fmt.Errorf("listing the control plane nodes of cluster %s: %w", clusterID, err)
	}

	if len(nodeList.Items) == 0 {
		return nil, fmt.Errorf("cluster %s has no node labelled %s", clusterID, authactions.ControlPlaneNodeLabel)
	}

	shellImage, err := client.WranglerContext.Mgmt.Setting().Get("shell-image", metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("reading the shell-image setting: %w", err)
	}

	image := shellImage.Value
	if image == "" {
		image = shellImage.Default
	}

	pods := []*TokenReviewPod{}

	for _, node := range nodeList.Items {
		created, err := downstream.Core.Pod().Create(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "kube-api-auth-review-", Namespace: kubectl.Namespace},
			Spec: corev1.PodSpec{
				NodeName:                      node.Name,
				TerminationGracePeriodSeconds: new(int64),
				HostNetwork:                   true,
				RestartPolicy:                 corev1.RestartPolicyNever,
				Tolerations:                   []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
				Containers:                    []corev1.Container{{Name: "review", Image: image, Command: []string{"sleep", "86400"}}},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("starting a TokenReview pod on node %s: %w", node.Name, err)
		}

		pods = append(pods, &TokenReviewPod{Node: node.Name, Name: created.Name, restConfig: downstream.RESTConfig})
	}

	for _, pod := range pods {
		var phase corev1.PodPhase

		err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.FiveMinuteTimeout, true,
			func(context.Context) (bool, error) {
				current, err := downstream.Core.Pod().Get(kubectl.Namespace, pod.Name, metav1.GetOptions{})
				if err != nil {
					return false, nil
				}

				phase = current.Status.Phase

				return phase == corev1.PodRunning, nil
			})
		if err != nil {
			return nil, fmt.Errorf("TokenReview pod %s on node %s is %q rather than running: %w", pod.Name, pod.Node, phase, err)
		}
	}

	return pods, nil
}

// SendTokenReview posts one TokenReview for the token to the kube-api-auth pod on this node and returns when it was sent and how it was answered
func (p *TokenReviewPod) SendTokenReview(token string) (tokenapi.Request, error) {
	started := time.Now()

	result, err := authactions.ExecTokenReview(p.restConfig, p.Name, authactions.NewTokenReviewPayload(authactions.TokenReviewAPIV1, authactions.TokenReviewKind, token))

	sentAt := started.Add(time.Since(started) / 2)
	if err != nil {
		return tokenapi.Request{At: sentAt}, fmt.Errorf("sending a TokenReview on node %s: %w", p.Node, err)
	}

	return tokenapi.Request{At: sentAt, StatusCode: result.StatusCode, Authenticated: result.Authenticated}, nil
}

// SendTokenReviewsOnCadence sends one TokenReview every interval until the span has passed and returns every review
func SendTokenReviewsOnCadence(pod *TokenReviewPod, token string, interval, span time.Duration) ([]tokenapi.Request, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	started := time.Now()
	requests := []tokenapi.Request{}

	for {
		request, err := pod.SendTokenReview(token)
		if err != nil {
			return requests, err
		}

		requests = append(requests, request)

		if time.Since(started)+interval > span {
			return requests, nil
		}

		<-ticker.C
	}
}

// SendTokenReviewsConcurrently sends one TokenReview per token at the same time and returns them in the order of the tokens
func SendTokenReviewsConcurrently(pod *TokenReviewPod, tokens []string) ([]tokenapi.Request, error) {
	requests := make([]tokenapi.Request, len(tokens))
	failures := make([]error, len(tokens))

	var group sync.WaitGroup

	for index, token := range tokens {
		group.Add(1)

		go func() {
			defer group.Done()

			requests[index], failures[index] = pod.SendTokenReview(token)
		}()
	}

	group.Wait()

	return requests, errors.Join(failures...)
}

// SendTokenReviewBursts sends bursts of simultaneous TokenReviews for the token, starting a burst every pause, and returns the reviews of each burst
func SendTokenReviewBursts(pod *TokenReviewPod, token string, bursts, perBurst int, pause time.Duration) ([][]tokenapi.Request, error) {
	tokens := slices.Repeat([]string{token}, perBurst)

	ticker := time.NewTicker(pause)
	defer ticker.Stop()

	sent := [][]tokenapi.Request{}

	for burst := 0; burst < bursts; burst++ {
		if burst > 0 {
			<-ticker.C
		}

		requests, err := SendTokenReviewsConcurrently(pod, tokens)
		sent = append(sent, requests)

		if err != nil {
			return sent, err
		}
	}

	return sent, nil
}

// SendTokenReviewsOnCadenceFromEveryPod runs SendTokenReviewsOnCadence from every pod at once and returns every review they sent
func SendTokenReviewsOnCadenceFromEveryPod(pods []*TokenReviewPod, token string, interval, span time.Duration) ([]tokenapi.Request, error) {
	results := make([][]tokenapi.Request, len(pods))
	failures := make([]error, len(pods))

	var group sync.WaitGroup

	for index, pod := range pods {
		group.Add(1)

		go func() {
			defer group.Done()

			results[index], failures[index] = SendTokenReviewsOnCadence(pod, token, interval, span)
		}()
	}

	group.Wait()

	return slices.Concat(results...), errors.Join(failures...)
}

// DisableTokenDuringTokenReviews reviews the token on a cadence, disables it right after a write lands, and returns the reviews, when the disable was requested and when it reached the cluster
func DisableTokenDuringTokenReviews(client *rancher.Client, pod *TokenReviewPod, clusterID string, credential *tokenapi.Credential, interval, span time.Duration) ([]tokenapi.Request, time.Time, time.Time, error) {
	clusterAuth := credential.ClusterAuthTarget(clusterID)

	unused, err := tokenapi.GetLastUsedAt(client, clusterAuth)
	if err != nil {
		return nil, time.Time{}, time.Time{}, err
	}

	var requests []tokenapi.Request

	var reviewErr error

	var group sync.WaitGroup

	group.Add(1)

	go func() {
		defer group.Done()

		requests, reviewErr = SendTokenReviewsOnCadence(pod, credential.Bearer, interval, span)
	}()

	_, disableErr := tokenapi.WaitForLastUsedAtChange(client, clusterAuth, unused)

	plantedAt := time.Now().Add(-defaults.TwoMinuteTimeout).Truncate(time.Second)

	if disableErr == nil {
		disableErr = authactions.SetClusterAuthTokenLastUsedAt(client, clusterID, credential.Name, plantedAt)
	}

	if disableErr == nil {
		_, disableErr = tokenapi.WaitForLastUsedAtChange(client, clusterAuth, &plantedAt)
	}

	disableRequestedAt := time.Now()

	if disableErr == nil {
		disableErr = tokenapi.SetCredentialEnabled(client, credential, false)
	}

	if disableErr == nil {
		disableErr = authactions.WaitForClusterAuthTokenEnabled(client, clusterID, credential.Name, false)
	}

	disabledAt := time.Now()

	group.Wait()

	if err := errors.Join(disableErr, reviewErr); err != nil {
		return nil, disableRequestedAt, disabledAt, err
	}

	return requests, disableRequestedAt, disabledAt, nil
}

// SendTokenReviewsAlongsideProxyRequests reviews the token against kube-api-auth while also using it through the Rancher proxy, and returns both sets
func SendTokenReviewsAlongsideProxyRequests(pod *TokenReviewPod, requester *tokenapi.Requester, clusterID string, credential *tokenapi.Credential, interval, span time.Duration) ([]tokenapi.Request, []tokenapi.Request, error) {
	var reviews []tokenapi.Request

	var reviewErr error

	var group sync.WaitGroup

	group.Add(1)

	go func() {
		defer group.Done()

		reviews, reviewErr = SendTokenReviewsOnCadence(pod, credential.Bearer, interval, span)
	}()

	proxied, proxyErr := requester.WithCluster(clusterID).Repeat(credential, tokenapi.ProxyPath, interval, span)

	group.Wait()

	return reviews, proxied, errors.Join(proxyErr, reviewErr)
}
