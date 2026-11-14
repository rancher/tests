package tokens

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	exttokenapi "github.com/rancher/shepherd/extensions/kubeapi/tokens"
	"github.com/rancher/shepherd/pkg/clientbase"
	authactions "github.com/rancher/tests/actions/auth"
	kubeapitokens "github.com/rancher/tests/actions/kubeapi/tokens"
	"github.com/rancher/tests/actions/kubeapi/tokens/exttokens"
	"github.com/rancher/tests/actions/rbac"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	ProxyPath              RequestPath = "proxy"
	BearerAPIPath          RequestPath = "bearer"
	BasicAPIPath           RequestPath = "basic"
	RequestMarkerParameter             = "marker"
	apiRequestPath                     = "/v3/clusters"
	credentialDivider                  = ":"
)

var tokenLifetime = defaults.ThirtyMinuteTimeout

// Category names which token, or which copy of a token, holds a lastUsedAt
type Category string

const (
	V3          Category = "v3"
	Ext         Category = "ext"
	ClusterAuth Category = "clusterauth"
)

// RequestPath names the route a request reaches Rancher by
type RequestPath string

// Target names one stored lastUsedAt
type Target struct {
	Category  Category
	Name      string
	ClusterID string
}

// Credential is a token under test together with the bearer that uses it
type Credential struct {
	Category Category
	Name     string
	Bearer   string
}

// Request records when a request was sent and how it was answered
type Request struct {
	At            time.Time
	StatusCode    int
	Authenticated bool
}

// Requester sends authenticated requests to the Rancher server
type Requester struct {
	baseURL          string
	insecure         bool
	freshConnections bool
	clusterID        string
}

// NewCredential mints a token of the given category that lives for the test lifetime and returns it with the bearer that uses it
func NewCredential(client *rancher.Client, category Category) (*Credential, error) {
	return NewShortLivedCredential(client, category, tokenLifetime)
}

// NewShortLivedCredential mints a token of the given category that expires after the lifetime
func NewShortLivedCredential(client *rancher.Client, category Category, lifetime time.Duration) (*Credential, error) {
	switch category {
	case V3:
		return mintV3(client, lifetime)
	case Ext:
		return mintExt(client, lifetime, "")
	default:
		return nil, fmt.Errorf("cannot mint a token of category %q", category)
	}
}

// NewClusterAuthCredential mints a token of the category scoped to the cluster and waits for its cluster auth token to reach the cluster
func NewClusterAuthCredential(client *rancher.Client, clusterID string, category Category) (*Credential, error) {
	var credential *Credential

	switch category {
	case V3:
		bearer, err := authactions.NewClusterScopedToken(client, clusterID, tokenLifetime)
		if err != nil {
			return nil, fmt.Errorf("minting a cluster scoped v3 token: %w", err)
		}

		credential, err = NewCredentialFromBearer(bearer)
		if err != nil {
			return nil, fmt.Errorf("building the v3 credential: %w", err)
		}
	case Ext:
		var err error

		credential, err = mintExt(client, tokenLifetime, clusterID)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("cannot mint a token of category %q", category)
	}

	if err := authactions.WaitForClusterAuthToken(client, clusterID, credential.Name); err != nil {
		return nil, fmt.Errorf("waiting for the cluster auth token %s: %w", credential.Name, err)
	}

	return credential, nil
}

// NewKubeconfigCredential generates a local cluster kubeconfig and returns the credential of the v3 token it carries
func NewKubeconfigCredential(client *rancher.Client) (*Credential, error) {
	token, err := authactions.ClusterAuthTokenFromKubeconfig(client, extclusterapi.LocalCluster)
	if err != nil {
		return nil, err
	}

	credential, err := NewCredentialFromBearer(token)
	if err != nil {
		return nil, err
	}

	if client.Session != nil {
		client.Session.RegisterCleanupFunc(func() error {
			return DeleteCredential(client, credential)
		})
	}

	return credential, nil
}

// NewUserSessionCredential logs a new standard user in and returns the credential of its v3 session token
func NewUserSessionCredential(client *rancher.Client) (*Credential, error) {
	_, userClient, err := rbac.SetupUser(client, rbac.StandardUser.String())
	if err != nil {
		return nil, err
	}

	return NewCredentialFromBearer(userClient.Management.APIBaseClient.Opts.TokenKey)
}

// NewCredentialFromBearer names the token a bearer of the form name:secret or ext/name:secret belongs to
func NewCredentialFromBearer(bearer string) (*Credential, error) {
	name, _, found := strings.Cut(bearer, credentialDivider)
	if !found {
		return nil, fmt.Errorf("a bearer should look like name%ssecret or %sname%ssecret", credentialDivider, authactions.ExtTokenPrefix, credentialDivider)
	}

	if strings.HasPrefix(name, authactions.ExtTokenPrefix) {
		return &Credential{Category: Ext, Name: strings.TrimPrefix(name, authactions.ExtTokenPrefix), Bearer: bearer}, nil
	}

	return &Credential{Category: V3, Name: name, Bearer: bearer}, nil
}

// CreateUsedCredential mints a token of the category, uses it once and returns it with the lastUsedAt that first use wrote
func CreateUsedCredential(client *rancher.Client, requester *Requester, category Category) (*Credential, *time.Time, error) {
	credential, err := NewCredential(client, category)
	if err != nil {
		return nil, nil, err
	}

	unused, err := GetLastUsedAt(client, credential.Target())
	if err != nil {
		return nil, nil, err
	}

	if unused != nil {
		return nil, nil, fmt.Errorf("%s token %s holds lastUsedAt %s before anything used it", category, credential.Name, unused.Format(time.RFC3339))
	}

	request, err := requester.Send(credential, ProxyPath)
	if err != nil {
		return nil, nil, err
	}

	if request.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("Rancher answered %d to the first request made with %s token %s", request.StatusCode, category, credential.Name)
	}

	used, err := WaitForLastUsedAtChange(client, credential.Target(), unused)
	if err != nil {
		return nil, nil, err
	}

	return credential, used, nil
}

// DeleteCredential deletes the token behind the credential, treating a token that is already gone as deleted
func DeleteCredential(client *rancher.Client, credential *Credential) error {
	var err error

	switch credential.Category {
	case V3:
		err = client.WranglerContext.Mgmt.Token().Delete(credential.Name, &metav1.DeleteOptions{})
	case Ext:
		err = exttokenapi.DeleteExtToken(client, credential.Name, false)
	default:
		return fmt.Errorf("cannot delete a token of category %q", credential.Category)
	}

	if apierrors.IsNotFound(err) {
		return nil
	}

	return err
}

// SetCredentialEnabled turns a token on or off
func SetCredentialEnabled(client *rancher.Client, credential *Credential, enabled bool) error {
	switch credential.Category {
	case V3:
		patch := []byte(fmt.Sprintf(`{"enabled":%t}`, enabled))
		if _, err := client.WranglerContext.Mgmt.Token().Patch(credential.Name, types.MergePatchType, patch); err != nil {
			return fmt.Errorf("setting v3 token %s to enabled=%t: %w", credential.Name, enabled, err)
		}

		return nil
	case Ext:
		token, err := exttokenapi.GetExtTokenByName(client, credential.Name)
		if err != nil {
			return fmt.Errorf("reading ext token %s: %w", credential.Name, err)
		}

		token.Spec.Enabled = &enabled
		if _, err := exttokenapi.UpdateExtToken(client, token); err != nil {
			return fmt.Errorf("setting ext token %s to enabled=%t: %w", credential.Name, enabled, err)
		}

		return nil
	default:
		return fmt.Errorf("cannot switch a token of category %q", credential.Category)
	}
}

// Target returns the stored lastUsedAt this credential's token keeps
func (c *Credential) Target() Target {
	return Target{Category: c.Category, Name: c.Name}
}

// ClusterAuthTarget returns the stored lastUsedAt kube-api-auth keeps for this credential's token on the cluster
func (c *Credential) ClusterAuthTarget(clusterID string) Target {
	return Target{Category: ClusterAuth, Name: c.Name, ClusterID: clusterID}
}

// NewRequester returns a requester that reaches the Rancher server the client is configured for
func NewRequester(client *rancher.Client) *Requester {
	insecure := false
	if client.RancherConfig.Insecure != nil {
		insecure = *client.RancherConfig.Insecure
	}

	return &Requester{baseURL: "https://" + client.RancherConfig.Host, insecure: insecure, clusterID: extclusterapi.LocalCluster}
}

// WithFreshConnections returns a requester that opens a new connection for every request
func (r *Requester) WithFreshConnections() *Requester {
	spread := *r
	spread.freshConnections = true

	return &spread
}

// WithCluster returns a requester whose proxy path reaches the given cluster rather than the Rancher local one
func (r *Requester) WithCluster(clusterID string) *Requester {
	scoped := *r
	scoped.clusterID = clusterID

	return &scoped
}

// Send makes one request authenticated by the credential and returns when it was sent and how it was answered
func (r *Requester) Send(credential *Credential, path RequestPath) (Request, error) {
	httpClient := r.newHTTPClient()
	defer httpClient.CloseIdleConnections()

	return r.send(httpClient, credential, path)
}

// Repeat makes a request every interval until the span has passed and fails on the first one Rancher does not accept
func (r *Requester) Repeat(credential *Credential, path RequestPath, interval, span time.Duration) ([]Request, error) {
	httpClient := r.newHTTPClient()
	defer httpClient.CloseIdleConnections()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	started := time.Now()
	requests := []Request{}

	for {
		request, err := r.send(httpClient, credential, path)
		if err != nil {
			return requests, err
		}

		requests = append(requests, request)

		if request.StatusCode != http.StatusOK {
			return requests, fmt.Errorf("Rancher answered %d to a request made with %s token %s", request.StatusCode, credential.Category, credential.Name)
		}

		if time.Since(started)+interval > span {
			return requests, nil
		}

		<-ticker.C
	}
}

// Burst releases count requests at once, each over a connection of its own that is opened first when warmed is set
func (r *Requester) Burst(credential *Credential, path RequestPath, count int, warmed bool) ([]Request, error) {
	start := make(chan struct{})
	requests := make([]Request, count)
	failures := make([]error, count)

	var groupReady, groupFinished sync.WaitGroup

	for index := 0; index < count; index++ {
		groupReady.Add(1)
		groupFinished.Add(1)

		go func() {
			defer groupFinished.Done()

			httpClient := r.newHTTPClient()
			defer httpClient.CloseIdleConnections()

			if warmed {
				_, failures[index] = clientbase.Do(httpClient, http.MethodGet, r.baseURL+"/ping", nil, nil)
			}

			groupReady.Done()
			<-start

			if failures[index] == nil {
				requests[index], failures[index] = r.send(httpClient, credential, path)
			}
		}()
	}

	groupReady.Wait()
	close(start)
	groupFinished.Wait()

	return requests, errors.Join(failures...)
}

// SendAll makes one request with each credential at the same time and returns them in the order of the credentials
func (r *Requester) SendAll(credentials []*Credential, path RequestPath) ([]Request, error) {
	requests := make([]Request, len(credentials))
	failures := make([]error, len(credentials))

	var group sync.WaitGroup

	for index, credential := range credentials {
		group.Add(1)

		go func() {
			defer group.Done()

			requests[index], failures[index] = r.Send(credential, path)
		}()
	}

	group.Wait()

	return requests, errors.Join(failures...)
}

// WaitForStatus polls until a request made with the credential is answered with the status
func (r *Requester) WaitForStatus(credential *Credential, path RequestPath, status int, timeout time.Duration) error {
	var last int

	var lastErr error

	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, timeout, true,
		func(context.Context) (bool, error) {
			request, err := r.Send(credential, path)
			if err != nil {
				lastErr = err

				return false, nil
			}

			lastErr = nil

			last = request.StatusCode

			return last == status, nil
		})
	if pollErr != nil {
		return fmt.Errorf("%s token %s is still answered %d rather than %d (last request error: %v): %w", credential.Category, credential.Name, last, status, lastErr, pollErr)
	}

	return nil
}

// GetLastUsedAt reads the lastUsedAt stored for the target
func GetLastUsedAt(client *rancher.Client, target Target) (*time.Time, error) {
	switch target.Category {
	case V3:
		return kubeapitokens.GetV3TokenLastUsedAt(client, target.Name)
	case Ext:
		return exttokens.GetExtTokenLastUsedAt(client, target.Name)
	case ClusterAuth:
		return authactions.GetClusterAuthTokenLastUsedAt(client, target.ClusterID, target.Name)
	default:
		return nil, fmt.Errorf("cannot read the lastUsedAt of a token of category %q", target.Category)
	}
}

// SetLastUsedAtAndWait stores a lastUsedAt and waits until it has read back unchanged long enough for Rancher's caches to see it
func SetLastUsedAtAndWait(client *rancher.Client, target Target, at time.Time) (*time.Time, error) {
	planted := at.UTC().Truncate(time.Second)

	var setErr error

	switch target.Category {
	case V3:
		setErr = kubeapitokens.SetV3TokenLastUsedAt(client, target.Name, planted)
	case Ext:
		setErr = exttokens.SetExtTokenLastUsedAt(client, target.Name, planted)
	case ClusterAuth:
		setErr = authactions.SetClusterAuthTokenLastUsedAt(client, target.ClusterID, target.Name, planted)
	default:
		setErr = fmt.Errorf("cannot set the lastUsedAt of a token of category %q", target.Category)
	}

	if setErr != nil {
		return nil, setErr
	}

	var heldSince time.Time

	var last *time.Time

	var lastErr error

	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.OneSecondTimeout, defaults.OneMinuteTimeout, true,
		func(context.Context) (bool, error) {
			current, err := GetLastUsedAt(client, target)
			if err != nil {
				lastErr = err

				return false, nil
			}

			last = current

			if !sameTime(current, &planted) {
				heldSince = time.Time{}

				return false, nil
			}

			if heldSince.IsZero() {
				heldSince = time.Now()
			}

			return time.Since(heldSince) >= defaults.FiveSecondTimeout, nil
		})
	if pollErr != nil {
		return nil, fmt.Errorf("the planted lastUsedAt %s of %s token %s never held for %s, last read %v (last read error: %v): %w",
			planted.Format(time.RFC3339), target.Category, target.Name, defaults.FiveSecondTimeout, last, lastErr, pollErr)
	}

	return &planted, nil
}

// SetLastUsedAtAndWaitUntil sets one lastUsedAt per target at the same time, then waits until the given time, failing when planting ran past it
func SetLastUsedAtAndWaitUntil(client *rancher.Client, targets []Target, times []time.Time, until time.Time) ([]*time.Time, error) {
	planted, err := SetLastUsedAtAndWaitConcurrently(client, targets, times)
	if err != nil {
		return nil, err
	}

	if !time.Now().Before(until) {
		return nil, fmt.Errorf("planting %d stored values ran past %s, when the requests were due", len(targets), until.UTC().Format(time.RFC3339))
	}

	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveHundredMillisecondTimeout, time.Until(until)+defaults.FiveSecondTimeout, true,
		func(context.Context) (bool, error) {
			return !time.Now().Before(until), nil
		})
	if err != nil {
		return nil, fmt.Errorf("waiting for %s after planting the stored values: %w", until.UTC().Format(time.RFC3339), err)
	}

	return planted, nil
}

// SetLastUsedAtAndWaitConcurrently sets one lastUsedAt per target at the same time and returns them in the order of the targets
func SetLastUsedAtAndWaitConcurrently(client *rancher.Client, targets []Target, times []time.Time) ([]*time.Time, error) {
	planted := make([]*time.Time, len(targets))
	failures := make([]error, len(targets))

	var group sync.WaitGroup

	for index := range targets {
		group.Add(1)

		go func() {
			defer group.Done()

			planted[index], failures[index] = SetLastUsedAtAndWait(client, targets[index], times[index])
		}()
	}

	group.Wait()

	return planted, errors.Join(failures...)
}

// WaitForLastUsedAtChange polls until the stored lastUsedAt differs from the one given and returns the new one
func WaitForLastUsedAtChange(client *rancher.Client, target Target, previous *time.Time) (*time.Time, error) {
	var changed *time.Time

	var lastErr error

	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.OneSecondTimeout, defaults.OneMinuteTimeout, true,
		func(context.Context) (bool, error) {
			current, err := GetLastUsedAt(client, target)
			if err != nil {
				lastErr = err

				return false, nil
			}

			lastErr = nil

			if sameTime(current, previous) {
				return false, nil
			}

			changed = current

			return true, nil
		})
	if pollErr != nil {
		return nil, fmt.Errorf("the lastUsedAt of %s token %s never moved from %v (last read error: %v): %w", target.Category, target.Name, previous, lastErr, pollErr)
	}

	return changed, nil
}

// RecordWrites uses the credential on a cadence while recording how often its lastUsedAt is written
func RecordWrites(client *rancher.Client, requester *Requester, credential *Credential, path RequestPath, interval, span time.Duration) ([]Request, []time.Time, error) {
	recorder, err := RecordLastUsedAt(client, credential.Category, "", credential.Name)
	if err != nil {
		return nil, nil, err
	}

	requests, requestErr := requester.Repeat(credential, path, interval, span)

	writes, recordErr := recorder.StopWhenQuiet(defaults.FiveSecondTimeout)

	return requests, writes[credential.Name], errors.Join(requestErr, recordErr)
}

// RecordBursts releases bursts of simultaneous requests one after another and returns the writes each of them caused
func RecordBursts(client *rancher.Client, requester *Requester, credential *Credential, path RequestPath, count, bursts int, warmed bool) ([][]Request, []int, error) {
	recorder, err := RecordLastUsedAt(client, credential.Category, "", credential.Name)
	if err != nil {
		return nil, nil, err
	}

	sent := [][]Request{}
	writesPerBurst := []int{}
	seen := 0

	for burst := 0; burst < bursts; burst++ {
		requests, burstErr := requester.Burst(credential, path, count, warmed)
		sent = append(sent, requests)

		quietErr := recorder.WaitForQuiet(defaults.FiveSecondTimeout)

		total := len(recorder.Writes(credential.Name))
		writesPerBurst = append(writesPerBurst, total-seen)
		seen = total

		if burstErr != nil || quietErr != nil {
			_, stopErr := recorder.Stop()

			return sent, writesPerBurst, errors.Join(burstErr, quietErr, stopErr)
		}
	}

	_, recordErr := recorder.Stop()

	return sent, writesPerBurst, recordErr
}

// RepeatAll uses every credential on a cadence at the same time and returns what each of them sent
func RepeatAll(requester *Requester, credentials []*Credential, path RequestPath, interval, span time.Duration) ([][]Request, error) {
	results := make([][]Request, len(credentials))
	failures := make([]error, len(credentials))

	var group sync.WaitGroup

	for index := range credentials {
		group.Add(1)

		go func() {
			defer group.Done()

			results[index], failures[index] = requester.Repeat(credentials[index], path, interval, span)
		}()
	}

	group.Wait()

	return results, errors.Join(failures...)
}

func mintV3(client *rancher.Client, lifetime time.Duration) (*Credential, error) {
	created, err := client.Management.Token.Create(&management.Token{Description: "lastUsedAt throttle coverage", TTLMillis: lifetime.Milliseconds()})
	if err != nil {
		return nil, fmt.Errorf("minting a v3 token: %w", err)
	}

	if created.Token == "" {
		return nil, fmt.Errorf("Rancher returned v3 token %s without its secret, which is only handed back at creation", created.Name)
	}

	return NewCredentialFromBearer(created.Token)
}

func mintExt(client *rancher.Client, lifetime time.Duration, clusterID string) (*Credential, error) {
	token := exttokens.NewExtTokenObject("", lifetime.Milliseconds())
	token.Spec.ClusterName = clusterID

	created, err := exttokenapi.CreateExtToken(client, token)
	if err != nil {
		return nil, fmt.Errorf("minting an ext token: %w", err)
	}

	if client.Session != nil {
		client.Session.RegisterCleanupFunc(func() error {
			return DeleteCredential(client, &Credential{Category: Ext, Name: created.Name})
		})
	}

	if created.Status.BearerToken == "" {
		return nil, fmt.Errorf("Rancher returned ext token %s without its bearer token, which is only handed back at creation", created.Name)
	}

	return NewCredentialFromBearer(created.Status.BearerToken)
}

func (r *Requester) newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaults.OneMinuteTimeout,
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: r.insecure}, // nolint:gosec
			DisableKeepAlives: r.freshConnections,
		},
	}
}

func (r *Requester) send(httpClient *http.Client, credential *Credential, path RequestPath) (Request, error) {
	marker := "?" + RequestMarkerParameter + "=" + credential.Name

	var url, authorization string

	switch path {
	case ProxyPath:
		url = r.baseURL + fmt.Sprintf("/k8s/clusters/%s/api/v1/namespaces/default", r.clusterID) + marker
		authorization = "Bearer " + credential.Bearer
	case BearerAPIPath:
		url = r.baseURL + apiRequestPath + marker
		authorization = "Bearer " + credential.Bearer
	case BasicAPIPath:
		url = r.baseURL + apiRequestPath + marker
		authorization = "Basic " + base64.StdEncoding.EncodeToString([]byte(credential.Bearer))
	default:
		return Request{}, fmt.Errorf("unknown request path %q", path)
	}

	sentAt := time.Now()

	response, err := clientbase.Do(httpClient, http.MethodGet, url, nil, map[string]string{"Authorization": authorization})
	if err != nil {
		return Request{At: sentAt}, err
	}

	return Request{At: sentAt, StatusCode: response.StatusCode, Authenticated: response.StatusCode == http.StatusOK}, nil
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}

	return a.Equal(*b)
}

// Recorder polls the lastUsedAt of the tokens it was started for twice a second and records every new value it reads
type Recorder struct {
	targets     []Target
	quit        chan struct{}
	done        chan struct{}
	stopOnce    sync.Once
	mutex       sync.Mutex
	last        map[string]*time.Time
	writes      map[string][]time.Time
	readErr     error
	lastWriteAt time.Time
}

// RecordLastUsedAt starts polling the lastUsedAt of the named tokens of one category and recording every change
func RecordLastUsedAt(client *rancher.Client, category Category, clusterID string, names ...string) (*Recorder, error) {
	recorder := &Recorder{
		quit:        make(chan struct{}),
		done:        make(chan struct{}),
		last:        map[string]*time.Time{},
		writes:      map[string][]time.Time{},
		lastWriteAt: time.Now(),
	}

	for _, name := range names {
		target := Target{Category: category, ClusterID: clusterID, Name: name}

		current, err := GetLastUsedAt(client, target)
		if err != nil {
			return nil, fmt.Errorf("reading the lastUsedAt of %s token %s: %w", category, name, err)
		}

		recorder.targets = append(recorder.targets, target)
		recorder.last[name] = current
	}

	go func() {
		defer close(recorder.done)

		ticker := time.NewTicker(defaults.FiveHundredMillisecondTimeout)
		defer ticker.Stop()

		for {
			select {
			case <-recorder.quit:
				return
			case <-ticker.C:
			}

			var group sync.WaitGroup

			for _, target := range recorder.targets {
				group.Add(1)

				go func() {
					defer group.Done()

					current, err := GetLastUsedAt(client, target)
					recorder.track(target.Name, current, err)
				}()
			}

			group.Wait()
		}
	}()

	if client.Session != nil {
		client.Session.RegisterCleanupFunc(func() error {
			recorder.halt()

			return nil
		})
	}

	return recorder, nil
}

// Stop ends the polling and returns the writes seen per token, failing when any read of a token failed along the way
func (r *Recorder) Stop() (map[string][]time.Time, error) {
	r.halt()

	r.mutex.Lock()
	defer r.mutex.Unlock()

	writes := make(map[string][]time.Time, len(r.writes))
	for name, seen := range r.writes {
		writes[name] = append([]time.Time(nil), seen...)
	}

	return writes, r.readErr
}

// StopWhenQuiet waits until no write has been seen for the quiet period, then ends the polling and returns the writes seen per token
func (r *Recorder) StopWhenQuiet(quiet time.Duration) (map[string][]time.Time, error) {
	quietErr := r.WaitForQuiet(quiet)

	writes, err := r.Stop()

	return writes, errors.Join(quietErr, err)
}

// WaitForQuiet polls until no write has been seen for the quiet period
func (r *Recorder) WaitForQuiet(quiet time.Duration) error {
	since := time.Now()

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.OneSecondTimeout, quiet+defaults.TwoMinuteTimeout, false,
		func(context.Context) (bool, error) {
			r.mutex.Lock()
			defer r.mutex.Unlock()

			quietSince := since
			if r.lastWriteAt.After(quietSince) {
				quietSince = r.lastWriteAt
			}

			return time.Since(quietSince) >= quiet, nil
		})
	if err != nil {
		return fmt.Errorf("the lastUsedAt kept being written, so it never went %s without a write: %w", quiet, err)
	}

	return nil
}

// Writes returns the writes seen so far for the named token while the polling keeps running
func (r *Recorder) Writes(name string) []time.Time {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	return append([]time.Time(nil), r.writes[name]...)
}

func (r *Recorder) halt() {
	r.stopOnce.Do(func() { close(r.quit) })
	<-r.done
}

func (r *Recorder) track(name string, current *time.Time, err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if err != nil {
		if r.readErr == nil {
			r.readErr = fmt.Errorf("a read of the lastUsedAt of token %s failed, so a write may have gone unseen: %w", name, err)
		}

		return
	}

	if current == nil || sameTime(current, r.last[name]) {
		return
	}

	r.last[name] = current
	r.writes[name] = append(r.writes[name], *current)
	r.lastWriteAt = time.Now()
}
