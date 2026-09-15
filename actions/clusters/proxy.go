package clusters

import (
	"context"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// ProxyDownstreamWithRetry re-logins and retries downstream Steve proxy creation while a cluster is becoming ready.
func ProxyDownstreamWithRetry(client *rancher.Client, clusterID string) (*v1.Client, error) {
	var downstreamClient *v1.Client
	var lastErr error

	pollErr := kwait.PollUntilContextTimeout(context.Background(), 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		refreshedClient, err := client.ReLogin()
		if err != nil {
			lastErr = err
			return false, nil
		}

		downstreamClient, err = refreshedClient.Steve.ProxyDownstream(clusterID)
		if err != nil {
			lastErr = err
			return false, nil
		}

		return true, nil
	})
	if pollErr != nil {
		if lastErr != nil {
			return nil, lastErr
		}

		return nil, pollErr
	}

	return downstreamClient, nil
}
