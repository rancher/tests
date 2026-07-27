//go:build validation

package cli

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/cli"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CLITestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
}

func (c *CLITestSuite) TearDownSuite() {
	c.session.Cleanup()
}

func (c *CLITestSuite) SetupSuite() {
	testSession := session.NewSession()
	c.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(c.T(), err)

	c.client = client
}

func (c *CLITestSuite) TestNamespaces() {
	var namespaceName = namegen.AppendRandomString("ns")
	var projectName = namegen.AppendRandomString("projects")

	logrus.Infof("Creating namespace: (%s) in cluster: (%s)", namespaceName, "local")
	err := cli.CreateNamespaces(c.client.CLI, "local", namespaceName)
	require.NoError(c.T(), err)

	logrus.Infof("Deleting namespace: (%s) in project: (%s)", namespaceName, projectName)
	err = cli.DeleteNamespaces(c.client.CLI, namespaceName)
	require.NoError(c.T(), err)
}

func (c *CLITestSuite) TestProjects() {
	var projectName = namegen.AppendRandomString("projects")
	var clusterName = namegen.AppendRandomString("cluster")

	logrus.Infof("Creating project: (%s) in cluster: (%s)", projectName, "local")
	err := cli.CreateProjects(c.client.CLI, projectName, "local")
	require.NoError(c.T(), err)

	logrus.Infof("Creating project: (%s) in cluster: (%s)", projectName, clusterName)
	err = cli.CreateProjects(c.client.CLI, projectName, clusterName)
	require.Error(c.T(), err)

	logrus.Infof("Deleting project: (%s) in cluster: (%s)", projectName, "local")
	err = cli.DeleteProjects(c.client.CLI, projectName)
	require.NoError(c.T(), err)
}
func TestCLITestSuite(t *testing.T) {
	suite.Run(t, new(CLITestSuite))
}
