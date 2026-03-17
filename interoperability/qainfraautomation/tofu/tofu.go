package tofu

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sirupsen/logrus"
)

// ShowState is the top-level structure returned by `tofu show -json`.
type ShowState struct {
	Values *ShowValues `json:"values"`
}

// ShowValues holds the root module of a tofu show result.
type ShowValues struct {
	RootModule ShowModule `json:"root_module"`
}

// ShowModule contains the list of resources in a tofu module.
type ShowModule struct {
	Resources []ShowResource `json:"resources"`
}

// ShowResource represents a single resource entry from `tofu show -json`.
type ShowResource struct {
	Address string                     `json:"address"`
	Type    string                     `json:"type"`
	Name    string                     `json:"name"`
	Values  map[string]json.RawMessage `json:"values"`
}

func run(name string, args []string, dir string, env []string) ([]byte, error) {
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

// Client runs OpenTofu commands within a specific module directory and workspace.
type Client struct {
	moduleDir string
	workspace string
}

// NewClient returns a Client targeting moduleDir in the given workspace.
func NewClient(moduleDir, workspace string) *Client {
	return &Client{
		moduleDir: moduleDir,
		workspace: workspace,
	}
}

// Init runs `tofu init` in the module directory.
func (c *Client) Init() error {
	logrus.Infof("[tofu] init in %s", c.moduleDir)
	out, err := run("tofu", []string{"init", "-input=false"}, c.moduleDir, nil)
	if err != nil {
		return fmt.Errorf("tofu init: %w", err)
	}
	logrus.Debugf("[tofu] init output:\n%s", string(out))
	return nil
}

// WorkspaceSelectOrCreate selects the client's workspace, creating it if it does not exist.
func (c *Client) WorkspaceSelectOrCreate() error {
	logrus.Infof("[tofu] selecting workspace %q in %s", c.workspace, c.moduleDir)

	_, err := run("tofu", []string{"workspace", "select", c.workspace}, c.moduleDir, nil)
	if err == nil {
		return nil
	}

	logrus.Infof("[tofu] workspace %q not found, creating", c.workspace)
	out, err := run("tofu", []string{"workspace", "new", c.workspace}, c.moduleDir, nil)
	if err != nil {
		return fmt.Errorf("tofu workspace new %q: %w", c.workspace, err)
	}
	logrus.Debugf("[tofu] workspace new output:\n%s", string(out))
	return nil
}

// Apply runs `tofu apply` with the given var file.
func (c *Client) Apply(varFile string) error {
	return c.apply(varFile, false)
}

// ApplyNoRefresh runs `tofu apply -refresh=false`. Use this when the remote provider's
// refresh call is incompatible with the current credentials.
func (c *Client) ApplyNoRefresh(varFile string) error {
	return c.apply(varFile, true)
}

func (c *Client) apply(varFile string, noRefresh bool) error {
	logrus.Infof("[tofu] apply in %s (workspace=%s)", c.moduleDir, c.workspace)
	args := []string{"apply", "-auto-approve", "-input=false"}
	if noRefresh {
		args = append(args, "-refresh=false")
	}
	if varFile != "" {
		args = append(args, "-var-file="+varFile)
	}
	out, err := run("tofu", args, c.moduleDir, nil)
	if err != nil {
		return fmt.Errorf("tofu apply: %w", err)
	}
	logrus.Debugf("[tofu] apply output:\n%s", string(out))
	c.logOutputs()
	return nil
}

func (c *Client) logOutputs() {
	out, err := run("tofu", []string{"output", "-json"}, c.moduleDir, nil)
	if err != nil {
		logrus.Warnf("[tofu] could not retrieve outputs after apply in %s: %v", c.moduleDir, err)
		return
	}
	var outputs map[string]struct {
		Value any `json:"value"`
	}
	if err := json.Unmarshal(out, &outputs); err != nil {
		logrus.Warnf("[tofu] could not parse outputs JSON after apply in %s: %v", c.moduleDir, err)
		return
	}
	if len(outputs) == 0 {
		logrus.Infof("[tofu] apply outputs: (none) in %s (workspace=%s)", c.moduleDir, c.workspace)
		return
	}
	for k, v := range outputs {
		logrus.Infof("[tofu] apply output %q = %v (workspace=%s)", k, v.Value, c.workspace)
	}
}

// Destroy runs `tofu destroy` with the given var file.
func (c *Client) Destroy(varFile string) error {
	return c.destroy(varFile, false)
}

// DestroyNoRefresh runs `tofu destroy -refresh=false`. Use this when the remote provider's
// refresh call is incompatible with the current credentials.
func (c *Client) DestroyNoRefresh(varFile string) error {
	return c.destroy(varFile, true)
}

func (c *Client) destroy(varFile string, noRefresh bool) error {
	logrus.Infof("[tofu] destroy in %s (workspace=%s)", c.moduleDir, c.workspace)
	args := []string{"destroy", "-auto-approve", "-input=false"}
	if noRefresh {
		args = append(args, "-refresh=false")
	}
	if varFile != "" {
		args = append(args, "-var-file="+varFile)
	}
	out, err := run("tofu", args, c.moduleDir, nil)
	if err != nil {
		return fmt.Errorf("tofu destroy: %w", err)
	}
	logrus.Debugf("[tofu] destroy output:\n%s", string(out))
	return nil
}

// Output returns the string value of the named tofu output in the current workspace.
func (c *Client) Output(name string) (string, error) {
	logrus.Infof("[tofu] output %q in %s (workspace=%s)", name, c.moduleDir, c.workspace)
	out, err := run("tofu", []string{"output", "-json", name}, c.moduleDir, nil)
	if err != nil {
		return "", fmt.Errorf("tofu output %q: %w", name, err)
	}
	var value string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &value); err != nil {
		return strings.TrimSpace(string(out)), nil
	}
	return value, nil
}

// ShowResources runs `tofu show -json` and returns the parsed state.
func (c *Client) ShowResources() (*ShowState, error) {
	logrus.Infof("[tofu] show -json in %s (workspace=%s)", c.moduleDir, c.workspace)
	out, err := run("tofu", []string{"show", "-json"}, c.moduleDir, nil)
	if err != nil {
		return nil, fmt.Errorf("tofu show: %w", err)
	}
	var state ShowState
	if err := json.Unmarshal(out, &state); err != nil {
		return nil, fmt.Errorf("tofu show: unmarshal JSON: %w", err)
	}
	return &state, nil
}
