package etcdsnapshot

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	apisV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	rancherv1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/etcdsnapshot"
	"github.com/rancher/tests/actions/kubeapi/nodes"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	SnapshotSteveResourceType = "rke.cattle.io.etcdsnapshot"
	SnapshotClusterNameLabel  = "rke.cattle.io/cluster-name"
)

// CreateRKE2K3SSnapshot is a helper function to create a snapshot on an RKE2 or k3s cluster.
// returns the list of snapshots and an error, if any.
func CreateRKE2K3SSnapshot(client *rancher.Client, clusterName string) ([]rancherv1.SteveAPIObject, error) {
	clusterObject, clusterSteveObject, err := clusters.GetProvisioningClusterByName(client, clusterName, namespaces.FleetDefault)
	if err != nil {
		return nil, err
	}

	if clusterObject.Spec.RKEConfig != nil {
		if clusterObject.Spec.RKEConfig.ETCDSnapshotCreate == nil {
			clusterObject.Spec.RKEConfig.ETCDSnapshotCreate = &rkev1.ETCDSnapshotCreate{
				Generation: 1,
			}
		} else {
			clusterObject.Spec.RKEConfig.ETCDSnapshotCreate = &rkev1.ETCDSnapshotCreate{
				Generation: clusterObject.Spec.RKEConfig.ETCDSnapshotCreate.Generation + 1,
			}
		}
	} else {
		clusterObject.Spec.RKEConfig = &apisV1.RKEConfig{
			ETCDSnapshotCreate: &rkev1.ETCDSnapshotCreate{
				Generation: 1,
			},
		}
	}

	updatedCluster, err := client.Steve.SteveType(clusters.ProvisioningSteveResourceType).Update(clusterSteveObject, clusterObject)
	if err != nil {
		return nil, err
	}

	updateTimestamp := time.Now()
	err = clusters.WaitOnClusterAfterSnapshot(client, updatedCluster.ID)
	if err != nil {
		return nil, err
	}

	var snapshots []rancherv1.SteveAPIObject

	err = kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.FiveMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		query, err := url.ParseQuery(fmt.Sprintf("labelSelector=%s=%s", SnapshotClusterNameLabel, clusterName))
		if err != nil {
			return false, nil
		}

		snapshotSteveObjList, err := client.Steve.SteveType(SnapshotSteveResourceType).List(query)
		if err != nil {
			return false, nil
		}

		if len(snapshotSteveObjList.Data) == 0 {
			return false, nil
		}

		snapshots = []rancherv1.SteveAPIObject{}
		for _, snapshot := range snapshotSteveObjList.Data {
			if snapshot.Labels[SnapshotClusterNameLabel] != clusterName {
				continue
			}

			// snapshot time doesn't include nanoseconds, but time.Now() does. Rounding up by 1 Second.
			if snapshot.CreationTimestamp.Time.Add(time.Duration(time.Second)).Compare(updateTimestamp) > -1 {
				snapshots = append(snapshots, snapshot)
			}
		}

		if len(snapshots) == 0 {
			return false, nil
		}

		return true, nil
	})

	logrus.Debugf("Successfully created snapshot %s for cluster: %s", snapshots[0].Name, clusterName)

	return snapshots, err
}

// RestoreRKE2K3SSnapshot is a helper function to restore a snapshot on an RKE2 or k3s cluster. Returns error if any.
func RestoreRKE2K3SSnapshot(client *rancher.Client, snapshotRestore *rkev1.ETCDSnapshotRestore, clusterName string) error {
	clusterObject, existingSteveAPIObject, err := clusters.GetProvisioningClusterByName(client, clusterName, namespaces.FleetDefault)
	if err != nil {
		return err
	}

	if clusterObject.Spec.RKEConfig == nil {
		clusterObject.Spec.RKEConfig = &apisV1.RKEConfig{}
	}

	clusterObject.Spec.RKEConfig.ETCDSnapshotRestore = snapshotRestore

	updatedCluster, err := client.Steve.SteveType(clusters.ProvisioningSteveResourceType).Update(existingSteveAPIObject, clusterObject)
	if err != nil {
		return err
	}

	err = clusters.WaitOnClusterAfterSnapshot(client, updatedCluster.ID)
	if err != nil {
		return err
	}

	logrus.Debugf("Successfully restored snapshot %s for cluster: %s", snapshotRestore.Name, clusterName)

	return nil
}

// RKE2K3SRetentionLimitCheck is a check that validates that the number of automatic snapshots
// on the cluster is under the retention limit.
func RKE2K3SRetentionLimitCheck(client *rancher.Client, clusterName string) error {
	v1ClusterID, err := clusters.GetV1ProvisioningClusterByName(client, clusterName)
	if err != nil {
		return err
	}

	clusterObj, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(v1ClusterID)
	if err != nil {
		return err
	}

	spec := apisV1.ClusterSpec{}
	err = rancherv1.ConvertToK8sType(clusterObj.Spec, &spec)
	if err != nil {
		return err
	}

	etcdConfig := spec.RKEConfig.ETCD
	retentionLimit := etcdConfig.SnapshotRetention

	isS3 := false
	if etcdConfig.S3 != nil {
		isS3 = true
	}

	query, err := url.ParseQuery(fmt.Sprintf("labelSelector=%s=%s", etcdsnapshot.SnapshotClusterNameLabel, clusterName))
	if err != nil {
		return err
	}

	snapshotSteveObjList, err := client.Steve.SteveType(etcdsnapshot.SnapshotSteveResourceType).List(query)
	if err != nil {
		return err
	}

	automaticSnapshots := []rancherv1.SteveAPIObject{}

	for _, snapshot := range snapshotSteveObjList.Data {
		if strings.Contains(snapshot.Annotations["etcdsnapshot.rke.io/snapshot-file-name"], "etcd-snapshot") {
			automaticSnapshots = append(automaticSnapshots, snapshot)
		}
	}

	downstreamClusterID, err := clusters.GetClusterIDByName(client, clusterName)
	if err != nil {
		return err
	}

	listOpts := metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/etcd=true"}
	etcdNodes, err := nodes.GetNodes(client, downstreamClusterID, listOpts)
	if err != nil {
		return err
	}

	expectedSnapshotsNum := int(retentionLimit) * len(etcdNodes)
	if isS3 {
		expectedSnapshotsNum = expectedSnapshotsNum * 2
	}

	if len(automaticSnapshots) > expectedSnapshotsNum {
		msg := fmt.Sprintf(
			"retention limit exceeded: expected %d snapshots, found %d snapshots",
			expectedSnapshotsNum, len(automaticSnapshots))

		return errors.New(msg)
	}

	logrus.Infof("Snapshot retention limit respected, Snapshots Expected: %v Snapshots Found: %v",
		expectedSnapshotsNum, len(automaticSnapshots))

	return nil
}
