package etcdsnapshot

import (
	"time"

	apisV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/sirupsen/logrus"
)

const (
	InitialIngress  = "ingress-before-restore"
	InitialWorkload = "wload-before-restore"

	all               = "all"
	containerName     = "nginx"
	isCattleLabeled   = true
	ingressPath       = "/index.html"
	kubernetesVersion = "kubernetesVersion"
	port              = "port"
	postWorkload      = "wload-after-backup"
	serviceAppendName = "service-"
	s3StorageType     = "s3"
	s3SchemePrefix    = "s3://"
	storageAnnotation = "etcdsnapshot.rke.io/storage"
)

// CreateAndValidateSnapshotV2Prov is a helper that takes a snapshot of a given v2prov cluster and validates is resources after the snapshot
func CreateAndValidateSnapshotV2Prov(client *rancher.Client, clusterName, clusterID string, etcdRestore *Config) (*apisV1.Cluster, string, error) {
	createdSnapshots, err := CreateRKE2K3SSnapshot(client, clusterName)
	if err != nil {
		return nil, "", err
	}

	selectedSnapshot := createdSnapshots[0]
	snapshotToRestore := selectedSnapshot.Name
	createdSnapshotIDs := []string{}

	for _, snapshot := range createdSnapshots {
		if CheckS3SnapshotLocation(snapshot) {
			selectedSnapshot = snapshot
			snapshotToRestore = snapshot.Name
		}

		createdSnapshotIDs = append(createdSnapshotIDs, snapshot.ID)
	}

	err = VerifyV2ProvSnapshots(client, clusterName, createdSnapshotIDs)
	if err != nil {
		return nil, "", err
	}

	cluster, _, err := clusters.GetProvisioningClusterByName(client, clusterName, namespaces.FleetDefault)
	if err != nil {
		return nil, "", err
	}

	if etcdRestore.SnapshotRestore == kubernetesVersion || etcdRestore.SnapshotRestore == all {
		err = upgradeClusterAndSnapshotSettings(client, clusterName, clusterID, etcdRestore)
		if err != nil {
			return nil, "", err
		}
	}

	return cluster, snapshotToRestore, err
}

// RestoreAndValidateSnapshotV2Prov restores a given snapshot for a v2prov cluster and validates its resources
// after the restore against the original cluster object
func RestoreAndValidateSnapshotV2Prov(client *rancher.Client, snapshot string, etcdRestore *Config, cluster *apisV1.Cluster,
	clusterID string) (*apisV1.Cluster, error) {
	clusterObject, _, err := clusters.GetProvisioningClusterByName(client, cluster.Name, namespaces.FleetDefault)
	if err != nil {
		return nil, err
	}

	// Give the option to restore the same snapshot multiple times. By default, it is set to 1.
	for i := 0; i < etcdRestore.RecurringRestores; i++ {
		if clusterObject.Spec.RKEConfig != nil {
			if clusterObject.Spec.RKEConfig.ETCDSnapshotRestore == nil {
				clusterObject.Spec.RKEConfig.ETCDSnapshotRestore = &rkev1.ETCDSnapshotRestore{
					Generation: 1,
				}
			} else {
				clusterObject.Spec.RKEConfig.ETCDSnapshotRestore = &rkev1.ETCDSnapshotRestore{
					Generation: clusterObject.Spec.RKEConfig.ETCDSnapshotRestore.Generation + 1,
				}
			}
		} else {
			clusterObject.Spec.RKEConfig = &apisV1.RKEConfig{
				ETCDSnapshotRestore: &rkev1.ETCDSnapshotRestore{
					Generation: 1,
				},
			}
		}

		err = VerifySnapshotReadyForRestore(client, clusterObject.Name, snapshot)
		if err != nil {
			return nil, err
		}

		snapshotRestore := &rkev1.ETCDSnapshotRestore{
			Generation:       clusterObject.Spec.RKEConfig.ETCDSnapshotRestore.Generation,
			Name:             snapshot,
			RestoreRKEConfig: etcdRestore.SnapshotRestore,
		}

		err = RestoreRKE2K3SSnapshot(client, snapshotRestore, clusterObject.Name)
		if err != nil {
			return nil, err
		}

		cluster, _, err = clusters.GetProvisioningClusterByName(client, clusterObject.Name, namespaces.FleetDefault)
		if err != nil {
			return nil, err
		}
	}

	return cluster, nil
}

// This function waits for retentionlimit+1 automatic snapshots to be taken before verifying that the retention limit is respected
func CreateSnapshotsUntilRetentionLimit(client *rancher.Client, clusterName string, retentionLimit int, timeBetweenSnapshots int) error {
	v1ClusterID, err := clusters.GetV1ProvisioningClusterByName(client, clusterName)

	if v1ClusterID == "" {
		v3ClusterID, err := clusters.GetClusterIDByName(client, clusterName)
		if err != nil {
			return err
		}

		v1ClusterID = "fleet-default/" + v3ClusterID
	}
	if err != nil {
		return err
	}

	fleetCluster, err := client.Steve.SteveType(stevetypes.FleetCluster).ByID(v1ClusterID)
	if err != nil {
		return err
	}

	provider := fleetCluster.ObjectMeta.Labels["provider.cattle.io"]
	if provider != "rke" {
		sleepNum := (retentionLimit + 1) * timeBetweenSnapshots
		logrus.Infof("Waiting %v minutes for %v automatic snapshots to be taken", sleepNum, (retentionLimit + 1))
		time.Sleep(time.Duration(sleepNum)*time.Minute + time.Minute*5)

		err := RKE2K3SRetentionLimitCheck(client, clusterName)
		if err != nil {
			return err
		}
	}

	return nil
}
