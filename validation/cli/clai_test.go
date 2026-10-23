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
	extrbacapi "github.com/rancher/shepherd/extensions/kubeapi/rbac"
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
	client             *rancher.Client
	bindingClient      *rancher.Client
	session            *session.Session
	adminCLI           *ranchercli.CLIClient
	standardUser       *management.User
	standardUserClient *rancher.Client
	standardUserCLI    *ranchercli.CLIClient
	downstreamCluster  *management.Cluster
}

type clusterTarget struct {
	name string
	id   string
}

func (c *CLAITestSuite) clusterTargets() []clusterTarget {
	return []clusterTarget{
		{name: "local", id: extclusterapi.LocalCluster},
		{name: "downstream", id: c.downstreamCluster.ID},
	}
}

// waitForPRTBPropagation waits for the backend API to recognize the standard user's project access
func (c *CLAITestSuite) waitForPRTBPropagation(projectID string) error {
	return kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := c.standardUserClient.Management.Project.ByID(projectID)
		return err == nil, nil
	})
}

func (c *CLAITestSuite) SetupSuite() {
	c.session = session.NewSession()

	rancherConfig := new(rancher.Config)
	shepherdconfig.LoadConfig(rancher.ConfigurationFileKey, rancherConfig)
	rancherConfig.RancherCLI = false

	adminCLI, err := ranchercli.NewCLIClient(ranchercli.CLIOptions{
		Name:    "admin-test",
		Host:    rancherConfig.Host,
		Token:   rancherConfig.AdminToken,
		CAFile:  rancherConfig.CAFile,
		CACerts: rancherConfig.CACerts,
		Session: c.session,
	})
	require.NoError(c.T(), err)
	c.adminCLI = adminCLI

	// Wrangler needs a CA file path, so reuse the one the CLI client resolved from rancher.caCerts.
	rancherConfig.CAFile = adminCLI.CAFile

	client, err := rancher.NewClientForConfig("", rancherConfig, c.session)
	require.NoError(c.T(), err)
	c.client = client
	bindingClient := *client
	bindingClient.Session = nil
	c.bindingClient = &bindingClient

	clusterName := c.client.RancherConfig.ClusterName
	require.NotEmpty(c.T(), clusterName, "rancher.clusterName must identify an existing downstream cluster")
	downstreamClusterID, err := clusters.GetClusterIDByName(c.client, clusterName)
	require.NoError(c.T(), err, "failed to resolve downstream cluster %q", clusterName)
	require.NotEqual(c.T(), extclusterapi.LocalCluster, downstreamClusterID, "rancher.clusterName must identify a downstream cluster")
	c.downstreamCluster, err = c.client.Management.Cluster.ByID(downstreamClusterID)
	require.NoError(c.T(), err, "failed to get downstream cluster %q", clusterName)

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

	c.standardUserCLI, err = ranchercli.NewCLIClient(ranchercli.CLIOptions{
		Name:    "standard-user-test",
		Host:    rancherConfig.Host,
		Token:   standardUserToken.Token,
		CAFile:  adminCLI.CAFile,
		Session: c.session,
	})
	require.NoError(c.T(), err)
}

func (c *CLAITestSuite) TearDownSuite() {
	if c.session != nil {
		c.session.Cleanup()
	}
}

func (c *CLAITestSuite) TestSchemaAndConfiguration() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	c.T().Log("Verifying CLI schema output")
	schema, err := c.adminCLI.RunJSON(context.Background(), "schema")
	require.NoError(c.T(), err)
	require.Equal(c.T(), "Schema", schema.Kind)
	require.Contains(c.T(), string(schema.Data), "access can")
	require.Contains(c.T(), string(schema.Data), "config status")
	require.Contains(c.T(), string(schema.Data), "create project")

	c.T().Log("Verifying CLI configuration path and server list")
	pathResult, err := c.adminCLI.Run(context.Background(), "config", "path")
	require.NoError(c.T(), err)
	require.Contains(c.T(), pathResult.Stdout, c.adminCLI.ConfigDirectory)

	servers, err := c.adminCLI.RunJSON(context.Background(), "config", "list-servers")
	require.NoError(c.T(), err)
	require.NotEmpty(c.T(), servers.Data)
	require.Contains(c.T(), string(servers.Data), c.adminCLI.Name)

	c.T().Log("Verifying CLI config status")
	status, err := c.adminCLI.RunJSON(context.Background(), "config", "status")
	require.NoError(c.T(), err)
	require.NotEmpty(c.T(), status.Data)
}

func (c *CLAITestSuite) TestAuthentication() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	testCases := []struct {
		name             string
		cli              *ranchercli.CLIClient
		expectedUsername string
	}{
		{name: "admin", cli: c.adminCLI, expectedUsername: "admin"},
		{name: "standard user", cli: c.standardUserCLI, expectedUsername: c.standardUser.ID},
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

	globalCommands := [][]string{
		{"get", "user", c.standardUser.ID},
		{"get", "setting", "server-version"},
		{"inspect", "user", c.standardUser.Username},
	}

	for _, args := range globalCommands {
		c.Run(args[0]+" "+args[1], func() {
			c.T().Logf("Executing global read command: rancher %s", strings.Join(args, " "))
			envelope, err := c.adminCLI.RunJSON(context.Background(), args...)
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
				envelope, err := c.adminCLI.RunJSON(context.Background(), args...)
				require.NoError(c.T(), err)
				require.NotEmpty(c.T(), envelope.Kind)
				require.NotEmpty(c.T(), envelope.Data)
			}
			c.T().Logf("Generating and validating kubeconfig for cluster %s", target.name)
			kubeconfig, err := c.adminCLI.Run(context.Background(), "kubeconfig", "--cluster", target.id)
			require.NoError(c.T(), err)
			require.Contains(c.T(), kubeconfig.Stdout, "apiVersion:")
			require.Contains(c.T(), kubeconfig.Stdout, "clusters:")
		})
	}
}

func (c *CLAITestSuite) TestAccessCommands() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	for _, target := range c.clusterTargets() {
		c.Run(target.name, func() {
			c.T().Logf("Verifying admin is allowed to read namespaces on cluster %s", target.name)
			allowed, err := c.adminCLI.RunJSON(context.Background(), "access", "can", "get", "namespaces", "--cluster", target.id)
			require.NoError(c.T(), err)
			require.Contains(c.T(), string(allowed.Data), "allowed")

			c.T().Logf("Verifying standard user is denied from deleting nodes on cluster %s", target.name)
			deniedResult, err := c.standardUserCLI.Run(context.Background(), "access", "can", "delete", "nodes", "--cluster", target.id, "-o", ranchercli.JSONOutput)
			require.Error(c.T(), err)
			exitCode, ok := ranchercli.ExitCodeOf(err)
			require.True(c.T(), ok)

			// Local cluster may allow the user to ask and return 12 (Denied).
			// Downstream clusters without synced users may return 6 (Forbidden).
			require.Contains(c.T(), []int{ranchercli.ExitAccessDenied, ranchercli.ExitRBAC}, exitCode)
			// If the API allowed the question (12), verify the structured JSON response says "denied"
			if exitCode == ranchercli.ExitAccessDenied {
				var denied ranchercli.Envelope
				require.NoError(c.T(), json.Unmarshal([]byte(deniedResult.Stdout), &denied))
				require.Contains(c.T(), string(denied.Data), "denied")
			}

			c.T().Logf("Verifying read-only access command structures for cluster %s", target.name)
			readOnlyCommands := [][]string{
				{"access", "list", "--cluster", target.id, "--namespace", "default"},
				{"access", "who-can", "get", "namespaces", "--cluster", target.id},
				{"access", "explain", "get", "namespaces", "--cluster", target.id},
			}
			for _, args := range readOnlyCommands {
				envelope, err := c.adminCLI.RunJSON(context.Background(), args...)
				require.NoError(c.T(), err)
				require.NotEmpty(c.T(), envelope.Data)
			}
		})
	}
}

func (c *CLAITestSuite) TestAdminProjectAndNamespaceCRUD() {
	for _, target := range c.clusterTargets() {
		c.Run(target.name, func() {
			testSession := c.session.NewSession()
			defer testSession.Cleanup()

			projectName := namegen.AppendRandomString("cli-project-")
			namespaceName := namegen.AppendRandomString("cli-namespace-")
			updatedDescription := "updated by Rancher CLI validation"

			c.T().Log("Creating and validating a project with the Rancher CLI")
			require.NoError(c.T(), cliapi.CreateProject(c.adminCLI, testSession, c.client, target.id, projectName))
			require.NoError(c.T(), cliapi.WaitForResourceData(c.adminCLI, cliapi.ProjectResource, target.id, projectName, outputContains(projectName)))
			require.NoError(c.T(), cliapi.WaitForResourceListed(c.adminCLI, cliapi.ProjectResource, target.id, projectName))

			project, err := extprojectapi.GetProjectByName(c.client, target.id, projectName)
			require.NoError(c.T(), err)
			require.Equal(c.T(), projectName, project.Spec.DisplayName)

			patch := fmt.Sprintf(`[{"op":"add","path":"/spec/description","value":%q}]`, updatedDescription)
			patchedProject, err := c.adminCLI.RunJSON(context.Background(), "patch", "project", projectName, "--cluster", target.id, "--patch", patch, "--apply")
			require.NoError(c.T(), err)
			require.Equal(c.T(), "Project", patchedProject.Kind)
			require.NoError(c.T(), cliapi.WaitForResourceData(c.adminCLI, cliapi.ProjectResource, target.id, projectName, outputContains(updatedDescription)))

			project, err = extprojectapi.GetProjectByName(c.client, target.id, projectName)
			require.NoError(c.T(), err)
			require.Equal(c.T(), updatedDescription, project.Spec.Description)

			c.T().Log("Creating a namespace and changing its project membership with the Rancher CLI")
			require.NoError(c.T(), cliapi.CreateNamespace(c.adminCLI, testSession, c.client, target.id, projectName, namespaceName))
			require.NoError(c.T(), cliapi.WaitForResourceData(c.adminCLI, cliapi.NamespaceResource, target.id, namespaceName, outputContains(namespaceName)))
			require.NoError(c.T(), cliapi.WaitForResourceListed(c.adminCLI, cliapi.NamespaceResource, target.id, namespaceName))
			require.NoError(c.T(), namespaceapi.WaitForProjectIDUpdate(c.client, target.id, project.Name, namespaceName))

			// The CLI has no command that reports a namespace's project membership, so membership is verified through the Rancher API.
			_, err = c.adminCLI.RunJSON(context.Background(), "project", "remove-namespace", namespaceName, "--project", projectName, "--cluster", target.id, "--apply")
			require.NoError(c.T(), err)
			require.NoError(c.T(), namespaceapi.WaitForProjectIDRemoved(c.client, target.id, namespaceName))

			_, err = c.adminCLI.RunJSON(context.Background(), "project", "add-namespace", namespaceName, "--project", projectName, "--cluster", target.id, "--apply")
			require.NoError(c.T(), err)
			require.NoError(c.T(), namespaceapi.WaitForProjectIDUpdate(c.client, target.id, project.Name, namespaceName))

			c.T().Log("Deleting the namespace and project with the Rancher CLI")
			require.NoError(c.T(), cliapi.DeleteNamespace(c.adminCLI, target.id, namespaceName))
			require.NoError(c.T(), extnamespaceapi.WaitForNamespaceDeletion(c.client, target.id, namespaceName))

			require.NoError(c.T(), cliapi.DeleteProject(c.adminCLI, target.id, projectName))
			require.NoError(c.T(), extprojectapi.WaitForProjectDeletion(c.client, target.id, project.Name))
		})
	}
}

func (c *CLAITestSuite) TestStandardUserNamespaceCRUD() {
	// Skipping until https://github.com/rancher/rancher-cli/issues/34 is fixed: standard users cannot reach their own downstream cluster through the CLI.
	c.T().Skip("standard users cannot access a downstream cluster via the Rancher CLI; see https://github.com/rancher/rancher-cli/issues/34")
	for _, target := range c.clusterTargets() {
		c.Run(target.name, func() {
			subSession := c.session.NewSession()
			defer subSession.Cleanup()

			projectName := namegen.AppendRandomString("standard-cli-project-")
			require.NoError(c.T(), cliapi.CreateProject(c.adminCLI, subSession, c.client, target.id, projectName))
			require.NoError(c.T(), cliapi.WaitForResource(c.adminCLI, cliapi.ProjectResource, target.id, projectName))

			project, err := extprojectapi.GetProjectByName(c.client, target.id, projectName)
			require.NoError(c.T(), err)

			projectRoleBinding, err := rbacapi.CreateProjectRoleTemplateBinding(c.bindingClient, c.standardUser.ID, project, rbac.ProjectOwner.String())
			require.NoError(c.T(), err)

			// Runs before the project cleanup registered on subSession, which also removes the project-owned bindings.
			defer func() {
				if err := extrbacapi.DeleteProjectRoleTemplateBinding(c.client, projectRoleBinding.Namespace, projectRoleBinding.Name, true); err != nil {
					c.T().Error(err)
				}
			}()

			err = c.waitForPRTBPropagation(target.id + ":" + project.Name)
			require.NoError(c.T(), err, "Standard user project owner API permissions did not propagate")

			err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (bool, error) {
				_, pollErr := c.standardUserCLI.Run(ctx, "get", "project", project.Name, "--cluster", target.id)
				return pollErr == nil, nil
			})
			require.NoError(c.T(), err, "Standard user CLI did not recognize project access in time")

			c.T().Log("Creating and deleting a namespace as a standard project owner")
			namespaceName := namegen.AppendRandomString("standard-cli-namespace-")
			require.NoError(c.T(), cliapi.CreateNamespace(c.standardUserCLI, subSession, c.client, target.id, project.Name, namespaceName))
			require.NoError(c.T(), cliapi.WaitForResourceData(c.standardUserCLI, cliapi.NamespaceResource, target.id, namespaceName, outputContains(namespaceName)))

			_, err = extnamespaceapi.GetNamespaceByName(c.client, target.id, namespaceName)
			require.NoError(c.T(), err)

			require.NoError(c.T(), cliapi.DeleteNamespace(c.standardUserCLI, target.id, namespaceName))
			require.NoError(c.T(), extnamespaceapi.WaitForNamespaceDeletion(c.client, target.id, namespaceName))

			c.T().Log("Verifying the standard user cannot mutate a project outside its scope")
			outOfScopeProjectName := namegen.AppendRandomString("out-of-scope-cli-project-")
			require.NoError(c.T(), cliapi.CreateProject(c.adminCLI, subSession, c.client, target.id, outOfScopeProjectName))
			require.NoError(c.T(), cliapi.WaitForResource(c.adminCLI, cliapi.ProjectResource, target.id, outOfScopeProjectName))

			deniedNamespaceName := namegen.AppendRandomString("denied-cli-namespace-")
			deniedResult, deniedErr := c.standardUserCLI.Run(context.Background(), "create", "namespace", deniedNamespaceName, "--cluster", target.id, "--project", outOfScopeProjectName, "--apply", "-o", ranchercli.JSONOutput)
			require.Error(c.T(), deniedErr)

			exitCode, ok := ranchercli.ExitCodeOf(deniedErr)
			require.True(c.T(), ok)
			require.Contains(c.T(), []int{ranchercli.ExitNotFound, ranchercli.ExitRBAC}, exitCode)
			require.NotEmpty(c.T(), deniedResult.Stderr)
		})
	}
}

// outputContains returns a matcher for polling CLI output data for a value.
func outputContains(value string) func(string) bool {
	return func(data string) bool {
		return strings.Contains(data, value)
	}
}

func TestCLAITestSuite(t *testing.T) {
	suite.Run(t, new(CLAITestSuite))
}
