package cli

import (
	ranchercli "github.com/rancher/shepherd/clients/ranchercli"
)

const (
	context    = "context"
	login      = "login"
	namespaces = "namespaces"
	projects   = "projects"
	rancher    = "rancher"
)

// Login will log into the Rancher server using the provided URL and token.
func Login(client *ranchercli.Client, url, token string) error {
	err := client.ExecuteCommand(rancher, login, url, "--token", token)
	if err != nil {
		return err
	}

	return nil
}

// SwitchContext will display the current context and switch to the default one.
func SwitchContext(client *ranchercli.Client, project string) error {
	err := client.ExecuteCommand(rancher, context, "switch", project)
	if err != nil {
		return err
	}

	return nil
}

// CreateProjects will create and projects in the specified cluster.
func CreateProjects(client *ranchercli.Client, projectName, cluster string) error {
	err := client.ExecuteCommand(rancher, projects, "create", "--cluster", cluster, projectName)
	if err != nil {
		return err
	}

	err = client.Exists(rancher, projects, projectName)
	if err != nil {
		return err
	}

	return nil
}

// DeleteProjects will delete projects in the specified cluster.
func DeleteProjects(client *ranchercli.Client, projectName string) error {
	err := client.Delete(projects, projectName)
	if err != nil {
		return err
	}

	err = client.ExecuteCommand(rancher, projects, "ls", "|", "grep", projectName)
	if err != nil {
		return err
	}

	return nil
}

// CreateNamespaces will create namespaces in the specified cluster.
func CreateNamespaces(client *ranchercli.Client, cluster, namespaceName string) error {
	err := client.ExecuteCommand(rancher, namespaces, "create", namespaceName)
	if err != nil {
		return err
	}

	err = client.Exists(rancher, namespaces, namespaceName)
	if err != nil {
		return err
	}

	return nil
}

// DeleteNamespaces will delete namespaces in the specified cluster.
func DeleteNamespaces(client *ranchercli.Client, namespaceName string) error {
	err := client.Delete(namespaces, namespaceName)
	if err != nil {
		return err
	}

	return nil
}
