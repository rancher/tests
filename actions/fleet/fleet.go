// Package fleet provides reusable actions for driving a Fleet GitRepo against
// a git remote served over SSH from a cluster node: seeding the SSH client
// secret, creating the GitRepo that tracks the node-local clone, pushing
// commits into it, and waiting on the resulting reconciliation states.
package fleet

import (
	"fmt"
	"time"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	extensionsfleet "github.com/rancher/shepherd/extensions/fleet"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/nodes"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	sshPublickey  = "ssh-publickey"
	sshPrivatekey = "ssh-privatekey"
	knownHosts    = "known_hosts"

	// Namespace is the fleet workspace namespace on the management cluster
	// where GitRepos and their SSH client secrets are created for clusters
	// registered in that workspace.
	Namespace = "fleet-default"

	// LocalNamespace is the workspace of the local (management) cluster: the
	// fleet-default controller only processes clusters registered in
	// fleet-default, so GitRepos targeting the local cluster must live here.
	LocalNamespace = "fleet-local"

	// DefaultBranch is the branch of the node-local git remote tracked by the
	// GitRepos created via CreateLocalFleetGitRepo.
	DefaultBranch = "master"

	// DefaultGitRepoPath is the in-repo path (the fleet-examples "simple"
	// layout) whose resources the created GitRepo deploys.
	DefaultGitRepoPath = "simple"
)

// GitPushCommit configures a git identity inside the node-local clone and
// creates an empty commit on its checked-out branch. The clone doubles as the
// SSH-served remote the GitRepo fetches from, so the new commit is what
// triggers Fleet's reconciliation on the next poll.
func GitPushCommit(sshNode *nodes.Node, repoName string) error {
	gitUserName := namegenerator.AppendRandomString("git-user")

	logrus.Info("Configuring Git User Name")
	_, err := sshNode.ExecuteCommand(fmt.Sprintf("git -C %s config --local user.name %s", repoName, gitUserName))
	if err != nil {
		return err
	}

	logrus.Info("Configuring Git User Email")
	_, err = sshNode.ExecuteCommand(fmt.Sprintf("git -C %s config --local user.email %s", repoName, gitUserName))
	if err != nil {
		return err
	}

	logrus.Info("Git Commit")
	_, err = sshNode.ExecuteCommand(fmt.Sprintf("git -C %s commit --allow-empty -m 'Trigger fleet update'", repoName))
	return err
}

// CreateLocalFleetGitRepo creates a Fleet GitRepo named gitRepoName in the
// repoNamespace workspace tracking the node-local clone at ~/repoName over
// SSH. The GitRepo targets the cluster named clusterName and deploys the
// DefaultGitRepoPath layout into namespaceName, authenticating with the SSH
// secret secretName, which must live in repoNamespace as well. The caller is
// responsible for waiting on the initial sync (for example with
// interoperability/fleet.VerifyGitRepo).
func CreateLocalFleetGitRepo(client *rancher.Client, sshNode *nodes.Node, gitRepoName string, repoName string, namespaceName string, clusterName string, secretName string, repoNamespace string) (*steveV1.SteveAPIObject, error) {
	gitSSHRepo := fmt.Sprintf("%s@%s:/home/%s/%s", sshNode.SSHUser, sshNode.PublicIPAddress, sshNode.SSHUser, repoName)

	fleetGitRepo := &v1alpha1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gitRepoName,
			Namespace: repoNamespace,
		},
		Spec: v1alpha1.GitRepoSpec{
			Repo:            gitSSHRepo,
			Branch:          DefaultBranch,
			TargetNamespace: namespaceName,
			Paths:           []string{DefaultGitRepoPath},
			Targets: []v1alpha1.GitTarget{
				{
					ClusterName: clusterName,
				},
			},
			ClientSecretName: secretName,
		},
	}

	logrus.Info("Creating a fleet git repo")
	repoObject, err := extensionsfleet.CreateFleetGitRepo(client, fleetGitRepo)
	if err != nil {
		return nil, err
	}

	return repoObject, nil
}

// VerifyNewGitCommit waits until the GitRepo's status reports a commit
// different from oldCommit, proving Fleet has fetched the pushed change.
func VerifyNewGitCommit(client *rancher.Client, oldCommit string, repoObjectID string) error {
	logrus.Info("Checking Fleet Git Commit has been updated")
	backoff := kwait.Backoff{
		Duration: 1 * time.Second,
		Factor:   1.1,
		Jitter:   0.1,
		Steps:    20,
	}

	err := kwait.ExponentialBackoff(backoff, func() (finished bool, err error) {
		gitRepo, err := client.Steve.SteveType(extensionsfleet.FleetGitRepoResourceType).ByID(repoObjectID)
		if err != nil {
			return true, err
		}

		newGitStatus := &v1alpha1.GitRepoStatus{}
		err = steveV1.ConvertToK8sType(gitRepo.Status, newGitStatus)
		if err != nil {
			return true, err
		}

		return oldCommit != newGitStatus.Commit, nil
	})
	return err
}

// VerifyRepoUpdate waits until the GitRepo's bundle summary reports Ready,
// meaning the resources from the latest commit are deployed on the target.
func VerifyRepoUpdate(client *rancher.Client, repoObjectID string) error {
	backoff := kwait.Backoff{
		Duration: 1 * time.Millisecond,
		Factor:   1,
		Jitter:   1,
		Steps:    500,
	}

	err := kwait.ExponentialBackoff(backoff, func() (finished bool, err error) {
		gitRepo, err := client.Steve.SteveType(extensionsfleet.FleetGitRepoResourceType).ByID(repoObjectID)
		if err != nil {
			return true, err
		}

		newGitRepo := &v1alpha1.GitRepo{}
		err = steveV1.ConvertToK8sType(gitRepo, newGitRepo)
		if err != nil {
			return true, err
		}

		return newGitRepo.Status.Summary.Ready == 1, nil
	})
	return err
}

// VerifyRepoPause waits until the GitRepo's spec paused state equals isPaused.
func VerifyRepoPause(client *rancher.Client, repoObjectID string, isPaused bool) error {
	backoff := kwait.Backoff{
		Duration: 1 * time.Second,
		Factor:   1,
		Jitter:   1,
		Steps:    10,
	}

	err := kwait.ExponentialBackoff(backoff, func() (finished bool, err error) {
		gitRepo, err := client.Steve.SteveType(extensionsfleet.FleetGitRepoResourceType).ByID(repoObjectID)
		if err != nil {
			return true, err
		}

		newGitRepo := &v1alpha1.GitRepo{}
		err = steveV1.ConvertToK8sType(gitRepo, newGitRepo)
		if err != nil {
			return true, err
		}

		return newGitRepo.Spec.Paused == isPaused, nil
	})
	return err
}

// CreateFleetSSHSecret creates an SSH auth secret named secretName in the
// namespace namespace holding the private key, its public key and the
// known_hosts entry for the node serving the git remote. It returns the name
// of the created secret.
func CreateFleetSSHSecret(client *rancher.Client, privateKey []byte, knownHostKey string, secretName string, namespace string) (string, error) {
	key, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		return "", err
	}

	keyData := map[string][]byte{
		sshPublickey:  []byte(key.PublicKey().Marshal()),
		sshPrivatekey: privateKey,
		knownHosts:    []byte(knownHostKey),
	}

	secretTemplate := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Data: keyData,
		Type: corev1.SecretTypeSSHAuth,
	}

	secretResp, err := client.WranglerContext.Core.Secret().Create(&secretTemplate)
	if err != nil {
		return "", err
	}

	return secretResp.Name, nil
}
