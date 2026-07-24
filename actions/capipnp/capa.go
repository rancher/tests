package capipnp

import (
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/tests/actions/provisioninginput"
)

// capaCluster is a function that builds the CAPI cluster manifest for the CAPA provider based on the provided configuration,
// machine pools, and cluster name. It modifies the provided documents to include the necessary information for the cluster.
func capaCluster(documents []map[string]any, config *Config, machinePools []provisioninginput.MachinePools, clusterName string) error {
	for index := 0; index < 3; index++ {
		buildCAPATemplate(documents[index], config, clusterName)
	}

	buildCAPAParameters(documents[3], config, clusterName)
	buildCAPACluster(documents[4], config, clusterName, machinePools)

	return nil
}

// capaProviderManifest is a function that iterates through the provided documents and set the credentials for
// the CAPA provider.
func capaProviderManifest(documents []map[string]any, config *Config) error {
	for _, document := range documents {
		kind, _ := document["kind"].(string)
		if kind != "Secret" {
			continue
		}

		if err := capaProviderCredentials(document, config); err != nil {
			return err
		}
	}

	return nil
}

// capaProviderCredentials is a function that sets the AWS credentials in the provided document for the CAPA provider.
func capaProviderCredentials(document map[string]any, config *Config) error {
	accessKeyID := config.AWSCredentials.AccessKeyID
	secretAccessKey := config.AWSCredentials.SecretAccessKey

	if accessKeyID == "" {
		return fmt.Errorf("missing required parameter AccessKeyID")
	}

	if secretAccessKey == "" {
		return fmt.Errorf("missing required parameter SecretAccessKey")
	}

	stringData, ok := document["stringData"].(map[string]any)
	if !ok {
		return fmt.Errorf("capa credentials template missing stringData")
	}

	stringData["AccessKeyID"] = accessKeyID
	stringData["SecretAccessKey"] = secretAccessKey

	return nil
}

// buildCAPATemplate is a function that modifies the provided document to set the appropriate names and configurations.
func buildCAPATemplate(document map[string]any, config *Config, clusterName string) {
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

	spec := document["spec"].(map[string]any)
	template := spec["template"].(map[string]any)
	templateSpec := template["spec"].(map[string]any)
	ami := templateSpec["ami"].(map[string]any)
	ami["id"] = config.AWSTemplate.AMI
	templateSpec["sshKeyName"] = config.AWSTemplate.SSHKeyName
}

// buildCAPAParameters is a function that modifies the provided document to set the appropriate parameters for the CAPA provider.
func buildCAPAParameters(document map[string]any, config *Config, clusterName string) {
	metadata := document["metadata"].(map[string]any)
	metadata["name"] = clusterName
	spec := document["spec"].(map[string]any)
	spec["region"] = config.AWSTemplate.Region
	network := spec["network"].(map[string]any)
	vpc := network["vpc"].(map[string]any)
	vpc["id"] = config.AWSTemplate.VPCID
	subnets := network["subnets"].([]any)
	subnets[0].(map[string]any)["id"] = config.AWSTemplate.SubnetID
	securityGroupOverrides := network["securityGroupOverrides"].(map[string]any)
	securityGroupOverrides["controlplane"] = config.AWSTemplate.ControlPlaneSecurityGroup
	securityGroupOverrides["node"] = config.AWSTemplate.NodeSecurityGroup
}

// buildCAPACluster is a function that modifies the provided document to set the appropriate cluster configuration for the CAPA provider.
func buildCAPACluster(document map[string]any, config *Config, clusterName string, machinePools []provisioninginput.MachinePools) {
	metadata := document["metadata"].(map[string]any)
	metadata["name"] = clusterName
	spec := document["spec"].(map[string]any)
	spec["kubernetesVersion"] = config.KubernetesVersion
	rkeConfig := spec["rkeConfig"].(map[string]any)
	infrastructureRef := rkeConfig["infrastructureRef"].(map[string]any)
	infrastructureRef["name"] = clusterName
	rkeConfig["machinePools"] = buildCAPAMachinePools(clusterName, machinePools)
}

// buildCAPAMachinePools is a function that constructs the machine pool configurations for the CAPA provider based on the provided
// cluster name and machine pools.
func buildCAPAMachinePools(clusterName string, machinePools []provisioninginput.MachinePools) []map[string]any {
	rendered := make([]map[string]any, 0, len(machinePools))

	for index, machinePool := range machinePools {
		poolConfig := machinePool.MachinePoolConfig
		templateSuffix := capaMachineTemplateSuffix(poolConfig.ControlPlane, poolConfig.Etcd, poolConfig.Worker)
		rendered = append(rendered, map[string]any{
			"name":             capaMachinePoolName(poolConfig.ControlPlane, poolConfig.Etcd, poolConfig.Worker, index),
			"etcdRole":         poolConfig.Etcd,
			"controlPlaneRole": poolConfig.ControlPlane,
			"workerRole":       poolConfig.Worker,
			"quantity":         poolConfig.Quantity,
			"machineConfigRef": map[string]any{
				"kind":       "AWSMachineTemplate",
				"name":       fmt.Sprintf("%s-%s", clusterName, templateSuffix),
				"namespace":  "fleet-default",
				"apiVersion": "infrastructure.cluster.x-k8s.io/v1beta2",
			},
		})
	}

	return rendered
}

// capaMachineTemplateSuffix is a helper function that determines the appropriate suffix for the machine template.
func capaMachineTemplateSuffix(controlPlane, etcd, worker bool) string {
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

// capaMachinePoolName is a helper function that generates the machine pool name based on the roles and index.
func capaMachinePoolName(controlPlane, etcd, worker bool, index int) string {
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

// waitCAPAProviderPrerequisitesReady is a function that waits for the CAPA provider prerequisites to be ready in the Rancher cluster.
func waitCAPAProviderPrerequisitesReady(client *rancher.Client) error {
	kubeconfigPath, cleanup, err := writeKubeconfigToTempFile(client, localCluster)
	if err != nil {
		return err
	}
	defer cleanup()

	waitCommands := [][]string{
		{"kubectl", "--kubeconfig", kubeconfigPath, "wait", "--for=condition=Ready", "capiprovider.turtles-capi.cattle.io/aws", "-n", "capa-system", "--timeout=300s"},
		{"kubectl", "--kubeconfig", kubeconfigPath, "wait", "--for=condition=Ready", "capiprovider/aws", "-n", "capa-system", "--timeout=300s"},
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
		return fmt.Errorf("wait for CAPA provider readiness failed: %w: %s", lastErr, strings.TrimSpace(lastOutput))
	}

	output, err := runLocalKubectl([]string{"kubectl", "--kubeconfig", kubeconfigPath, "wait", "--for=create", "secret/capa-credentials", "-n", "capa-system", "--timeout=120s"}, nil)
	if err != nil {
		return fmt.Errorf("wait for CAPA credentials secret failed: %w: %s", err, strings.TrimSpace(output))
	}

	return nil
}
