package kubeapiauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	clusterv3 "github.com/rancher/rancher/pkg/apis/cluster.cattle.io/v3"
	extapi "github.com/rancher/rancher/pkg/apis/ext.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/kubeapi/workloads/daemonsets"
	authactions "github.com/rancher/tests/actions/auth"
	"github.com/rancher/tests/actions/kubeapi/rancherserver"
	"github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/kubeapi/tokens/exttokens"
	"github.com/rancher/tests/actions/kubeapi/workloads/pods"
	tokenapi "github.com/rancher/tests/actions/tokens"
	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
)

const (
	LastUsedAtWriteError   = "Error updating lastUsedAt"
	clusterAuthTokenPlural = "clusterauthtokens"
)

var errorMarkers = []string{"error updating clusterAuthToken", "forbidden", "conflict"}

// VerifyNoKubeAPIAuthErrors fails when any kube-api-auth pod logged a failed write, a forbidden or a conflict since the baseline
func VerifyNoKubeAPIAuthErrors(client *rancher.Client, clusterID string, baseline map[string]string) error {
	current, err := authactions.KubeAPIAuthLogs(client, clusterID)
	if err != nil {
		return fmt.Errorf("reading what %v logged: %w", authactions.KubeAPIAuthDaemonSet, err)
	}

	for pod, logs := range current {
		appended := authactions.LogsSince(baseline[pod], logs)

		for _, marker := range errorMarkers {
			if strings.Contains(strings.ToLower(appended), strings.ToLower(marker)) {
				return fmt.Errorf("%s on %s logged %q", authactions.KubeAPIAuthDaemonSet, pod, marker)
			}
		}
	}

	return nil
}

// VerifyKubeAPIAuthRunsOnEveryControlPlaneNode fails unless one kube-api-auth pod is running for each control plane node
func VerifyKubeAPIAuthRunsOnEveryControlPlaneNode(client *rancher.Client, clusterID string) error {
	wanted, err := authactions.ControlPlaneNodeCount(client, clusterID)
	if err != nil {
		return fmt.Errorf("counting the control plane nodes: %w", err)
	}

	podList, err := pods.GetPodsByLabelSelector(client, clusterID, authactions.KubeAPIAuthNamespace, authactions.KubeAPIAuthAppLabel)
	if err != nil {
		return fmt.Errorf("listing the %s pods on cluster %s: %w", authactions.KubeAPIAuthDaemonSet, clusterID, err)
	}

	running := 0

	for _, pod := range podList {
		if pod.Status.Phase == corev1.PodRunning {
			running++
		}
	}

	if running != wanted {
		return fmt.Errorf("%d %s pods are running for %d control plane nodes", running, authactions.KubeAPIAuthDaemonSet, wanted)
	}

	return nil
}

// VerifyKubeAPIAuthCanPatchClusterAuthTokens fails unless the kube-api-auth service account may patch cluster auth tokens
func VerifyKubeAPIAuthCanPatchClusterAuthTokens(client *rancher.Client, clusterID string) error {
	daemonSet, err := daemonsets.GetDaemonSetByName(client, clusterID, authactions.KubeAPIAuthNamespace, authactions.KubeAPIAuthDaemonSet)
	if err != nil {
		return fmt.Errorf("reading the %s daemon set on cluster %s: %w", authactions.KubeAPIAuthDaemonSet, clusterID, err)
	}

	serviceAccount := daemonSet.Spec.Template.Spec.ServiceAccountName
	if serviceAccount == "" {
		return fmt.Errorf("the %s daemon set runs under no service account of its own", authactions.KubeAPIAuthDaemonSet)
	}

	downstream, err := client.WranglerContext.DownStreamClusterWranglerContext(clusterID)
	if err != nil {
		return fmt.Errorf("reaching cluster %s: %w", clusterID, err)
	}

	clientset, err := kubernetes.NewForConfig(downstream.RESTConfig)
	if err != nil {
		return fmt.Errorf("building a clientset for cluster %s: %w", clusterID, err)
	}

	review, err := clientset.AuthorizationV1().SubjectAccessReviews().Create(context.Background(), &authzv1.SubjectAccessReview{
		Spec: authzv1.SubjectAccessReviewSpec{
			User: fmt.Sprintf("system:serviceaccount:%s:%s", authactions.KubeAPIAuthNamespace, serviceAccount),
			ResourceAttributes: &authzv1.ResourceAttributes{
				Verb: rbac.PatchVerb, Group: "cluster.cattle.io", Resource: clusterAuthTokenPlural, Namespace: authactions.KubeAPIAuthNamespace,
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("asking cluster %s whether %s may patch %s: %w", clusterID, serviceAccount, clusterAuthTokenPlural, err)
	}

	if !review.Status.Allowed {
		return fmt.Errorf("service account %s may not %s %s, so a write of lastUsedAt would be forbidden", serviceAccount, rbac.PatchVerb, clusterAuthTokenPlural)
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

// VerifyNoLastUsedAtErrors fails when Rancher logged a failed write of lastUsedAt that the baseline does not hold
func VerifyNoLastUsedAtErrors(client *rancher.Client, baseline []string) error {
	current, err := rancherserver.FindRancherServerLogs(client, LastUsedAtWriteError)
	if err != nil {
		return fmt.Errorf("reading the Rancher server logs: %w", err)
	}

	known := map[string]struct{}{}
	for _, line := range baseline {
		known[line] = struct{}{}
	}

	for _, line := range current {
		if _, found := known[line]; !found {
			return fmt.Errorf("Rancher logged a new %q line: %s", LastUsedAtWriteError, line)
		}
	}

	return nil
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
		delete(copied.Data, exttokens.ExtTokenLastUsedAtField)
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

// VerifyAllReviewed fails unless there is at least one TokenReview and every one was answered with a 200 that authenticated the token
func VerifyAllReviewed(requests []tokenapi.Request) error {
	if len(requests) == 0 {
		return errors.New("no TokenReview was answered")
	}

	for _, request := range requests {
		if request.StatusCode != http.StatusOK || !request.Authenticated {
			return fmt.Errorf("the review sent at %s was answered %d, authenticated=%t", request.At.UTC().Format(time.RFC3339), request.StatusCode, request.Authenticated)
		}
	}

	return nil
}

// VerifyWritesAreCopies fails unless every upstream write holds a value the cluster auth token held at some point
func VerifyWritesAreCopies(upstream, clusterAuth []time.Time) error {
	held := map[int64]struct{}{}
	for _, write := range clusterAuth {
		held[write.Unix()] = struct{}{}
	}

	for _, write := range upstream {
		if _, found := held[write.Unix()]; !found {
			return fmt.Errorf("the upstream token holds %s, which the cluster auth token never held", write.Format(time.RFC3339))
		}
	}

	return nil
}
