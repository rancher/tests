package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/ranchercli"
	"github.com/rancher/shepherd/extensions/defaults"
	extnamespaceapi "github.com/rancher/shepherd/extensions/kubeapi/namespaces"
	extprojectapi "github.com/rancher/shepherd/extensions/kubeapi/projects"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/sirupsen/logrus"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	// ProjectResource and NamespaceResource are the resource names accepted by the CLI get, create and delete commands.
	ProjectResource   = "project"
	NamespaceResource = "namespace"

	projectKind   = "Project"
	namespaceKind = "Namespace"

	createCommand = "create"
	deleteCommand = "delete"
	getCommand    = "get"
	clusterFlag   = "--cluster"
	projectFlag   = "--project"
	applyFlag     = "--apply"
	yesFlag       = "--yes"
)

// listResource maps a resource to the plural name its CLI list command uses.
var listResource = map[string]string{
	ProjectResource:   "projects",
	NamespaceResource: "namespaces",
}

// CreateProject creates a project through the CLI and registers a session cleanup that deletes it through the Rancher API.
func CreateProject(cli *ranchercli.CLIClient, testSession *session.Session, client *rancher.Client, clusterID, projectName string) error {
	logrus.Infof("Running: rancher %s %s %s %s %s %s", createCommand, ProjectResource, projectName, clusterFlag, clusterID, applyFlag)
	envelope, err := cli.RunJSON(context.Background(), createCommand, ProjectResource, projectName, clusterFlag, clusterID, applyFlag)
	if err != nil {
		return fmt.Errorf("failed to create project %q with the Rancher CLI: %w", projectName, err)
	}

	testSession.RegisterCleanupFunc(func() error {
		return extprojectapi.DeleteProject(client, clusterID, projectName, true)
	})

	if envelope.Kind != projectKind {
		return fmt.Errorf("unexpected kind %q after creating project %q", envelope.Kind, projectName)
	}

	return nil
}

// CreateNamespace creates a namespace in a project through the CLI and registers a session cleanup that deletes it through the Rancher API.
func CreateNamespace(cli *ranchercli.CLIClient, testSession *session.Session, client *rancher.Client, clusterID, projectName, namespaceName string) error {
	logrus.Infof("Running: rancher %s %s %s %s %s %s %s %s", createCommand, NamespaceResource, namespaceName, clusterFlag, clusterID, projectFlag, projectName, applyFlag)
	envelope, err := cli.RunJSON(context.Background(), createCommand, NamespaceResource, namespaceName, clusterFlag, clusterID, projectFlag, projectName, applyFlag)
	if err != nil {
		return fmt.Errorf("failed to create namespace %q with the Rancher CLI: %w", namespaceName, err)
	}

	testSession.RegisterCleanupFunc(func() error {
		return extnamespaceapi.DeleteNamespace(client, clusterID, namespaceName, true)
	})

	if envelope.Kind != namespaceKind {
		return fmt.Errorf("unexpected kind %q after creating namespace %q", envelope.Kind, namespaceName)
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
	logrus.Infof("Polling: rancher %s %s %s %s %s until the CLI reports not found", getCommand, resource, name, clusterFlag, clusterID)
	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := cli.Run(ctx, getCommand, resource, name, clusterFlag, clusterID)
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
	logrus.Infof("Running: rancher %s %s %s %s %s %s %s", deleteCommand, resource, name, clusterFlag, clusterID, applyFlag, yesFlag)
	_, err := cli.Run(context.Background(), deleteCommand, resource, name, clusterFlag, clusterID, applyFlag, yesFlag)
	if err != nil {
		return fmt.Errorf("failed to delete %s %q with the Rancher CLI: %w", resource, name, err)
	}

	if err := WaitForResourceDeleted(cli, resource, clusterID, name); err != nil {
		return err
	}

	return WaitForResourceUnlisted(cli, resource, clusterID, name)
}

func getArgs(resource, name, clusterID string) []string {
	return []string{getCommand, resource, name, clusterFlag, clusterID}
}

func listArgs(resource, clusterID string) []string {
	return []string{getCommand, listResource[resource], clusterFlag, clusterID}
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
