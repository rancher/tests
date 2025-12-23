package cloudprovider

import (
	"strings"
	"testing"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	extensionscharts "github.com/rancher/shepherd/extensions/charts"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/providers"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	wloads "github.com/rancher/shepherd/extensions/workloads"
	"github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/reports"
	"github.com/rancher/tests/actions/services"
	"github.com/rancher/tests/actions/storage"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	externalProviderString = "external"
	clusterIPPrefix        = "cip"
	loadBalancerPrefix     = "lb"
	portName               = "port"
	nginxName              = "nginx"

	awsUpstreamCloudProviderRepo = "https://github.com/kubernetes/cloud-provider-aws.git"
	masterBranch                 = "master"
	awsUpstreamChartName         = "aws-cloud-controller-manager"
	systemProject                = "System"
)

// VerifyCloudProvider verifies the cloud provider is working correctly by creating additional workload(s) or
// service(s) that use the upstream provider to create resources on the cluster's behalf, Namely storage and LBs
func VerifyCloudProvider(t *testing.T, client *rancher.Client, clusterType string, testClusterConfig *clusters.ClusterConfig, clusterObject *steveV1.SteveAPIObject, rke1ClusterObject *management.Cluster) {
	if strings.Contains(clusterType, extensionscluster.RKE1ClusterType.String()) {
		adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
		require.NoError(t, err)

		if strings.Contains(testClusterConfig.CloudProvider, provisioninginput.AWSProviderName.String()) {
			if strings.Contains(testClusterConfig.CloudProvider, externalProviderString) {
				clusterMeta, err := extensionscluster.NewClusterMeta(client, rke1ClusterObject.Name)
				require.NoError(t, err)

				err = CreateAndInstallAWSExternalCharts(client, clusterMeta, false)
				require.NoError(t, err)

				podErrors := pods.StatusPods(client, rke1ClusterObject.ID)
				require.Empty(t, podErrors)
			}

			clusterObject, err = adminClient.Steve.SteveType(extensionscluster.ProvisioningSteveResourceType).ByID(provisioninginput.Namespace + "/" + rke1ClusterObject.ID)
			require.NoError(t, err)

			lbServiceResp := CreateAWSCloudProviderWorkloadAndServicesLB(t, client, clusterObject)

			status := &provv1.ClusterStatus{}
			err = steveV1.ConvertToK8sType(clusterObject.Status, status)
			require.NoError(t, err)

			services.VerifyAWSLoadBalancer(t, client, lbServiceResp, status.ClusterName)

		} else if strings.Contains(testClusterConfig.CloudProvider, "external") {
			rke1ClusterObject, err := adminClient.Management.Cluster.ByID(rke1ClusterObject.ID)
			require.NoError(t, err)

			if strings.Contains(rke1ClusterObject.AppliedSpec.DisplayName, provisioninginput.VsphereProviderName.String()) {
				chartConfig := new(charts.Config)
				config.LoadConfig(charts.ConfigurationFileKey, chartConfig)

				err := charts.InstallVsphereOutOfTreeCharts(client, catalog.RancherChartRepo, rke1ClusterObject.Name, !chartConfig.IsUpgradable)
				reports.TimeoutRKEReport(rke1ClusterObject, err)
				require.NoError(t, err)

				podErrors := pods.StatusPods(client, rke1ClusterObject.ID)
				require.Empty(t, podErrors)

				storage.CreatePVCWorkload(t, client, rke1ClusterObject.ID, "")
			}
		}
	} else if strings.Contains(clusterType, extensionscluster.RKE2ClusterType.String()) {
		if testClusterConfig.CloudProvider == provisioninginput.AWSProviderName.String() {
			VerifyAWSCloudProvider(t, client, clusterObject)
		} else if testClusterConfig.CloudProvider == provisioninginput.HarvesterProviderName.String() {
			VerifyHarvesterCloudProvider(t, client, clusterObject)
		} else if testClusterConfig.CloudProvider == provisioninginput.VsphereCloudProviderName.String() {
			VerifyVSphereCloudProvider(t, client, clusterObject)
		}
	}
}

func VerifyAWSCloudProvider(t *testing.T, client *rancher.Client, clusterObject *steveV1.SteveAPIObject) {
	status := &provv1.ClusterStatus{}
	err := steveV1.ConvertToK8sType(clusterObject.Status, status)
	require.NoError(t, err)

	lbServiceResp := CreateAWSCloudProviderWorkloadAndServicesLB(t, client, clusterObject)

	services.VerifyAWSLoadBalancer(t, client, lbServiceResp, status.ClusterName)
}

func VerifyHarvesterCloudProvider(t *testing.T, client *rancher.Client, clusterObject *steveV1.SteveAPIObject) {
	status := &provv1.ClusterStatus{}
	err := steveV1.ConvertToK8sType(clusterObject.Status, status)
	require.NoError(t, err)

	lbServiceResp := CreateHarvesterCloudProviderWorkloadAndServicesLB(t, client, clusterObject)

	services.VerifyHarvesterLoadBalancer(t, client, lbServiceResp, status.ClusterName)
	storage.CreatePVCWorkload(t, client, status.ClusterName, "")

	podErrors := pods.StatusPods(client, status.ClusterName)
	require.Empty(t, podErrors)
}

func VerifyVSphereCloudProvider(t *testing.T, client *rancher.Client, clusterObject *steveV1.SteveAPIObject) {
	status := &provv1.ClusterStatus{}
	err := steveV1.ConvertToK8sType(clusterObject.Status, status)
	require.NoError(t, err)

	storage.CreatePVCWorkload(t, client, status.ClusterName, "")

	podErrors := pods.StatusPods(client, status.ClusterName)
	require.Empty(t, podErrors)
}

// CreateAWSCloudProviderWorkloadAndServicesLB creates a test workload, clusterIP service and LoadBalancer service.
// This should be used when testing cloud provider with in-tree or out-of-tree set on the cluster.
func CreateAWSCloudProviderWorkloadAndServicesLB(t *testing.T, client *rancher.Client, cluster *steveV1.SteveAPIObject) *steveV1.SteveAPIObject {
	status := &provv1.ClusterStatus{}

	err := steveV1.ConvertToK8sType(cluster.Status, status)
	require.NoError(t, err)

	adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
	require.NoError(t, err)

	steveclient, err := adminClient.Steve.ProxyDownstream(status.ClusterName)
	require.NoError(t, err)

	nginxWorkload, err := createNginxDeployment(steveclient, status.ClusterName)
	require.NoError(t, err)

	nginxSpec := &appv1.DeploymentSpec{}
	err = steveV1.ConvertToK8sType(nginxWorkload.Spec, nginxSpec)
	require.NoError(t, err)

	clusterIPserviceName := namegenerator.AppendRandomString(clusterIPPrefix)
	clusterIPserviceTemplate := services.NewServiceTemplate(clusterIPserviceName, namespaces.Default, corev1.ServiceTypeClusterIP, []corev1.ServicePort{{Name: portName, Port: 80}}, nginxSpec.Selector.MatchLabels)
	_, err = steveclient.SteveType(services.ServiceSteveType).Create(clusterIPserviceTemplate)
	require.NoError(t, err)

	lbServiceName := namegenerator.AppendRandomString(loadBalancerPrefix)

	machineConfigSpec := machinepools.LoadMachineConfigs(providers.AWS)
	serviceAnnotations := map[string]string{
		"service.beta.kubernetes.io/aws-load-balancer-subnets": machineConfigSpec.AmazonEC2MachineConfigs.AWSMachineConfig[0].SubnetID,
	}
	lbServiceTemplate := services.NewServiceTemplateWithAnnotations(lbServiceName, namespaces.Default, corev1.ServiceTypeLoadBalancer, []corev1.ServicePort{{Name: portName, Port: 80}}, nginxSpec.Selector.MatchLabels, serviceAnnotations)
	lbServiceResp, err := steveclient.SteveType(services.ServiceSteveType).Create(lbServiceTemplate)
	require.NoError(t, err)
	logrus.Info("aws loadbalancer created for nginx workload.")

	return lbServiceResp
}

// CreateHarvesterCloudProviderWorkloadAndServicesLB creates a test workload, clusterIP service and LoadBalancer service.
// This should be used when testing cloud provider with in-tree or out-of-tree set on the cluster.
func CreateHarvesterCloudProviderWorkloadAndServicesLB(t *testing.T, client *rancher.Client, cluster *steveV1.SteveAPIObject) *steveV1.SteveAPIObject {
	status := &provv1.ClusterStatus{}

	err := steveV1.ConvertToK8sType(cluster.Status, status)
	require.NoError(t, err)

	adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
	require.NoError(t, err)

	steveclient, err := adminClient.Steve.ProxyDownstream(status.ClusterName)
	require.NoError(t, err)

	nginxWorkload, err := createNginxDeployment(steveclient, status.ClusterName)
	require.NoError(t, err)

	nginxSpec := &appv1.DeploymentSpec{}
	err = steveV1.ConvertToK8sType(nginxWorkload.Spec, nginxSpec)
	require.NoError(t, err)

	clusterIPserviceName := namegenerator.AppendRandomString(clusterIPPrefix)

	annotations := map[string]string{
		"cloudprovider.harvesterhci.io/ipam": "dhcp",
	}

	clusterIPserviceTemplate := services.NewServiceTemplateWithAnnotations(clusterIPserviceName, namespaces.Default, corev1.ServiceTypeClusterIP, []corev1.ServicePort{{Name: portName, Port: 80}}, nginxSpec.Selector.MatchLabels, annotations)
	_, err = steveclient.SteveType(services.ServiceSteveType).Create(clusterIPserviceTemplate)
	require.NoError(t, err)

	lbServiceName := namegenerator.AppendRandomString(loadBalancerPrefix)
	lbServiceTemplate := services.NewServiceTemplateWithAnnotations(lbServiceName, namespaces.Default, corev1.ServiceTypeLoadBalancer, []corev1.ServicePort{{Name: portName, Port: 80}}, nginxSpec.Selector.MatchLabels, annotations)
	lbServiceResp, err := steveclient.SteveType(services.ServiceSteveType).Create(lbServiceTemplate)
	require.NoError(t, err)
	logrus.Info("harvester loadbalancer created for nginx workload.")

	return lbServiceResp
}

// CreateAndInstallAWSExternalCharts is a helper function for rke1 external-aws cloud provider
// clusters that install the appropriate chart(s) and returns an error, if any.
func CreateAndInstallAWSExternalCharts(client *rancher.Client, cluster *extensionscluster.ClusterMeta, isLeaderMigration bool) error {
	steveclient, err := client.Steve.ProxyDownstream(cluster.ID)
	if err != nil {
		return err
	}

	repoName := namegenerator.AppendRandomString(provisioninginput.AWSProviderName.String())
	err = extensionscharts.CreateChartRepoFromGithub(steveclient, awsUpstreamCloudProviderRepo, masterBranch, repoName)
	if err != nil {
		return err
	}

	project, err := projects.GetProjectByName(client, cluster.ID, systemProject)
	if err != nil {
		return err
	}

	catalogClient, err := client.GetClusterCatalogClient(cluster.ID)
	if err != nil {
		return err
	}

	latestVersion, err := catalogClient.GetLatestChartVersion(awsUpstreamChartName, repoName)
	if err != nil {
		return err
	}

	installOptions := &charts.InstallOptions{
		Cluster:   cluster,
		Version:   latestVersion,
		ProjectID: project.ID,
	}
	err = charts.InstallAWSOutOfTreeChart(client, installOptions, repoName, cluster.ID, isLeaderMigration)
	return err
}

// createNginxDeployment is a helper function that creates a nginx deployment in a cluster's default namespace
func createNginxDeployment(steveclient *steveV1.Client, containerNamePrefix string) (*steveV1.SteveAPIObject, error) {
	containerName := namegenerator.AppendRandomString(containerNamePrefix)

	containerTemplate := wloads.NewContainer(nginxName, nginxName, corev1.PullAlways, []corev1.VolumeMount{}, []corev1.EnvFromSource{}, nil, nil, nil)
	podTemplate := wloads.NewPodTemplate([]corev1.Container{containerTemplate}, []corev1.Volume{}, []corev1.LocalObjectReference{}, nil, nil)
	deployment := wloads.NewDeploymentTemplate(containerName, namespaces.Default, podTemplate, true, nil)

	deploymentResp, err := steveclient.SteveType(stevetypes.Deployment).Create(deployment)
	if err != nil {
		return nil, err
	}

	return deploymentResp, err
}
