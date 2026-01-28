package permutations

import (
	"strings"

	"github.com/rancher/shepherd/clients/corral"
	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/cloudprovider"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/reports"
	"github.com/rancher/tests/actions/rke1/componentchecks"
	"github.com/rancher/tests/actions/rke1/nodetemplates"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	RKE2CustomCluster    = "rke2Custom"
	RKE2ProvisionCluster = "rke2"
	RKE2AirgapCluster    = "rke2Airgap"
	K3SCustomCluster     = "k3sCustom"
	K3SProvisionCluster  = "k3s"
	K3SAirgapCluster     = "k3sAirgap"
	RKE1CustomCluster    = "rke1Custom"
	RKE1ProvisionCluster = "rke1"
	RKE1AirgapCluster    = "rke1Airgap"
	CorralProvider       = "corral"
)

// RunTestPermutations runs through all relevant perumutations in a given config file, including node providers, k8s versions, and CNIs
func RunTestPermutations(s *suite.Suite, testNamePrefix string, client *rancher.Client, provisioningConfig *provisioninginput.Config, clusterType string, hostnameTruncation []machinepools.HostnameTruncation, corralPackages *corral.Packages) {
	var name string
	var providers []string
	var testClusterConfig *clusters.ClusterConfig
	var err error

	testSession := session.NewSession()
	defer testSession.Cleanup()
	client, err = client.WithSession(testSession)
	require.NoError(s.T(), err)

	if strings.Contains(clusterType, "Custom") {
		providers = provisioningConfig.NodeProviders
	} else if strings.Contains(clusterType, "Airgap") {
		providers = []string{"Corral"}
	} else {
		providers = provisioningConfig.Providers
	}

	for _, nodeProviderName := range providers {

		nodeProvider, rke1Provider, customProvider, kubeVersions := GetClusterProvider(clusterType, nodeProviderName, provisioningConfig)

		for _, kubeVersion := range kubeVersions {
			for _, cni := range provisioningConfig.CNIs {

				testClusterConfig = clusters.ConvertConfigToClusterConfig(provisioningConfig)
				testClusterConfig.CNI = cni
				name = testNamePrefix + " Node Provider: " + nodeProviderName + " Kubernetes version: " + kubeVersion + " cni: " + cni

				clusterObject := &steveV1.SteveAPIObject{}
				rke1ClusterObject := &management.Cluster{}
				nodeTemplate := &nodetemplates.NodeTemplate{}

				s.Run(name, func() {

					switch clusterType {
					case RKE2ProvisionCluster, K3SProvisionCluster:
						testClusterConfig.KubernetesVersion = kubeVersion

						credentialSpec := cloudcredentials.LoadCloudCredential(string(nodeProviderName))
						machineConfigSpec := machinepools.LoadMachineConfigs(string(nodeProviderName))

						clusterObject, err = provisioning.CreateProvisioningCluster(client, *nodeProvider, credentialSpec, testClusterConfig, machineConfigSpec, hostnameTruncation)
						reports.TimeoutClusterReport(clusterObject, err)
						require.NoError(s.T(), err)

						logrus.Infof("Verifying the cluster is ready (%s)", clusterObject.Name)
						err = provisioning.VerifyClusterReady(client, clusterObject)
						require.NoError(s.T(), err)

						logrus.Infof("Verifying cluster pods (%s)", clusterObject.Name)
						err = pods.VerifyClusterPods(client, clusterObject)
						require.NoError(s.T(), err)

						logrus.Infof("Verifying cluster features (%s)", clusterObject.Name)
						provisioning.VerifyDynamicCluster(s.T(), client, clusterObject)

					case RKE1ProvisionCluster:
						testClusterConfig.KubernetesVersion = kubeVersion
						nodeTemplate, err = rke1Provider.NodeTemplateFunc(client)
						require.NoError(s.T(), err)
						// workaround to simplify config for rke1 clusters with cloud provider set. This will allow external charts to be installed
						// while using the rke2 CloudProvider.
						if testClusterConfig.CloudProvider == provisioninginput.VsphereCloudProviderName.String() {
							testClusterConfig.CloudProvider = "external"
						}

						rke1ClusterObject, err = provisioning.CreateProvisioningRKE1Cluster(client, *rke1Provider, testClusterConfig, nodeTemplate)
						reports.TimeoutRKEReport(rke1ClusterObject, err)
						require.NoError(s.T(), err)

						provisioning.VerifyRKE1Cluster(s.T(), client, testClusterConfig, rke1ClusterObject)

					case RKE2CustomCluster, K3SCustomCluster:
						testClusterConfig.KubernetesVersion = kubeVersion

						awsEC2Configs := new(ec2.AWSEC2Configs)
						config.LoadConfig(ec2.ConfigurationFileKey, awsEC2Configs)

						clusterObject, err = provisioning.CreateProvisioningCustomCluster(client, customProvider, testClusterConfig, awsEC2Configs)
						reports.TimeoutClusterReport(clusterObject, err)
						require.NoError(s.T(), err)

						logrus.Infof("Verifying the cluster is ready (%s)", clusterObject.Name)
						err = provisioning.VerifyClusterReady(client, clusterObject)
						require.NoError(s.T(), err)

						logrus.Infof("Verifying cluster pods (%s)", clusterObject.Name)
						err = pods.VerifyClusterPods(client, clusterObject)
						require.NoError(s.T(), err)

						logrus.Infof("Verifying cluster features (%s)", clusterObject.Name)
						provisioning.VerifyDynamicCluster(s.T(), client, clusterObject)

					case RKE1CustomCluster:
						testClusterConfig.KubernetesVersion = kubeVersion
						// workaround to simplify config for rke1 clusters with cloud provider set. This will allow external charts to be installed
						// while using the rke2 CloudProvider name in the
						if testClusterConfig.CloudProvider == provisioninginput.VsphereCloudProviderName.String() {
							testClusterConfig.CloudProvider = "external"
						}

						awsEC2Configs := new(ec2.AWSEC2Configs)
						config.LoadConfig(ec2.ConfigurationFileKey, awsEC2Configs)

						rke1ClusterObject, nodes, err := provisioning.CreateProvisioningRKE1CustomCluster(client, customProvider, testClusterConfig, awsEC2Configs)
						reports.TimeoutRKEReport(rke1ClusterObject, err)
						require.NoError(s.T(), err)

						provisioning.VerifyRKE1Cluster(s.T(), client, testClusterConfig, rke1ClusterObject)
						etcdVersion, err := componentchecks.CheckETCDVersion(client, nodes, rke1ClusterObject.ID)
						require.NoError(s.T(), err)
						require.NotEmpty(s.T(), etcdVersion)

					// airgap currently uses corral to create nodes and register with rancher
					case RKE2AirgapCluster, K3SAirgapCluster:
						testClusterConfig.KubernetesVersion = kubeVersion
						clusterObject, err = provisioning.CreateProvisioningAirgapCustomCluster(client, testClusterConfig, corralPackages)
						reports.TimeoutClusterReport(clusterObject, err)
						require.NoError(s.T(), err)

						logrus.Infof("Verifying the cluster is ready (%s)", clusterObject.Name)
						err = provisioning.VerifyClusterReady(client, clusterObject)
						require.NoError(s.T(), err)

						logrus.Infof("Verifying cluster pods (%s)", clusterObject.Name)
						err = pods.VerifyClusterPods(client, clusterObject)
						require.NoError(s.T(), err)

						logrus.Infof("Verifying cluster features (%s)", clusterObject.Name)
						provisioning.VerifyDynamicCluster(s.T(), client, clusterObject)

					case RKE1AirgapCluster:
						testClusterConfig.KubernetesVersion = kubeVersion
						// workaround to simplify config for rke1 clusters with cloud provider set. This will allow external charts to be installed
						// while using the rke2 CloudProvider name in the
						if testClusterConfig.CloudProvider == provisioninginput.VsphereCloudProviderName.String() {
							testClusterConfig.CloudProvider = "external"
						}

						clusterObject, err := provisioning.CreateProvisioningRKE1AirgapCustomCluster(client, testClusterConfig, corralPackages)
						reports.TimeoutRKEReport(clusterObject, err)
						require.NoError(s.T(), err)

						provisioning.VerifyRKE1Cluster(s.T(), client, testClusterConfig, clusterObject)

					default:
						s.T().Fatalf("Invalid cluster type: %s", clusterType)
					}

					cloudprovider.VerifyCloudProvider(s.T(), client, clusterType, testClusterConfig, clusterObject, rke1ClusterObject)
				})
			}
		}
	}
}

// GetClusterProvider returns a provider object given cluster type, nodeProviderName (for custom clusters) and the provisioningConfig
func GetClusterProvider(clusterType string, nodeProviderName string, provisioningConfig *provisioninginput.Config) (*provisioning.Provider, *provisioning.RKE1Provider, *provisioning.ExternalNodeProvider, []string) {
	var nodeProvider provisioning.Provider
	var rke1NodeProvider provisioning.RKE1Provider
	var customProvider provisioning.ExternalNodeProvider
	var kubeVersions []string

	switch clusterType {
	case RKE2ProvisionCluster:
		nodeProvider = provisioning.CreateProvider(nodeProviderName)
		kubeVersions = provisioningConfig.RKE2KubernetesVersions
	case K3SProvisionCluster:
		nodeProvider = provisioning.CreateProvider(nodeProviderName)
		kubeVersions = provisioningConfig.K3SKubernetesVersions
	case RKE1ProvisionCluster:
		rke1NodeProvider = provisioning.CreateRKE1Provider(nodeProviderName)
		kubeVersions = provisioningConfig.RKE1KubernetesVersions
	case RKE2CustomCluster:
		customProvider = provisioning.ExternalNodeProviderSetup(nodeProviderName)
		kubeVersions = provisioningConfig.RKE2KubernetesVersions
	case K3SCustomCluster:
		customProvider = provisioning.ExternalNodeProviderSetup(nodeProviderName)
		kubeVersions = provisioningConfig.K3SKubernetesVersions
	case RKE1CustomCluster:
		customProvider = provisioning.ExternalNodeProviderSetup(nodeProviderName)
		kubeVersions = provisioningConfig.RKE1KubernetesVersions
	case K3SAirgapCluster:
		kubeVersions = provisioningConfig.K3SKubernetesVersions
	case RKE1AirgapCluster:
		kubeVersions = provisioningConfig.RKE1KubernetesVersions
	case RKE2AirgapCluster:
		kubeVersions = provisioningConfig.RKE2KubernetesVersions
	default:
		panic("Cluster type not found")
	}
	return &nodeProvider, &rke1NodeProvider, &customProvider, kubeVersions
}
