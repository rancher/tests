//go:build validation || recurring

package rke2

import (
	"os"
	"testing"

	v1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type aceTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func aceSetup(t *testing.T) aceTest {
	var r aceTest
	testSession := session.NewSession()
	r.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	r.cattleConfig, err = defaults.LoadPackageDefaults(r.cattleConfig, "")
	assert.NoError(t, err)

	r.cattleConfig, err = defaults.SetK8sDefault(client, defaults.RKE2, r.cattleConfig)
	require.NoError(t, err)

	r.standardUserClient, err = standard.CreateStandardUser(r.client)
	assert.NoError(t, err)

	return r
}

func TestACE(t *testing.T) {
	t.Parallel()
	r := aceSetup(t)

	nodeRoles0 := []provisioninginput.MachinePools{
		{
			MachinePoolConfig: machinepools.MachinePoolConfig{
				NodeRoles: machinepools.NodeRoles{
					ControlPlane: true,
					Etcd:         false,
					Worker:       false,
					Quantity:     3,
				},
			},
		},
		{
			MachinePoolConfig: machinepools.MachinePoolConfig{
				NodeRoles: machinepools.NodeRoles{
					ControlPlane: false,
					Etcd:         true,
					Worker:       false,
					Quantity:     1,
				},
			},
		},
		{
			MachinePoolConfig: machinepools.MachinePoolConfig{
				NodeRoles: machinepools.NodeRoles{
					ControlPlane: false,
					Etcd:         false,
					Worker:       true,
					Quantity:     1,
				},
			},
		},
	}

	tests := []struct {
		name         string
		machinePools []provisioninginput.MachinePools
		client       *rancher.Client
	}{
		{"ACE|etcd|3_cp|worker", nodeRoles0, r.standardUserClient},
	}
	// Test is obsolete when ACE is not set.
	for _, tt := range tests {
		var err error
		t.Cleanup(func() {
			logrus.Info("Running cleanup")
			r.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)

			networking := provisioninginput.Networking{
				LocalClusterAuthEndpoint: &v1.LocalClusterAuthEndpoint{
					Enabled: true,
				},
			}
			clusterConfig.Networking = &networking
			clusterConfig.MachinePools = tt.machinePools

			provider := provisioning.CreateProvider(clusterConfig.Provider)
			credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
			machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

			clusterObject, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
			require.NoError(t, err)

			provisioning.VerifyCluster(t, tt.client, clusterConfig, clusterObject)
		})

		params := provisioning.GetProvisioningSchemaParams(tt.client, r.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}
