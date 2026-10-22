// Package neuvector provides reusable helpers around NeuVector on Rancher-managed
// clusters: test configuration and admission-control enforcement exercised by the
// validation suites.
package neuvector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/tests/actions/namespaces"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	// AdmissionControlSteveType is the steve type of the cluster-scoped
	// NvAdmissionControlSecurityRule custom resource served by the
	// neuvector-crd chart (group neuvector.com, singular nvadmissioncontrolsecurityrule).
	AdmissionControlSteveType = "neuvector.com.nvadmissioncontrolsecurityrule"

	// AdmissionControlCRName is the resource name the NeuVector CRD documentation
	// requires for the admission control custom resource: always "local".
	AdmissionControlCRName = "local"

	// ValidatingWebhookSteveType is the steve type of the Kubernetes
	// ValidatingWebhookConfiguration resource on the downstream cluster.
	ValidatingWebhookSteveType = "admissionregistration.k8s.io.validatingwebhookconfiguration"

	// NeuVectorValidatingWebhookName is the ValidatingWebhookConfiguration NeuVector
	// registers automatically once admission control is enabled.
	NeuVectorValidatingWebhookName = "neuvector-validating-admission-webhook"

	// admissionWebhookDenialMarker is the substring a Kubernetes API server error
	// contains when a create is rejected by an admission webhook; matched
	// case-insensitively.
	admissionWebhookDenialMarker = "admission webhook"

	admissionPollInterval = 5 * time.Second
	admissionPollTimeout  = 3 * time.Minute

	// admissionProbePodName is the deterministic name of the pod whose creation
	// the admission deny rule must reject; it never starts running, so the name
	// stays free for every verification attempt.
	admissionProbePodName = "rancher-admission-deny-probe"

	// admissionProbePodImage is only referenced by the denied create request and
	// is never pulled while the webhook enforces the deny rule.
	admissionProbePodImage = "busybox:1.36"

	admissionProbeContainerName = "deny-probe"
)

// EnableAdmissionControlDenyNamespace enables NeuVector admission control on the
// cluster and applies a protect-mode deny rule rejecting any workload create in
// denyNamespace. The NvAdmissionControlSecurityRule custom resource is created
// (or updated) under the NeuVector-documented name "local" with the shape of the
// console-exported manifest: config enable/protect/service plus a deny rule whose
// namespace criterion uses the containsAny operator. The deny namespace is created
// if missing, and the call returns only after NeuVector has registered its
// validating webhook, so enforcement can be proven right away with
// VerifyAdmissionDenial. Everything is named deterministically and nothing is
// registered for session cleanup: upgrade-test phases must keep the rule in force
// across separate go test processes.
func EnableAdmissionControlDenyNamespace(client *rancher.Client, clusterID, denyNamespace string) error {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	if err := ensureNamespace(steveClient, denyNamespace); err != nil {
		return err
	}

	if err := applyAdmissionControlRule(steveClient, denyNamespace); err != nil {
		return err
	}

	return waitForAdmissionWebhook(steveClient)
}

// VerifyAdmissionDenial proves the NeuVector admission webhook enforces the deny
// rule for denyNamespace: it attempts to create a pod there and expects the API
// server to reject the create with an admission-webhook denial. Attempts are
// retried within a bounded window because the NeuVector controllers reload the
// CRD rules asynchronously (for example while rolling after a chart upgrade), and
// a pod created before the rule is loaded again is deleted before the next
// attempt. An unexpected create success that outlives the window, or a persistent
// non-webhook error, is returned as an error.
func VerifyAdmissionDenial(client *rancher.Client, clusterID, denyNamespace string) error {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	var lastErr error
	err = kwait.PollUntilContextTimeout(context.Background(), admissionPollInterval, admissionPollTimeout, true, func(ctx context.Context) (bool, error) {
		lastErr = attemptAdmissionProbeCreate(steveClient, denyNamespace)
		switch {
		case lastErr == nil:
			// The deny rule did not fire: NeuVector may still be loading the CRD
			// rules after a restart, so remove the stray pod and retry.
			logrus.Warnf("admission probe pod in namespace %s was created instead of denied; deleting it and retrying", denyNamespace)
			if deleteErr := deleteAdmissionProbePod(steveClient, denyNamespace); deleteErr != nil {
				return false, deleteErr
			}
			lastErr = fmt.Errorf("pod %s/%s was created but the NeuVector admission webhook must deny workload creates in this namespace", denyNamespace, admissionProbePodName)
			return false, nil
		case isAdmissionWebhookDenial(lastErr):
			logrus.Infof("NeuVector admission webhook denied the probe create in namespace %s as expected", denyNamespace)
			return true, nil
		default:
			// Retry transient API errors, but keep the surprise for the timeout.
			logrus.Warnf("probe create in namespace %s failed with a non-webhook error, retrying: %v", denyNamespace, lastErr)
			return false, nil
		}
	})
	if err != nil {
		return fmt.Errorf("NeuVector admission enforcement not proven for namespace %s: %w", denyNamespace, lastErr)
	}

	return nil
}

// applyAdmissionControlRule creates or updates the NvAdmissionControlSecurityRule
// named "local" with the deny rule for denyNamespace.
func applyAdmissionControlRule(steveClient *v1.Client, denyNamespace string) error {
	crClient := steveClient.SteveType(AdmissionControlSteveType)

	existing, err := crClient.ByID(AdmissionControlCRName)
	if err != nil {
		if !k8sErrors.IsNotFound(err) {
			return err
		}

		cr := &v1.SteveAPIObject{}
		cr.TypeMeta = metav1.TypeMeta{Kind: "NvAdmissionControlSecurityRule", APIVersion: "neuvector.com/v1"}
		cr.Name = AdmissionControlCRName
		cr.Spec = admissionControlSpec(denyNamespace)

		logrus.Infof("creating NeuVector admission control rule denying workload creates in namespace %s", denyNamespace)
		_, err = crClient.Create(cr)
		return err
	}

	updated := *existing
	updated.Spec = admissionControlSpec(denyNamespace)

	logrus.Infof("updating NeuVector admission control rule denying workload creates in namespace %s", denyNamespace)
	_, err = crClient.Update(existing, &updated)
	return err
}

// admissionControlSpec builds the spec of the admission control custom resource
// following the console-exported manifest documented by NeuVector: admission
// control enabled in protect mode through the service client, plus a deny rule
// matching the deny namespace with the containsAny operator on the namespace
// criterion. Protect mode is what turns the deny action into an inline rejection.
func admissionControlSpec(denyNamespace string) map[string]any {
	return map[string]any{
		"config": map[string]any{
			"enable":      true,
			"mode":        "protect",
			"client_mode": "service",
		},
		"rules": []any{
			map[string]any{
				"action":     "deny",
				"containers": []any{"containers"},
				"criteria": []any{
					map[string]any{
						"name":  "namespace",
						"op":    "containsAny",
						"path":  "namespace",
						"value": denyNamespace,
					},
				},
				"disabled":  false,
				"rule_mode": "",
			},
		},
	}
}

// waitForAdmissionWebhook polls for the ValidatingWebhookConfiguration NeuVector
// registers once admission control is enabled; until it exists the API server
// cannot consult NeuVector at all.
func waitForAdmissionWebhook(steveClient *v1.Client) error {
	webhookClient := steveClient.SteveType(ValidatingWebhookSteveType)

	return kwait.PollUntilContextTimeout(context.Background(), admissionPollInterval, admissionPollTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := webhookClient.ByID(NeuVectorValidatingWebhookName)
		if err != nil {
			if k8sErrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}

		return true, nil
	})
}

// ensureNamespace creates the namespace if it does not exist yet; a namespace
// create is not a workload create, so the admission deny rule does not reject it.
func ensureNamespace(steveClient *v1.Client, name string) error {
	nsClient := steveClient.SteveType(namespaces.NamespaceSteveType)

	_, err := nsClient.ByID(name)
	if err == nil {
		return nil
	}
	if !k8sErrors.IsNotFound(err) {
		return err
	}

	ns := &v1.SteveAPIObject{}
	ns.TypeMeta = metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"}
	ns.Name = name

	logrus.Infof("creating namespace %s for the admission deny probe", name)
	if _, err = nsClient.Create(ns); err != nil {
		// Tolerate a create race by re-checking existence.
		if _, getErr := nsClient.ByID(name); getErr != nil {
			return err
		}
	}

	return nil
}

// attemptAdmissionProbeCreate tries to create the probe pod whose name and image
// are fixed; a nil return means the create was accepted.
func attemptAdmissionProbeCreate(steveClient *v1.Client, namespace string) error {
	probe := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      admissionProbePodName,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  admissionProbeContainerName,
					Image: admissionProbePodImage,
				},
			},
		},
	}

	_, err := steveClient.SteveType(stevetypes.Pod).Create(probe)
	return err
}

// deleteAdmissionProbePod removes a probe pod that escaped the deny rule.
func deleteAdmissionProbePod(steveClient *v1.Client, namespace string) error {
	probe, err := steveClient.SteveType(stevetypes.Pod).ByID(namespace + "/" + admissionProbePodName)
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	return steveClient.SteveType(stevetypes.Pod).Delete(probe)
}

// isAdmissionWebhookDenial reports whether err is a Kubernetes admission-webhook
// rejection, the error shape the API server returns for a denied create.
func isAdmissionWebhookDenial(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), admissionWebhookDenialMarker)
}
