// Package ansible provides helpers for driving Ansible playbooks from Go tests
// in the qa-infra-automation integration.
package ansible

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

// runner abstracts command execution for testability.
type runner func(name string, args []string, dir string, env []string) ([]byte, error)

// defaultRunner executes a command and returns combined stdout+stderr.
func defaultRunner(name string, args []string, dir string, env []string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		// Inherit the current process environment and append extras.
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("command %q %v failed: %w\noutput:\n%s", name, args, err, string(out))
	}
	return out, nil
}

// Client drives Ansible playbooks for a specific qa-infra-automation repository.
type Client struct {
	// repoPath is the absolute path to the rancher-qa-infra-automation repository.
	repoPath string
	// run is the command runner (replaceable for testing).
	run runner
}

// NewClient creates a new Ansible client for the given repo root.
func NewClient(repoPath string) *Client {
	return &Client{
		repoPath: repoPath,
		run:      defaultRunner,
	}
}

// AddSSHKey adds the given private key to the running ssh-agent so Ansible can
// connect to nodes without prompting for a passphrase.
func (c *Client) AddSSHKey(privateKeyPath string) error {
	logrus.Infof("[ansible] ssh-add %s", privateKeyPath)
	out, err := c.run("ssh-add", []string{privateKeyPath}, c.repoPath, nil)
	if err != nil {
		return fmt.Errorf("ssh-add: %w", err)
	}
	logrus.Debugf("[ansible] ssh-add output:\n%s", string(out))
	return nil
}

// GenerateInventory renders an inventory file from the template by substituting
// the provided environment variables using envsubst (GNU gettext).
//
// templatePath is relative to the repo root (e.g. "ansible/rke2/default/inventory-template.yml").
// outputPath is the destination path for the rendered inventory (relative or absolute).
// env is a map of variable name → value to substitute (e.g. {"TERRAFORM_NODE_SOURCE": "tofu/harvester/modules/vm", "TF_WORKSPACE": "mytest"}).
func (c *Client) GenerateInventory(templatePath, outputPath string, env map[string]string) error {
	absTemplate := filepath.Join(c.repoPath, templatePath)
	absOutput := outputPath
	if !filepath.IsAbs(outputPath) {
		absOutput = filepath.Join(c.repoPath, outputPath)
	}

	logrus.Infof("[ansible] generating inventory from %s → %s", absTemplate, absOutput)

	templateBytes, err := os.ReadFile(absTemplate)
	if err != nil {
		return fmt.Errorf("read inventory template %s: %w", absTemplate, err)
	}

	rendered := string(templateBytes)
	for k, v := range env {
		rendered = strings.ReplaceAll(rendered, "$"+k, v)
	}

	if err := os.WriteFile(absOutput, []byte(rendered), 0644); err != nil {
		return fmt.Errorf("write inventory to %s: %w", absOutput, err)
	}
	return nil
}

// WriteVarsYAML writes an Ansible vars file (YAML) to the given path (relative to repo root).
// vars is marshalled to YAML and written to the destination.
func (c *Client) WriteVarsYAML(relPath string, vars map[string]string) error {
	absPath := filepath.Join(c.repoPath, relPath)
	logrus.Infof("[ansible] writing vars file %s", absPath)

	data, err := yaml.Marshal(vars)
	if err != nil {
		return fmt.Errorf("marshal vars YAML: %w", err)
	}

	if err := os.WriteFile(absPath, data, 0644); err != nil {
		return fmt.Errorf("write vars file %s: %w", absPath, err)
	}
	return nil
}

// RunPlaybook runs the given playbook with the provided inventory file.
// All entries in extraEnv are added to the subprocess environment (KEY=VALUE format).
// playbookPath and inventoryPath are relative to the repo root.
func (c *Client) RunPlaybook(playbookPath, inventoryPath string, extraEnv []string) error {
	absPlaybook := filepath.Join(c.repoPath, playbookPath)
	absInventory := filepath.Join(c.repoPath, inventoryPath)

	logrus.Infof("[ansible] running playbook %s with inventory %s", absPlaybook, absInventory)

	args := []string{
		absPlaybook,
		"-i", absInventory,
	}

	out, err := c.run("ansible-playbook", args, c.repoPath, extraEnv)
	if err != nil {
		return fmt.Errorf("ansible-playbook %s: %w", playbookPath, err)
	}
	logrus.Debugf("[ansible] playbook output:\n%s", string(out))
	return nil
}
