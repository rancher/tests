package auth

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
	"github.com/rancher/tests/actions/kubeapi/rancherserver"
	"github.com/rancher/tests/actions/kubeapi/tokens"
	"github.com/rancher/tests/actions/kubeapi/tokens/exttokens"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	ProxyPath     RequestPath = "proxy"
	BearerAPIPath RequestPath = "bearer"
	BasicAPIPath  RequestPath = "basic"

	lastUsedAtTokenDescription = "lastUsedAt throttle coverage"
	lastUsedAtErrorMarker      = "Error updating lastUsedAt"
	lastUsedAtExtTokenTTL      = int64(time.Hour / time.Millisecond)

	proxyRequestPath = "/k8s/clusters/%s/api/v1/namespaces/default"
	apiRequestPath   = "/v3/clusters"
	pingPath         = "/ping"

	RequestMarkerParameter = "marker"
	extBearerPrefix        = "ext/"
	credentialDivider      = ":"
)

// RequestPath names the route a request reaches Rancher by
type RequestPath string

// Credential is a token under test together with the bearer that uses it
type Credential struct {
	Kind   tokens.Kind
	Name   string
	Bearer string
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

// NewV3Credential mints a v3 token through the Norman API and returns it with the bearer that uses it
func NewV3Credential(client *rancher.Client) (*Credential, error) {
	created, err := client.Management.Token.Create(&management.Token{Description: lastUsedAtTokenDescription})
	if err != nil {
		return nil, fmt.Errorf("minting a v3 token: %w", err)
	}

	if created.Token == "" {
		return nil, fmt.Errorf("Rancher returned v3 token %s without its secret, which is only handed back at creation", created.Name)
	}

	return NewCredentialFromBearer(tokens.V3, created.Token)
}

// NewExtCredential mints an ext token through the public API and returns it with the bearer that uses it
func NewExtCredential(client *rancher.Client) (*Credential, error) {
	created, err := exttokens.CreateExtToken(client, lastUsedAtExtTokenTTL)
	if err != nil {
		return nil, fmt.Errorf("minting an ext token: %w", err)
	}

	if created.Status.BearerToken == "" {
		return nil, fmt.Errorf("Rancher returned ext token %s without its bearer token, which is only handed back at creation", created.Name)
	}

	return NewCredentialFromBearer(tokens.Ext, created.Status.BearerToken)
}

// NewLoginCredential returns the credential of the session token the client logged in with
func NewLoginCredential(client *rancher.Client) (*Credential, error) {
	bearer := client.Management.APIBaseClient.Opts.TokenKey

	if strings.HasPrefix(bearer, extBearerPrefix) {
		return NewCredentialFromBearer(tokens.Ext, bearer)
	}

	return NewCredentialFromBearer(tokens.V3, bearer)
}

// NewCredentialFromBearer names the token a bearer of the form name:secret or ext/name:secret belongs to
func NewCredentialFromBearer(kind tokens.Kind, bearer string) (*Credential, error) {
	name, _, found := strings.Cut(bearer, credentialDivider)
	if !found {
		return nil, fmt.Errorf("the bearer of a %s token should look like name%ssecret", kind, credentialDivider)
	}

	return &Credential{Kind: kind, Name: strings.TrimPrefix(name, extBearerPrefix), Bearer: bearer}, nil
}

// Target returns the stored lastUsedAt this credential's token keeps
func (c *Credential) Target() tokens.Target {
	return tokens.Target{Kind: c.Kind, Name: c.Name}
}

// NewRequester returns a requester that reaches the Rancher server the client is configured for
func NewRequester(client *rancher.Client) *Requester {
	insecure := false
	if client.RancherConfig.Insecure != nil {
		insecure = *client.RancherConfig.Insecure
	}

	return &Requester{baseURL: "https://" + client.RancherConfig.Host, insecure: insecure, clusterID: extclusterapi.LocalCluster}
}

// WithFreshConnections returns a requester that opens a new connection for every request so that a load balancer can spread them
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

	requests := []Request{}

	for sent := 0; time.Duration(sent)*interval < span; sent++ {
		if sent > 0 {
			<-ticker.C
		}

		request, err := r.send(httpClient, credential, path)
		if err != nil {
			return requests, err
		}

		requests = append(requests, request)

		if request.StatusCode != http.StatusOK {
			return requests, fmt.Errorf("Rancher answered %d to a request made with %s token %s", request.StatusCode, credential.Kind, credential.Name)
		}
	}

	return requests, nil
}

// Burst releases count requests at once, each over a connection of its own that is opened first when warmed is set
func (r *Requester) Burst(credential *Credential, path RequestPath, count int, warmed bool) ([]Request, error) {
	start := make(chan struct{})
	requests := make([]Request, count)
	failures := make([]error, count)

	var ready, finished sync.WaitGroup

	for i := 0; i < count; i++ {
		ready.Add(1)
		finished.Add(1)

		go func(i int) {
			defer finished.Done()

			httpClient := r.newHTTPClient()
			defer httpClient.CloseIdleConnections()

			if warmed {
				_, failures[i] = clientbase.Do(httpClient, http.MethodGet, r.baseURL+pingPath, nil, nil)
			}

			ready.Done()
			<-start

			if failures[i] == nil {
				requests[i], failures[i] = r.send(httpClient, credential, path)
			}
		}(i)
	}

	ready.Wait()
	close(start)
	finished.Wait()

	for _, failure := range failures {
		if failure != nil {
			return requests, failure
		}
	}

	return requests, nil
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
	url, header, err := r.describe(credential, path)
	if err != nil {
		return Request{}, err
	}

	sentAt := time.Now()

	response, err := clientbase.Do(httpClient, http.MethodGet, url, nil, header)
	if err != nil {
		return Request{At: sentAt}, err
	}

	return Request{At: sentAt, StatusCode: response.StatusCode}, nil
}

func (r *Requester) describe(credential *Credential, path RequestPath) (string, map[string]string, error) {
	marker := "?" + RequestMarkerParameter + "=" + credential.Name

	switch path {
	case ProxyPath:
		return r.baseURL + fmt.Sprintf(proxyRequestPath, r.clusterID) + marker, map[string]string{"Authorization": "Bearer " + credential.Bearer}, nil
	case BearerAPIPath:
		return r.baseURL + apiRequestPath + marker, map[string]string{"Authorization": "Bearer " + credential.Bearer}, nil
	case BasicAPIPath:
		return r.baseURL + apiRequestPath + marker, map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(credential.Bearer))}, nil
	}

	return "", nil, fmt.Errorf("unknown request path %q", path)
}

// PlantLastUsedAt stores a lastUsedAt, confirms the API reports it back and gives Rancher's caches time to see it
func PlantLastUsedAt(client *rancher.Client, target tokens.Target, at time.Time) (*tokens.LastUsedAt, error) {
	if err := tokens.SetLastUsedAt(client, target, at); err != nil {
		return nil, err
	}

	var planted *tokens.LastUsedAt

	var lastErr error

	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.OneSecondTimeout, defaults.OneMinuteTimeout, true,
		func(context.Context) (bool, error) {
			current, err := tokens.GetLastUsedAt(client, target)
			if err != nil {
				lastErr = err

				return false, nil
			}

			planted = current

			return current.Time != nil && current.Time.Equal(at.Truncate(time.Second)), nil
		})
	if pollErr != nil {
		return nil, fmt.Errorf("the planted lastUsedAt of %s token %s never read back (last read error: %v): %w", target.Kind, target.Name, lastErr, pollErr)
	}

	<-time.After(defaults.FiveSecondTimeout)

	return planted, nil
}

// PlantLastUsedAtConcurrently plants one lastUsedAt per target at the same time and returns the reads in the order of the targets
func PlantLastUsedAtConcurrently(client *rancher.Client, targets []tokens.Target, times []time.Time) ([]*tokens.LastUsedAt, error) {
	planted := make([]*tokens.LastUsedAt, len(targets))
	failures := make([]error, len(targets))

	var group sync.WaitGroup

	for index := range targets {
		group.Add(1)

		go func(position int) {
			defer group.Done()

			planted[position], failures[position] = PlantLastUsedAt(client, targets[position], times[position])
		}(index)
	}

	group.Wait()

	return planted, errors.Join(failures...)
}

// WaitForLastUsedAtChange polls until the stored lastUsedAt differs from the one given and returns the new one
func WaitForLastUsedAtChange(client *rancher.Client, target tokens.Target, previous *tokens.LastUsedAt) (*tokens.LastUsedAt, error) {
	var changed *tokens.LastUsedAt

	var lastErr error

	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.OneSecondTimeout, defaults.OneMinuteTimeout, true,
		func(context.Context) (bool, error) {
			current, err := tokens.GetLastUsedAt(client, target)
			if err != nil {
				lastErr = err

				return false, nil
			}

			lastErr = nil

			if tokens.SameTime(current.Time, previous.Time) {
				return false, nil
			}

			changed = current

			return true, nil
		})
	if pollErr != nil {
		return nil, fmt.Errorf("the lastUsedAt of %s token %s never moved from %v (last read error: %v): %w",
			target.Kind, target.Name, previous.Time, lastErr, pollErr)
	}

	return changed, nil
}

// VerifyAllSucceeded fails when any request was not answered with a 200
func VerifyAllSucceeded(requests []Request) error {
	for _, request := range requests {
		if request.StatusCode != http.StatusOK {
			return fmt.Errorf("a request sent at %s was answered %d rather than 200", request.At.UTC().Format(time.RFC3339), request.StatusCode)
		}
	}

	return nil
}

// VerifyWritesPerMinute fails unless the writes seen over the span number about one a minute
func VerifyWritesPerMinute(writes []tokens.Write, span time.Duration) error {
	minutes := int(span / defaults.OneMinuteTimeout)
	minimum := max(minutes-1, 1)
	maximum := minutes + 1

	if len(writes) < minimum || len(writes) > maximum {
		return fmt.Errorf("saw %d writes of lastUsedAt over %s, where a write a minute allows %d to %d", len(writes), span.Round(time.Second), minimum, maximum)
	}

	return nil
}

// VerifyWritesMatchRequests fails unless every write holds the whole-second time of one of the requests rather than a rounded one
func VerifyWritesMatchRequests(writes []tokens.Write, requests []Request) error {
	roundedToTheMinute := true

	for _, write := range writes {
		if write.Time.Nanosecond() != 0 {
			return fmt.Errorf("the lastUsedAt %s carries a fraction of a second", write.Time.Format(time.RFC3339Nano))
		}

		if write.Time.Second() != 0 {
			roundedToTheMinute = false
		}

		if !matchesAnyRequest(write.Time, requests) {
			return fmt.Errorf("the lastUsedAt %s is within %s of none of the %d requests", write.Time.Format(time.RFC3339), defaults.FiveSecondTimeout, len(requests))
		}
	}

	if len(writes) > 1 && roundedToTheMinute {
		return fmt.Errorf("every one of %d writes landed on a round minute, so lastUsedAt looks rounded rather than exact", len(writes))
	}

	return nil
}

// VerifyEveryRequestWasWritten fails unless each request moved lastUsedAt to its own time, in order
func VerifyEveryRequestWasWritten(writes []tokens.Write, requests []Request) error {
	if len(writes) != len(requests) {
		return fmt.Errorf("saw %d writes of lastUsedAt for %d requests that were each more than a minute after the last", len(writes), len(requests))
	}

	for i, write := range writes {
		if difference := write.Time.Sub(requests[i].At).Abs(); difference > defaults.FiveSecondTimeout {
			return fmt.Errorf("write %d holds %s, %s from its request", i+1, write.Time.Format(time.RFC3339), difference.Round(time.Second))
		}
	}

	return nil
}

// VerifyWritesNeverMoveBack fails when a write holds a time earlier than the one before it by more than the tolerance
func VerifyWritesNeverMoveBack(writes []tokens.Write) error {
	for i := 1; i < len(writes); i++ {
		if writes[i].Time.Before(writes[i-1].Time.Add(-defaults.FiveSecondTimeout)) {
			return fmt.Errorf("write %d moved lastUsedAt back from %s to %s", i+1, writes[i-1].Time.Format(time.RFC3339), writes[i].Time.Format(time.RFC3339))
		}
	}

	return nil
}

// VerifyLastUsedAtUnchanged fails when the stored lastUsedAt moved since it was read
func VerifyLastUsedAtUnchanged(client *rancher.Client, target tokens.Target, before *tokens.LastUsedAt) error {
	current, err := tokens.GetLastUsedAt(client, target)
	if err != nil {
		return fmt.Errorf("reading the lastUsedAt of %s token %s: %w", target.Kind, target.Name, err)
	}

	if !tokens.SameTime(current.Time, before.Time) {
		return fmt.Errorf("%s token %s was written, its lastUsedAt moved from %v to %v", target.Kind, target.Name, before.Time, current.Time)
	}

	return nil
}

// VerifyLastUsedAtNear fails unless the stored lastUsedAt is the whole-second time of the request, give or take the tolerance
func VerifyLastUsedAtNear(stored *tokens.LastUsedAt, at time.Time) error {
	if stored.Time == nil {
		return fmt.Errorf("lastUsedAt is unset where it should hold %s", at.UTC().Format(time.RFC3339))
	}

	if difference := stored.Time.Sub(at).Abs(); difference > defaults.FiveSecondTimeout {
		return fmt.Errorf("lastUsedAt holds %s, %s from the request made at %s",
			stored.Time.UTC().Format(time.RFC3339), difference.Round(time.Second), at.UTC().Format(time.RFC3339))
	}

	return nil
}

func matchesAnyRequest(stamp time.Time, requests []Request) bool {
	for _, request := range requests {
		if stamp.Sub(request.At).Abs() <= defaults.FiveSecondTimeout {
			return true
		}
	}

	return false
}

// NewCredential mints a token of the given kind and returns it with the bearer that uses it
func NewCredential(client *rancher.Client, kind tokens.Kind) (*Credential, error) {
	switch kind {
	case tokens.V3:
		return NewV3Credential(client)
	case tokens.Ext:
		return NewExtCredential(client)
	}

	return nil, fmt.Errorf("cannot mint a token of kind %q", kind)
}

// NewShortLivedCredential mints a token of the given kind that expires after the lifetime
func NewShortLivedCredential(client *rancher.Client, kind tokens.Kind, lifetime time.Duration) (*Credential, error) {
	switch kind {
	case tokens.V3:
		created, err := client.Management.Token.Create(&management.Token{Description: lastUsedAtTokenDescription, TTLMillis: lifetime.Milliseconds()})
		if err != nil {
			return nil, fmt.Errorf("minting a v3 token that lives %s: %w", lifetime, err)
		}

		return NewCredentialFromBearer(tokens.V3, created.Token)
	case tokens.Ext:
		created, err := exttokens.CreateExtToken(client, lifetime.Milliseconds())
		if err != nil {
			return nil, fmt.Errorf("minting an ext token that lives %s: %w", lifetime, err)
		}

		return NewCredentialFromBearer(tokens.Ext, created.Status.BearerToken)
	}

	return nil, fmt.Errorf("cannot mint a token of kind %q", kind)
}

// NewUsedCredential mints a token of the kind, uses it once and returns it with the lastUsedAt that first use wrote
func NewUsedCredential(client *rancher.Client, requester *Requester, kind tokens.Kind) (*Credential, *tokens.LastUsedAt, error) {
	credential, err := NewCredential(client, kind)
	if err != nil {
		return nil, nil, err
	}

	unused, err := tokens.GetLastUsedAt(client, credential.Target())
	if err != nil {
		return nil, nil, err
	}

	if unused.Time != nil {
		return nil, nil, fmt.Errorf("%s token %s holds lastUsedAt %s before anything used it", kind, credential.Name, unused.Time.Format(time.RFC3339))
	}

	request, err := requester.Send(credential, ProxyPath)
	if err != nil {
		return nil, nil, err
	}

	if request.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("Rancher answered %d to the first request made with %s token %s", request.StatusCode, kind, credential.Name)
	}

	used, err := WaitForLastUsedAtChange(client, credential.Target(), unused)
	if err != nil {
		return nil, nil, err
	}

	return credential, used, nil
}

// SetCredentialEnabled turns a token on or off
func SetCredentialEnabled(client *rancher.Client, credential *Credential, enabled bool) error {
	switch credential.Kind {
	case tokens.V3:
		patch := []byte(fmt.Sprintf(`{"enabled":%t}`, enabled))
		if _, err := client.WranglerContext.Mgmt.Token().Patch(credential.Name, types.MergePatchType, patch); err != nil {
			return fmt.Errorf("setting v3 token %s to enabled=%t: %w", credential.Name, enabled, err)
		}

		return nil
	case tokens.Ext:
		token, err := exttokenapi.GetExtTokenByName(client, credential.Name)
		if err != nil {
			return fmt.Errorf("reading ext token %s: %w", credential.Name, err)
		}

		token.Spec.Enabled = &enabled
		if _, err := exttokenapi.UpdateExtToken(client, token); err != nil {
			return fmt.Errorf("setting ext token %s to enabled=%t: %w", credential.Name, enabled, err)
		}

		return nil
	}

	return fmt.Errorf("cannot switch a token of kind %q", credential.Kind)
}

// DeleteCredential deletes a token
func DeleteCredential(client *rancher.Client, credential *Credential) error {
	switch credential.Kind {
	case tokens.V3:
		if err := client.WranglerContext.Mgmt.Token().Delete(credential.Name, &metav1.DeleteOptions{}); err != nil {
			return fmt.Errorf("deleting v3 token %s: %w", credential.Name, err)
		}

		return nil
	case tokens.Ext:
		return exttokenapi.DeleteExtToken(client, credential.Name, true)
	}

	return fmt.Errorf("cannot delete a token of kind %q", credential.Kind)
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

			last = request.StatusCode

			return last == status, nil
		})
	if pollErr != nil {
		return fmt.Errorf("%s token %s is still answered %d rather than %d (last request error: %v): %w", credential.Kind, credential.Name, last, status, lastErr, pollErr)
	}

	return nil
}

// RecordWrites uses the credential on a cadence while watching how often its lastUsedAt is written
func RecordWrites(client *rancher.Client, requester *Requester, credential *Credential, path RequestPath, interval, span time.Duration) ([]Request, []tokens.Write, error) {
	recorder, err := tokens.WatchLastUsedAt(client, credential.Kind, "", credential.Name)
	if err != nil {
		return nil, nil, err
	}

	requests, requestErr := requester.Repeat(credential, path, interval, span)

	<-time.After(defaults.FiveSecondTimeout)

	writes, watchErr := recorder.Stop()
	if requestErr != nil {
		return requests, writes[credential.Name], requestErr
	}

	return requests, writes[credential.Name], watchErr
}

// RecordBursts releases bursts of simultaneous requests one after another and returns the writes each of them caused
func RecordBursts(client *rancher.Client, requester *Requester, credential *Credential, path RequestPath, count, bursts int, warmed bool) ([][]Request, []int, error) {
	recorder, err := tokens.WatchLastUsedAt(client, credential.Kind, "", credential.Name)
	if err != nil {
		return nil, nil, err
	}

	sent := [][]Request{}
	writesPerBurst := []int{}
	seen := 0

	for burst := 0; burst < bursts; burst++ {
		requests, burstErr := requester.Burst(credential, path, count, warmed)
		sent = append(sent, requests)

		<-time.After(defaults.FiveSecondTimeout)

		total := len(recorder.Writes(credential.Name))
		writesPerBurst = append(writesPerBurst, total-seen)
		seen = total

		if burstErr != nil {
			_, stopErr := recorder.Stop()

			return sent, writesPerBurst, errors.Join(burstErr, stopErr)
		}
	}

	_, watchErr := recorder.Stop()

	return sent, writesPerBurst, watchErr
}

// RepeatAll uses every credential on a cadence at the same time and returns what each of them sent
func RepeatAll(requester *Requester, credentials []*Credential, path RequestPath, interval, span time.Duration) ([][]Request, error) {
	results := make([][]Request, len(credentials))
	failures := make([]error, len(credentials))

	var finished sync.WaitGroup

	for i := range credentials {
		finished.Add(1)

		go func(i int) {
			defer finished.Done()

			results[i], failures[i] = requester.Repeat(credentials[i], path, interval, span)
		}(i)
	}

	finished.Wait()

	for _, failure := range failures {
		if failure != nil {
			return results, failure
		}
	}

	return results, nil
}

// ListLastUsedAtErrors returns the lines Rancher has logged about a failed write of lastUsedAt
func ListLastUsedAtErrors(client *rancher.Client) ([]string, error) {
	return rancherserver.FindRancherServerLogs(client, lastUsedAtErrorMarker)
}

// VerifyNoLastUsedAtErrors fails when Rancher logged a failed write of lastUsedAt that the baseline does not hold
func VerifyNoLastUsedAtErrors(client *rancher.Client, baseline []string) error {
	current, err := ListLastUsedAtErrors(client)
	if err != nil {
		return fmt.Errorf("listing the lastUsedAt errors: %w", err)
	}

	known := map[string]struct{}{}
	for _, line := range baseline {
		known[line] = struct{}{}
	}

	for _, line := range current {
		if _, found := known[line]; !found {
			return fmt.Errorf("Rancher logged a new %q line: %s", lastUsedAtErrorMarker, line)
		}
	}

	return nil
}
