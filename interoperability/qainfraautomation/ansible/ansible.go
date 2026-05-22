package ansible

import (
	"bufio"
	"encoding/json"
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

// clusterNodesInput mirrors the JSON structure emitted by the Tofu cluster_nodes output.
type clusterNodesInput struct {
	Type     string              `json:"type"`
	Metadata clusterNodeMetadata `json:"metadata"`
	Nodes    []clusterNode       `json:"nodes"`
}

type clusterNodeMetadata struct {
	KubeAPIHost   string `json:"kube_api_host"`
	FQDN          string `json:"fqdn"`
	SSHUser       string `json:"ssh_user"`
	SSHPrivateKey string `json:"ssh_private_key,omitempty"`
}

type clusterNode struct {
	Name      string   `json:"name"`
	Roles     []string `json:"roles"`
	PublicIP  string   `json:"public_ip"`
	PrivateIP string   `json:"private_ip"`
}

// inventorySchemas hardcodes the group mappings from qa-infra-automation's
// _inventory-schema.yaml. This avoids depending on the file being present in
// the embed FS (the embed directive does not include it).
var inventorySchemas = map[string]map[string]distroEnvSchema{
	"rke2": {
		"default": {
			ipField: "public_ip",
			groups: []namedGroup{
				{name: "master", def: groupSchemaDef{roles: []string{"etcd"}, firstOnly: true}},
				{name: "servers", def: groupSchemaDef{roles: []string{"cp"}}},
				{name: "workers", def: groupSchemaDef{roles: []string{"worker"}}},
			},
		},
	},
	"k3s": {
		"default": {
			ipField: "public_ip",
			groups: []namedGroup{
				{name: "master", def: groupSchemaDef{roles: []string{"cp"}, firstOnly: true}},
				{name: "servers", def: groupSchemaDef{roles: []string{"cp"}}},
				{name: "workers", def: groupSchemaDef{roles: []string{"worker"}}},
			},
		},
	},
}

type distroEnvSchema struct {
	ipField string
	groups  []namedGroup
}

type namedGroup struct {
	name string
	def  groupSchemaDef
}

type groupSchemaDef struct {
	roles     []string
	firstOnly bool
}

// GenerateInventoryFromNodes builds an Ansible inventory file from the Tofu
// cluster_nodes_json output, using hardcoded group mappings that mirror the
// _inventory-schema.yaml from qa-infra-automation. Returns the path to the
// generated inventory file.
func (c *Client) GenerateInventoryFromNodes(clusterNodesJSON, distro, env string) (string, error) {
	var input clusterNodesInput
	if err := json.Unmarshal([]byte(clusterNodesJSON), &input); err != nil {
		return "", fmt.Errorf("parse cluster_nodes_json: %w", err)
	}

	if input.Type != "cluster_nodes" {
		return "", fmt.Errorf("unexpected input type %q, expected cluster_nodes", input.Type)
	}

	distroMap, ok := inventorySchemas[distro]
	if !ok {
		return "", fmt.Errorf("no schema entry for distro %q", distro)
	}
	schema, ok := distroMap[env]
	if !ok {
		return "", fmt.Errorf("no schema entry for distro=%q env=%q", distro, env)
	}

	ipField := schema.ipField
	if ipField == "" {
		ipField = "public_ip"
	}

	inventoryYAML, err := buildClusterNodesInventory(input, schema, ipField)
	if err != nil {
		return "", fmt.Errorf("build inventory: %w", err)
	}

	f, err := os.CreateTemp("", "ansible-inventory-*.yml")
	if err != nil {
		return "", fmt.Errorf("create temp inventory file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(inventoryYAML); err != nil {
		return "", fmt.Errorf("write inventory to %s: %w", f.Name(), err)
	}

	logrus.Infof("[ansible] wrote inventory to %s (distro=%s env=%s, %d nodes)", f.Name(), distro, env, len(input.Nodes))
	return f.Name(), nil
}

func nodeIP(n clusterNode, ipField string) string {
	if ipField == "private_ip" {
		return n.PrivateIP
	}
	return n.PublicIP
}

func buildClusterNodesInventory(input clusterNodesInput, schema distroEnvSchema, ipField string) ([]byte, error) {
	meta := input.Metadata
	nodes := input.Nodes

	groups := make(map[string][]clusterNode, len(schema.groups))
	for _, g := range schema.groups {
		groups[g.name] = nil
	}

	for _, node := range nodes {
		nodeRoles := make(map[string]bool, len(node.Roles))
		for _, r := range node.Roles {
			nodeRoles[r] = true
		}
		for _, g := range schema.groups {
			for _, reqRole := range g.def.roles {
				if nodeRoles[reqRole] {
					groups[g.name] = append(groups[g.name], node)
					break
				}
			}
		}
	}

	// Apply first_only constraint
	for _, g := range schema.groups {
		if g.def.firstOnly && len(groups[g.name]) > 0 {
			groups[g.name] = groups[g.name][:1]
		}
	}

	// Enforce mutual exclusivity: each node belongs to only the first matching group
	nodeToGroup := make(map[string]string)
	for _, g := range schema.groups {
		for _, n := range groups[g.name] {
			if _, exists := nodeToGroup[n.Name]; !exists {
				nodeToGroup[n.Name] = g.name
			}
		}
	}

	exclusiveGroups := make(map[string][]clusterNode, len(schema.groups))
	for _, g := range schema.groups {
		exclusiveGroups[g.name] = nil
	}
	for nodeName, gname := range nodeToGroup {
		for _, n := range nodes {
			if n.Name == nodeName {
				exclusiveGroups[gname] = append(exclusiveGroups[gname], n)
				break
			}
		}
	}

	// Determine rke2_node_role per node
	type hostEntry struct {
		AnsibleHost           string   `yaml:"ansible_host"`
		NodeRoles             []string `yaml:"node_roles"`
		RKE2NodeRole          string   `yaml:"rke2_node_role"`
		AnsibleSSHPrivateKey  string   `yaml:"ansible_ssh_private_key_file,omitempty"`
	}

	allHosts := make(map[string]hostEntry, len(nodes))
	for _, node := range nodes {
		group := nodeToGroup[node.Name]
		var rke2Role string
		if group == "master" {
			rke2Role = "master"
		} else {
			hasCP := false
			hasEtcd := false
			for _, r := range node.Roles {
				if r == "cp" {
					hasCP = true
				}
				if r == "etcd" {
					hasEtcd = true
				}
			}
			if hasCP || hasEtcd {
				rke2Role = "server"
			} else {
				rke2Role = "agent"
			}
		}

		entry := hostEntry{
			AnsibleHost:  nodeIP(node, ipField),
			NodeRoles:    node.Roles,
			RKE2NodeRole: rke2Role,
		}
		if meta.SSHPrivateKey != "" {
			entry.AnsibleSSHPrivateKey = meta.SSHPrivateKey
		}
		allHosts[node.Name] = entry
	}

	// Build children groups
	type groupHostEntry struct {
		AnsibleHost string `yaml:"ansible_host"`
	}
	children := make(map[string]map[string]map[string]groupHostEntry)
	for _, g := range schema.groups {
		gnodes := exclusiveGroups[g.name]
		if len(gnodes) == 0 {
			continue
		}
		hosts := make(map[string]groupHostEntry, len(gnodes))
		for _, n := range gnodes {
			hosts[n.Name] = groupHostEntry{AnsibleHost: nodeIP(n, ipField)}
		}
		children[g.name] = map[string]map[string]groupHostEntry{"hosts": hosts}
	}

	// Build the final inventory map
	allVars := map[string]string{
		"ansible_ssh_common_args": "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null",
		"ansible_user":            meta.SSHUser,
		"kube_api_host":           meta.KubeAPIHost,
		"fqdn":                    meta.FQDN,
	}

	inventory := map[string]interface{}{
		"all": map[string]interface{}{
			"vars":     allVars,
			"hosts":    allHosts,
			"children": children,
		},
	}

	if meta.SSHPrivateKey != "" {
		allVars["ansible_ssh_private_key_file"] = meta.SSHPrivateKey
	}

	return yaml.Marshal(inventory)
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
