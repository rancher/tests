//go:build (validation || pit.weekly) && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package rancherupgrade

import (
	"fmt"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	actionsClusters "github.com/rancher/tests/actions/clusters"
	actionsFleet "github.com/rancher/tests/actions/fleet"
	"github.com/rancher/tests/interoperability/fleet"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// FleetUpgradeTestSuite validates that Fleet keeps reconciling git-driven
// workloads across a Rancher server upgrade and that the embedded
// fleet-controller and the target cluster's fleet-agent are upgraded in
// lockstep with the server.
type FleetUpgradeTestSuite struct {
	suite.Suite
	session          *session.Session
	client           *rancher.Client
	cluster          *clusters.ClusterMeta
	fleetTarget      string
	gitRepoNamespace string
}

func (s *FleetUpgradeTestSuite) SetupSuite() {
	cfg := loadUpgradeInput()
	pipeline := new(PipelineInput)
	config.LoadConfig(PipelineConfigKey, pipeline)

	if cfg.TargetVersion == "" {
		// The dedicated pipeline always writes both keys, so an empty target
		// there is a pipeline bug and must fail, not skip.
		require.False(s.T(), pipeline.Enabled, "rancher-upgrade pipeline config is missing %s.targetVersion", UpgradeInputConfigKey)
		// Generic PIT legs never upgrade the server between phases; without
		// an upgrade target the phases have nothing to assert, so skip.
		s.T().Skipf("%s.targetVersion is not set: this suite runs only in the rancher-upgrade pipeline, which upgrades the server between phases", UpgradeInputConfigKey)
	}

	testSession := session.NewSession()
	s.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)
	s.client = client

	s.cluster, err = clusters.NewClusterMeta(client, client.RancherConfig.ClusterName)
	require.NoError(s.T(), err)

	fleetTarget, err := fleetTargetClusterName(client, s.cluster)
	require.NoError(s.T(), err)
	s.fleetTarget = fleetTarget
	s.gitRepoNamespace = fleetGitRepoWorkspace(fleetTarget)
}

// TearDownSuite deliberately does not call session.Cleanup(): resources created in
// the pre-upgrade phase (git repo clone, SSH secret, target namespace, GitRepo,
// state ConfigMap) must survive into the post-upgrade phase, which runs as a
// separate go test process. The pipeline environment teardown is the cleanup
// for these resources.
func (s *FleetUpgradeTestSuite) TearDownSuite() {
}

func (s *FleetUpgradeTestSuite) TestFleetPreUpgrade() {
	logrus.Infof("pre-upgrade phase: creating SSH node on cluster %s", s.cluster.ID)
	sshNode, err := fleetSSHNode(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	knownHostKey, err := sshNode.ExecuteCommand(fmt.Sprintf("ssh-keyscan -H %s", sshNode.PublicIPAddress))
	require.NoError(s.T(), err)

	logrus.Infof("pre-upgrade phase: creating fleet SSH secret %s in %s", fleetSSHSecretName, s.gitRepoNamespace)
	_, err = actionsFleet.CreateFleetSSHSecret(s.client, sshNode.SSHKey, knownHostKey, fleetSSHSecretName, s.gitRepoNamespace)
	require.NoError(s.T(), err)

	logrus.Infof("pre-upgrade phase: cloning %s into ~/%s on the SSH node", fleet.ExampleRepo, fleetGitRepoDir)
	err = cloneFleetExampleRepo(sshNode)
	require.NoError(s.T(), err)

	logrus.Infof("pre-upgrade phase: creating target namespace %s on cluster %s", fleetTargetNamespace, s.cluster.ID)
	err = createFleetTargetNamespace(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	logrus.Infof("pre-upgrade phase: creating GitRepo %s/%s targeting cluster %s", s.gitRepoNamespace, fleetGitRepoName, s.fleetTarget)
	_, err = actionsFleet.CreateLocalFleetGitRepo(s.client, sshNode, fleetGitRepoName, fleetGitRepoDir, fleetTargetNamespace, s.fleetTarget, fleetSSHSecretName, s.gitRepoNamespace)
	require.NoError(s.T(), err)

	gitRepoID := fmt.Sprintf("%s/%s", s.gitRepoNamespace, fleetGitRepoName)
	err = verifyFleetGitRepoSynced(s.client, gitRepoID, s.cluster.ID, fmt.Sprintf("%s/%s", fleet.Namespace, s.fleetTarget))
	require.NoError(s.T(), err)

	commit, err := gitRepoCommit(s.client, gitRepoID)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), commit, "synced GitRepo must report a commit before the upgrade")

	controllerVersion, err := fleet.GetDeploymentVersion(s.client, fleet.FleetControllerName, fleet.LocalName)
	require.NoError(s.T(), err)
	agentVersion, err := fleet.GetDeploymentVersion(s.client, fleetAgentDeploymentID(s.cluster.ID), s.cluster.ID)
	require.NoError(s.T(), err)

	err = recordFleetState(s.client, map[string]string{
		fleetStateKeyControllerVersion: controllerVersion,
		fleetStateKeyAgentVersion:      agentVersion,
		fleetStateKeyCommit:            commit,
	})
	require.NoError(s.T(), err)
	logrus.Infof("pre-upgrade phase: fleet state seeded (controller %s, agent %s, commit %s)", controllerVersion, agentVersion, commit)
}

func (s *FleetUpgradeTestSuite) TestFleetPostUpgrade() {
	cfg := loadUpgradeInput()
	logrus.Infof("post-upgrade phase: waiting for rancher server version %s", cfg.TargetVersion)

	err := actionsClusters.WaitRancherVersion(s.client, cfg.TargetVersion, defaults.TenMinuteTimeout)
	require.NoError(s.T(), err)
	logrus.Infof("post-upgrade phase: server version %s observed; checking fleet state", cfg.TargetVersion)

	state, err := loadFleetState(s.client)
	require.NoError(s.T(), err, "pre-upgrade fleet state ConfigMap %s/%s is missing: run TestFleetPreUpgrade first", fleet.Namespace, fleetStateConfigMapName)
	preControllerVersion := state[fleetStateKeyControllerVersion]
	preAgentVersion := state[fleetStateKeyAgentVersion]
	preCommit := state[fleetStateKeyCommit]
	require.NotEmpty(s.T(), preControllerVersion, "state ConfigMap is missing %s", fleetStateKeyControllerVersion)
	require.NotEmpty(s.T(), preAgentVersion, "state ConfigMap is missing %s", fleetStateKeyAgentVersion)
	require.NotEmpty(s.T(), preCommit, "state ConfigMap is missing %s", fleetStateKeyCommit)

	gitRepoID := fmt.Sprintf("%s/%s", s.gitRepoNamespace, fleetGitRepoName)
	err = verifyFleetGitRepoSynced(s.client, gitRepoID, s.cluster.ID, fmt.Sprintf("%s/%s", fleet.Namespace, s.fleetTarget))
	require.NoError(s.T(), err)
	logrus.Infof("post-upgrade phase: GitRepo %s still in sync; driving a new commit through the reconcile loop", gitRepoID)

	sshNode, err := fleetSSHNode(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	err = actionsFleet.GitPushCommit(sshNode, fleetGitRepoDir)
	require.NoError(s.T(), err)

	err = actionsFleet.VerifyNewGitCommit(s.client, preCommit, gitRepoID)
	require.NoError(s.T(), err)

	err = actionsFleet.VerifyRepoUpdate(s.client, gitRepoID)
	require.NoError(s.T(), err)

	err = verifyFleetGitRepoSynced(s.client, gitRepoID, s.cluster.ID, fmt.Sprintf("%s/%s", fleet.Namespace, s.fleetTarget))
	require.NoError(s.T(), err)
	logrus.Infof("post-upgrade phase: commit moved past the pre-upgrade SHA %s and resources redeployed downstream", preCommit)

	controllerVersion, err := fleet.GetDeploymentVersion(s.client, fleet.FleetControllerName, fleet.LocalName)
	require.NoError(s.T(), err)
	agentVersion, err := fleet.GetDeploymentVersion(s.client, fleetAgentDeploymentID(s.cluster.ID), s.cluster.ID)
	require.NoError(s.T(), err)

	require.NotEqual(s.T(), preControllerVersion, controllerVersion,
		"fleet-controller image was not upgraded by the Rancher server upgrade: still %s", controllerVersion)
	require.NotEqual(s.T(), preAgentVersion, agentVersion,
		"fleet-agent image was not upgraded by the Rancher server upgrade: still %s", agentVersion)
	require.Equal(s.T(), controllerVersion, agentVersion,
		"fleet-controller (%s) and fleet-agent (%s) must run the same fleet version after the upgrade", controllerVersion, agentVersion)
	logrus.Infof("post-upgrade phase: fleet upgraded in lockstep to %s", controllerVersion)
}

func TestFleetUpgradeTestSuite(t *testing.T) {
	suite.Run(t, new(FleetUpgradeTestSuite))
}
