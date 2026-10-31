package charts

import (
	"context"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// UpdateClusterRepo refreshes the indicated cluster, ensuring that the newest versions are available.
// Doesn't return and error and logs a warning if updating the cluster fails.
func UpdateClusterRepo(adminClient *rancher.Client, repoName string) {
	err := updateClusterRepo(adminClient.Catalog, repoName)

	if err != nil {
		logrus.Warnf("Failed to refresh cluster repo %s: %s", repoName, err.Error())
	} else {
		logrus.Infof("Successfully refreshed cluster repo %s", repoName)
	}
}

func updateClusterRepo(catalogClient *catalog.Client, repoName string) error {
	clusterRepo, err := catalogClient.ClusterRepos().Get(context.Background(), repoName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	forceUpdate := metav1.Now()
	clusterRepo.Spec.ForceUpdate = &forceUpdate

	_, err = catalogClient.ClusterRepos().Update(context.Background(), clusterRepo, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	return kwait.PollUntilContextTimeout(context.Background(), 500*time.Millisecond, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		repo, err := catalogClient.ClusterRepos().Get(ctx, repoName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		return !repo.Status.DownloadTime.Before(&forceUpdate), nil
	})
}
