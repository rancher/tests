package clusters

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/sirupsen/logrus"
)

const (
	LabelWorker             = "labelSelector=node-role.kubernetes.io/worker=true"
	SmallerPoolMessageError = "Machine pool cluster size is smaller than expected pool size"
)

// VerifyNodePoolSize is a helper function that checks if the machine pool cluster size is greater than or equal to poolSize
func VerifyNodePoolSize(steveClient *steveV1.Client, labelSelector string, poolSize int) error {
	logrus.Info("Checking node pool")

	logrus.Infof("Getting the node using the label [%v]", labelSelector)
	query, err := url.ParseQuery(labelSelector)
	if err != nil {
		return err
	}

	nodeList, err := steveClient.SteveType("node").List(query)
	if err != nil {
		return err
	}

	if len(nodeList.Data) < poolSize {
		return errors.New(SmallerPoolMessageError)
	}

	return nil
}

// VerifyServiceAccountTokenSecret verifies if a serviceAccountTokenSecret exists or not in the cluster.
func VerifyServiceAccountTokenSecret(client *rancher.Client, clusterName string) error {
	clusterID, err := clusters.GetClusterIDByName(client, clusterName)
	if err != nil {
		return err
	}

	cluster, err := client.Management.Cluster.ByID(clusterID)
	if err != nil {
		return err
	}

	if cluster.ServiceAccountTokenSecret == "" {
		return fmt.Errorf("ServiceAccountTokenSecret does not exist on cluster (%s)", clusterName)
	}

	return nil
}
