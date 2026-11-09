package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/ranchercli"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/sirupsen/logrus"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	ProjectResource   = "project"
	NamespaceResource = "namespace"
	ProjectKind       = "Project"
	NamespaceKind     = "Namespace"
	CreateCommand     = "create"
	DeleteCommand     = "delete"
	GetCommand        = "get"
	GrantCommand      = "grant"
	ClusterFlag   	  = "--cluster"
	RoleFlag      	  = "--role"
	ProjectFlag       = "--project"
	ApplyFlag         = "--apply"
	YesFlag           = "--yes"
	OutputFlag        = "-o"
)

// CLAISettings reads the CLAI opt-in flag; rancherCLI stays reserved for the legacy CLI.
type CLAISettings struct {
	RancherCLAI bool `yaml:"rancherCLAI" json:"rancherCLAI"`
}

// listResource maps a resource to the plural name its CLI list command uses.
var listResource = map[string]string{
	ProjectResource:   rbacapi.ProjectResource,
	NamespaceResource: rbacapi.NamespaceResource,
}

// CreateProject creates a project through the CLI
func CreateProject(cli *ranchercli.CLIClient, clusterID, projectName string) error {
	logrus.Infof("Running: rancher %s %s %s %s %s %s", CreateCommand, ProjectResource, projectName, ClusterFlag, clusterID, ApplyFlag)
	result, err := cli.Create(context.Background(), ranchercli.Resource{
		Kind:       ProjectResource,
		Name:       projectName,
		CreateArgs: []string{ClusterFlag, clusterID, ApplyFlag, OutputFlag, ranchercli.JSONOutput},
		DeleteArgs: []string{ClusterFlag, clusterID, ApplyFlag},
	})
	if err != nil {
		return fmt.Errorf("failed to create project %q with the Rancher CLI: %w", projectName, err)
	}

	return checkCreatedKind(result, ProjectKind, projectName)
}

// CreateNamespace creates a namespace in a project through the CLI
func CreateNamespace(cli *ranchercli.CLIClient, clusterID, projectName, namespaceName string) error {
	logrus.Infof("Running: rancher %s %s %s %s %s %s %s %s", CreateCommand, NamespaceResource, namespaceName, ClusterFlag, clusterID, ProjectFlag, projectName, ApplyFlag)
	result, err := cli.Create(context.Background(), ranchercli.Resource{
		Kind:       NamespaceResource,
		Name:       namespaceName,
		CreateArgs: []string{ClusterFlag, clusterID, ProjectFlag, projectName, ApplyFlag, OutputFlag, ranchercli.JSONOutput},
		DeleteArgs: []string{ClusterFlag, clusterID, ApplyFlag},
	})
	if err != nil {
		return fmt.Errorf("failed to create namespace %q with the Rancher CLI: %w", namespaceName, err)
	}

	return checkCreatedKind(result, NamespaceKind, namespaceName)
}

// GrantClusterRole grants a cluster role to a subject through the CLI
func GrantClusterRole(cli *ranchercli.CLIClient, clusterID, roleID, subject string) error {
	args := []string{RoleFlag, roleID, ClusterFlag, clusterID, ApplyFlag, YesFlag, subject}
	logrus.Infof("Running: rancher %s %s", GrantCommand, strings.Join(args, " "))
	if _, err := cli.Grant(context.Background(), args...); err != nil {
		return fmt.Errorf("failed to grant role %q on cluster %q to %q with the Rancher CLI: %w", roleID, clusterID, subject, err)
	}

	return nil
}

// GrantProjectRole grants a project role to a subject through the CLI
func GrantProjectRole(cli *ranchercli.CLIClient, clusterID, projectName, roleID, subject string) error {
	args := []string{RoleFlag, roleID, ProjectFlag, projectName, ClusterFlag, clusterID, ApplyFlag, YesFlag, subject}
	logrus.Infof("Running: rancher %s %s", GrantCommand, strings.Join(args, " "))
	if _, err := cli.Grant(context.Background(), args...); err != nil {
		return fmt.Errorf("failed to grant role %q on project %q to %q with the Rancher CLI: %w", roleID, projectName, subject, err)
	}

	return nil
}

// checkCreatedKind verifies that the JSON envelope the CLI printed for a created resource has the expected kind
func checkCreatedKind(result ranchercli.Result, wantKind, name string) error {
	envelope := &ranchercli.Envelope{}
	if err := json.Unmarshal([]byte(result.Stdout), envelope); err != nil {
		return fmt.Errorf("decode CLI envelope after creating %q: %w", name, err)
	}

	if envelope.Kind != wantKind {
		return fmt.Errorf("unexpected kind %q after creating %q", envelope.Kind, name)
	}

	return nil
}

// DeleteProject deletes a project through the CLI and waits until the CLI reports it as not found.
func DeleteProject(cli *ranchercli.CLIClient, clusterID, projectName string) error {
	return deleteResource(cli, ProjectResource, clusterID, projectName)
}

// DeleteNamespace deletes a namespace through the CLI and waits until the CLI reports it as not found.
func DeleteNamespace(cli *ranchercli.CLIClient, clusterID, namespaceName string) error {
	return deleteResource(cli, NamespaceResource, clusterID, namespaceName)
}

// DeleteNamespaceAsStandardUser deletes a namespace through the CLI and waits until the CLI reports it as not found.
// It skips the cluster-scoped list check, which standard users are not allowed to perform.
func DeleteNamespaceAsStandardUser(cli *ranchercli.CLIClient, clusterID, namespaceName string) error {
	if err := runDelete(cli, NamespaceResource, clusterID, namespaceName); err != nil {
		return err
	}

	return WaitForResourceDeleted(cli, NamespaceResource, clusterID, namespaceName)
}

// WaitForResource waits until the CLI can get a resource, tolerating only the CLI not-found exit code.
func WaitForResource(cli *ranchercli.CLIClient, resource, clusterID, name string) error {
	return pollCommand(cli, getArgs(resource, name, clusterID), func(*ranchercli.Envelope) bool { return true })
}

// WaitForResourceData waits until the data the CLI returns for a resource satisfies matches.
func WaitForResourceData(cli *ranchercli.CLIClient, resource, clusterID, name string, matches func(data string) bool) error {
	return pollCommand(cli, getArgs(resource, name, clusterID), func(envelope *ranchercli.Envelope) bool {
		return matches(string(envelope.Data))
	})
}

// WaitForResourceListed waits until the CLI list command (for example rancher get projects) includes the named resource.
func WaitForResourceListed(cli *ranchercli.CLIClient, resource, clusterID, name string) error {
	return pollCommand(cli, listArgs(resource, clusterID), func(envelope *ranchercli.Envelope) bool {
		return strings.Contains(string(envelope.Data), name)
	})
}

// WaitForResourceUnlisted waits until the CLI list command no longer includes the named resource.
func WaitForResourceUnlisted(cli *ranchercli.CLIClient, resource, clusterID, name string) error {
	return pollCommand(cli, listArgs(resource, clusterID), func(envelope *ranchercli.Envelope) bool {
		return !strings.Contains(string(envelope.Data), name)
	})
}

// WaitForResourceDeleted waits until the CLI reports a resource as not found.
func WaitForResourceDeleted(cli *ranchercli.CLIClient, resource, clusterID, name string) error {
	logrus.Infof("Polling: rancher %s %s %s %s %s until the CLI reports not found", GetCommand, resource, name, ClusterFlag, clusterID)
	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := cli.Run(ctx, GetCommand, resource, name, ClusterFlag, clusterID)
		if ranchercli.IsExitCode(err, ranchercli.ExitNotFound) {
			return true, nil
		}

		return false, err
	})
	if err != nil {
		return fmt.Errorf("rancher CLI still returns %s %q: %w", resource, name, err)
	}

	return nil
}

// deleteResource deletes through the CLI and then confirms with the CLI itself that the resource is gone.
func deleteResource(cli *ranchercli.CLIClient, resource, clusterID, name string) error {
	if err := runDelete(cli, resource, clusterID, name); err != nil {
		return err
	}

	if err := WaitForResourceDeleted(cli, resource, clusterID, name); err != nil {
		return err
	}

	return WaitForResourceUnlisted(cli, resource, clusterID, name)
}

// runDelete issues the CLI delete command without waiting for the resource to disappear.
func runDelete(cli *ranchercli.CLIClient, resource, clusterID, name string) error {
	logrus.Infof("Running: rancher %s %s %s %s %s %s %s", DeleteCommand, resource, name, ClusterFlag, clusterID, ApplyFlag, YesFlag)
	_, err := cli.Run(context.Background(), DeleteCommand, resource, name, ClusterFlag, clusterID, ApplyFlag, YesFlag)
	if err != nil {
		return fmt.Errorf("failed to delete %s %q with the Rancher CLI: %w", resource, name, err)
	}

	return nil
}

func getArgs(resource, name, clusterID string) []string {
	return []string{GetCommand, resource, name, ClusterFlag, clusterID}
}

func listArgs(resource, clusterID string) []string {
	return []string{GetCommand, listResource[resource], ClusterFlag, clusterID}
}

// pollCommand retries a CLI get or list command until check passes, treating only a not-found exit code as retryable.
func pollCommand(cli *ranchercli.CLIClient, args []string, check func(*ranchercli.Envelope) bool) error {
	logrus.Infof("Polling: rancher %s", strings.Join(args, " "))
	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		envelope, err := cli.RunJSON(ctx, args...)
		if ranchercli.IsExitCode(err, ranchercli.ExitNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}

		return check(envelope), nil
	})
	if err != nil {
		return fmt.Errorf("rancher CLI did not return the expected output for 'rancher %s': %w", strings.Join(args, " "), err)
	}

	return nil
}
