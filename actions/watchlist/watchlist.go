package watchlist

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/meta"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
)

// WaitForWatchListEnd monitors a watch channel for the Bookmark event containing the initial-events-end annotation.
func WaitForWatchListEnd(ctx context.Context, watcher watch.Interface) error {
	defer watcher.Stop()

	// Use a 0 interval because the channel itself provides the 'waiting' mechanism
	return kwait.PollUntilContextCancel(ctx, 0, true, func(ctx context.Context) (bool, error) {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return false, fmt.Errorf("watch channel closed")
			}

			metaAccessor, err := meta.Accessor(event.Object)
			if err != nil {
				return false, fmt.Errorf("failed to access event metadata: %v", err)
			}

			log.Printf("Event: %-8s | Resource: %s/%s\n",
				event.Type,
				metaAccessor.GetNamespace(),
				metaAccessor.GetName(),
			)

			if event.Type == watch.Bookmark {
				metaAccessor, err := meta.Accessor(event.Object)
				if err != nil {
					return false, err
				}

				annotations := metaAccessor.GetAnnotations()
				if annotations["k8s.io/initial-events-end"] == "true" {
					log.Printf("Received WatchList completion signal successfully")
					return true, nil
				}
				return false, fmt.Errorf("bookmark received but annotation was stripped")
			}
			return false, nil
		}
	})
}
