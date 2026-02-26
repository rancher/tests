//go:build validation && airgap

package fleet

import (
	"testing"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/norman/types"
	"github.com/rancher/shepherd/clients/rancher"
	client "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extensionsfleet "github.com/rancher/shepherd/extensions/fleet"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/interoperability/fleet"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type FleetAirgapTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
}

func (f *FleetAirgapTestSuite) TearDownSuite() {
	f.session.Cleanup()
}

func (f *FleetAirgapTestSuite) SetupSuite() {
	f.session = session.NewSession()

	client, err := rancher.NewClient("", f.session)
	require.NoError(f.T(), err)

	f.client = client
}

func (f *FleetAirgapTestSuite) TestGitRepoAllDownstreamClusters() {
	clusterList, err := f.client.Management.Cluster.List(&types.ListOpts{})
	require.NoError(f.T(), err)

	downstreamClusters := []client.Cluster{}
	for _, cluster := range clusterList.Data {
		if cluster.ID != fleet.LocalName && cluster.Labels[fleet.ProviderMatchKey] != fleet.HarvesterName {
			downstreamClusters = append(downstreamClusters, cluster)
		}
	}

	if len(downstreamClusters) == 0 {
		f.T().Skip("This test requires the presence of at least one downstream cluster")
	}

	fleetVersion, err := fleet.GetDeploymentVersion(f.client, fleet.FleetControllerName, fleet.LocalName)
	require.NoError(f.T(), err)

	f.T().Log("Running fleet " + fleetVersion)

	fleetGitRepo := v1alpha1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fleet.FleetMetaName + namegenerator.RandStringLower(5),
			Namespace: fleet.Namespace,
		},
		Spec: v1alpha1.GitRepoSpec{
			Repo:            fleet.ExampleRepo,
			Branch:          fleet.BranchName,
			Paths:           []string{fleet.GitRepoPathLinux},
			TargetNamespace: namegenerator.AppendRandomString("fleet-airgap-test-namespace"),
			CorrectDrift:    &v1alpha1.CorrectDrift{},
			ImageScanCommit: &v1alpha1.CommitSpec{AuthorName: "", AuthorEmail: ""},
			Targets: []v1alpha1.GitTarget{ // This is the default selector in the UI and avoids including the cluster dedicated for Harvester,
				{
					ClusterSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      fleet.ProviderMatchKey,
								Operator: fleet.NotInMatchOperator,
								Values: []string{
									fleet.HarvesterName,
								},
							},
						},
					},
				},
			},
		},
	}

	var firstCluster bool
	var usingWindows bool
	for i, cluster := range downstreamClusters {
		usingWindows, err = fleet.AddWindowsPathsToGitRepo(f.client, cluster.ID, &fleetGitRepo)
		require.NoError(f.T(), err)
		if i == 0 {
			firstCluster = usingWindows
		} else if usingWindows != firstCluster {
			f.T().Skip("Some downstream clusters demand using a windows Fleet version and others do not")
		}
	}

	if usingWindows {
		f.T().Log("Using " + fleet.GitRepoPathWindows + " due to the presence of windows nodes")
	}

	f.T().Log("Creating Fleet GitRepo for all downstream clusters")
	gitRepoObject, err := extensionsfleet.CreateFleetGitRepo(f.client, &fleetGitRepo)
	require.NoError(f.T(), err)

	for _, cluster := range downstreamClusters {
		f.T().Log("Check for expected failure on GitRepo deploy for cluster " + cluster.Name)
		err = fleet.VerifyGitRepo(f.client, gitRepoObject.ID, cluster.ID, cluster.Name)
		require.Error(f.T(), err)
	}
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestFleetAirgapTestSuite(t *testing.T) {
	suite.Run(t, new(FleetAirgapTestSuite))
}
