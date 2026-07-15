package encryptionkeyrotation

import (
	"context"
	"time"

	apiv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/stevestates"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/sirupsen/logrus"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// EnableSecretsEncryption is a helper function to enable secrets encryption on an RKE2 or K3s cluster.
func EnableSecretsEncryption(client *rancher.Client, clusterName string) error {
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

	secretEncryption := clusterSpec.RKEConfig.MachineGlobalConfig.Data["secrets-encryption"]
	isEncryptionDisabled := secretEncryption == nil || secretEncryption == false

	if isEncryptionDisabled {
		clusterSpec.RKEConfig.MachineGlobalConfig.Data["secrets-encryption"] = true
		updatedCluster.Spec = *clusterSpec

		cluster, err = client.Steve.SteveType(clusters.ProvisioningSteveResourceType).Update(cluster, updatedCluster)
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
	}

	logrus.Infof("Secrets encryption enabled on cluster %s", clusterName)

	return nil
}
