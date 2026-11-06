package tokens

import (
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	clusterv3 "github.com/rancher/rancher/pkg/apis/cluster.cattle.io/v3"
	managementv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	"github.com/rancher/shepherd/extensions/kubeapi/secrets"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
)

const (
	V3          Kind = "v3"
	Ext         Kind = "ext"
	ClusterAuth Kind = "clusterauth"

	ExtSecretNamespace        = "cattle-tokens"
	ClusterAuthTokenNamespace = "cattle-system"

	extLastUsedAtField = "last-used-at"

	reopenAttempts = 5
)

// Kind is the resource a token's lastUsedAt is stored on
type Kind string

// Target names one stored lastUsedAt
type Target struct {
	Kind      Kind
	Name      string
	ClusterID string
}

// LastUsedAt is a read of a stored lastUsedAt with the resourceVersion it was read at
type LastUsedAt struct {
	ResourceVersion string
	Time            *time.Time
}

// Write is one change to a stored lastUsedAt seen by a watch
type Write struct {
	ResourceVersion string
	Time            time.Time
	ObservedAt      time.Time
}

// Watch records every change to the lastUsedAt of the tokens it was started for
type Watch struct {
	kind Kind
	open func(resourceVersion string) (watch.Interface, error)
	done chan struct{}
	quit chan struct{}

	mutex           sync.Mutex
	watcher         watch.Interface
	resourceVersion string
	last            map[string]*time.Time
	writes          map[string][]Write
	stopped         bool
	err             error
}

// GetLastUsedAt reads the lastUsedAt stored for the target together with the resourceVersion it was read at
func GetLastUsedAt(client *rancher.Client, target Target) (*LastUsedAt, error) {
	switch target.Kind {
	case V3:
		token, err := client.WranglerContext.Mgmt.Token().Get(target.Name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("reading token %s: %w", target.Name, err)
		}

		return &LastUsedAt{ResourceVersion: token.ResourceVersion, Time: fromMetaTime(token.LastUsedAt)}, nil
	case Ext:
		secret, err := secrets.GetSecretByName(client, extclusterapi.LocalCluster, ExtSecretNamespace, target.Name)
		if err != nil {
			return nil, fmt.Errorf("reading the secret backing ext token %s: %w", target.Name, err)
		}

		stored, err := fromSecret(secret)
		if err != nil {
			return nil, fmt.Errorf("reading lastUsedAt of ext token %s: %w", target.Name, err)
		}

		return &LastUsedAt{ResourceVersion: secret.ResourceVersion, Time: stored}, nil
	case ClusterAuth:
		clusterAuthToken, err := GetClusterAuthToken(client, target.ClusterID, target.Name)
		if err != nil {
			return nil, fmt.Errorf("reading cluster auth token %s: %w", target.Name, err)
		}

		return &LastUsedAt{ResourceVersion: clusterAuthToken.ResourceVersion, Time: fromMetaTime(clusterAuthToken.LastUsedAt)}, nil
	}

	return nil, fmt.Errorf("unknown token kind %q", target.Kind)
}

// SetLastUsedAt overwrites the lastUsedAt stored for the target
func SetLastUsedAt(client *rancher.Client, target Target, at time.Time) error {
	stamp := at.UTC().Format(time.RFC3339)

	switch target.Kind {
	case V3:
		if _, err := PatchToken(client, target.Name, "add", "/lastUsedAt", stamp); err != nil {
			return fmt.Errorf("setting lastUsedAt of token %s: %w", target.Name, err)
		}

		return nil
	case Ext:
		patch := []byte(fmt.Sprintf(`{"data":{%q:%q}}`, extLastUsedAtField, base64.StdEncoding.EncodeToString([]byte(stamp))))
		if _, err := client.WranglerContext.Core.Secret().Patch(ExtSecretNamespace, target.Name, types.MergePatchType, patch); err != nil {
			return fmt.Errorf("setting lastUsedAt in the secret backing ext token %s: %w", target.Name, err)
		}

		return nil
	case ClusterAuth:
		downstream, err := client.WranglerContext.DownStreamClusterWranglerContext(target.ClusterID)
		if err != nil {
			return fmt.Errorf("reaching cluster %s to set lastUsedAt of cluster auth token %s: %w", target.ClusterID, target.Name, err)
		}

		patch := []byte(fmt.Sprintf(`{"lastUsedAt":%q}`, stamp))
		if _, err := downstream.Cluster.ClusterAuthToken().Patch(ClusterAuthTokenNamespace, target.Name, types.MergePatchType, patch); err != nil {
			return fmt.Errorf("setting lastUsedAt of cluster auth token %s on cluster %s: %w", target.Name, target.ClusterID, err)
		}

		return nil
	}

	return fmt.Errorf("unknown token kind %q", target.Kind)
}

// WatchLastUsedAt starts recording every change to the lastUsedAt of the named tokens of one kind
func WatchLastUsedAt(client *rancher.Client, kind Kind, clusterID string, names ...string) (*Watch, error) {
	recorder := &Watch{
		kind:   kind,
		done:   make(chan struct{}),
		quit:   make(chan struct{}),
		last:   map[string]*time.Time{},
		writes: map[string][]Write{},
	}

	for _, name := range names {
		current, err := GetLastUsedAt(client, Target{Kind: kind, Name: name, ClusterID: clusterID})
		if err != nil {
			return nil, fmt.Errorf("reading the lastUsedAt of %s token %s: %w", kind, name, err)
		}

		recorder.last[name] = current.Time
	}

	timeout := defaults.WatchTimeoutSeconds

	switch kind {
	case V3:
		list, listErr := client.WranglerContext.Mgmt.Token().List(metav1.ListOptions{Limit: 1})
		if listErr != nil {
			return nil, fmt.Errorf("listing tokens to start watching from: %w", listErr)
		}

		recorder.resourceVersion = list.ResourceVersion
		recorder.open = func(resourceVersion string) (watch.Interface, error) {
			return client.WranglerContext.Mgmt.Token().Watch(metav1.ListOptions{
				ResourceVersion: resourceVersion, AllowWatchBookmarks: true, TimeoutSeconds: &timeout})
		}
	case Ext:
		list, listErr := client.WranglerContext.Core.Secret().List(ExtSecretNamespace, metav1.ListOptions{Limit: 1})
		if listErr != nil {
			return nil, fmt.Errorf("listing the secrets backing ext tokens to start watching from: %w", listErr)
		}

		recorder.resourceVersion = list.ResourceVersion
		recorder.open = func(resourceVersion string) (watch.Interface, error) {
			return client.WranglerContext.Core.Secret().Watch(ExtSecretNamespace, metav1.ListOptions{
				ResourceVersion: resourceVersion, AllowWatchBookmarks: true, TimeoutSeconds: &timeout})
		}
	case ClusterAuth:
		downstream, downErr := client.WranglerContext.DownStreamClusterWranglerContext(clusterID)
		if downErr != nil {
			return nil, fmt.Errorf("reaching cluster %s to watch its cluster auth tokens: %w", clusterID, downErr)
		}

		list, listErr := downstream.Cluster.ClusterAuthToken().List(ClusterAuthTokenNamespace, metav1.ListOptions{Limit: 1})
		if listErr != nil {
			return nil, fmt.Errorf("listing cluster auth tokens on cluster %s to start watching from: %w", clusterID, listErr)
		}

		recorder.resourceVersion = list.ResourceVersion
		recorder.open = func(resourceVersion string) (watch.Interface, error) {
			return downstream.Cluster.ClusterAuthToken().Watch(ClusterAuthTokenNamespace, metav1.ListOptions{
				ResourceVersion: resourceVersion, AllowWatchBookmarks: true, TimeoutSeconds: &timeout})
		}
	default:
		return nil, fmt.Errorf("unknown token kind %q", kind)
	}

	watcher, err := recorder.open(recorder.resourceVersion)
	if err != nil {
		return nil, fmt.Errorf("starting to watch %s lastUsedAt: %w", kind, err)
	}

	recorder.watcher = watcher

	go recorder.record()

	client.Session.RegisterCleanupFunc(func() error {
		recorder.halt()

		return nil
	})

	return recorder, nil
}

// Stop ends the watch and returns the writes seen per token, failing when the watch could not be resumed after the server closed it
func (w *Watch) Stop() (map[string][]Write, error) {
	w.halt()

	w.mutex.Lock()
	defer w.mutex.Unlock()

	return w.copyWrites(), w.err
}

func (w *Watch) halt() {
	w.mutex.Lock()
	if !w.stopped {
		w.stopped = true
		close(w.quit)
	}

	watcher := w.watcher
	w.mutex.Unlock()

	watcher.Stop()
	<-w.done
}

// Writes returns the writes seen so far for the named token while the watch keeps running
func (w *Watch) Writes(name string) []Write {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	return append([]Write(nil), w.writes[name]...)
}

func (w *Watch) copyWrites() map[string][]Write {
	copied := make(map[string][]Write, len(w.writes))
	for name, writes := range w.writes {
		copied[name] = append([]Write(nil), writes...)
	}

	return copied
}

func (w *Watch) record() {
	defer close(w.done)

	for w.consume() && w.reopen() {
	}
}

func (w *Watch) consume() bool {
	w.mutex.Lock()
	watcher := w.watcher
	w.mutex.Unlock()

	for event := range watcher.ResultChan() {
		if event.Type == watch.Error {
			status := apierrors.FromObject(event.Object)
			if apierrors.IsResourceExpired(status) || apierrors.IsGone(status) {
				w.fail(fmt.Errorf("the %s lastUsedAt watch cannot resume because its resourceVersion expired: %w", w.kind, status))

				return false
			}

			return true
		}

		if accessor, err := meta.Accessor(event.Object); err == nil && accessor.GetResourceVersion() != "" {
			w.mutex.Lock()
			w.resourceVersion = accessor.GetResourceVersion()
			w.mutex.Unlock()
		}

		if event.Type != watch.Added && event.Type != watch.Modified {
			continue
		}

		name, resourceVersion, stored, err := w.extract(event.Object)
		if err != nil {
			w.fail(err)

			continue
		}

		w.trackWrite(name, resourceVersion, stored)
	}

	return true
}

func (w *Watch) reopen() bool {
	var lastErr error

	for attempt := 0; attempt < reopenAttempts; attempt++ {
		w.mutex.Lock()
		stopped, previous, resourceVersion := w.stopped, w.watcher, w.resourceVersion
		w.mutex.Unlock()

		if stopped {
			return false
		}

		previous.Stop()

		next, err := w.open(resourceVersion)
		if err == nil {
			w.mutex.Lock()
			defer w.mutex.Unlock()

			if w.stopped {
				next.Stop()

				return false
			}

			w.watcher = next

			return true
		}

		lastErr = err

		select {
		case <-w.quit:
			return false
		case <-time.After(defaults.OneSecondTimeout):
		}
	}

	w.fail(fmt.Errorf("the %s lastUsedAt watch could not be resumed after the server closed it, so later writes would have gone uncounted: %w", w.kind, lastErr))

	return false
}

func (w *Watch) extract(object any) (string, string, *time.Time, error) {
	switch typed := object.(type) {
	case *managementv3.Token:
		return typed.Name, typed.ResourceVersion, fromMetaTime(typed.LastUsedAt), nil
	case *corev1.Secret:
		stored, err := fromSecret(typed)

		return typed.Name, typed.ResourceVersion, stored, err
	case *clusterv3.ClusterAuthToken:
		return typed.Name, typed.ResourceVersion, fromMetaTime(typed.LastUsedAt), nil
	}

	return "", "", nil, fmt.Errorf("the %s lastUsedAt watch delivered an unexpected %T", w.kind, object)
}

func (w *Watch) trackWrite(name, resourceVersion string, stored *time.Time) {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	previous, tracked := w.last[name]
	if !tracked || stored == nil || (previous != nil && previous.Equal(*stored)) {
		return
	}

	w.last[name] = stored
	w.writes[name] = append(w.writes[name], Write{ResourceVersion: resourceVersion, Time: *stored, ObservedAt: time.Now()})
}

func (w *Watch) fail(err error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	if w.err == nil {
		w.err = err
	}
}

func fromMetaTime(stored *metav1.Time) *time.Time {
	if stored == nil {
		return nil
	}

	parsed := stored.Time

	return &parsed
}

func fromSecret(secret *corev1.Secret) (*time.Time, error) {
	raw := string(secret.Data[extLastUsedAtField])
	if raw == "" {
		return nil, nil
	}

	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("parsing the stored %s %q: %w", extLastUsedAtField, raw, err)
	}

	return &parsed, nil
}

// GetClusterAuthToken reads the whole cluster auth token the given name has on the downstream cluster
func GetClusterAuthToken(client *rancher.Client, clusterID, name string) (*clusterv3.ClusterAuthToken, error) {
	downstream, err := client.WranglerContext.DownStreamClusterWranglerContext(clusterID)
	if err != nil {
		return nil, fmt.Errorf("reaching cluster %s to read cluster auth token %s: %w", clusterID, name, err)
	}

	clusterAuthToken, err := downstream.Cluster.ClusterAuthToken().Get(ClusterAuthTokenNamespace, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("reading cluster auth token %s on cluster %s: %w", name, clusterID, err)
	}

	return clusterAuthToken, nil
}

// SameTime reports whether two stored lastUsedAt values hold the same instant, where unset equals unset
func SameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}

	return a.Equal(*b)
}
