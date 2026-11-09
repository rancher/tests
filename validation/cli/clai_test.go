//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/auth"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/clients/ranchercli"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extnamespaceapi "github.com/rancher/shepherd/extensions/kubeapi/namespaces"
	extprojectapi "github.com/rancher/shepherd/extensions/kubeapi/projects"
	extrbac "github.com/rancher/shepherd/extensions/kubeapi/rbac"
	"github.com/rancher/shepherd/extensions/users"
	shepherdconfig "github.com/rancher/shepherd/pkg/config"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	cliapi "github.com/rancher/tests/actions/cli"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/rbac"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

type CLAITestSuite struct {
	suite.Suite
	client              *rancher.Client
	session             *session.Session
	host                string
	adminToken          string
	caFile              string
	standardUser        *management.User
	standardUserClient  *rancher.Client
	standardUserToken   string
	downstreamClusterID string
}

type clusterTarget struct {
	name string
	id   string
}

func (c *CLAITestSuite) clusterTargets() []clusterTarget {
	return []clusterTarget{
		{name: "local", id: extclusterapi.LocalCluster},
		{name: "downstream", id: c.downstreamClusterID},
	}
}

func (c *CLAITestSuite) setupCLIClient(subSession *session.Session, name, token string) (*ranchercli.CLIClient, error) {
	return ranchercli.NewCLIClient(ranchercli.CLIOptions{
		Name:    name,
		Host:    c.host,
		Token:   token,
		CAFile:  c.caFile,
		Session: subSession,
	})
}

func (c *CLAITestSuite) SetupSuite() {
	c.session = session.NewSession()

	settings := new(cliapi.CLAISettings)
	shepherdconfig.LoadConfig(rancher.ConfigurationFileKey, settings)
	if !settings.RancherCLAI {
		c.T().Skip("set rancherCLAI: true in the cattle config to run the Rancher CLAI test suite")
	}

	rancherConfig := new(rancher.Config)
	shepherdconfig.LoadConfig(rancher.ConfigurationFileKey, rancherConfig)
	rancherConfig.RancherCLI = false
	c.host = rancherConfig.Host
	c.adminToken = rancherConfig.AdminToken

	adminCLI, err := ranchercli.NewCLIClient(ranchercli.CLIOptions{
		Name:    "admin-test",
		Host:    rancherConfig.Host,
		Token:   rancherConfig.AdminToken,
		CAFile:  rancherConfig.CAFile,
		CACerts: rancherConfig.CACerts,
		Session: c.session,
	})
	require.NoError(c.T(), err)
	c.caFile = adminCLI.CAFile
	rancherConfig.CAFile = adminCLI.CAFile

	client, err := rancher.NewClientForConfig("", rancherConfig, c.session)
	require.NoError(c.T(), err)
	c.client = client

	clusterName := c.client.RancherConfig.ClusterName
	require.NotEmpty(c.T(), clusterName, "rancher.clusterName must identify an existing downstream cluster")
	downstreamClusterID, err := clusters.GetClusterIDByName(c.client, clusterName)
	require.NoError(c.T(), err, "failed to resolve downstream cluster %q", clusterName)
	require.NotEmpty(c.T(), downstreamClusterID, "no cluster named %q exists", clusterName)
	require.NotEqual(c.T(), extclusterapi.LocalCluster, downstreamClusterID, "rancher.clusterName must identify a downstream cluster")
	c.downstreamClusterID = downstreamClusterID

	userConfig := users.UserConfig()
	standardUser, err := users.CreateUserWithRole(c.client, userConfig, rbac.StandardUser.String())
	require.NoError(c.T(), err)
	c.standardUser = standardUser

	standardUserClient, err := c.client.AsAuthUser(standardUser, auth.LocalAuth)
	require.NoError(c.T(), err)
	standardUserClient, err = standardUserClient.ReLoginForConfig(rancherConfig)
	require.NoError(c.T(), err)
	c.standardUserClient = standardUserClient

	standardUserToken, err := c.standardUserClient.Management.Token.Create(&management.Token{
		Description: "new Rancher CLI standard-user test token",
		UserID:      c.standardUserClient.UserID,
	})
	require.NoError(c.T(), err)
	require.NotEmpty(c.T(), standardUserToken.Token)
	c.standardUserToken = standardUserToken.Token
}

func (c *CLAITestSuite) TearDownSuite() {
	c.session.Cleanup()
}

func (c *CLAITestSuite) TestSchemaAndConfiguration() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	adminCLI, err := c.setupCLIClient(subSession, "admin-test", c.adminToken)
	require.NoError(c.T(), err)

	c.T().Log("Verifying CLI schema output")
	schema, err := adminCLI.RunJSON(context.Background(), "schema")
	require.NoError(c.T(), err)
	require.Equal(c.T(), "Schema", schema.Kind)
	require.Contains(c.T(), string(schema.Data), "access can")
	require.Contains(c.T(), string(schema.Data), "config status")
	require.Contains(c.T(), string(schema.Data), "create project")

	c.T().Log("Verifying CLI configuration path and server list")
	pathResult, err := adminCLI.Run(context.Background(), "config", "path")
	require.NoError(c.T(), err)
	require.Contains(c.T(), pathResult.Stdout, adminCLI.ConfigDirectory)

	servers, err := adminCLI.RunJSON(context.Background(), "config", "list-servers")
	require.NoError(c.T(), err)
	require.NotEmpty(c.T(), servers.Data)
	require.Contains(c.T(), string(servers.Data), adminCLI.Name)

	c.T().Log("Verifying CLI config status")
	status, err := adminCLI.RunJSON(context.Background(), "config", "status")
	require.NoError(c.T(), err)
	require.NotEmpty(c.T(), status.Data)
}

func (c *CLAITestSuite) TestAuthentication() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	adminCLI, err := c.setupCLIClient(subSession, "admin-test", c.adminToken)
	require.NoError(c.T(), err)
	standardUserCLI, err := c.setupCLIClient(subSession, "standard-user-test", c.standardUserToken)
	require.NoError(c.T(), err)

	testCases := []struct {
		name             string
		cli              *ranchercli.CLIClient
		expectedUsername string
	}{
		{name: "admin", cli: adminCLI, expectedUsername: "admin"},
		{name: "standard user", cli: standardUserCLI, expectedUsername: c.standardUser.ID},
	}

	for _, testCase := range testCases {
		c.Run(testCase.name, func() {
			c.T().Logf("Verifying auth status for identity: %s", testCase.name)
			status, err := testCase.cli.RunJSON(context.Background(), "auth", "status")
			require.NoError(c.T(), err)
			assert.Equal(c.T(), "AuthCheck", status.Kind)
			assert.Contains(c.T(), string(status.Data), testCase.expectedUsername)
		})
	}
}

func (c *CLAITestSuite) TestReadCommands() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	adminCLI, err := c.setupCLIClient(subSession, "admin-test", c.adminToken)
	require.NoError(c.T(), err)

	globalCommands := [][]string{
		{"get", "user", c.standardUser.ID},
		{"get", "setting", "server-version"},
		{"inspect", "user", c.standardUser.Username},
	}

	for _, args := range globalCommands {
		c.Run(args[0]+" "+args[1], func() {
			c.T().Logf("Executing global read command: rancher %s", strings.Join(args, " "))
			envelope, err := adminCLI.RunJSON(context.Background(), args...)
			require.NoError(c.T(), err)
			assert.NotEmpty(c.T(), envelope.Kind)
			assert.NotEmpty(c.T(), envelope.Data)
		})
	}

	for _, target := range c.clusterTargets() {
		c.Run(target.name, func() {
			for _, args := range [][]string{
				{"get", "cluster", target.id},
				{"inspect", "cluster", target.id},
			} {
				c.T().Logf("Executing read command on cluster %s: rancher %s", target.name, strings.Join(args, " "))
				envelope, err := adminCLI.RunJSON(context.Background(), args...)
				require.NoError(c.T(), err)
				require.NotEmpty(c.T(), envelope.Kind)
				require.NotEmpty(c.T(), envelope.Data)
			}
			c.T().Logf("Generating and validating kubeconfig for cluster %s", target.name)
			kubeconfig, err := adminCLI.Run(context.Background(), "kubeconfig", cliapi.ClusterFlag, target.id)
			require.NoError(c.T(), err)
			require.Contains(c.T(), kubeconfig.Stdout, "apiVersion:")
			require.Contains(c.T(), kubeconfig.Stdout, "clusters:")
		})
	}
}

func (c *CLAITestSuite) TestAccessCommands() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	adminCLI, err := c.setupCLIClient(subSession, "admin-test", c.adminToken)
	require.NoError(c.T(), err)
	standardUserCLI, err := c.setupCLIClient(subSession, "standard-user-test", c.standardUserToken)
	require.NoError(c.T(), err)

	for _, target := range c.clusterTargets() {
		c.Run(target.name, func() {
			c.T().Logf("Verifying admin is allowed to read namespaces on cluster %s", target.name)
			allowed, err := adminCLI.RunJSON(context.Background(), "access", "can", "get", "namespaces", cliapi.ClusterFlag, target.id)
			require.NoError(c.T(), err)
			require.Contains(c.T(), string(allowed.Data), "allowed")

			c.T().Logf("Granting standard user cluster-member on cluster %s so it can see the cluster", target.name)
			require.NoError(c.T(), cliapi.GrantClusterRole(adminCLI, target.id, rbac.ClusterMember.String(), c.standardUser.ID))
			kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (bool, error) {
				_, pollErr := standardUserCLI.Run(ctx, "get", "cluster", target.id)
				return pollErr == nil, nil
			})
			require.NoError(c.T(), err, "Standard user CLI did not recognize cluster access in time")

			c.T().Logf("Verifying standard user is denied from deleting nodes on cluster %s", target.name)
			deniedResult, err := standardUserCLI.Run(context.Background(), "access", "can", "delete", "nodes", cliapi.ClusterFlag, target.id, "-o", ranchercli.JSONOutput)
			require.Error(c.T(), err)
			exitCode, ok := ranchercli.ExitCodeOf(err)
			require.True(c.T(), ok)

			require.Contains(c.T(), []int{ranchercli.ExitAccessDenied, ranchercli.ExitRBAC}, exitCode)
			if exitCode == ranchercli.ExitAccessDenied {
				var denied ranchercli.Envelope
				require.NoError(c.T(), json.Unmarshal([]byte(deniedResult.Stdout), &denied))
				require.Contains(c.T(), string(denied.Data), "denied")
			}

			c.T().Logf("Verifying read-only access command structures for cluster %s", target.name)
			readOnlyCommands := [][]string{
				{"access", "list", cliapi.ClusterFlag, target.id, "--namespace", "default"},
				{"access", "who-can", "get", "namespaces", cliapi.ClusterFlag, target.id},
				{"access", "explain", "get", "namespaces", cliapi.ClusterFlag, target.id},
			}
			for _, args := range readOnlyCommands {
				envelope, err := adminCLI.RunJSON(context.Background(), args...)
				require.NoError(c.T(), err)
				require.NotEmpty(c.T(), envelope.Data)
			}
		})
	}
}

func (c *CLAITestSuite) TestAdminProjectAndNamespaceCRUD() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	adminCLI, err := c.setupCLIClient(subSession, "admin-test", c.adminToken)
	require.NoError(c.T(), err)

	for _, target := range c.clusterTargets() {
		c.Run(target.name, func() {
			projectName := namegen.AppendRandomString("cli-project-")
			namespaceName := namegen.AppendRandomString("cli-namespace-")
			updatedDescription := "updated by Rancher CLI validation"

			c.T().Log("Creating and validating a project with the Rancher CLI")
			require.NoError(c.T(), cliapi.CreateProject(adminCLI, target.id, projectName))
			require.NoError(c.T(), cliapi.WaitForResourceData(adminCLI, cliapi.ProjectResource, target.id, projectName, cliapi.OutputContains(projectName)))
			require.NoError(c.T(), cliapi.WaitForResourceListed(adminCLI, cliapi.ProjectResource, target.id, projectName))

			project, err := extprojectapi.GetProjectByName(c.client, target.id, projectName)
			require.NoError(c.T(), err)
			require.Equal(c.T(), projectName, project.Spec.DisplayName)

			patch := fmt.Sprintf(`[{"op":"add","path":"/spec/description","value":%q}]`, updatedDescription)
			patchedProject, err := adminCLI.RunJSON(context.Background(), "patch", "project", projectName, cliapi.ClusterFlag, target.id, "--patch", patch, "--apply")
			require.NoError(c.T(), err)
			require.Equal(c.T(), "Project", patchedProject.Kind)
			require.NoError(c.T(), cliapi.WaitForResourceData(adminCLI, cliapi.ProjectResource, target.id, projectName, cliapi.OutputContains(updatedDescription)))

			project, err = extprojectapi.GetProjectByName(c.client, target.id, projectName)
			require.NoError(c.T(), err)
			require.Equal(c.T(), updatedDescription, project.Spec.Description)

			c.T().Log("Creating a namespace and changing its project membership with the Rancher CLI")
			require.NoError(c.T(), cliapi.CreateNamespace(adminCLI, target.id, projectName, namespaceName))
			require.NoError(c.T(), cliapi.WaitForResourceData(adminCLI, cliapi.NamespaceResource, target.id, namespaceName, cliapi.OutputContains(namespaceName)))
			require.NoError(c.T(), cliapi.WaitForResourceListed(adminCLI, cliapi.NamespaceResource, target.id, namespaceName))
			require.NoError(c.T(), namespaceapi.WaitForProjectIDUpdate(c.client, target.id, project.Name, namespaceName))

			_, err = adminCLI.RunJSON(context.Background(), "project", "remove-namespace", namespaceName, cliapi.ClusterFlag, target.id, "--apply")
			require.NoError(c.T(), err)
			require.NoError(c.T(), namespaceapi.WaitForProjectIDRemoved(c.client, target.id, namespaceName))

			_, err = adminCLI.RunJSON(context.Background(), "project", "add-namespace", namespaceName, "--project", projectName, cliapi.ClusterFlag, target.id, "--apply")
			require.NoError(c.T(), err)
			require.NoError(c.T(), namespaceapi.WaitForProjectIDUpdate(c.client, target.id, project.Name, namespaceName))

			c.T().Log("Deleting the namespace and project with the Rancher CLI")
			require.NoError(c.T(), cliapi.DeleteNamespace(adminCLI, target.id, namespaceName))
			require.NoError(c.T(), extnamespaceapi.WaitForNamespaceDeletion(c.client, target.id, namespaceName))

			require.NoError(c.T(), cliapi.DeleteProject(adminCLI, target.id, projectName))
			require.NoError(c.T(), extprojectapi.WaitForProjectDeletion(c.client, target.id, project.Name))
		})
	}
}

func (c *CLAITestSuite) TestStandardUserNamespaceCRUD() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	adminCLI, err := c.setupCLIClient(subSession, "admin-test", c.adminToken)
	require.NoError(c.T(), err)
	standardUserCLI, err := c.setupCLIClient(subSession, "standard-user-test", c.standardUserToken)
	require.NoError(c.T(), err)

	for _, target := range c.clusterTargets() {
		c.Run(target.name, func() {
			projectName := namegen.AppendRandomString("standard-cli-project-")
			require.NoError(c.T(), cliapi.CreateProject(adminCLI, target.id, projectName))
			require.NoError(c.T(), cliapi.WaitForResource(adminCLI, cliapi.ProjectResource, target.id, projectName))

			project, err := extprojectapi.GetProjectByName(c.client, target.id, projectName)
			require.NoError(c.T(), err)
			require.NoError(c.T(), cliapi.GrantProjectRole(adminCLI, target.id, projectName, rbac.ProjectOwner.String(), c.standardUser.ID))

			prtb, err := rbacapi.GetProjectRoleTemplateBindingForUser(c.client, project, c.standardUser.ID)
			require.NoError(c.T(), err, "Standard user project owner PRTB was not created")

			_, err = extrbac.WaitForProjectRoleTemplateBindingToExist(c.client, prtb.ProjectName, prtb.Namespace, prtb.Name, c.standardUser.ID)
			require.NoError(c.T(), err, "Standard user project owner API permissions did not propagate")

			err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (bool, error) {
				_, pollErr := standardUserCLI.Run(ctx, "get", "project", projectName, cliapi.ClusterFlag, target.id)
				return pollErr == nil, nil
			})
			require.NoError(c.T(), err, "Standard user CLI did not recognize project access in time")

			c.T().Log("Creating and deleting a namespace as a standard project owner")
			namespaceName := namegen.AppendRandomString("standard-cli-namespace-")
			require.NoError(c.T(), cliapi.CreateNamespace(standardUserCLI, target.id, project.Name, namespaceName))
			require.NoError(c.T(), cliapi.WaitForResourceData(standardUserCLI, cliapi.NamespaceResource, target.id, namespaceName, cliapi.OutputContains(namespaceName)))

			_, err = extnamespaceapi.GetNamespaceByName(c.client, target.id, namespaceName)
			require.NoError(c.T(), err)

			require.NoError(c.T(), cliapi.DeleteNamespaceAsStandardUser(standardUserCLI, target.id, namespaceName))
			require.NoError(c.T(), extnamespaceapi.WaitForNamespaceDeletion(c.client, target.id, namespaceName))

			c.T().Log("Verifying the standard user cannot mutate a project outside its scope")
			outOfScopeProjectName := namegen.AppendRandomString("out-of-scope-cli-project-")
			require.NoError(c.T(), cliapi.CreateProject(adminCLI, target.id, outOfScopeProjectName))
			require.NoError(c.T(), cliapi.WaitForResource(adminCLI, cliapi.ProjectResource, target.id, outOfScopeProjectName))

			deniedNamespaceName := namegen.AppendRandomString("denied-cli-namespace-")
			deniedResult, deniedErr := standardUserCLI.Run(context.Background(), "create", "namespace", deniedNamespaceName, cliapi.ClusterFlag, target.id, "--project", outOfScopeProjectName, "--apply", "-o", ranchercli.JSONOutput)
			require.Error(c.T(), deniedErr)

			exitCode, ok := ranchercli.ExitCodeOf(deniedErr)
			require.True(c.T(), ok)
			require.Contains(c.T(), []int{ranchercli.ExitNotFound, ranchercli.ExitRBAC}, exitCode)
			require.NotEmpty(c.T(), deniedResult.Stderr)
		})
	}
}

func TestCLAITestSuite(t *testing.T) {
	suite.Run(t, new(CLAITestSuite))
}
