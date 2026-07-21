//go:build validation

package snapshot

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	extensionsfleet "github.com/rancher/shepherd/extensions/fleet"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/etcdsnapshot"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/interoperability/fleet"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	containerImage        = "nginx"
	windowsContainerImage = "mcr.microsoft.com/windows/servercore/iis"
)

type FleetWithSnapshotTestSuite struct {
	suite.Suite
	client       *rancher.Client
	session      *session.Session
	fleetGitRepo *v1alpha1.GitRepo
	clusterID    string
}

func (f *FleetWithSnapshotTestSuite) TearDownSuite() {
	f.session.Cleanup()
}

func (f *FleetWithSnapshotTestSuite) SetupSuite() {
	f.session = session.NewSession()

	client, err := rancher.NewClient("", f.session)
	require.NoError(f.T(), err)

	f.client = client

	f.fleetGitRepo = &v1alpha1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fleet.FleetMetaName + namegenerator.RandStringLower(5),
			Namespace: fleet.Namespace,
		},
		Spec: v1alpha1.GitRepoSpec{
			Repo:            fleet.ExampleRepo,
			Branch:          fleet.BranchName,
			Paths:           []string{fleet.GitRepoPathLinux},
			CorrectDrift:    &v1alpha1.CorrectDrift{},
			ImageScanCommit: &v1alpha1.CommitSpec{AuthorName: "", AuthorEmail: ""},
			Targets:         []v1alpha1.GitTarget{{ClusterName: f.client.RancherConfig.ClusterName}},
		},
	}

	f.client, err = f.client.ReLogin()
	require.NoError(f.T(), err)

	userConfig := new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, userConfig)

	clusterObject, _, _ := extensionscluster.GetProvisioningClusterByName(f.client, f.client.RancherConfig.ClusterName, fleet.Namespace)
	if clusterObject != nil {
		status := &provv1.ClusterStatus{}
		err := steveV1.ConvertToK8sType(clusterObject.Status, status)
		require.NoError(f.T(), err)

		f.clusterID = status.ClusterName
	} else {
		f.clusterID, err = extensionscluster.GetClusterIDByName(f.client, f.client.RancherConfig.ClusterName)
		require.NoError(f.T(), err)
	}

	// NOTE: Skipped cluster-wide StatusPods check. See public_gitrepo_test.go for rationale.
	// See: https://github.com/rancher/shepherd/issues/574

}

func (f *FleetWithSnapshotTestSuite) TestSnapshotThenFleetRestore() {
	snapshotRestoreAll := &etcdsnapshot.Config{
		UpgradeKubernetesVersion:     "",
		SnapshotRestore:              "all",
		ControlPlaneConcurrencyValue: "15%",
		ControlPlaneUnavailableValue: "3",
		WorkerConcurrencyValue:       "20%",
		WorkerUnavailableValue:       "15%",
		RecurringRestores:            1,
	}

	tests := []struct {
		name         string
		etcdSnapshot *etcdsnapshot.Config
	}{
		{" Restore cluster config Kubernetes version and etcd", snapshotRestoreAll},
	}

	for _, tt := range tests {
		testSession := session.NewSession()
		defer testSession.Cleanup()
		client, err := f.client.WithSession(testSession)
		require.NoError(f.T(), err)

		fleetVersion, err := fleet.GetDeploymentVersion(client, fleet.FleetControllerName, fleet.LocalName)
		require.NoError(f.T(), err)

		// as of rancher v2.12, this returns the full list of nodes instead of j
		urlQuery, err := url.ParseQuery(fmt.Sprintf("labelSelector=%s=%s", "cattle.io/os", "windows"))
		require.NoError(f.T(), err)

		steveClient, err := client.Steve.ProxyDownstream(f.clusterID)
		require.NoError(f.T(), err)

		winsNodeList, err := steveClient.SteveType("node").List(urlQuery)
		require.NoError(f.T(), err)

		if len(winsNodeList.Data) > 0 {
			urlQuery, err = url.ParseQuery(fmt.Sprintf("labelSelector=%s=%s", "kubernetes.io/os", "linux"))
			require.NoError(f.T(), err)

			linuxNodeList, err := steveClient.SteveType("node").List(urlQuery)
			require.NoError(f.T(), err)

			if len(winsNodeList.Data) < len(linuxNodeList.Data) {
				f.fleetGitRepo.Spec.Paths = []string{fleet.GitRepoPathWindows}
				f.fleetGitRepo.Name += "windows"
				tt.name += "windows"
			}
		}

		client, err = client.ReLogin()
		require.NoError(f.T(), err)

		f.Run(fleet.FleetName+" "+fleetVersion+tt.name, func() {
			var gitRepoObject *steveV1.SteveAPIObject

			logrus.Info("deploying fleet post-snapshot to test persistence after restore is complete")

			cluster, snapshotName, err := etcdsnapshot.CreateAndValidateSnapshotV2Prov(client, client.RancherConfig.ClusterName, f.clusterID, tt.etcdSnapshot)
			require.NoError(f.T(), err)

			logrus.Info("Deploying public fleet gitRepo")
			gitRepoObject, err = extensionsfleet.CreateFleetGitRepo(client, f.fleetGitRepo)
			require.NoError(f.T(), err)

			err = fleet.VerifyGitRepo(client, gitRepoObject.ID, f.clusterID, namespaces.FleetDefault+"/"+client.RancherConfig.ClusterName)
			require.NoError(f.T(), err)

			_, err = etcdsnapshot.RestoreAndValidateSnapshotV2Prov(client, snapshotName, tt.etcdSnapshot, cluster, f.clusterID)
			require.NoError(f.T(), err)

			err = fleet.VerifyGitRepo(client, gitRepoObject.ID, f.clusterID, fleet.Namespace+"/"+client.RancherConfig.ClusterName)
			require.NoError(f.T(), err)

		})
	}
}

func (f *FleetWithSnapshotTestSuite) TestFleetThenSnapshotRestore() {
	snapshotRestoreAll := &etcdsnapshot.Config{
		UpgradeKubernetesVersion:     "",
		SnapshotRestore:              "all",
		ControlPlaneConcurrencyValue: "15%",
		ControlPlaneUnavailableValue: "3",
		WorkerConcurrencyValue:       "20%",
		WorkerUnavailableValue:       "15%",
		RecurringRestores:            1,
	}

	tests := []struct {
		name         string
		etcdSnapshot *etcdsnapshot.Config
	}{
		{" Restore cluster config Kubernetes version and etcd", snapshotRestoreAll},
	}

	for _, tt := range tests {
		testSession := session.NewSession()
		defer testSession.Cleanup()
		client, err := f.client.WithSession(testSession)
		require.NoError(f.T(), err)

		fleetVersion, err := fleet.GetDeploymentVersion(client, fleet.FleetControllerName, fleet.LocalName)
		require.NoError(f.T(), err)

		urlQuery, err := url.ParseQuery(fmt.Sprintf("labelSelector=%s=%s", "cattle.io/os", "windows"))
		require.NoError(f.T(), err)

		steveClient, err := client.Steve.ProxyDownstream(f.clusterID)
		require.NoError(f.T(), err)

		winsNodeList, err := steveClient.SteveType("node").List(urlQuery)
		require.NoError(f.T(), err)

		if len(winsNodeList.Data) > 0 {
			f.fleetGitRepo.Spec.Paths = []string{fleet.GitRepoPathWindows}
			f.fleetGitRepo.Name += "windows"
			tt.name += "windows"
		}

		client, err = client.ReLogin()
		require.NoError(f.T(), err)

		f.Run(fleet.FleetName+" "+fleetVersion+tt.name, func() {

			var gitRepoObject *steveV1.SteveAPIObject
			logrus.Info("Deploying public fleet gitRepo")
			gitRepoObject, err = extensionsfleet.CreateFleetGitRepo(client, f.fleetGitRepo)
			require.NoError(f.T(), err)

			err = fleet.VerifyGitRepo(client, gitRepoObject.ID, f.clusterID, fleet.Namespace+"/"+client.RancherConfig.ClusterName)
			require.NoError(f.T(), err)

			logrus.Info("having fleet deployed pre-snapshot to test fleet as a pre-snapshot resource")
			cluster, snapshotName, err := etcdsnapshot.CreateAndValidateSnapshotV2Prov(client, client.RancherConfig.ClusterName, f.clusterID, tt.etcdSnapshot)
			require.NoError(f.T(), err)

			_, err = etcdsnapshot.RestoreAndValidateSnapshotV2Prov(client, snapshotName, tt.etcdSnapshot, cluster, f.clusterID)
			require.NoError(f.T(), err)

			err = fleet.VerifyGitRepo(client, gitRepoObject.ID, f.clusterID, fleet.Namespace+"/"+client.RancherConfig.ClusterName)
			require.NoError(f.T(), err)

		})
	}
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestSnapshotRestoreWithFleetTestSuite(t *testing.T) {
	suite.Run(t, new(FleetWithSnapshotTestSuite))
}
