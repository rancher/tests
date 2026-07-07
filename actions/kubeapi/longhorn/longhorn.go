package longhorn

import (
	"context"
	"fmt"

	longhorn "github.com/longhorn/longhorn-manager/k8s/pkg/apis/longhorn/v1beta2"
	longhornTypes "github.com/longhorn/longhorn-manager/types"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/kubeapi/cluster"
	"github.com/rancher/shepherd/extensions/kubeapi/secrets"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/charts"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
)

var (
	LonghornStorageClasses                = []string{"longhorn", "longhorn-static"}
	LonghornStorageClassProvisioner       = "driver.longhorn.io"
	LonghornBackupTargetSetting           = "backup-target"
	LonghornBackupTargetCredentialSetting = "backup-target-credential-secret"
)

// GetNumberOfLonghornVolumes gets the number of existing Longhorn volumes.
func GetNumberOfLonghornVolumes(client *rancher.Client, clusterID string) (int, error) {
	wrangler, err := cluster.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return -1, err
	}

	volumeController, err := wrangler.ControllerFactory.ForKind(schema.GroupVersionKind{
		Group:   longhorn.SchemeGroupVersion.Group,
		Version: longhorn.SchemeGroupVersion.Version,
		Kind:    longhornTypes.LonghornKindVolume,
	})
	if err != nil {
		return -1, err
	}

	var volumeList longhorn.VolumeList
	err = volumeController.Client().List(context.Background(), "", &volumeList, metav1.ListOptions{})
	return len(volumeList.Items), err
}

// CreateS3LonghornBackupTarget creates a Longhorn Backup Target using S3.
func CreateS3LonghornBackupTarget(client *rancher.Client, clusterID string, awsCreds cloudcredentials.AmazonEC2CredentialConfig, region, bucketName string) (*longhorn.BackupTarget, error) {
	secretName := namegenerator.AppendRandomString("aws-creds")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: charts.LonghornNamespace,
		},
		StringData: map[string]string{
			"AWS_ACCESS_KEY_ID":     awsCreds.AccessKey,
			"AWS_SECRET_ACCESS_KEY": awsCreds.SecretKey,
			"AWS_ENDPOINTS":         "", // Leaving empty for AWS S3
		},
	}

	_, err := secrets.CreateSecretWithTemplate(client, clusterID, secret)
	if err != nil {
		return nil, err
	}

	wrangler, err := cluster.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	backupTargetController, err := wrangler.ControllerFactory.ForKind(schema.GroupVersionKind{
		Group:   longhorn.SchemeGroupVersion.Group,
		Version: longhorn.SchemeGroupVersion.Version,
		Kind:    longhornTypes.LonghornKindBackupTarget,
	})
	if err != nil {
		return nil, err
	}

	// Patch "backup-target" setting to use S3.
	targetName := namegenerator.AppendRandomString("s3-backup")
	backupTarget := &longhorn.BackupTarget{
		TypeMeta: metav1.TypeMeta{
			Kind:       longhornTypes.LonghornKindBackupTarget,
			APIVersion: longhorn.SchemeGroupVersion.Group + "/" + longhorn.SchemeGroupVersion.Version,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      targetName,
			Namespace: charts.LonghornNamespace,
		},
		Spec: longhorn.BackupTargetSpec{
			BackupTargetURL:  fmt.Sprintf("s3://%s@%s/", bucketName, region),
			CredentialSecret: secretName,
		},
	}

	err = backupTargetController.Client().Create(context.Background(), charts.LonghornNamespace, backupTarget, backupTarget, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	client.Session.RegisterCleanupFunc(func() error {
		return backupTargetController.Client().Delete(context.Background(), charts.LonghornNamespace, targetName, metav1.DeleteOptions{})
	})

	return backupTarget, nil
}

// CreateLonghornVolumeBackup creates a snapshot for a Longhorn Volume and then creates a backup from this snapshot.
// This waits until the backuṕ is succesfully created and returns the URL for that backup in S3.
func CreateLonghornVolumeBackup(client *rancher.Client, clusterID string, namespace string, volumeName string, backupTargetName string) (*longhorn.Backup, error) {
	wrangler, err := cluster.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	volumeController, err := wrangler.ControllerFactory.ForKind(schema.GroupVersionKind{
		Group:   longhorn.SchemeGroupVersion.Group,
		Version: longhorn.SchemeGroupVersion.Version,
		Kind:    longhornTypes.LonghornKindVolume,
	})
	if err != nil {
		return nil, err
	}

	var volume longhorn.Volume
	err = volumeController.Client().Get(context.Background(), namespace, volumeName, &volume, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("Error getting volume %s on namespace %s: %w", volumeName, namespace, err)
	}

	volume.Spec.BackupTargetName = backupTargetName
	err = volumeController.Client().Update(context.Background(), namespace, &volume, &volume, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("Error updating backup target on volume %s on namespace %s: %w", volumeName, namespace, err)
	}

	snapshotController, err := wrangler.ControllerFactory.ForKind(schema.GroupVersionKind{
		Group:   longhorn.SchemeGroupVersion.Group,
		Version: longhorn.SchemeGroupVersion.Version,
		Kind:    longhornTypes.LonghornKindSnapshot,
	})
	if err != nil {
		return nil, err
	}

	snapshot := longhorn.Snapshot{
		TypeMeta: metav1.TypeMeta{
			Kind:       longhornTypes.LonghornKindSnapshot,
			APIVersion: longhorn.SchemeGroupVersion.Group + "/" + longhorn.SchemeGroupVersion.Version,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namegenerator.AppendRandomString("backup-test"),
			Namespace: namespace,
		},
		Spec: longhorn.SnapshotSpec{
			Volume:         volumeName,
			CreateSnapshot: true,
		},
	}
	err = snapshotController.Client().Create(context.Background(), namespace, &snapshot, nil, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("Error creating snapshot for volume %s on namespace %s: %w", volumeName, namespace, err)
	}

	client.Session.RegisterCleanupFunc(func() error {
		return snapshotController.Client().Delete(context.Background(), namespace, snapshot.Name, metav1.DeleteOptions{})
	})

	backupController, err := wrangler.ControllerFactory.ForKind(schema.GroupVersionKind{
		Group:   longhorn.SchemeGroupVersion.Group,
		Version: longhorn.SchemeGroupVersion.Version,
		Kind:    longhornTypes.LonghornKindBackup,
	})
	if err != nil {
		return nil, err
	}

	backup := &longhorn.Backup{
		TypeMeta: metav1.TypeMeta{
			Kind:       longhornTypes.LonghornKindBackup,
			APIVersion: longhorn.SchemeGroupVersion.Group + "/" + longhorn.SchemeGroupVersion.Version,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namegenerator.AppendRandomString("backup-test"),
			Namespace: namespace,
			Labels: map[string]string{
				"backup-volume": volumeName,
			},
		},
		Spec: longhorn.BackupSpec{
			SnapshotName: snapshot.Name,
			BackupMode:   "incremental",
			Labels:       map[string]string{"type": "manual"},
		},
	}
	err = backupController.Client().Create(context.Background(), namespace, backup, backup, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("Error creating backup from snapshot %s on namespace %s: %w", snapshot.Name, namespace, err)
	}

	client.Session.RegisterCleanupFunc(func() error {
		return backupController.Client().Delete(context.Background(), namespace, snapshot.Name, metav1.DeleteOptions{})
	})

	err = wait.PollUntilContextTimeout(context.Background(), defaults.FiveHundredMillisecondTimeout, defaults.FiveMinuteTimeout, false, func(context.Context) (bool, error) {
		err = backupController.Client().Get(context.Background(), namespace, backup.Name, backup, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		if backup.Status.State != longhorn.BackupStateCompleted {
			return false, nil
		}

		if backup.Status.URL == "" {
			return false, nil
		}

		return true, nil
	})

	return backup, err
}

// RestoreLonghornVolumeFromBackup restores a Longhorn Volume from the provided Backup.
func RestoreLonghornVolumeFromBackup(client *rancher.Client, clusterID string, backup longhorn.Backup) error {
	wrangler, err := cluster.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	volumeController, err := wrangler.ControllerFactory.ForKind(schema.GroupVersionKind{
		Group:   longhorn.SchemeGroupVersion.Group,
		Version: longhorn.SchemeGroupVersion.Version,
		Kind:    longhornTypes.LonghornKindVolume,
	})
	if err != nil {
		return err
	}

	// This volume is based on the docs: https://longhorn.io/docs/1.11.1/snapshots-and-backups/backup-and-restore/restore-from-a-backup/
	volume := &longhorn.Volume{
		TypeMeta: metav1.TypeMeta{
			Kind:       longhornTypes.LonghornKindVolume,
			APIVersion: longhorn.SchemeGroupVersion.Group + "/" + longhorn.SchemeGroupVersion.Version,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namegenerator.AppendRandomString("restored-" + backup.Status.VolumeName),
			Namespace: backup.Namespace,
		},
		Spec: longhorn.VolumeSpec{
			Size:       backup.Spec.BackupBlockSize,
			FromBackup: backup.Status.URL,
			Frontend:   "blockdev",
			DataEngine: "v1",
		},
	}

	err = volumeController.Client().Create(context.Background(), backup.Namespace, volume, volume, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	client.Session.RegisterCleanupFunc(func() error {
		return volumeController.Client().Delete(context.Background(), backup.Namespace, volume.Name, metav1.DeleteOptions{})
	})

	err = wait.PollUntilContextTimeout(context.Background(), defaults.FiveHundredMillisecondTimeout, defaults.FiveMinuteTimeout, false, func(context.Context) (bool, error) {
		err = volumeController.Client().Get(context.Background(), backup.Namespace, volume.Name, volume, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		return !volume.Status.RestoreRequired && volume.Status.State == longhorn.VolumeStateDetached, nil
	})

	return err
}

// DeleteLonghornVolume deletes the specified volume.longhorn.io resource.
func DeleteLonghornVolume(client *rancher.Client, clusterID string, volumeNamespace string, volumeName string) error {
	wrangler, err := cluster.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	volumeController, err := wrangler.ControllerFactory.ForKind(schema.GroupVersionKind{
		Group:   longhorn.SchemeGroupVersion.Group,
		Version: longhorn.SchemeGroupVersion.Version,
		Kind:    longhornTypes.LonghornKindVolume,
	})
	if err != nil {
		return err
	}

	return volumeController.Client().Delete(context.Background(), volumeNamespace, volumeName, metav1.DeleteOptions{})
}
