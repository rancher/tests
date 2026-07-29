package capipnp

import (
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/tests/actions/provisioninginput"
)

// capvCluster builds the CAPI cluster manifest for the CAPV provider.
func capvCluster(documents []map[string]any, config *Config, machinePools []provisioninginput.MachinePools, clusterName string) error {
	for index := 0; index < 3; index++ {
		buildCAPVTemplate(documents[index], config, clusterName)
	}

	if err := buildCAPVCPISecret(documents[3], config, clusterName); err != nil {
		return err
	}
	buildCAPVInfrastructureCluster(documents[4], config, clusterName)
	buildCAPVCluster(documents[5], config, clusterName, machinePools)

	return nil
}

// capvProviderManifest sets credentials for the CAPV provider manifests.
func capvProviderManifest(documents []map[string]any, config *Config) error {
	for _, document := range documents {
		kind, _ := document["kind"].(string)
		if kind != "Secret" {
			continue
		}

		if err := capvProviderCredentials(document, config); err != nil {
			return err
		}
	}

	return nil
}

// capvProviderCredentials sets vSphere credentials in the provider credentials secret.
func capvProviderCredentials(document map[string]any, config *Config) error {
	username := config.VSphereCredentials.Username
	password := config.VSphereCredentials.Password

	if username == "" {
		return fmt.Errorf("missing required parameter Username")
	}

	if password == "" {
		return fmt.Errorf("missing required parameter Password")
	}

	stringData, ok := document["stringData"].(map[string]any)
	if !ok {
		return fmt.Errorf("capv credentials template missing stringData")
	}

	stringData["username"] = username
	stringData["password"] = password

	return nil
}

// buildCAPVTemplate updates VSphereMachineTemplate docs with configured template data.
func buildCAPVTemplate(document map[string]any, config *Config, clusterName string) {
	metadata := document["metadata"].(map[string]any)
	currentName, _ := metadata["name"].(string)

	suffix := "worker"
	switch {
	case strings.HasSuffix(currentName, "-control-plane"):
		suffix = "control-plane"
	case strings.HasSuffix(currentName, "-etcd"):
		suffix = "etcd"
	}

	metadata["name"] = fmt.Sprintf("%s-%s", clusterName, suffix)

	templateSpec := document["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	templateSpec["datacenter"] = config.VSphereTemplate.Datacenter
	templateSpec["datastore"] = config.VSphereTemplate.Datastore
	templateSpec["diskGiB"] = config.VSphereTemplate.DiskGiB
	templateSpec["folder"] = config.VSphereTemplate.Folder
	templateSpec["memoryMiB"] = config.VSphereTemplate.MemoryMiB
	templateSpec["numCPUs"] = config.VSphereTemplate.NumCPUs
	templateSpec["resourcePool"] = config.VSphereTemplate.ResourcePool
	templateSpec["template"] = config.VSphereTemplate.Template

	network := templateSpec["network"].(map[string]any)
	devices := network["devices"].([]any)
	devices[0].(map[string]any)["networkName"] = config.VSphereTemplate.NetworkName
}

// buildCAPVCPISecret updates the CPI credentials secret used by downstream clusters.
func buildCAPVCPISecret(document map[string]any, config *Config, clusterName string) error {
	metadata := document["metadata"].(map[string]any)
	annotations := metadata["annotations"].(map[string]any)
	annotations["rke.cattle.io/object-authorized-for-clusters"] = clusterName

	host := strings.TrimSpace(config.VSphereTemplate.Host)
	username := config.VSphereCredentials.Username
	password := config.VSphereCredentials.Password

	if host == "" || strings.Contains(host, "<required>") {
		return fmt.Errorf("missing required parameter host")
	}

	if username == "" {
		return fmt.Errorf("missing required parameter username")
	}

	if password == "" {
		return fmt.Errorf("missing required parameter password")
	}

	stringData := document["stringData"].(map[string]any)

	var usernameKey, passwordKey string
	for key := range stringData {
		if !strings.Contains(key, "<required>") {
			continue
		}

		resolvedKey := strings.ReplaceAll(key, "<required>", host)
		switch {
		case strings.HasSuffix(key, ".username"):
			usernameKey = resolvedKey
			stringData[resolvedKey] = username
		case strings.HasSuffix(key, ".password"):
			passwordKey = resolvedKey
			stringData[resolvedKey] = password
		default:
			stringData[resolvedKey] = stringData[key]
		}

		delete(stringData, key)
	}

	if usernameKey == "" || passwordKey == "" {
		for key := range stringData {
			switch {
			case usernameKey == "" && strings.HasSuffix(key, ".username"):
				usernameKey = key
			case passwordKey == "" && strings.HasSuffix(key, ".password"):
				passwordKey = key
			}
		}
	}

	if usernameKey == "" {
		usernameKey = fmt.Sprintf("%s.username", host)
	}

	if passwordKey == "" {
		passwordKey = fmt.Sprintf("%s.password", host)
	}

	stringData[usernameKey] = username
	stringData[passwordKey] = password

	return nil
}

// buildCAPVInfrastructureCluster updates the VSphereCluster infrastructure object.
func buildCAPVInfrastructureCluster(document map[string]any, config *Config, clusterName string) {
	metadata := document["metadata"].(map[string]any)
	metadata["name"] = clusterName

	spec := document["spec"].(map[string]any)
	spec["server"] = config.VSphereTemplate.Host
}

// buildCAPVCluster updates the provisioning cluster object.
func buildCAPVCluster(document map[string]any, config *Config, clusterName string, machinePools []provisioninginput.MachinePools) {
	metadata := document["metadata"].(map[string]any)
	metadata["name"] = clusterName

	spec := document["spec"].(map[string]any)
	spec["kubernetesVersion"] = config.KubernetesVersion

	rkeConfig := spec["rkeConfig"].(map[string]any)
	infrastructureRef := rkeConfig["infrastructureRef"].(map[string]any)
	infrastructureRef["name"] = clusterName
	rkeConfig["machinePools"] = buildCAPVMachinePools(clusterName, machinePools)

	chartValues := rkeConfig["chartValues"].(map[string]any)
	rancherVSphereCPI := chartValues["rancher-vsphere-cpi"].(map[string]any)
	vCenter := rancherVSphereCPI["vCenter"].(map[string]any)
	vCenter["datacenters"] = config.VSphereTemplate.Datacenter
	vCenter["host"] = config.VSphereTemplate.Host
}

// buildCAPVMachinePools constructs machine pool definitions for CAPV.
func buildCAPVMachinePools(clusterName string, machinePools []provisioninginput.MachinePools) []map[string]any {
	rendered := make([]map[string]any, 0, len(machinePools))

	for index, machinePool := range machinePools {
		poolConfig := machinePool.MachinePoolConfig
		templateSuffix := capvMachineTemplateSuffix(poolConfig.ControlPlane, poolConfig.Etcd, poolConfig.Worker)
		rendered = append(rendered, map[string]any{
			"name":             capvMachinePoolName(poolConfig.ControlPlane, poolConfig.Etcd, poolConfig.Worker, index),
			"etcdRole":         poolConfig.Etcd,
			"controlPlaneRole": poolConfig.ControlPlane,
			"workerRole":       poolConfig.Worker,
			"quantity":         poolConfig.Quantity,
			"machineConfigRef": map[string]any{
				"kind":       "VSphereMachineTemplate",
				"name":       fmt.Sprintf("%s-%s", clusterName, templateSuffix),
				"namespace":  "fleet-default",
				"apiVersion": "infrastructure.cluster.x-k8s.io/v1beta2",
			},
		})
	}

	return rendered
}

func capvMachineTemplateSuffix(controlPlane, etcd, worker bool) string {
	switch {
	case controlPlane:
		return "control-plane"
	case etcd:
		return "etcd"
	case worker:
		return "worker"
	default:
		return "worker"
	}
}

func capvMachinePoolName(controlPlane, etcd, worker bool, index int) string {
	switch {
	case controlPlane && etcd && worker:
		return "all-roles"
	case controlPlane && etcd:
		return "etcd-control-plane"
	case controlPlane && worker:
		return "control-plane-worker"
	case etcd && worker:
		return "etcd-worker"
	case etcd:
		return "etcd"
	case controlPlane:
		return "control-plane"
	case worker:
		return "worker"
	default:
		return fmt.Sprintf("pool-%d", index+1)
	}
}

// waitCAPVProviderPrerequisitesReady waits for CAPV provider prerequisites in Rancher.
func waitCAPVProviderPrerequisitesReady(client *rancher.Client) error {
	kubeconfigPath, cleanup, err := writeKubeconfigToTempFile(client, localCluster)
	if err != nil {
		return err
	}
	defer cleanup()

	waitCommands := [][]string{
		{"kubectl", "--kubeconfig", kubeconfigPath, "--insecure-skip-tls-verify=true", "wait", "--for=condition=Ready", "capiprovider.turtles-capi.cattle.io/vsphere", "-n", "capv-system", "--timeout=300s"},
		{"kubectl", "--kubeconfig", kubeconfigPath, "--insecure-skip-tls-verify=true", "wait", "--for=condition=Ready", "capiprovider/vsphere", "-n", "capv-system", "--timeout=300s"},
	}

	var lastOutput string
	var lastErr error
	for _, command := range waitCommands {
		output, waitErr := runLocalKubectl(command, nil)
		if waitErr == nil {
			lastErr = nil
			break
		}

		lastErr = waitErr
		lastOutput = output
	}

	if lastErr != nil {
		return fmt.Errorf("wait for CAPV provider readiness failed: %w: %s", lastErr, strings.TrimSpace(lastOutput))
	}

	output, err := runLocalKubectl([]string{"kubectl", "--kubeconfig", kubeconfigPath, "--insecure-skip-tls-verify=true", "wait", "--for=create", "secret/vsphere-credentials", "-n", "capv-system", "--timeout=120s"}, nil)
	if err != nil {
		return fmt.Errorf("wait for CAPV credentials secret failed: %w: %s", err, strings.TrimSpace(output))
	}

	return nil
}
