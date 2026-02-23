// Package tofu provides a thin wrapper around the OpenTofu CLI for use in QA infra automation.
// It covers the operations needed to provision and destroy infrastructure:
// init, workspace management, apply, destroy, output, and show.
package tofu

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sirupsen/logrus"
)

// runner abstracts command execution for testability.
type runner func(name string, args []string, dir string, env []string) ([]byte, error)

// defaultRunner executes a command and returns combined stdout+stderr on error.
func defaultRunner(name string, args []string, dir string, env []string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("command %q %v failed: %w\noutput:\n%s", name, args, err, string(out))
	}
	return out, nil
}

// Client is an OpenTofu CLI client scoped to a specific module directory and workspace.
type Client struct {
	// moduleDir is the absolute path to the tofu module directory (e.g. repoPath/tofu/harvester/modules/vm).
	moduleDir string
	// workspace is the OpenTofu workspace to use for state isolation.
	workspace string
	// run is the command runner (replaceable for testing).
	run runner
}

// NewClient creates a new tofu Client for the given module directory and workspace.
func NewClient(moduleDir, workspace string) *Client {
	return &Client{
		moduleDir: moduleDir,
		workspace: workspace,
		run:       defaultRunner,
	}
}

// Init runs `tofu init` in the module directory.
func (c *Client) Init() error {
	logrus.Infof("[tofu] init in %s", c.moduleDir)
	out, err := c.run("tofu", []string{"init", "-input=false"}, c.moduleDir, nil)
	if err != nil {
		return fmt.Errorf("tofu init: %w", err)
	}
	logrus.Debugf("[tofu] init output:\n%s", string(out))
	return nil
}

// WorkspaceSelectOrCreate selects the workspace, creating it if it does not exist.
func (c *Client) WorkspaceSelectOrCreate() error {
	logrus.Infof("[tofu] selecting workspace %q in %s", c.workspace, c.moduleDir)

	// Try to select first.
	_, err := c.run("tofu", []string{"workspace", "select", c.workspace}, c.moduleDir, nil)
	if err == nil {
		return nil
	}

	// Workspace doesn't exist — create it.
	logrus.Infof("[tofu] workspace %q not found, creating", c.workspace)
	out, err := c.run("tofu", []string{"workspace", "new", c.workspace}, c.moduleDir, nil)
	if err != nil {
		return fmt.Errorf("tofu workspace new %q: %w", c.workspace, err)
	}
	logrus.Debugf("[tofu] workspace new output:\n%s", string(out))
	return nil
}

// Apply runs `tofu apply` with the given tfvars file path (JSON).
// Pass an empty string for varFile if no var file is needed.
func (c *Client) Apply(varFile string) error {
	logrus.Infof("[tofu] apply in %s (workspace=%s)", c.moduleDir, c.workspace)
	args := []string{"apply", "-auto-approve", "-input=false"}
	if varFile != "" {
		args = append(args, "-var-file="+varFile)
	}
	out, err := c.run("tofu", args, c.moduleDir, nil)
	if err != nil {
		return fmt.Errorf("tofu apply: %w", err)
	}
	logrus.Debugf("[tofu] apply output:\n%s", string(out))
	return nil
}

// Destroy runs `tofu destroy` with the given tfvars file path (JSON).
// Pass an empty string for varFile if no var file is needed.
func (c *Client) Destroy(varFile string) error {
	logrus.Infof("[tofu] destroy in %s (workspace=%s)", c.moduleDir, c.workspace)
	args := []string{"destroy", "-auto-approve", "-input=false"}
	if varFile != "" {
		args = append(args, "-var-file="+varFile)
	}
	out, err := c.run("tofu", args, c.moduleDir, nil)
	if err != nil {
		return fmt.Errorf("tofu destroy: %w", err)
	}
	logrus.Debugf("[tofu] destroy output:\n%s", string(out))
	return nil
}

// Output reads a single output value by name from the current workspace state.
// The value is returned as a raw JSON string (e.g. `"my-cluster-abc"` for a string output).
func (c *Client) Output(name string) (string, error) {
	logrus.Infof("[tofu] output %q in %s (workspace=%s)", name, c.moduleDir, c.workspace)
	out, err := c.run("tofu", []string{"output", "-json", name}, c.moduleDir, nil)
	if err != nil {
		return "", fmt.Errorf("tofu output %q: %w", name, err)
	}
	// The JSON output for a string value looks like: "\"my-cluster-abc\"\n"
	// Unmarshal to extract the plain string.
	var value string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &value); err != nil {
		// Return raw output if not a plain string (e.g. complex types).
		return strings.TrimSpace(string(out)), nil
	}
	return value, nil
}

// ShowResources runs `tofu show -json` and returns the parsed state.
// This is used to extract resource attributes (e.g. IP addresses) after apply.
func (c *Client) ShowResources() (*ShowState, error) {
	logrus.Infof("[tofu] show -json in %s (workspace=%s)", c.moduleDir, c.workspace)
	out, err := c.run("tofu", []string{"show", "-json"}, c.moduleDir, nil)
	if err != nil {
		return nil, fmt.Errorf("tofu show: %w", err)
	}
	var state ShowState
	if err := json.Unmarshal(out, &state); err != nil {
		return nil, fmt.Errorf("tofu show: unmarshal JSON: %w", err)
	}
	return &state, nil
}

// ShowState is a minimal representation of `tofu show -json` output.
type ShowState struct {
	Values *ShowValues `json:"values"`
}

// ShowValues contains the root module resources.
type ShowValues struct {
	RootModule ShowModule `json:"root_module"`
}

// ShowModule contains a list of resources in a module.
type ShowModule struct {
	Resources []ShowResource `json:"resources"`
}

// ShowResource represents a single resource in the tofu state.
type ShowResource struct {
	// Address is the full resource address, e.g. "ansible_host.vm[0]".
	Address string `json:"address"`
	// Type is the resource type, e.g. "ansible_host".
	Type string `json:"type"`
	// Name is the resource name within its type.
	Name string `json:"name"`
	// Values contains the resource attribute values.
	Values map[string]json.RawMessage `json:"values"`
}
