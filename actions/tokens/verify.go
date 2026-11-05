package tokens

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// VerifyLoginTokenRecordsLastUsedAt fails unless a use of the login token after a minute is written
func VerifyLoginTokenRecordsLastUsedAt(client, loginClient *rancher.Client) error {
	credential, err := NewCredentialFromBearer(loginClient.Management.APIBaseClient.Opts.TokenKey)
	if err != nil {
		return err
	}

	planted, err := SetLastUsedAtAndWait(client, credential.Target(), time.Now().Add(-defaults.TwoMinuteTimeout))
	if err != nil {
		return err
	}

	request, err := NewRequester(client).Send(credential, ProxyPath)
	if err != nil {
		return err
	}

	if request.StatusCode != http.StatusOK {
		return fmt.Errorf("Rancher answered %d to a request made with the login token", request.StatusCode)
	}

	changed, err := WaitForLastUsedAtChange(client, credential.Target(), planted)
	if err != nil {
		return err
	}

	return VerifyLastUsedAtNear(changed, request.At)
}

// VerifyAllSucceeded fails unless there is at least one request and every request was answered with a 200
func VerifyAllSucceeded(requests []Request) error {
	if len(requests) == 0 {
		return errors.New("no request was sent")
	}

	for _, request := range requests {
		if request.StatusCode != http.StatusOK {
			return fmt.Errorf("a request sent at %s was answered %d rather than 200", request.At.UTC().Format(time.RFC3339), request.StatusCode)
		}
	}

	return nil
}

// VerifyWritesPerMinute fails unless the writes seen over the span number about one a minute
func VerifyWritesPerMinute(writes []time.Time, span time.Duration) error {
	minutes := int(span / defaults.OneMinuteTimeout)
	minimum := max(minutes-1, 1)
	maximum := int((span+defaults.OneSecondTimeout)/defaults.OneMinuteTimeout) + 1

	if len(writes) < minimum || len(writes) > maximum {
		return fmt.Errorf("saw %d writes of lastUsedAt over %s, where a write a minute allows %d to %d", len(writes), span.Round(time.Second), minimum, maximum)
	}

	return nil
}

// VerifyWritesMatchRequests fails unless every write holds the exact second of a request
func VerifyWritesMatchRequests(writes []time.Time, requests []Request) error {
	for _, write := range writes {
		if write.Nanosecond() != 0 {
			return fmt.Errorf("the lastUsedAt %s carries a fraction of a second", write.Format(time.RFC3339Nano))
		}

		if !slices.ContainsFunc(requests, func(request Request) bool { return write.Sub(request.At).Abs() <= 2*defaults.OneSecondTimeout }) {
			return fmt.Errorf("the lastUsedAt %s is within %s of none of the %d requests", write.Format(time.RFC3339), 2*defaults.OneSecondTimeout, len(requests))
		}
	}

	return nil
}

// VerifyEveryRequestWasWritten fails unless each request moved lastUsedAt to its own time, in order
func VerifyEveryRequestWasWritten(writes []time.Time, requests []Request) error {
	if len(requests) == 0 || len(writes) != len(requests) {
		return fmt.Errorf("saw %d writes of lastUsedAt for %d requests that were each more than a minute after the last", len(writes), len(requests))
	}

	for i, write := range writes {
		if difference := write.Sub(requests[i].At).Abs(); difference > defaults.FiveSecondTimeout {
			return fmt.Errorf("write %d holds %s, %s from its request", i+1, write.Format(time.RFC3339), difference.Round(time.Second))
		}
	}

	return nil
}

// VerifyWritesNeverMoveBack fails when a write holds a time earlier than the one before it by more than the tolerance
func VerifyWritesNeverMoveBack(writes []time.Time) error {
	for i := 1; i < len(writes); i++ {
		if writes[i].Before(writes[i-1].Add(-defaults.FiveSecondTimeout)) {
			return fmt.Errorf("write %d moved lastUsedAt back from %s to %s", i+1, writes[i-1].Format(time.RFC3339), writes[i].Format(time.RFC3339))
		}
	}

	return nil
}

// VerifyLastUsedAtUnchanged polls every target's stored lastUsedAt at once for as long as a write is given to land, and returns one result per target that fails if the value ever moved from the one given
func VerifyLastUsedAtUnchanged(client *rancher.Client, targets []Target, before []*time.Time) []error {
	results := make([]error, len(targets))

	var group sync.WaitGroup

	for index, target := range targets {
		group.Add(1)

		go func() {
			defer group.Done()

			var moved *time.Time

			reads := 0

			var lastErr error

			pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.OneSecondTimeout, defaults.OneMinuteTimeout, true,
				func(context.Context) (bool, error) {
					current, err := GetLastUsedAt(client, target)
					if err != nil {
						lastErr = err

						return false, nil
					}

					reads++

					if !sameTime(current, before[index]) {
						moved = current

						return true, nil
					}

					return false, nil
				})

			switch {
			case pollErr == nil:
				results[index] = fmt.Errorf("%s token %s was written, its lastUsedAt moved from %v to %v", target.Category, target.Name, before[index], moved)
			case !kwait.Interrupted(pollErr):
				results[index] = pollErr
			case reads == 0:
				results[index] = fmt.Errorf("the lastUsedAt of %s token %s could not be read: %w", target.Category, target.Name, lastErr)
			}
		}()
	}

	group.Wait()

	return results
}

// VerifyLastUsedAtNear fails unless the stored lastUsedAt is the whole-second time of the request, give or take the tolerance
func VerifyLastUsedAtNear(stored *time.Time, at time.Time) error {
	if stored == nil {
		return fmt.Errorf("lastUsedAt is unset where it should hold %s", at.UTC().Format(time.RFC3339))
	}

	if difference := stored.Sub(at).Abs(); difference > defaults.FiveSecondTimeout {
		return fmt.Errorf("lastUsedAt holds %s, %s from the request made at %s",
			stored.UTC().Format(time.RFC3339), difference.Round(time.Second), at.UTC().Format(time.RFC3339))
	}

	return nil
}
