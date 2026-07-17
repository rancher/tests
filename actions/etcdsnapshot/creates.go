package etcdsnapshot

import (
	"errors"
	"fmt"
	"time"

	apisV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	shepherdsnapshot "github.com/rancher/shepherd/extensions/etcdsnapshot"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/workloads/deployment"
	actionspods "github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

// CreateAndValidateSnapshotRestore is an e2e helper that determines the engine type of the cluster, then takes a snapshot, and finally restores the cluster to the original snapshot
func CreateAndValidateSnapshotRestore(client *rancher.Client, clusterName string, etcdRestore *Config, containerImage string) error {
	clusterID, err := clusters.GetClusterIDByName(client, clusterName)
	if err != nil {
		return err
	}

	steveclient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	podTemplate, deploymentTemplate, deploymentResp, serviceResp, ingressResp, err := createAndVerifyResources(client, clusterID, containerImage)
	if err != nil {
		return err
	}

	logrus.Debugf("Creating snapshot on cluster %s", clusterName)
	cluster, snapshotName, postDeploymentResp, postServiceResp, err := CreateAndValidateSnapshotV2Prov(client, podTemplate, deploymentTemplate, clusterName, clusterID, etcdRestore)
	if err != nil {
		return err
	}

	err = RestoreAndValidateSnapshotV2Prov(client, snapshotName, etcdRestore, cluster, clusterID)
	if err != nil {
		return err
	}

	_, err = steveclient.SteveType(stevetypes.Deployment).ByID(postDeploymentResp.ID)
	if err == nil {
		return errors.New("expecting cluster restore to remove resource")
	}

	_, err = steveclient.SteveType(stevetypes.Service).ByID(postServiceResp.ID)
	if err == nil {
		return errors.New("expecting cluster restore to remove resource")
	}

	logrus.Infof("Deleting created workloads...")
	err = steveclient.SteveType(stevetypes.Deployment).Delete(deploymentResp)
	if err != nil {
		return err
	}

	err = steveclient.SteveType(stevetypes.Service).Delete(serviceResp)
	if err != nil {
		return err
	}

	err = steveclient.SteveType(stevetypes.Ingress).Delete(ingressResp)
	if err != nil {
		return err
	}

	return err
}

// CreateAndValidateSnapshotV2Prov is a helper that takes a snapshot of a given v2prov cluster and validates is resources after the snapshot
func CreateAndValidateSnapshotV2Prov(client *rancher.Client, podTemplate *corev1.PodTemplateSpec, deployment *v1.Deployment, clusterName, clusterID string,
	etcdRestore *Config) (*apisV1.Cluster, string, *steveV1.SteveAPIObject, *steveV1.SteveAPIObject, error) {
	createdSnapshots, err := shepherdsnapshot.CreateRKE2K3SSnapshot(client, clusterName)
	if err != nil {
		return nil, "", nil, nil, err
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
		return nil, "", nil, nil, err
	}

	cluster, _, err := clusters.GetProvisioningClusterByName(client, clusterName, namespaces.FleetDefault)
	if err != nil {
		return nil, "", nil, nil, err
	}

	postDeploymentResp, postServiceResp, err := createPostBackupWorkloads(client, clusterID, *podTemplate, deployment)
	if err != nil {
		return nil, "", nil, nil, err
	}

	if etcdRestore.SnapshotRestore == kubernetesVersion || etcdRestore.SnapshotRestore == all {
		err = upgradeClusterAndSnapshotSettings(client, clusterName, clusterID, etcdRestore)
		if err != nil {
			return nil, "", nil, nil, err
		}
	}

	return cluster, snapshotToRestore, postDeploymentResp, postServiceResp, err
}

// RestoreAndValidateSnapshotV2Prov restores a given snapshot for a v2prov cluster and validates its resources
// after the restore against the original cluster object
func RestoreAndValidateSnapshotV2Prov(client *rancher.Client, snapshotID string, etcdRestore *Config, cluster *apisV1.Cluster, clusterID string) error {
	clusterObject, _, err := clusters.GetProvisioningClusterByName(client, cluster.Name, namespaces.FleetDefault)
	if err != nil {
		return err
	}

	// Give the option to restore the same snapshot multiple times. By default, it is set to 1.
	for i := 0; i < etcdRestore.RecurringRestores; i++ {
		generation := int(1)

		if clusterObject.Spec.RKEConfig.ETCDSnapshotRestore != nil {
			generation = clusterObject.Spec.RKEConfig.ETCDSnapshotRestore.Generation + 1
		}

		err = VerifySnapshotReadyForRestore(client, clusterObject.Name, snapshotID)
		if err != nil {
			return err
		}

		snapshotRKE2K3SRestore := &rkev1.ETCDSnapshotRestore{
			Name:             snapshotID,
			Generation:       generation,
			RestoreRKEConfig: etcdRestore.SnapshotRestore,
		}

		err := shepherdsnapshot.RestoreRKE2K3SSnapshot(client, snapshotRKE2K3SRestore, clusterObject.Name)
		if err != nil {
			return err
		}

		steveCluster, err := client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + clusterObject.Name)
		if err != nil {
			return err
		}

		logrus.Tracef("Waiting for cluster %s to finish upgrade", cluster.Name)
		err = provisioning.VerifyClusterReady(client, steveCluster)
		if err != nil {
			return err
		}

		logrus.Tracef("Verifying deployments on cluster %s", cluster.Name)
		err = deployment.VerifyClusterDeployments(client, steveCluster)
		if err != nil {
			return err
		}

		logrus.Tracef("Verifying pods on cluster %s", cluster.Name)
		err = actionspods.VerifyClusterPods(client, steveCluster)
		if err != nil {
			return err
		}

		clusterObject, _, err = clusters.GetProvisioningClusterByName(client, steveCluster.Name, namespaces.FleetDefault)
		if err != nil {
			return err
		}

		if cluster.Spec.KubernetesVersion != clusterObject.Spec.KubernetesVersion {
			return fmt.Errorf("K8s Version after upgrade %s does not match expected version %s after restore", clusterObject.Spec.KubernetesVersion, cluster.Spec.KubernetesVersion)
		}

		if etcdRestore.SnapshotRestore == all && etcdRestore.ControlPlaneConcurrencyValue != "" && etcdRestore.WorkerConcurrencyValue != "" {
			logrus.Infof("Control plane concurrency value is restored to: %s", clusterObject.Spec.RKEConfig.UpgradeStrategy.ControlPlaneConcurrency)
			logrus.Infof("Worker concurrency value is restored to: %s", clusterObject.Spec.RKEConfig.UpgradeStrategy.WorkerConcurrency)

			if cluster.Spec.RKEConfig.UpgradeStrategy.ControlPlaneConcurrency != clusterObject.Spec.RKEConfig.UpgradeStrategy.ControlPlaneConcurrency {
				return fmt.Errorf("controlPlaneConcurrency after restore %s does not match expected version %s", clusterObject.Spec.RKEConfig.UpgradeStrategy.ControlPlaneConcurrency, cluster.Spec.RKEConfig.UpgradeStrategy.ControlPlaneConcurrency)
			}

			if cluster.Spec.RKEConfig.UpgradeStrategy.WorkerConcurrency != clusterObject.Spec.RKEConfig.UpgradeStrategy.WorkerConcurrency {
				return fmt.Errorf("wokerConcurrency after restore %s does not match expected version %s", clusterObject.Spec.RKEConfig.UpgradeStrategy.WorkerConcurrency, cluster.Spec.RKEConfig.UpgradeStrategy.WorkerConcurrency)
			}
		}
	}

	return nil
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
