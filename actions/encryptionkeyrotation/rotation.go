package encryptionkeyrotation

import (
	"context"
	"time"

	apisV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	apiv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/stevestates"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// RotateEncryptionKey is a helper function to rotate the encryption key on an RKE2 or K3s cluster.
func RotateEncryptionKey(client *rancher.Client, clusterName string) error {
	id, err := clusters.GetV1ProvisioningClusterByName(client, clusterName)
	if err != nil {
		return err
	}

	cluster, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(id)
	if err != nil {
		return err
	}

	clusterSpec := &apiv1.ClusterSpec{}
	err = v1.ConvertToK8sType(cluster.Spec, clusterSpec)
	if err != nil {
		return err
	}

	updatedCluster := *cluster

	if clusterSpec.RKEConfig != nil {
		if clusterSpec.RKEConfig.RotateEncryptionKeys == nil {
			clusterSpec.RKEConfig.RotateEncryptionKeys = &rkev1.RotateEncryptionKeys{
				Generation: 1,
			}
		} else {
			clusterSpec.RKEConfig.RotateEncryptionKeys = &rkev1.RotateEncryptionKeys{
				Generation: clusterSpec.RKEConfig.RotateEncryptionKeys.Generation + 1,
			}
		}
	} else {
		clusterSpec.RKEConfig = &apisV1.RKEConfig{
			RotateEncryptionKeys: &rkev1.RotateEncryptionKeys{
				Generation: 1,
			},
		}
	}

	updatedCluster.Spec = *clusterSpec

	_, err = client.Steve.SteveType(stevetypes.Provisioning).Update(cluster, updatedCluster)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaults.FifteenMinuteTimeout)
	defer cancel()

	err = kwait.PollUntilContextTimeout(ctx, 10*time.Second, defaults.ThirtyMinuteTimeout, false, func(context.Context) (done bool, err error) {
		cluster, err = client.Steve.SteveType(stevetypes.Provisioning).ByID(cluster.ID)
		if err != nil {
			return false, nil
		}

		clusterStatus := &provv1.ClusterStatus{}

		err = steveV1.ConvertToK8sType(cluster.Status, clusterStatus)
		if err != nil {
			return false, nil
		}

		if !clusterStatus.Ready || cluster.State.Name != stevestates.Active || cluster.State.Error == true {
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		return err
	}

	return err
}
