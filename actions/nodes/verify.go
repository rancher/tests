package nodes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rancher/norman/types"
	"github.com/rancher/shepherd/clients/rancher"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// VerifyNodesReady polls until every node in the cluster is ready or the timeout is reached.
func VerifyNodesReady(client *rancher.Client, clusterID string, timeout time.Duration) error {
	var lastErr error

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err := kwait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, false,
		func(ctx context.Context) (bool, error) {
			nodes, err := client.Management.Node.List(&types.ListOpts{
				Filters: map[string]any{
					"clusterId": clusterID,
				},
			})
			if err != nil {
				lastErr = fmt.Errorf("listing nodes: %w", err)
				return false, nil
			}

			if len(nodes.Data) == 0 {
				lastErr = fmt.Errorf("no nodes found for cluster")
				return false, nil
			}

			var notReady []string

			for _, n := range nodes.Data {
				isReady := false
				reason := "no Ready condition"

				for _, cond := range n.Conditions {
					if cond.Type != "Ready" {
						continue
					}

					if cond.Status == "True" {
						isReady = true
					} else {
						reason = fmt.Sprintf("Ready=%s: %s %s", cond.Status, cond.Reason, cond.Message)
					}

					break
				}

				if !isReady {
					notReady = append(notReady, fmt.Sprintf("%s (state %q): %s", n.NodeName, n.State, reason))
				}
			}

			if len(notReady) > 0 {
				lastErr = fmt.Errorf("%d of %d nodes not ready: %s", len(notReady), len(nodes.Data), strings.Join(notReady, "; "))
				return false, nil
			}

			return true, nil
		})
	if err != nil && lastErr != nil {
		return fmt.Errorf("%w: last observed: %v", err, lastErr)
	}

	return err
}
