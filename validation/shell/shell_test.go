//go:build (validation || infra.any || cluster.any) && !stress && !sanity && !extended

package shell

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher"

	"github.com/rancher/shepherd/pkg/session"

	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/settings"
	"github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	cattleSystemNameSpace = "cattle-system"
	shellName             = "shell-image"
	clusterName           = "local"
)

type ShellTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
}

func (s *ShellTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *ShellTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)

	s.client = client
}

func (s *ShellTestSuite) TestShell() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	s.Run("Verify the version of shell on local cluster", func() {
		shellImage, err := settings.ShellVersion(s.client, clusterName, shellName)
		require.NoError(s.T(), err)
		assert.Equal(s.T(), shellImage, s.client.RancherConfig.ShellImage)
	})

	s.Run("Verify the helm operations for the shell succeeded", func() {
		steveClient := s.client.Steve
		err := kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.FiveMinuteTimeout, true, func(ctx context.Context) (bool, error) {
			podList, err := steveClient.SteveType(pods.PodResourceSteveType).NamespacedSteveClient(cattleSystemNameSpace).List(nil)
			if err != nil {
				return false, nil
			}

			for _, pod := range podList.Data {
				if strings.Contains(pod.Name, "helm") {
					podStatus := &corev1.PodStatus{}
					if err = steveV1.ConvertToK8sType(pod.Status, podStatus); err != nil {
						return false, err
					}
					if string(podStatus.Phase) != "Succeeded" {
						return false, nil
					}
				}
			}
			return true, nil
		})
		require.NoError(s.T(), err)
	})
}

func TestShellTestSuite(t *testing.T) {
	suite.Run(t, new(ShellTestSuite))
}
