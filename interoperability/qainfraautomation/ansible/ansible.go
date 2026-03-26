package ansible

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

func run(name string, args []string, dir string, env []string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("command %q %v failed: %w\noutput:\n%s", name, args, err, string(out))
	}

	return out, nil
}

// Client runs ansible commands within a local repository checkout.
type Client struct {
	repoPath string
}

// NewClient returns a Client rooted at repoPath.
func NewClient(repoPath string) *Client {
	return &Client{
		repoPath: repoPath,
	}
}

// AddSSHKey adds privateKeyPath to the running ssh-agent.
func (c *Client) AddSSHKey(privateKeyPath string) error {
	if privateKeyPath == "" {
		return fmt.Errorf("ssh-add: privateKeyPath is required but was not set — ensure sshPrivateKeyPath is configured in your harvester/aws config")
	}
	logrus.Infof("[ansible] ssh-add %s", privateKeyPath)
	out, err := run("ssh-add", []string{privateKeyPath}, c.repoPath, nil)
	if err != nil {
		return fmt.Errorf("ssh-add: %w", err)
	}
	logrus.Debugf("[ansible] ssh-add output:\n%s", string(out))
	return nil
}

// GenerateInventory renders templatePath with env substitutions and writes the result to a temp file.
// It returns the path to the generated inventory file.
func (c *Client) GenerateInventory(templatePath string, env map[string]string) (string, error) {
	absTemplate := filepath.Join(c.repoPath, templatePath)

	logrus.Infof("[ansible] generating inventory from %s", absTemplate)

	templateBytes, err := os.ReadFile(absTemplate)
	if err != nil {
		return "", fmt.Errorf("read inventory template %s: %w", absTemplate, err)
	}

	rendered := string(templateBytes)
	for k, v := range env {
		rendered = strings.ReplaceAll(rendered, "$"+k, v)
	}

	f, err := os.CreateTemp("", "ansible-inventory-*.yml")
	if err != nil {
		return "", fmt.Errorf("create temp inventory file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(rendered); err != nil {
		return "", fmt.Errorf("write inventory to %s: %w", f.Name(), err)
	}

	logrus.Infof("[ansible] wrote inventory to %s", f.Name())
	return f.Name(), nil
}

// WriteVarsYAML marshals vars to YAML and writes the file at relPath inside the repository.
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

// RunPlaybook executes ansible-playbook with the given playbook and inventory paths, streaming output via logrus.
func (c *Client) RunPlaybook(playbookPath, inventoryPath string, extraEnv []string) error {
	absPlaybook := filepath.Join(c.repoPath, playbookPath)

	logrus.Infof("[ansible] running playbook %s with inventory %s", absPlaybook, inventoryPath)

	args := []string{
		absPlaybook,
		"-i", inventoryPath,
	}

	cmd := exec.Command("ansible-playbook", args...)
	cmd.Dir = c.repoPath
	cmd.Env = append(os.Environ(), extraEnv...)

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	var lines []string
	var scanErr error
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			line := scanner.Text()
			logrus.Infof("[ansible] %s", line)
			lines = append(lines, line)
		}
		scanErr = scanner.Err()
	}()

	runErr := cmd.Run()
	pw.Close()
	<-streamDone

	if runErr != nil {
		return fmt.Errorf("ansible-playbook %s: %w\noutput:\n%s", playbookPath, runErr, strings.Join(lines, "\n"))
	}
	if scanErr != nil {
		return fmt.Errorf("ansible-playbook %s: reading output: %w\noutput:\n%s", playbookPath, scanErr, strings.Join(lines, "\n"))
	}
	return nil
}
