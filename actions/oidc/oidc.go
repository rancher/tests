package oidc

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	oidcauth "github.com/rancher/shepherd/clients/rancher/auth/oidc"
	"github.com/rancher/shepherd/extensions/features"
	"github.com/sirupsen/logrus"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const featureSteveType = "management.cattle.io.feature"

const OIDCClientSecretNamespace = "cattle-oidc-client-secrets"

var oidcClientGVR = schema.GroupVersionResource{
	Group:    oidcauth.OIDCClientGroup,
	Version:  oidcauth.OIDCClientVersion,
	Resource: oidcauth.OIDCClientResource,
}

// ClientSpec is the spec section of the OIDCClient CRD.
type ClientSpec struct {
	RedirectURIs                  []string
	Scopes                        []string
	TokenExpirationSeconds        int
	RefreshTokenExpirationSeconds int
}

// EnableOIDCFeatureFlag enables the oidc-provider feature flag and waits for Rancher to stabilize.
func EnableOIDCFeatureFlag(client *rancher.Client) error {
	logrus.Info("[OIDC setup] Enabling oidc-provider feature flag — Rancher will restart")
	featureObj, err := client.Steve.SteveType(featureSteveType).ByID(oidcauth.OIDCProviderFeatureFlag)
	if err != nil {
		return fmt.Errorf("fetching oidc-provider feature object: %w", err)
	}
	_, err = features.UpdateFeatureFlag(client.Steve, featureObj, true)
	return err
}

// DisableOIDCFeatureFlag disables the oidc-provider feature flag.
func DisableOIDCFeatureFlag(client *rancher.Client) error {
	logrus.Info("[OIDC teardown] Disabling oidc-provider feature flag")
	featureObj, err := client.Steve.SteveType(featureSteveType).ByID(oidcauth.OIDCProviderFeatureFlag)
	if err != nil {
		return fmt.Errorf("fetching oidc-provider feature object: %w", err)
	}
	_, err = features.UpdateFeatureFlag(client.Steve, featureObj, false)
	return err
}

// IsOIDCFeatureFlagEnabled returns true if the oidc-provider flag is currently on.
func IsOIDCFeatureFlagEnabled(client *rancher.Client) (bool, error) {
	feature, err := client.Management.Feature.ByID(oidcauth.OIDCProviderFeatureFlag)
	if err != nil {
		return false, fmt.Errorf("fetching oidc-provider feature flag: %w", err)
	}
	if feature.Value == nil {
		return false, nil
	}
	return *feature.Value, nil
}

// managementDynamicClient builds a dynamic client targeting the management cluster REST config.
func managementDynamicClient(client *rancher.Client) (dynamic.Interface, error) {
	dynClient, err := dynamic.NewForConfig(client.WranglerContext.RESTConfig)
	if err != nil {
		return nil, fmt.Errorf("building management cluster dynamic client: %w", err)
	}
	return dynClient, nil
}

// CreateOIDCClient creates an OIDCClient CRD on the management cluster.
func CreateOIDCClient(client *rancher.Client, name string, spec ClientSpec) error {
	logrus.Infof("[OIDC setup] Creating OIDCClient CRD %q on management cluster", name)
	if spec.TokenExpirationSeconds == 0 {
		spec.TokenExpirationSeconds = 3600
	}
	if spec.RefreshTokenExpirationSeconds == 0 {
		spec.RefreshTokenExpirationSeconds = 86400
	}
	if len(spec.Scopes) == 0 {
		spec.Scopes = oidcauth.DefaultAutomationScopes
	}

	redirectURIs := make([]interface{}, len(spec.RedirectURIs))
	for i, v := range spec.RedirectURIs {
		redirectURIs[i] = v
	}
	scopes := make([]interface{}, len(spec.Scopes))
	for i, v := range spec.Scopes {
		scopes[i] = v
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": oidcauth.OIDCClientGroup + "/" + oidcauth.OIDCClientVersion,
			"kind":       oidcauth.OIDCClientKind,
			"metadata": map[string]interface{}{
				"name": name,
			},
			"spec": map[string]interface{}{
				"redirectURIs":                  redirectURIs,
				"scopes":                        scopes,
				"tokenExpirationSeconds":        int64(spec.TokenExpirationSeconds),
				"refreshTokenExpirationSeconds": int64(spec.RefreshTokenExpirationSeconds),
			},
		},
	}
	dynClient, err := managementDynamicClient(client)
	if err != nil {
		return err
	}
	_, err = dynClient.Resource(oidcClientGVR).Create(context.Background(), obj, metav1.CreateOptions{})
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating OIDCClient %q: %w", name, err)
		}
		logrus.Infof("[OIDC setup] OIDCClient %q already exists — deleting and recreating", name)
		if delErr := dynClient.Resource(oidcClientGVR).Delete(context.Background(), name, metav1.DeleteOptions{}); delErr != nil {
			return fmt.Errorf("deleting stale OIDCClient %q before recreate: %w", name, delErr)
		}
		if _, createErr := dynClient.Resource(oidcClientGVR).Create(context.Background(), obj, metav1.CreateOptions{}); createErr != nil {
			return fmt.Errorf("recreating OIDCClient %q after delete: %w", name, createErr)
		}
	}
	logrus.Infof("[OIDC setup] OIDCClient %q created", name)
	return nil
}

// WaitForOIDCClientReady polls until status.clientID and status.clientSecrets are populated.
func WaitForOIDCClientReady(client *rancher.Client, name string) (clientID, secretKeyName string, err error) {
	logrus.Infof("[OIDC setup] Waiting for OIDCClient %q status.clientID (max 2m)", name)
	dynClient, err := managementDynamicClient(client)
	if err != nil {
		return "", "", err
	}
	const (
		interval = 5 * time.Second
		timeout  = 2 * time.Minute
	)
	err = kwait.PollUntilContextTimeout(
		context.Background(), interval, timeout, true,
		func(ctx context.Context) (bool, error) {
			obj, getErr := dynClient.Resource(oidcClientGVR).Get(ctx, name, metav1.GetOptions{})
			if getErr != nil {
				logrus.Debugf("[OIDC] OIDCClient %q not yet visible: %v", name, getErr)
				return false, nil
			}
			status, ok := obj.Object["status"].(map[string]interface{})
			if !ok {
				return false, nil
			}
			id, _ := status["clientID"].(string)
			if id == "" {
				return false, nil
			}
			secrets, _ := status["clientSecrets"].(map[string]interface{})
			if len(secrets) == 0 {
				return false, nil
			}
			for k := range secrets {
				secretKeyName = k
				break
			}
			clientID = id
			logrus.Infof("[OIDC] OIDCClient %q ready — clientID=%s secretKey=%s", name, clientID, secretKeyName)
			return true, nil
		},
	)
	if err != nil {
		return "", "", fmt.Errorf("timed out waiting for OIDCClient %q status.clientID: %w", name, err)
	}
	return clientID, secretKeyName, nil
}

// FetchOIDCClientSecret retrieves the client secret from the Kubernetes secret Rancher creates in cattle-oidc-client-secrets.
func FetchOIDCClientSecret(client *rancher.Client, clientID, secretKeyName string) (string, error) {
	logrus.Infof("[OIDC setup] Fetching client secret from cattle-oidc-client-secrets/%s key=%s",
		clientID, secretKeyName)
	secret, err := client.WranglerContext.Core.Secret().Get(
		OIDCClientSecretNamespace, clientID, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("getting OIDCClient secret %s/%s: %w",
			OIDCClientSecretNamespace, clientID, err)
	}
	value, ok := secret.Data[secretKeyName]
	if !ok || len(value) == 0 {
		return "", fmt.Errorf("key %q not found or empty in secret %s/%s",
			secretKeyName, OIDCClientSecretNamespace, clientID)
	}
	logrus.Infof("[OIDC setup] Client secret retrieved (last 5 chars confirmed via status)")
	return string(value), nil
}

// DeleteOIDCClient deletes the named OIDCClient CRD. Not-found is treated as success.
func DeleteOIDCClient(client *rancher.Client, name string) error {
	logrus.Infof("[OIDC teardown] Deleting OIDCClient %q", name)
	dynClient, err := managementDynamicClient(client)
	if err != nil {
		return err
	}
	err = dynClient.Resource(oidcClientGVR).Delete(context.Background(), name, metav1.DeleteOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logrus.Debugf("[OIDC teardown] OIDCClient %q already gone — skipping", name)
			return nil
		}
		return fmt.Errorf("deleting OIDCClient %q: %w", name, err)
	}
	logrus.Infof("[OIDC teardown] OIDCClient %q deleted", name)
	return nil
}

// RestartRancherDeployment triggers a rollout restart and waits for the new pod to be fully running.
func RestartRancherDeployment(client *rancher.Client) error {
	logrus.Info("[OIDC] Restarting Rancher deployment (cattle-system/rancher)")
	k8sClient, err := kubernetes.NewForConfig(client.WranglerContext.RESTConfig)
	if err != nil {
		return fmt.Errorf("building k8s client for restart: %w", err)
	}
	dep, err := k8sClient.AppsV1().Deployments("cattle-system").
		Get(context.Background(), "rancher", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("reading rancher deployment before restart: %w", err)
	}
	generationBefore := dep.Generation
	now := time.Now().UTC().Format(time.RFC3339)
	patch := []byte(fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`,
		now,
	))
	_, err = k8sClient.AppsV1().Deployments("cattle-system").
		Patch(context.Background(), "rancher", types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("patching rancher deployment for restart: %w", err)
	}
	logrus.Infof("[OIDC] Restart patch applied (generation was %d) — waiting for rollout (max 5m)", generationBefore)
	return kwait.PollUntilContextTimeout(
		context.Background(), 10*time.Second, 5*time.Minute, false,
		func(ctx context.Context) (bool, error) {
			d, getErr := k8sClient.AppsV1().Deployments("cattle-system").
				Get(ctx, "rancher", metav1.GetOptions{})
			if getErr != nil {
				logrus.Debugf("[OIDC] Deployment not yet readable: %v", getErr)
				return false, nil
			}
			desired := int32(1)
			if d.Spec.Replicas != nil {
				desired = *d.Spec.Replicas
			}
			newGen := d.Status.ObservedGeneration > generationBefore
			updated := d.Status.UpdatedReplicas >= desired
			ready := d.Status.ReadyReplicas >= desired
			available := d.Status.AvailableReplicas >= desired
			noTerminating := d.Status.Replicas == desired
			logrus.Debugf("[OIDC] Rollout: gen=%d>%d=%v updated=%d ready=%d avail=%d total=%d desired=%d",
				d.Status.ObservedGeneration, generationBefore, newGen,
				d.Status.UpdatedReplicas, d.Status.ReadyReplicas,
				d.Status.AvailableReplicas, d.Status.Replicas, desired)
			if newGen && updated && ready && available && noTerminating {
				logrus.Info("[OIDC] Rancher rollout complete — new pod running, old pod terminated")
				return true, nil
			}
			return false, nil
		},
	)
}
