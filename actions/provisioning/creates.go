package provisioning

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"

	apiv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"

	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/cloudcredentials/aws"
	"github.com/rancher/shepherd/extensions/cloudcredentials/azure"
	"github.com/rancher/shepherd/extensions/cloudcredentials/google"
	shepherdclusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/aks"
	"github.com/rancher/shepherd/extensions/clusters/eks"
	"github.com/rancher/shepherd/extensions/clusters/gke"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/etcdsnapshot"
	"github.com/rancher/shepherd/extensions/tokenregistration"
	"github.com/rancher/shepherd/pkg/environmentflag"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/nodes"
	"github.com/rancher/shepherd/pkg/wait"
	"github.com/rancher/tests/actions/cloudprovider"
	"github.com/rancher/tests/actions/clusters"
	k3sHardening "github.com/rancher/tests/actions/hardening/k3s"
	rke2Hardening "github.com/rancher/tests/actions/hardening/rke2"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/pipeline"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/secrets"
	"github.com/rancher/tests/actions/ssh"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"

	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
)

const (
	active     = "active"
	internalIP = "alpha.kubernetes.io/provided-node-ip"
)

// CreateProvisioningCluster provisions a non-rke1 cluster, then runs verify checks
func CreateProvisioningCluster(client *rancher.Client, provider Provider, credentialSpec cloudcredentials.CloudCredential, clustersConfig *clusters.ClusterConfig, machineConfigSpec machinepools.MachineConfigs, hostnameTruncation []machinepools.HostnameTruncation) (*v1.SteveAPIObject, error) {
	var clusterName string

	if clustersConfig.ResourcePrefix != "" {
		clusterName = namegen.AppendRandomString(clustersConfig.ResourcePrefix)
	} else {
		clusterName = namegen.AppendRandomString(provider.Name.String())
	}

	logrus.Debugf("Creating Cloud credential (%s)", clusterName)
	cloudCredential, err := provider.CloudCredFunc(client, credentialSpec)
	if err != nil {
		return nil, err
	}

	if clustersConfig.PSACT == string(provisioninginput.RancherBaseline) {
		err = clusters.CreateRancherBaselinePSACT(client, clustersConfig.PSACT)
		if err != nil {
			return nil, err
		}
	}

	generatedPoolName := fmt.Sprintf("nc-%s-pool1-", clusterName)
	machinePoolConfigs := provider.MachinePoolFunc(machineConfigSpec, generatedPoolName, namespaces.FleetDefault)

	var machinePoolResponses []v1.SteveAPIObject

	logrus.Debugf("Creating Machine Pools (%s)", clusterName)
	for _, machinePoolConfig := range machinePoolConfigs {
		machinePoolConfigResp, err := client.Steve.
			SteveType(provider.MachineConfigPoolResourceSteveType).
			Create(&machinePoolConfig)
		if err != nil {
			return nil, err
		}
		machinePoolResponses = append(machinePoolResponses, *machinePoolConfigResp)
	}

	if clustersConfig.Registries != nil {
		if clustersConfig.Registries.RKE2Registries != nil {
			if clustersConfig.Registries.RKE2Username != "" && clustersConfig.Registries.RKE2Password != "" {
				steveClient, err := client.Steve.ProxyDownstream("local")
				if err != nil {
					return nil, err
				}

				secretName := fmt.Sprintf("priv-reg-sec-%s", clusterName)
				secretTemplate := secrets.NewSecretTemplate(secretName, namespaces.FleetDefault, map[string][]byte{
					"password": []byte(clustersConfig.Registries.RKE2Password),
					"username": []byte(clustersConfig.Registries.RKE2Username),
				},
					corev1.SecretTypeBasicAuth,
					nil,
					nil,
				)

				registrySecret, err := steveClient.SteveType(secrets.SecretSteveType).Create(secretTemplate)
				if err != nil {
					return nil, err
				}

				for registryName, registry := range clustersConfig.Registries.RKE2Registries.Configs {
					registry.AuthConfigSecretName = registrySecret.Name
					clustersConfig.Registries.RKE2Registries.Configs[registryName] = registry
				}
			}
		}
	}

	var machineConfigs []machinepools.MachinePoolConfig
	var pools []machinepools.Pools
	for _, pool := range clustersConfig.MachinePools {
		machineConfigs = append(machineConfigs, pool.MachinePoolConfig)
		pools = append(pools, pool.Pools)
	}

	machinePools, err := machinepools.
		CreateAllMachinePools(machineConfigs, pools, machinePoolResponses, provider.GetMachineRolesFunc(machineConfigSpec), hostnameTruncation)
	if err != nil {
		return nil, err
	}

	additionalData := make(map[string]interface{})
	if clustersConfig.CloudProvider == provisioninginput.VsphereCloudProviderName.String() {
		additionalData["datacenter"] = machinePoolConfigs[0].Object["datacenter"]
		additionalData["datastoreUrl"] = machinePoolConfigs[0].Object["datastoreUrl"]
	} else if clustersConfig.CloudProvider == provisioninginput.HarvesterProviderName.String() {
		additionalData["clusterName"] = clusterName
	}

	clustersConfig, err = cloudprovider.CreateCloudProviderAddOns(client, clustersConfig, credentialSpec, additionalData)
	if err != nil {
		return nil, err
	}

	cluster := clusters.NewK3SRKE2ClusterConfig(clusterName, namespaces.FleetDefault, clustersConfig, machinePools, cloudCredential.Namespace+":"+cloudCredential.Name)

	for _, truncatedPool := range hostnameTruncation {
		if truncatedPool.PoolNameLengthLimit > 0 || truncatedPool.ClusterNameLengthLimit > 0 {
			cluster.GenerateName = "t-"
			if truncatedPool.ClusterNameLengthLimit > 0 {
				cluster.Spec.RKEConfig.MachinePoolDefaults.HostnameLengthLimit = truncatedPool.ClusterNameLengthLimit
			}

			break
		}
	}

	logrus.Debugf("Creating cluster steve object (%s)", clusterName)
	_, err = shepherdclusters.CreateK3SRKE2Cluster(client, cluster)
	if err != nil {
		return nil, err
	}

	if client.Flags.GetValue(environmentflag.UpdateClusterName) {
		pipeline.UpdateConfigClusterName(clusterName)
	}

	logrus.Debugf("Get cluster object (%s)", clusterName)
	createdCluster, err := clusters.GetClusterByName(client, clusterName)
	if err != nil {
		return nil, err
	}

	return createdCluster, nil
}

// CreateProvisioningCustomCluster provisions a non-rke1 cluster using a 3rd party client for its nodes, then runs verify checks
func CreateProvisioningCustomCluster(client *rancher.Client, externalNodeProvider *ExternalNodeProvider, clustersConfig *clusters.ClusterConfig, ec2Configs *ec2.AWSEC2Configs) (*v1.SteveAPIObject, error) {
	var clusterName string
	rolesPerNode := []string{}
	quantityPerPool := []int32{}
	rolesPerPool := []string{}
	for _, pool := range clustersConfig.MachinePools {
		var finalRoleCommand string
		if pool.MachinePoolConfig.ControlPlane {
			finalRoleCommand += " --controlplane"
		}

		if pool.MachinePoolConfig.Etcd {
			finalRoleCommand += " --etcd"
		}

		if pool.MachinePoolConfig.Worker {
			finalRoleCommand += " --worker"
		}

		if pool.MachinePoolConfig.Windows {
			finalRoleCommand += " --windows"
		}

		quantityPerPool = append(quantityPerPool, pool.MachinePoolConfig.Quantity)
		rolesPerPool = append(rolesPerPool, finalRoleCommand)
		for i := int32(0); i < pool.MachinePoolConfig.Quantity; i++ {
			rolesPerNode = append(rolesPerNode, finalRoleCommand)
		}
	}

	if clustersConfig.PSACT == string(provisioninginput.RancherBaseline) {
		err := clusters.CreateRancherBaselinePSACT(client, clustersConfig.PSACT)
		if err != nil {
			return nil, err
		}
	}

	if clustersConfig.ResourcePrefix != "" {
		clusterName = namegen.AppendRandomString(clustersConfig.ResourcePrefix)
	} else {
		clusterName = namegen.AppendRandomString(externalNodeProvider.Name)
	}

	logrus.Debug("Creating custom cluster nodes")
	nodes, err := externalNodeProvider.NodeCreationFunc(client, rolesPerPool, quantityPerPool, ec2Configs, clustersConfig.IPv6Cluster)
	if err != nil {
		return nil, err
	}

	cluster := clusters.NewK3SRKE2ClusterConfig(clusterName, namespaces.FleetDefault, clustersConfig, nil, "")

	if (clustersConfig.Compliance || clustersConfig.Hardened) && strings.Contains(clustersConfig.KubernetesVersion, shepherdclusters.RKE2ClusterType.String()) {
		logrus.Debugf("Hardening cluster (%s)", clusterName)
		err = rke2Hardening.HardenRKE2Nodes(nodes, rolesPerNode)
		if err != nil {
			return nil, err
		}

		cluster = clusters.HardenRKE2ClusterConfig(clusterName, namespaces.FleetDefault, clustersConfig, nil, "")
	}

	clusterResp, err := shepherdclusters.CreateK3SRKE2Cluster(client, cluster)
	if err != nil {
		return nil, err
	}

	if client.Flags.GetValue(environmentflag.UpdateClusterName) {
		pipeline.UpdateConfigClusterName(clusterName)
	}

	client, err = client.ReLogin()
	if err != nil {
		return nil, err
	}

	customCluster, err := client.Steve.SteveType(etcdsnapshot.ProvisioningSteveResouceType).ByID(clusterResp.ID)
	if err != nil {
		return nil, err
	}

	clusterStatus := &apiv1.ClusterStatus{}
	err = v1.ConvertToK8sType(customCluster.Status, clusterStatus)
	if err != nil {
		return nil, err
	}

	token, err := tokenregistration.GetRegistrationToken(client, clusterStatus.ClusterName)
	if err != nil {
		return nil, err
	}

	logrus.Debugf("Registering linux nodes (%s)", clusterName)

	var command string
	totalNodesObserved := 0
	for poolIndex, poolRole := range rolesPerPool {
		if strings.Contains(poolRole, "windows") {
			totalNodesObserved += int(quantityPerPool[poolIndex])
			continue
		}
		for nodeIndex := 0; nodeIndex < int(quantityPerPool[poolIndex]); nodeIndex++ {
			node := nodes[totalNodesObserved+nodeIndex]

			logrus.Tracef("Execute Registration Command for node %s", node.NodeID)

			command = fmt.Sprintf("%s %s", token.InsecureNodeCommand, poolRole)
			if clustersConfig.MachinePools[poolIndex].IsSecure {
				command = fmt.Sprintf("%s %s", token.NodeCommand, poolRole)
			}

			if clustersConfig.IPv6Cluster {
				command = createRegistrationCommand(command, node.PublicIPv6Address, node.PrivateIPv6Address, clustersConfig.MachinePools[poolIndex])
			} else {
				command = createRegistrationCommand(command, node.PrivateIPAddress, node.PrivateIPAddress, clustersConfig.MachinePools[poolIndex])
			}

			logrus.Tracef("Command: %s", command)

			output, err := node.ExecuteCommand(command)
			if err != nil {
				return nil, err
			}

			logrus.Trace(output)
		}

		totalNodesObserved += int(quantityPerPool[poolIndex])
	}

	err = VerifyClusterReady(client, customCluster)
	if err != nil {
		return nil, err
	}

	totalNodesObserved = 0
	for poolIndex := 0; poolIndex < len(rolesPerPool); poolIndex++ {
		if strings.Contains(rolesPerPool[poolIndex], "windows") {
			logrus.Debug("Registering Windows Nodes")

			for nodeIndex := 0; nodeIndex < int(quantityPerPool[poolIndex]); nodeIndex++ {
				node := nodes[totalNodesObserved+nodeIndex]

				logrus.Tracef("Execute Registration Command for node %s", node.NodeID)
				logrus.Tracef("Windows pool detected, using powershell.exe...")

				escCommand := strings.ReplaceAll(token.InsecureWindowsNodeCommand, `"`, `\"`)
				command = fmt.Sprintf(`powershell.exe -Command "%s"`, escCommand)

				if clustersConfig.MachinePools[poolIndex].IsSecure {
					escCommand := strings.ReplaceAll(token.WindowsNodeCommand, `"`, `\"`)
					command = fmt.Sprintf(`powershell.exe -Command "%s"`, escCommand)
				}

				command = createWindowsRegistrationCommand(command, node.PublicIPAddress, node.PrivateIPAddress, clustersConfig.MachinePools[poolIndex])
				logrus.Tracef("Command: %s", command)

				output, err := node.ExecuteCommand(command)
				if err != nil {
					return nil, err
				}

				logrus.Trace(output)
			}
		}

		totalNodesObserved += int(quantityPerPool[poolIndex])
	}

	if clustersConfig.Compliance || clustersConfig.Hardened {
		if strings.Contains(clustersConfig.KubernetesVersion, shepherdclusters.K3SClusterType.String()) {
			logrus.Debugf("Hardening cluster (%s)", clusterName)
			err = k3sHardening.HardenK3SNodes(nodes, rolesPerNode, clustersConfig.KubernetesVersion, clustersConfig.PathToRepo)
			if err != nil {
				return nil, err
			}

			hardenCluster := clusters.HardenK3SClusterConfig(clusterName, namespaces.FleetDefault, clustersConfig, nil, "")

			_, err := shepherdclusters.UpdateK3SRKE2Cluster(client, clusterResp, hardenCluster)
			if err != nil {
				return nil, err
			}
		} else {
			err = rke2Hardening.PostRKE2HardeningConfig(nodes, rolesPerNode, clustersConfig.PathToRepo)
			if err != nil {
				return nil, err
			}
		}
	}

	createdCluster, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(namespaces.FleetDefault + "/" + clusterName)

	return createdCluster, err
}

// CreateProvisioningAirgapCustomCluster provisions a rke2/k3s cluster, then runs verify checks
func CreateProvisioningAirgapCustomCluster(client *rancher.Client, clustersConfig *clusters.ClusterConfig, externalNodeProvider *ExternalNodeProvider, ec2Configs *ec2.AWSEC2Configs, airgapBastion string) (*v1.SteveAPIObject, error) {
	var clusterName string
	rolesPerNode := []string{}
	quantityPerPool := []int32{}
	rolesPerPool := []string{}

	for _, pool := range clustersConfig.MachinePools {
		var finalRoleCommand string
		if pool.MachinePoolConfig.ControlPlane {
			finalRoleCommand += " --controlplane"
		}

		if pool.MachinePoolConfig.Etcd {
			finalRoleCommand += " --etcd"
		}

		if pool.MachinePoolConfig.Worker {
			finalRoleCommand += " --worker"
		}

		if pool.MachinePoolConfig.Windows {
			finalRoleCommand += " --windows"
		}

		quantityPerPool = append(quantityPerPool, pool.MachinePoolConfig.Quantity)
		rolesPerPool = append(rolesPerPool, finalRoleCommand)

		for i := int32(0); i < pool.MachinePoolConfig.Quantity; i++ {
			rolesPerNode = append(rolesPerNode, finalRoleCommand)
		}
	}

	if clustersConfig.PSACT == string(provisioninginput.RancherBaseline) {
		err := clusters.CreateRancherBaselinePSACT(client, clustersConfig.PSACT)
		if err != nil {
			return nil, err
		}
	}

	if clustersConfig.ResourcePrefix != "" {
		clusterName = namegen.AppendRandomString(clustersConfig.ResourcePrefix)
	} else {
		clusterName = namegen.AppendRandomString(externalNodeProvider.Name)
	}

	logrus.Debug("Creating custom cluster nodes")
	nodes, err := externalNodeProvider.AirgapNodeCreationFunc(client, rolesPerPool, quantityPerPool, ec2Configs)
	if err != nil {
		return nil, err
	}

	cluster := clusters.NewK3SRKE2ClusterConfig(clusterName, namespaces.FleetDefault, clustersConfig, nil, "")

	clusterResp, err := shepherdclusters.CreateK3SRKE2Cluster(client, cluster)
	if err != nil {
		return nil, err
	}

	client, err = client.ReLogin()
	if err != nil {
		return nil, err
	}

	customCluster, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(clusterResp.ID)
	if err != nil {
		return nil, err
	}

	clusterStatus := &apiv1.ClusterStatus{}
	err = v1.ConvertToK8sType(customCluster.Status, clusterStatus)
	if err != nil {
		return nil, err
	}

	token, err := tokenregistration.GetRegistrationToken(client, clusterStatus.ClusterName)
	if err != nil {
		return nil, err
	}

	logrus.Debugf("Registering linux nodes via bastion: %s", airgapBastion)

	var command string
	totalNodesObserved := 0

	for poolIndex, poolRole := range rolesPerPool {
		if strings.Contains(poolRole, "windows") {
			totalNodesObserved += int(quantityPerPool[poolIndex])
			continue
		}

		for nodeIndex := 0; nodeIndex < int(quantityPerPool[poolIndex]); nodeIndex++ {
			node := nodes[totalNodesObserved+nodeIndex]
			logrus.Tracef("Execute Registration Command for node %s", node.NodeID)
			command = fmt.Sprintf("%s %s", token.InsecureNodeCommand, poolRole)

			if clustersConfig.MachinePools[poolIndex].IsSecure {
				command = fmt.Sprintf("%s %s", token.NodeCommand, poolRole)
			}

			command = createRegistrationCommand(command, node.PrivateIPAddress, node.PrivateIPAddress, clustersConfig.MachinePools[poolIndex])

			logrus.Tracef("Command: %s", command)

			sshKeyPath := ssh.GetNodeSSHKeyPath(rolesPerPool[poolIndex], ec2Configs)
			pemOnBastion := "/home/" + clustersConfig.BastionUser + "/" + ec2Configs.AWSEC2Config[0].AWSSSHKeyName

			scpCmd := fmt.Sprintf("scp -o StrictHostKeyChecking=no -i %s %s %s@%s:%s",
				sshKeyPath,
				sshKeyPath,
				clustersConfig.BastionUser,
				airgapBastion,
				pemOnBastion,
			)

			_, scpErr := ssh.RunLocalCommand(scpCmd)
			if scpErr != nil {
				return nil, fmt.Errorf("failed to copy pem to bastion: %w", scpErr)
			}

			sshCmd := fmt.Sprintf(
				"ssh -o StrictHostKeyChecking=no -i %s %s@%s 'ssh -o StrictHostKeyChecking=no -i %s %s@%s \"%s\"'",
				sshKeyPath,
				clustersConfig.BastionUser,
				airgapBastion,
				pemOnBastion,
				node.SSHUser,
				node.PrivateIPAddress,
				command,
			)

			output, err := ssh.RunLocalCommand(sshCmd)
			if err != nil {
				return nil, fmt.Errorf("failed to register node %s via bastion: %w", node.NodeID, err)
			}

			logrus.Trace(output)
		}

		totalNodesObserved += int(quantityPerPool[poolIndex])
	}

	err = VerifyClusterReady(client, customCluster)
	if err != nil {
		return nil, err
	}

	totalNodesObserved = 0
	for poolIndex := 0; poolIndex < len(rolesPerPool); poolIndex++ {
		if strings.Contains(rolesPerPool[poolIndex], "windows") {
			logrus.Debug("Registering Windows Nodes via bastion")

			linuxSSHKeyPath := ssh.GetNodeSSHKeyPath("linux", ec2Configs)
			windowsSSHKeyPath := ssh.GetNodeSSHKeyPath("windows", ec2Configs)

			for nodeIndex := 0; nodeIndex < int(quantityPerPool[poolIndex]); nodeIndex++ {
				node := nodes[totalNodesObserved+nodeIndex]
				logrus.Tracef("Execute Registration Command for node %s", node.NodeID)
				logrus.Tracef("Windows pool detected, using powershell.exe via bastion...")

				escCommand := strings.ReplaceAll(token.InsecureWindowsNodeCommand, `"`, `\\\"`)
				command = fmt.Sprintf(`powershell.exe -Command \"%s\"`, escCommand)

				if clustersConfig.MachinePools[poolIndex].IsSecure {
					escCommand := strings.ReplaceAll(token.WindowsNodeCommand, `"`, `\\\"`)
					command = fmt.Sprintf(`powershell.exe -Command \"%s\"`, escCommand)
				}

				command = createWindowsRegistrationCommand(command, node.PublicIPAddress, node.PrivateIPAddress, clustersConfig.MachinePools[poolIndex])
				logrus.Tracef("Command: %s", command)

				pemOnBastion := "/home/" + clustersConfig.BastionUser + "/" + ec2Configs.AWSEC2Config[0].AWSSSHKeyName
				scpCmd := fmt.Sprintf("scp -o StrictHostKeyChecking=no -i %s %s %s@%s:%s",
					linuxSSHKeyPath,
					windowsSSHKeyPath,
					clustersConfig.BastionUser,
					airgapBastion,
					pemOnBastion,
				)

				_, scpErr := ssh.RunLocalCommand(scpCmd)
				if scpErr != nil {
					return nil, fmt.Errorf("failed to copy Windows pem to bastion: %w", scpErr)
				}

				sshCmd := fmt.Sprintf(
					"ssh -A -o StrictHostKeyChecking=no -i %s %s@%s 'ssh -o StrictHostKeyChecking=no -i %s %s@%s \"%s\"'",
					linuxSSHKeyPath,
					clustersConfig.BastionUser,
					airgapBastion,
					pemOnBastion,
					clustersConfig.BastionWindowsUser,
					node.PrivateIPAddress,
					command,
				)

				output, err := ssh.RunLocalCommand(sshCmd)
				if err != nil {
					return nil, fmt.Errorf("failed to register Windows node %s via bastion: %w", node.NodeID, err)
				}

				logrus.Trace(output)
			}
		}

		totalNodesObserved += int(quantityPerPool[poolIndex])
	}

	createdCluster, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(namespaces.FleetDefault + "/" + clusterName)

	return createdCluster, err
}

// CreateProvisioningAKSHostedCluster provisions an AKS cluster, then runs verify checks
func CreateProvisioningAKSHostedCluster(client *rancher.Client, aksClusterConfig aks.ClusterConfig) (*management.Cluster, error) {
	cloudCredentialConfig := cloudcredentials.LoadCloudCredential("azure")
	cloudCredential, err := azure.CreateAzureCloudCredentials(client, cloudCredentialConfig)
	if err != nil {
		return nil, err
	}

	clusterName := namegen.AppendRandomString("akshostcluster")

	clusterResp, err := aks.CreateAKSHostedCluster(client, clusterName, cloudCredential.Namespace+":"+cloudCredential.Name, aksClusterConfig, false, false, false, false, nil)
	if err != nil {
		return nil, err
	}

	if client.Flags.GetValue(environmentflag.UpdateClusterName) {
		pipeline.UpdateConfigClusterName(clusterName)
	}

	client, err = client.ReLogin()
	if err != nil {
		return nil, err
	}

	return client.Management.Cluster.ByID(clusterResp.ID)
}

// CreateProvisioningEKSHostedCluster provisions an EKS cluster, then runs verify checks
func CreateProvisioningEKSHostedCluster(client *rancher.Client, eksClusterConfig eks.ClusterConfig) (*management.Cluster, error) {
	cloudCredentialConfig := cloudcredentials.LoadCloudCredential("aws")
	cloudCredential, err := aws.CreateAWSCloudCredentials(client, cloudCredentialConfig)
	if err != nil {
		return nil, err
	}

	clusterName := namegen.AppendRandomString("ekshostcluster")
	clusterResp, err := eks.CreateEKSHostedCluster(client, clusterName, cloudCredential.Namespace+":"+cloudCredential.Name, eksClusterConfig, false, false, false, false, nil)
	if err != nil {
		return nil, err
	}

	if client.Flags.GetValue(environmentflag.UpdateClusterName) {
		pipeline.UpdateConfigClusterName(clusterName)
	}

	client, err = client.ReLogin()
	if err != nil {
		return nil, err
	}

	return client.Management.Cluster.ByID(clusterResp.ID)
}

// CreateProvisioningGKEHostedCluster provisions an GKE cluster, then runs verify checks
func CreateProvisioningGKEHostedCluster(client *rancher.Client, gkeClusterConfig gke.ClusterConfig) (*management.Cluster, error) {
	credentialSpec := cloudcredentials.LoadCloudCredential(provisioninginput.GoogleProviderName.String())
	cloudCredential, err := google.CreateGoogleCloudCredentials(client, credentialSpec)
	if err != nil {
		return nil, err
	}

	clusterName := namegen.AppendRandomString("gkehostcluster")
	clusterResp, err := gke.CreateGKEHostedCluster(client, clusterName, cloudCredential.Namespace+":"+cloudCredential.Name, gkeClusterConfig, false, false, false, false, nil)
	if err != nil {
		return nil, err
	}

	if client.Flags.GetValue(environmentflag.UpdateClusterName) {
		pipeline.UpdateConfigClusterName(clusterName)
	}

	client, err = client.ReLogin()
	if err != nil {
		return nil, err
	}

	return client.Management.Cluster.ByID(clusterResp.ID)
}

// createRegistrationCommand is a helper for rke2/k3s custom clusters to create the registration command with advanced options configured per node
func createRegistrationCommand(command, publicIP, privateIP string, machinePool provisioninginput.MachinePools) string {
	if machinePool.SpecifyCustomPublicIP {
		command += fmt.Sprintf(" --address %s", publicIP)
	}
	if machinePool.SpecifyCustomPrivateIP {
		command += fmt.Sprintf(" --internal-address %s", privateIP)
	}
	if machinePool.CustomNodeNameSuffix != "" {
		command += fmt.Sprintf(" --node-name %s", namegen.AppendRandomString(machinePool.CustomNodeNameSuffix))
	}
	for labelKey, labelValue := range machinePool.NodeLabels {
		command += fmt.Sprintf(" --label %s=%s", labelKey, labelValue)
	}
	for _, taint := range machinePool.NodeTaints {
		command += fmt.Sprintf(" --taints %s=%s:%s", taint.Key, taint.Value, taint.Effect)
	}
	return command
}

// createWindowsRegistrationCommand is a helper for rke2 windows custom clusters to create the registration command with advanced options configured per node
func createWindowsRegistrationCommand(command, publicIP, privateIP string, machinePool provisioninginput.MachinePools) string {
	if machinePool.SpecifyCustomPublicIP {
		command += fmt.Sprintf(" -Address '%s'", publicIP)
	}
	if machinePool.SpecifyCustomPrivateIP {
		command += fmt.Sprintf(" -InternalAddress '%s'", privateIP)
	}
	if machinePool.CustomNodeNameSuffix != "" {
		command += fmt.Sprintf(" -NodeName '%s'", namegen.AppendRandomString(machinePool.CustomNodeNameSuffix))
	}
	// powershell requires only 1 flag per command, so we need to append the custom labels and taints together which is different from linux
	if len(machinePool.NodeLabels) > 0 {
		// there is an existing label for all windows nodes, so we need to insert the custom labels after the existing label
		labelIndex := strings.Index(command, " -Label '") + len(" -Label '")
		customLabels := ""
		for labelKey, labelValue := range machinePool.NodeLabels {
			customLabels += fmt.Sprintf("%s=%s,", labelKey, labelValue)
		}
		command = command[:labelIndex] + customLabels + command[labelIndex:]
	}
	if len(machinePool.NodeTaints) > 0 {
		var customTaints string
		for _, taint := range machinePool.NodeTaints {
			customTaints += fmt.Sprintf("%s=%s:%s,", taint.Key, taint.Value, taint.Effect)
		}
		wrappedTaints := fmt.Sprintf(" -Taint '%s'", customTaints)
		command += wrappedTaints
	}
	return command
}

// AddRKE2K3SCustomClusterNodes is a helper method that will add nodes to the custom RKE2/K3S custom cluster.
func AddRKE2K3SCustomClusterNodes(client *rancher.Client, cluster *v1.SteveAPIObject, nodes []*nodes.Node, rolesPerNode []string,
	clustersConfig *clusters.ClusterConfig) error {
	clusterStatus := &apiv1.ClusterStatus{}
	err := v1.ConvertToK8sType(cluster.Status, clusterStatus)
	if err != nil {
		return err
	}

	token, err := tokenregistration.GetRegistrationToken(client, clusterStatus.ClusterName)
	if err != nil {
		return err
	}

	var command string
	for key, node := range nodes {
		logrus.Infof("Adding node %s to cluster %s", node.NodeID, cluster.Name)
		if strings.Contains(rolesPerNode[key], "windows") {
			escCommand := strings.ReplaceAll(token.InsecureWindowsNodeCommand, `"`, `\"`)
			command = fmt.Sprintf(`powershell.exe -Command "%s"`, escCommand)
		} else {
			command = fmt.Sprintf("%s %s", token.InsecureNodeCommand, rolesPerNode[key])
		}

		command = createRegistrationCommand(command, node.PublicIPAddress, node.PrivateIPAddress, clustersConfig.MachinePools[key])

		output, err := node.ExecuteCommand(command)
		if err != nil {
			return err
		}

		logrus.Trace(output)
	}

	kubeProvisioningClient, err := client.GetKubeAPIProvisioningClient()
	if err != nil {
		return err
	}

	result, err := kubeProvisioningClient.Clusters(namespaces.FleetDefault).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + cluster.Name,
		TimeoutSeconds: &defaults.WatchTimeoutSeconds,
	})
	if err != nil {
		return err
	}

	checkFunc := shepherdclusters.IsProvisioningClusterReady
	err = wait.WatchWait(result, checkFunc)
	if err != nil {
		return err
	}

	return nil
}

// DeleteRKE2K3SCustomClusterNodes is a method that will delete nodes from the custom RKE2/K3S custom cluster.
func DeleteRKE2K3SCustomClusterNodes(client *rancher.Client, clusterID string, cluster *v1.SteveAPIObject, nodesToDelete []*nodes.Node) error {
	steveclient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	nodesSteveObjList, err := steveclient.SteveType("node").List(nil)
	if err != nil {
		return err
	}

	for _, nodeToDelete := range nodesToDelete {
		for _, node := range nodesSteveObjList.Data {
			snippedIP := strings.Split(node.Annotations[internalIP], ",")[0]

			if snippedIP == nodeToDelete.PrivateIPAddress {
				machine, err := client.Steve.SteveType(stevetypes.Machine).ByID(namespaces.FleetDefault + "/" + node.Annotations[machineNameAnnotation])
				if err != nil {
					return err
				}

				logrus.Infof("Deleting node %s from cluster %s", nodeToDelete.NodeID, cluster.Name)
				err = client.Steve.SteveType(stevetypes.Machine).Delete(machine)
				if err != nil {
					return err
				}

				err = kwait.PollUntilContextTimeout(context.TODO(), 500*time.Millisecond, defaults.ThirtyMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
					_, err = client.Steve.SteveType(stevetypes.Machine).ByID(machine.ID)
					if err != nil {
						logrus.Infof("Node has successfully been deleted!")
						return true, nil
					}
					return false, nil
				})
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}
