package charts

import (
	"context"
	"strings"
	"time"

	"github.com/rancher/shepherd/pkg/wait"
	"github.com/sirupsen/logrus"

	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	// DefaultWatchRetries is the number of times to retry watch operations that fail
	// with transient connection errors or timeout errors.
	DefaultWatchRetries = 5
	watchRetryDelay     = 5 * time.Second
)

func isRetryableWatchError(errStr string) bool {
	retryableErrors := []string{
		wait.WatchConnectionError, // "error with watch connection"
		wait.TimeoutError,         // "timeout waiting on condition"
		"context deadline exceeded",
	}
	for _, retryable := range retryableErrors {
		if strings.Contains(errStr, retryable) {
			return true
		}
	}
	return false
}

// RetryOnWatchError retries fn up to maxRetries times when the error
// indicates a transient watch failure (connection drop, timeout, or context deadline).
// Non-retryable errors are returned immediately.
func RetryOnWatchError(maxRetries int, fn func() error) error {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !isRetryableWatchError(lastErr.Error()) {
			return lastErr
		}
		logrus.Warnf("Watch error (attempt %d/%d): %v", i+1, maxRetries, lastErr)
		_ = kwait.PollUntilContextTimeout(context.TODO(), watchRetryDelay, watchRetryDelay+time.Second, false, func(context.Context) (bool, error) {
			return true, nil
		})
	}
	return lastErr
}
