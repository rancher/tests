//go:build !pit.daily && !pit.weekly && !pit.event && !pit.harvester.daily && !pit.elemental && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package watch

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults"
	extkubeconfigs "github.com/rancher/shepherd/extensions/kubeapi/kubeconfigs"
	exttokens "github.com/rancher/shepherd/extensions/kubeapi/tokens"
	"github.com/rancher/shepherd/pkg/session"
	kubeconfigactions "github.com/rancher/tests/actions/kubeapi/kubeconfigs"
	tokenactions "github.com/rancher/tests/actions/kubeapi/tokens/exttokens"
	"github.com/rancher/tests/actions/rbac"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	kubeconfigSchemaID = "ext.cattle.io.kubeconfig"
	tokenSchemaID      = "ext.cattle.io.token"
	watchUpdateLabel   = "qa.cattle.io/steve-watch-updated"

	subscriptionStartTimeout = time.Minute
	watchEventTimeout        = time.Minute
	watchEventSettleTime     = 2 * time.Second
)

type steveSubscribeEvent struct {
	Name         string `json:"name"`
	ResourceType string `json:"resourceType"`
	Data         struct {
		ID       string `json:"id"`
		Error    string `json:"error"`
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	} `json:"data"`
}

func (s steveSubscribeEvent) objectName() string {
	if s.Data.Metadata.Name != "" {
		return s.Data.Metadata.Name
	}
	return s.Data.ID
}

type steveSubscription struct {
	events     <-chan steveSubscribeEvent
	readErrors <-chan error
	close      func()
}

type watchResourceCase struct {
	name     string
	schemaID string
	create   func(client *rancher.Client) (string, error)
	update   func(client *rancher.Client, name string) error
	delete   func(client *rancher.Client, name string) error
}

type trackedWatchResource struct {
	client  *rancher.Client
	name    string
	deleted bool
}

type OwnershipWatchTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
}

func (o *OwnershipWatchTestSuite) SetupSuite() {
	o.session = session.NewSession()

	client, err := rancher.NewClient("", o.session)
	require.NoError(o.T(), err)
	o.client = client
}

func (o *OwnershipWatchTestSuite) TearDownSuite() {
	o.session.Cleanup()
}

func (o *OwnershipWatchTestSuite) TestSubscribeEventsRespectOwnership() {
	testSession := o.session.NewSession()
	defer testSession.Cleanup()

	adminClient, err := o.client.WithSession(testSession)
	require.NoError(o.T(), err)

	localCluster, err := adminClient.Management.Cluster.ByID("local")
	require.NoError(o.T(), err, "Failed to get the local cluster")

	_, subscriberClient, err := rbac.AddUserWithRoleToCluster(
		adminClient,
		rbac.StandardUser.String(),
		rbac.ClusterMember.String(),
		localCluster,
		nil,
	)
	require.NoError(o.T(), err, "Failed to create the subscribing cluster member")

	resourceCases := []watchResourceCase{
		newKubeconfigWatchCase(localCluster),
		newTokenWatchCase(),
	}

	for _, resourceCase := range resourceCases {
		resourceCase := resourceCase
		o.Run(resourceCase.name, func() {
			o.runOwnershipWatchCase(adminClient, subscriberClient, resourceCase)
		})
	}
}

func (o *OwnershipWatchTestSuite) runOwnershipWatchCase(
	adminClient *rancher.Client,
	subscriberClient *rancher.Client,
	resourceCase watchResourceCase,
) {
	fixtures := []*trackedWatchResource{}
	defer func() {
		for index := len(fixtures) - 1; index >= 0; index-- {
			fixture := fixtures[index]
			if fixture.deleted {
				continue
			}
			if err := resourceCase.delete(fixture.client, fixture.name); err != nil && !apierrors.IsNotFound(err) {
				o.T().Errorf("Failed to clean up %s %q: %v", resourceCase.name, fixture.name, err)
			}
		}
	}()

	createFixture := func(client *rancher.Client) *trackedWatchResource {
		name, err := resourceCase.create(client)
		require.NoError(o.T(), err, "Failed to create %s as user %q", resourceCase.name, client.UserID)
		require.NotEmpty(o.T(), name)

		fixture := &trackedWatchResource{client: client, name: name}
		fixtures = append(fixtures, fixture)
		return fixture
	}

	deleteFixture := func(fixture *trackedWatchResource) {
		err := resourceCase.delete(fixture.client, fixture.name)
		require.NoError(o.T(), err, "Failed to delete %s %q", resourceCase.name, fixture.name)
		fixture.deleted = true
	}

	ownedBaseline := createFixture(subscriberClient)
	foreignBaseline := createFixture(adminClient)

	revision := o.waitForOwnershipVisibility(
		resourceCase,
		adminClient,
		subscriberClient,
		ownedBaseline.name,
		foreignBaseline.name,
	)

	subscriberWatch, err := openSteveSubscription(subscriberClient, resourceCase.schemaID, "")
	require.NoError(o.T(), err)
	defer subscriberWatch.close()

	adminWatch, err := openSteveSubscription(adminClient, resourceCase.schemaID, revision)
	require.NoError(o.T(), err)
	defer adminWatch.close()

	waitForSubscriptionStart(o.T(), subscriberWatch, resourceCase.schemaID)
	waitForSubscriptionStart(o.T(), adminWatch, resourceCase.schemaID)

	if !o.Run("updates", func() {
		require.NoError(o.T(), resourceCase.update(adminClient, foreignBaseline.name))
		require.NoError(o.T(), resourceCase.update(subscriberClient, ownedBaseline.name))

		assertSubscriptionEvents(
			o.T(),
			adminWatch,
			[]string{"resource.change"},
			[]string{foreignBaseline.name, ownedBaseline.name},
			nil,
			0,
		)
		assertSubscriptionEvents(
			o.T(),
			subscriberWatch,
			[]string{"resource.change"},
			[]string{ownedBaseline.name},
			[]string{foreignBaseline.name},
			watchEventSettleTime,
		)
	}) {
		return
	}

	var ownedCreated *trackedWatchResource
	var foreignCreated *trackedWatchResource
	if !o.Run("creates", func() {
		foreignCreated = createFixture(adminClient)
		ownedCreated = createFixture(subscriberClient)

		createEventNames := []string{"resource.create", "resource.change"}
		assertSubscriptionEvents(
			o.T(),
			adminWatch,
			createEventNames,
			[]string{foreignCreated.name, ownedCreated.name},
			nil,
			0,
		)
		assertSubscriptionEvents(
			o.T(),
			subscriberWatch,
			createEventNames,
			[]string{ownedCreated.name},
			[]string{foreignCreated.name},
			watchEventSettleTime,
		)
	}) {
		return
	}

	o.Run("deletes", func() {
		deleteFixture(foreignBaseline)
		deleteFixture(ownedBaseline)

		assertSubscriptionEvents(
			o.T(),
			adminWatch,
			[]string{"resource.remove"},
			[]string{foreignBaseline.name, ownedBaseline.name},
			nil,
			0,
		)
		assertSubscriptionEvents(
			o.T(),
			subscriberWatch,
			[]string{"resource.remove"},
			[]string{ownedBaseline.name},
			[]string{foreignBaseline.name},
			watchEventSettleTime,
		)
	})
}

func (o *OwnershipWatchTestSuite) waitForOwnershipVisibility(
	resourceCase watchResourceCase,
	adminClient *rancher.Client,
	subscriberClient *rancher.Client,
	ownedName string,
	foreignName string,
) string {
	converged := assert.EventuallyWithT(o.T(), func(collect *assert.CollectT) {
		adminNames, _, err := listSteveResourceNames(adminClient, resourceCase.schemaID)
		if !assert.NoError(collect, err, "Admin failed to list %s", resourceCase.name) {
			return
		}
		assert.Contains(collect, adminNames, ownedName, "Admin did not see the subscriber-owned %s", resourceCase.name)
		assert.Contains(collect, adminNames, foreignName, "Admin did not see the foreign-owned %s", resourceCase.name)

		subscriberNames, _, err := listSteveResourceNames(subscriberClient, resourceCase.schemaID)
		if !assert.NoError(collect, err, "Subscriber failed to list %s", resourceCase.name) {
			return
		}
		assert.Contains(collect, subscriberNames, ownedName, "Subscriber did not see its own %s", resourceCase.name)
		assert.NotContains(collect, subscriberNames, foreignName, "Subscriber listed a foreign-owned %s", resourceCase.name)
	}, defaults.OneMinuteTimeout, defaults.FiveSecondTimeout, "%s ownership visibility did not converge", resourceCase.name)
	require.True(o.T(), converged)

	_, revision, err := listSteveResourceNames(adminClient, resourceCase.schemaID)
	require.NoError(o.T(), err)
	return revision
}

func listSteveResourceNames(client *rancher.Client, schemaID string) ([]string, string, error) {
	collection, err := client.Steve.SteveType(schemaID).List(nil)
	if err != nil {
		return nil, "", err
	}

	names := collection.Names()
	revision := collection.Revision
	seenNextURLs := map[string]struct{}{}
	for collection.Pagination != nil && collection.Pagination.Next != "" {
		nextURL := collection.Pagination.Next
		if _, seen := seenNextURLs[nextURL]; seen {
			return nil, "", fmt.Errorf("Steve %q pagination repeated next URL %q", schemaID, nextURL)
		}
		seenNextURLs[nextURL] = struct{}{}

		nextCollection := &steveV1.SteveCollection{}
		if err := client.Steve.Ops.DoNext(nextURL, nextCollection); err != nil {
			return nil, "", fmt.Errorf("failed to get next Steve %q page: %w", schemaID, err)
		}
		names = append(names, nextCollection.Names()...)
		collection = nextCollection
	}

	return names, revision, nil
}

func openSteveSubscription(client *rancher.Client, resourceType, resourceVersion string) (*steveSubscription, error) {
	endpoint, err := url.Parse(client.Steve.Opts.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Steve URL %q: %w", client.Steve.Opts.URL, err)
	}

	switch endpoint.Scheme {
	case "https":
		endpoint.Scheme = "wss"
	case "http":
		endpoint.Scheme = "ws"
	default:
		return nil, fmt.Errorf("unsupported Steve URL scheme %q", endpoint.Scheme)
	}
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/") + "/subscribe"
	endpoint.RawQuery = ""
	endpoint.Fragment = ""

	conn, response, err := client.Steve.Websocket(endpoint.String(), nil)
	if err != nil {
		status := ""
		if response != nil {
			status = response.Status
			if response.Body != nil {
				_ = response.Body.Close()
			}
		}
		return nil, fmt.Errorf("failed to open Steve subscription for %q (status %q): %w", resourceType, status, err)
	}

	payload := map[string]string{"resourceType": resourceType}
	if resourceVersion != "" {
		payload["resourceVersion"] = resourceVersion
	}
	if err := conn.WriteJSON(payload); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to subscribe to %q: %w", resourceType, err)
	}

	events := make(chan steveSubscribeEvent, 256)
	readErrors := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(events)
		for {
			var event steveSubscribeEvent
			if err := conn.ReadJSON(&event); err != nil {
				select {
				case readErrors <- err:
				case <-done:
				}
				return
			}
			select {
			case events <- event:
			case <-done:
				return
			}
		}
	}()

	var closeOnce sync.Once
	return &steveSubscription{
		events:     events,
		readErrors: readErrors,
		close: func() {
			closeOnce.Do(func() {
				close(done)
				_ = conn.Close()
			})
		},
	}, nil
}

func waitForSubscriptionStart(t *testing.T, subscription *steveSubscription, resourceType string) {
	t.Helper()

	timer := time.NewTimer(subscriptionStartTimeout)
	defer timer.Stop()

	for {
		select {
		case event, ok := <-subscription.events:
			if !ok {
				failForClosedSubscription(t, subscription, "waiting for resource.start")
				return
			}
			if event.Name == "resource.error" {
				require.FailNow(t, "Steve subscription returned resource.error", "resource=%q error=%q", resourceType, event.Data.Error)
			}
			if event.Name == "resource.start" && event.ResourceType == resourceType {
				return
			}
		case err := <-subscription.readErrors:
			require.NoError(t, err, "Steve subscription closed while waiting for resource.start for %q", resourceType)
		case <-timer.C:
			require.FailNow(t, "Timed out waiting for Steve subscription to start", "resource=%q", resourceType)
		}
	}
}

func assertSubscriptionEvents(
	t *testing.T,
	subscription *steveSubscription,
	acceptedEventNames []string,
	requiredObjectNames []string,
	forbiddenObjectNames []string,
	settleTime time.Duration,
) {
	t.Helper()

	accepted := stringSet(acceptedEventNames)
	required := stringSet(requiredObjectNames)
	forbidden := stringSet(forbiddenObjectNames)
	seenRequired := map[string]struct{}{}

	deadline := time.NewTimer(watchEventTimeout)
	defer deadline.Stop()

	var settle <-chan time.Time
	var settleTimer *time.Timer
	defer func() {
		if settleTimer != nil {
			settleTimer.Stop()
		}
	}()

	for {
		if len(seenRequired) == len(required) && settle == nil {
			if settleTime == 0 {
				return
			}
			settleTimer = time.NewTimer(settleTime)
			settle = settleTimer.C
		}

		select {
		case event, ok := <-subscription.events:
			if !ok {
				failForClosedSubscription(t, subscription, "waiting for resource events")
				return
			}
			if event.Name == "resource.error" {
				require.FailNow(t, "Steve subscription returned resource.error", "error=%q", event.Data.Error)
			}
			if !isResourceMutationEvent(event.Name) {
				continue
			}

			objectName := event.objectName()
			if _, found := forbidden[objectName]; found {
				require.FailNow(t, "Steve subscription leaked a foreign-owned resource event", "event=%q object=%q", event.Name, objectName)
			}
			if _, acceptedEvent := accepted[event.Name]; !acceptedEvent {
				continue
			}
			if _, wanted := required[objectName]; wanted {
				seenRequired[objectName] = struct{}{}
			}
		case err := <-subscription.readErrors:
			require.NoError(t, err, "Steve subscription closed while waiting for resource events")
		case <-settle:
			return
		case <-deadline.C:
			missing := []string{}
			for name := range required {
				if _, seen := seenRequired[name]; !seen {
					missing = append(missing, name)
				}
			}
			require.FailNow(t, "Timed out waiting for Steve resource events", "accepted events=%v missing objects=%v", acceptedEventNames, missing)
		}
	}
}

func failForClosedSubscription(t *testing.T, subscription *steveSubscription, operation string) {
	t.Helper()
	select {
	case err := <-subscription.readErrors:
		require.NoError(t, err, "Steve subscription closed while %s", operation)
	default:
		require.FailNow(t, "Steve subscription closed unexpectedly", operation)
	}
}

func isResourceMutationEvent(name string) bool {
	switch name {
	case "resource.create", "resource.change", "resource.remove":
		return true
	default:
		return false
	}
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func newKubeconfigWatchCase(cluster *management.Cluster) watchResourceCase {
	return watchResourceCase{
		name:     "kubeconfigs",
		schemaID: kubeconfigSchemaID,
		create: func(client *rancher.Client) (string, error) {
			created, err := kubeconfigactions.CreateKubeconfig(client, []string{cluster.ID}, "", nil)
			if err != nil {
				return "", err
			}
			return created.Name, nil
		},
		update: func(client *rancher.Client, name string) error {
			kubeconfig, err := extkubeconfigs.GetKubeconfigByName(client, name)
			if err != nil {
				return err
			}
			if kubeconfig.Labels == nil {
				kubeconfig.Labels = map[string]string{}
			}
			kubeconfig.Labels[watchUpdateLabel] = "true"
			_, err = extkubeconfigs.UpdateKubeconfig(client, kubeconfig)
			return err
		},
		delete: func(client *rancher.Client, name string) error {
			return extkubeconfigs.DeleteKubeconfig(client, name, true)
		},
	}
}

func newTokenWatchCase() watchResourceCase {
	return watchResourceCase{
		name:     "tokens",
		schemaID: tokenSchemaID,
		create: func(client *rancher.Client) (string, error) {
			created, err := tokenactions.CreateExtToken(client, 0)
			if err != nil {
				return "", err
			}
			return created.Name, nil
		},
		update: func(client *rancher.Client, name string) error {
			token, err := exttokens.GetExtTokenByName(client, name)
			if err != nil {
				return err
			}
			if token.Labels == nil {
				token.Labels = map[string]string{}
			}
			token.Labels[watchUpdateLabel] = "true"
			_, err = exttokens.UpdateExtToken(client, token)
			return err
		},
		delete: func(client *rancher.Client, name string) error {
			return exttokens.DeleteExtToken(client, name, true)
		},
	}
}

func TestOwnershipWatchTestSuite(t *testing.T) {
	suite.Run(t, new(OwnershipWatchTestSuite))
}
