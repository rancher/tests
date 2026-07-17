package etcdsnapshot

import (
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/tests/actions/config/defaults"
	actionspods "github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
)

func upgradeClusterAndSnapshotSettings(client *rancher.Client, clusterName, clusterID string, etcdRestore *Config) error {
	clusterObject, clusterResponse, err := clusters.GetProvisioningClusterByName(client, clusterName, namespaces.FleetDefault)
	if err != nil {
		return err
	}

	var upgradeKubernetesVersion string
	var defaultVersion []string

	initialKubernetesVersion := clusterObject.Spec.KubernetesVersion

	if etcdRestore.UpgradeKubernetesVersion == "" {
		if strings.Contains(initialKubernetesVersion, defaults.RKE2) {
			defaultVersion, err = kubernetesversions.Default(client, defaults.RKE2, nil)
			if err != nil {
				return err
			}

			upgradeKubernetesVersion = defaultVersion[0]
		} else if strings.Contains(initialKubernetesVersion, defaults.K3S) {
			defaultVersion, err = kubernetesversions.Default(client, defaults.K3S, nil)
			if err != nil {
				return err
			}
		}
	}

	upgradeKubernetesVersion = defaultVersion[0]

	clusterObject.Spec.KubernetesVersion = upgradeKubernetesVersion

	if etcdRestore.SnapshotRestore == all && etcdRestore.ControlPlaneConcurrencyValue != "" && etcdRestore.WorkerConcurrencyValue != "" {
		clusterObject.Spec.RKEConfig.UpgradeStrategy.ControlPlaneConcurrency = etcdRestore.ControlPlaneConcurrencyValue
		clusterObject.Spec.RKEConfig.UpgradeStrategy.WorkerConcurrency = etcdRestore.WorkerConcurrencyValue
	}

	logrus.Infof("Upgrading K8s version to %s on cluster: %s", clusterObject.Spec.KubernetesVersion, clusterObject.Name)
	steveCluster, err := client.Steve.SteveType(stevetypes.Provisioning).Update(clusterResponse, clusterObject)
	if err != nil {
		return err
	}

	err = clusters.WaitClusterToBeUpgraded(client, clusterID)
	if err != nil {
		return err
	}

	err = actionspods.VerifyClusterPods(client, steveCluster)
	if err != nil {
		return err
	}

	if upgradeKubernetesVersion != clusterObject.Spec.KubernetesVersion {
		return fmt.Errorf("K8s Version after upgrade %s does not match expected version %s", clusterObject.Spec.KubernetesVersion, etcdRestore.UpgradeKubernetesVersion)
	}

	if etcdRestore.SnapshotRestore == all && etcdRestore.ControlPlaneConcurrencyValue != "" && etcdRestore.WorkerConcurrencyValue != "" {
		logrus.Infof("Control plane concurrency value is set to: %s", clusterObject.Spec.RKEConfig.UpgradeStrategy.ControlPlaneConcurrency)
		logrus.Infof("Worker concurrency value is set to: %s", clusterObject.Spec.RKEConfig.UpgradeStrategy.WorkerConcurrency)

		if etcdRestore.ControlPlaneConcurrencyValue != clusterObject.Spec.RKEConfig.UpgradeStrategy.ControlPlaneConcurrency {
			return fmt.Errorf("controlPlaneConcurrency after upgrade %s does not match expected version %s", clusterObject.Spec.RKEConfig.UpgradeStrategy.ControlPlaneConcurrency, etcdRestore.ControlPlaneConcurrencyValue)
		}

		if etcdRestore.WorkerConcurrencyValue != clusterObject.Spec.RKEConfig.UpgradeStrategy.WorkerConcurrency {
			return fmt.Errorf("wokerConcurrency after upgrade %s does not match expected version %s", clusterObject.Spec.RKEConfig.UpgradeStrategy.WorkerConcurrency, etcdRestore.WorkerUnavailableValue)
		}
	}

	// sometimes we get a false positive on the cluster's state where it briefly goes 'active'. This is a way to mitigate that.
	clusterSteveObject, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(clusterID)
	if err != nil {
		return err
	}

	if clusterSteveObject.State == nil {
		err = clusters.WaitClusterUntilUpgrade(client, clusterID)
		if err != nil {
			return err
		}
	}

	return nil
}
