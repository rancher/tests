package charts

import (
	"strings"
	"time"

	"github.com/rancher/shepherd/pkg/wait"
	"github.com/sirupsen/logrus"
)

const (
	// DefaultWatchRetries is the number of times to retry watch operations that fail
	// with transient connection errors.
	DefaultWatchRetries = 5
	watchRetryDelay     = 5 * time.Second
)

// RetryOnWatchError retries fn up to maxRetries times, but only when the error
// indicates a transient watch connection failure (watch.WatchConnectionError).
// Non-watch errors are returned immediately.
func RetryOnWatchError(maxRetries int, fn func() error) error {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !strings.Contains(lastErr.Error(), wait.WatchConnectionError) {
			return lastErr
		}
		logrus.Warnf("Watch connection error (attempt %d/%d): %v", i+1, maxRetries, lastErr)
		time.Sleep(watchRetryDelay)
	}
	return lastErr
}
