//go:build (validation || infra.rke2k3s || cluster.any || sanity || pit.daily) && !stress && !extended

package connectivity

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	client "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	shepherdclusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/cloudprovider"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	deploymentapi "github.com/rancher/tests/actions/kubeapi/workloads/deployments"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/networking"
	projectsapi "github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/services"
	"github.com/rancher/tests/actions/workloads"
	"github.com/rancher/tests/actions/workloads/daemonset"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	nodePoolsize = 3
	defaultPort  = 80
)

type PortTestSuite struct {
	suite.Suite
	session          *session.Session
	client           *rancher.Client
	cluster          *client.Cluster
	cattleConfig     map[string]any
	downstreamClient *v1.Client
	namespace        *corev1.Namespace
}

func (p *PortTestSuite) TearDownSuite() {
	p.session.Cleanup()
}

func (p *PortTestSuite) SetupSuite() {
	testSession := session.NewSession()
	p.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(p.T(), err)

	p.client = client

	p.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	p.cattleConfig, err = defaults.LoadPackageDefaults(p.cattleConfig, "")
	require.NoError(p.T(), err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, p.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(p.T(), err)

	clusterID, err := shepherdclusters.GetClusterIDByName(p.client, p.client.RancherConfig.ClusterName)
	require.NoError(p.T(), err, "Error getting cluster ID")

	p.cluster, err = p.client.Management.Cluster.ByID(clusterID)
	require.NoError(p.T(), err)

	p.downstreamClient, err = p.client.Steve.ProxyDownstream(p.cluster.ID)
	require.NoError(p.T(), err)

	_, p.namespace, err = projectsapi.CreateProjectAndNamespace(p.client, p.cluster.ID)
	require.NoError(p.T(), err)
}

func (p *PortTestSuite) TestHostPort() {
	networkPolicyTests := []struct {
		name string
	}{
		{"Host_Port_Connectivity"},
	}

	for _, networkPolicyTest := range networkPolicyTests {
		p.Suite.Run(networkPolicyTest.name, func() {
			workloadConfigs := new(workloads.Workloads)
			operations.LoadObjectFromMap(workloads.WorkloadsConfigurationFileKey, p.cattleConfig, workloadConfigs)
			hostPort := rand.Intn(55283) + 10251

			workloadConfigs.DaemonSet.ObjectMeta.Namespace = p.namespace.Name
			workloadConfigs.DaemonSet.ObjectMeta.GenerateName = "host-port-connectivity-"
			workloadConfigs.DaemonSet.Spec.Template.Spec.Containers[0].Ports = []corev1.ContainerPort{{
				HostPort:      int32(hostPort),
				ContainerPort: defaultPort,
				Protocol:      corev1.ProtocolTCP,
			}}

			logrus.Infof("Creating daemonset with name prefix: %s", workloadConfigs.DaemonSet.ObjectMeta.GenerateName)
			testDaemonset, err := daemonset.CreateDaemonSetFromConfig(p.downstreamClient, p.cluster.ID, workloadConfigs.DaemonSet)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying daemonset %s is running", testDaemonset.Name)
			err = daemonset.VerifyDaemonset(p.client, p.cluster.ID, p.namespace.Name, testDaemonset.Name)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying host port %d for daemonset %s", hostPort, testDaemonset.Name)
			err = networking.VerifyHostPortConnectivity(p.client, p.cluster.ID, hostPort, testDaemonset.Name)
			require.NoError(p.T(), err)
		})
	}
}

func (p *PortTestSuite) TestNodePort() {
	nodePortTests := []struct {
		name string
	}{
		{"Node_Port_Connectivity"},
	}

	for _, nodePortTest := range nodePortTests {
		p.Suite.Run(nodePortTest.name, func() {
			workloadConfigs := new(workloads.Workloads)
			operations.LoadObjectFromMap(workloads.WorkloadsConfigurationFileKey, p.cattleConfig, workloadConfigs)
			nodePort := rand.Intn(2767) + 30000

			workloadConfigs.DaemonSet.ObjectMeta.Namespace = p.namespace.Name
			workloadConfigs.DaemonSet.ObjectMeta.GenerateName = "node-port-connectivity-"

			logrus.Infof("Creating daemonset with name prefix: %s", workloadConfigs.DaemonSet.ObjectMeta.GenerateName)
			testDaemonset, err := daemonset.CreateDaemonSetFromConfig(p.downstreamClient, p.cluster.ID, workloadConfigs.DaemonSet)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying daemonset %s is running", testDaemonset.Name)
			err = daemonset.VerifyDaemonset(p.client, p.cluster.ID, p.namespace.Name, testDaemonset.Name)
			require.NoError(p.T(), err)

			serviceName := namegen.AppendRandomString("test-service")
			logrus.Infof("Creating NodePort service %s on port %d", serviceName, nodePort)
			ports := []corev1.ServicePort{
				{
					Protocol: corev1.ProtocolTCP,
					Port:     defaultPort,
					NodePort: int32(nodePort),
				},
			}
			nodePortService := services.NewServiceTemplate(serviceName, p.namespace.Name, corev1.ServiceTypeNodePort, ports, workloadConfigs.DaemonSet.Spec.Template.Labels)
			serviceResp, err := services.CreateService(p.downstreamClient, nodePortService)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying service %s is ready", serviceResp.Name)
			err = services.VerifyService(p.downstreamClient, serviceResp)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying node port %d for daemonset %s", nodePort, testDaemonset.Name)
			err = networking.VerifyNodePortConnectivity(p.client, p.cluster.ID, nodePort, testDaemonset.Name)
			require.NoError(p.T(), err)
		})
	}
}

func (p *PortTestSuite) TestClusterIP() {
	clusterIPTests := []struct {
		name string
	}{
		{"Cluster_IP_Connectivity"},
	}

	for _, clusterIPTest := range clusterIPTests {
		p.Suite.Run(clusterIPTest.name, func() {
			workloadConfigs := new(workloads.Workloads)
			operations.LoadObjectFromMap(workloads.WorkloadsConfigurationFileKey, p.cattleConfig, workloadConfigs)
			port := rand.Intn(55283) + 10251

			workloadConfigs.DaemonSet.ObjectMeta.Namespace = p.namespace.Name
			workloadConfigs.DaemonSet.ObjectMeta.GenerateName = "cluster-ip-connectivity-"

			logrus.Infof("Creating daemonset with name prefix: %s", workloadConfigs.DaemonSet.ObjectMeta.GenerateName)
			testDaemonset, err := daemonset.CreateDaemonSetFromConfig(p.downstreamClient, p.cluster.ID, workloadConfigs.DaemonSet)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying daemonset %s is running", testDaemonset.Name)
			err = daemonset.VerifyDaemonset(p.client, p.cluster.ID, p.namespace.Name, testDaemonset.Name)
			require.NoError(p.T(), err)

			serviceName := namegen.AppendRandomString("test-service")
			logrus.Infof("Creating ClusterIP service %s on port %d", serviceName, port)
			ports := []corev1.ServicePort{
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       int32(port),
					TargetPort: intstr.FromInt(defaultPort),
				},
			}
			clusterIPService := services.NewServiceTemplate(serviceName, p.namespace.Name, corev1.ServiceTypeClusterIP, ports, workloadConfigs.DaemonSet.Spec.Template.Labels)
			serviceResp, err := services.CreateService(p.downstreamClient, clusterIPService)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying service %s is ready", serviceResp.Name)
			err = services.VerifyService(p.downstreamClient, serviceResp)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying Cluster connectivity for daemonset %s on port %d", testDaemonset.Name, port)
			err = networking.VerifyClusterConnectivity(p.client, p.cluster.ID, serviceResp.ID, fmt.Sprintf("%d/name.html", port), testDaemonset.Name)
			require.NoError(p.T(), err)
		})
	}
}

func (p *PortTestSuite) TestLoadBalancer() {
	loadBalancerTests := []struct {
		name string
	}{
		{"Load_Balancer_Connectivity"},
	}

	for _, loadBalancerTest := range loadBalancerTests {
		p.Suite.Run(loadBalancerTest.name, func() {
			isEnabled, err := cloudprovider.IsCloudProviderEnabled(p.client, p.cluster.ID)
			require.NoError(p.T(), err)

			if !isEnabled {
				p.T().Skip("Load Balance test requires access to cloud provider.")
			}

			workloadConfigs := new(workloads.Workloads)
			operations.LoadObjectFromMap(workloads.WorkloadsConfigurationFileKey, p.cattleConfig, workloadConfigs)

			port := rand.Intn(55283) + 10251
			nodePort := rand.Intn(2767) + 30000

			workloadConfigs.DaemonSet.ObjectMeta.Namespace = p.namespace.Name
			workloadConfigs.DaemonSet.ObjectMeta.GenerateName = "load-balancer-connectivity-"

			logrus.Infof("Creating daemonset with name prefix: %s", workloadConfigs.DaemonSet.ObjectMeta.GenerateName)
			testDaemonset, err := daemonset.CreateDaemonSetFromConfig(p.downstreamClient, p.cluster.ID, workloadConfigs.DaemonSet)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying daemonset %s is running", testDaemonset.Name)
			err = daemonset.VerifyDaemonset(p.client, p.cluster.ID, p.namespace.Name, testDaemonset.Name)
			require.NoError(p.T(), err)

			serviceName := namegen.AppendRandomString("test-service")
			logrus.Infof("Creating LoadBalancer service %s on ports %d/%d", serviceName, port, nodePort)
			ports := []corev1.ServicePort{
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       int32(port),
					TargetPort: intstr.FromInt(defaultPort),
					NodePort:   int32(nodePort),
				},
			}
			lbService := services.NewServiceTemplate(serviceName, p.namespace.Name, corev1.ServiceTypeLoadBalancer, ports, workloadConfigs.DaemonSet.Spec.Template.Labels)
			serviceResp, err := services.CreateService(p.downstreamClient, lbService)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying service %s is ready", serviceResp.Name)
			err = services.VerifyService(p.downstreamClient, serviceResp)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying load balancer connectivity for daemonset %s on node port %d", testDaemonset.Name, nodePort)
			err = networking.VerifyNodePortConnectivity(p.client, p.cluster.ID, nodePort, testDaemonset.Name)
			require.NoError(p.T(), err)
		})
	}
}

func (p *PortTestSuite) TestClusterIPScaleAndUpgrade() {
	clusterIPScaleTests := []struct {
		name string
	}{
		{"Cluster_IP_Scale_And_Upgrade"},
	}

	for _, tt := range clusterIPScaleTests {
		p.Suite.Run(tt.name, func() {
			_, namespace, err := projectsapi.CreateProjectAndNamespace(p.client, p.cluster.ID)
			require.NoError(p.T(), err)

			workloadConfigs := new(workloads.Workloads)
			operations.LoadObjectFromMap(workloads.WorkloadsConfigurationFileKey, p.cattleConfig, workloadConfigs)

			replicas := int32(2)
			port := rand.Intn(55283) + 10251
			workloadConfigs.Deployment.ObjectMeta.Namespace = namespace.Name
			workloadConfigs.Deployment.ObjectMeta.GenerateName = "cluster-ip-scale-"
			workloadConfigs.Deployment.Spec.Replicas = &replicas

			logrus.Infof("Creating deployment with prefix: %s", workloadConfigs.Deployment.ObjectMeta.GenerateName)
			testDeployment, err := deployment.CreateDeploymentFromConfig(p.downstreamClient, p.cluster.ID, workloadConfigs.Deployment)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying deployment %s is running", testDeployment.Name)
			err = deployment.VerifyDeployment(p.client, p.cluster.ID, testDeployment.Namespace, testDeployment.Name)
			require.NoError(p.T(), err)

			serviceName := namegen.AppendRandomString("test-service")
			logrus.Infof("Creating ClusterIP service %s on port %d", serviceName, port)
			ports := []corev1.ServicePort{
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       int32(port),
					TargetPort: intstr.FromInt(defaultPort),
				},
			}
			clusterIPService := services.NewServiceTemplate(serviceName, namespace.Name, corev1.ServiceTypeClusterIP, ports, testDeployment.Spec.Template.Labels)
			serviceResp, err := services.CreateService(p.downstreamClient, clusterIPService)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying service %s is ready", serviceResp.Name)
			err = services.VerifyService(p.downstreamClient, serviceResp)
			require.NoError(p.T(), err)

			logrus.Infof("Scaling up deployment %s to 3 replicas", testDeployment.Name)
			replicas = 3
			testDeployment.Spec.Replicas = &replicas
			testDeployment, err = deploymentapi.UpdateDeployment(p.client, p.cluster.ID, namespace.Name, testDeployment, true)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying cluster IP connectivity after scale up for deployment %s", testDeployment.Name)
			err = networking.VerifyClusterConnectivity(p.client, p.cluster.ID, serviceResp.ID, fmt.Sprintf("%d/name.html", port), testDeployment.Name)
			require.NoError(p.T(), err)

			logrus.Infof("Scaling down deployment %s to 2 replicas", testDeployment.Name)
			replicas = 2
			testDeployment.Spec.Replicas = &replicas
			testDeployment, err = deploymentapi.UpdateDeployment(p.client, p.cluster.ID, namespace.Name, testDeployment, true)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying cluster IP connectivity after scale down for deployment %s", testDeployment.Name)
			err = networking.VerifyClusterConnectivity(p.client, p.cluster.ID, serviceResp.ID, fmt.Sprintf("%d/name.html", port), testDeployment.Name)
			require.NoError(p.T(), err)

			logrus.Infof("Upgrading deployment %s container", testDeployment.Name)
			testDeployment.Spec.Template.Spec.Containers[0].Name = namegen.AppendRandomString("test-upgrade")
			testDeployment, err = deploymentapi.UpdateDeployment(p.client, p.cluster.ID, namespace.Name, testDeployment, true)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying cluster IP connectivity after upgrade for deployment %s", testDeployment.Name)
			err = networking.VerifyClusterConnectivity(p.client, p.cluster.ID, serviceResp.ID, fmt.Sprintf("%d/name.html", port), testDeployment.Name)
			require.NoError(p.T(), err)
		})
	}
}

func (p *PortTestSuite) TestHostPortScaleAndUpgrade() {
	hostPortScaleTests := []struct {
		name string
	}{
		{"Host_Port_Scale_And_Upgrade"},
	}

	for _, tt := range hostPortScaleTests {
		p.Suite.Run(tt.name, func() {
			err := clusters.VerifyNodePoolSize(p.downstreamClient, clusters.LabelWorker, nodePoolsize)
			if err != nil && strings.Contains(err.Error(), clusters.SmallerPoolMessageError) {
				p.T().Skip("The Host Port scale up/down test requires at least 3 worker nodes")
			}
			require.NoError(p.T(), err)

			_, namespace, err := projectsapi.CreateProjectAndNamespace(p.client, p.cluster.ID)
			require.NoError(p.T(), err)

			workloadConfigs := new(workloads.Workloads)
			operations.LoadObjectFromMap(workloads.WorkloadsConfigurationFileKey, p.cattleConfig, workloadConfigs)

			hostPort := rand.Intn(55283) + 10251
			replicas := int32(2)
			workloadConfigs.Deployment.ObjectMeta.Namespace = namespace.Name
			workloadConfigs.Deployment.ObjectMeta.GenerateName = "host-port-scale-"
			workloadConfigs.Deployment.Spec.Replicas = &replicas
			workloadConfigs.Deployment.Spec.Template.Spec.Containers[0].Ports = []corev1.ContainerPort{{
				HostPort:      int32(hostPort),
				ContainerPort: defaultPort,
				Protocol:      corev1.ProtocolTCP,
			}}

			logrus.Infof("Creating deployment with prefix: %s", workloadConfigs.Deployment.ObjectMeta.GenerateName)
			testDeployment, err := deployment.CreateDeploymentFromConfig(p.downstreamClient, p.cluster.ID, workloadConfigs.Deployment)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying deployment %s is running", testDeployment.Name)
			err = deployment.VerifyDeployment(p.client, p.cluster.ID, testDeployment.Namespace, testDeployment.Name)
			require.NoError(p.T(), err)

			logrus.Infof("Scaling up deployment %s to 3 replicas", testDeployment.Name)
			replicas = 3
			testDeployment.Spec.Replicas = &replicas
			testDeployment, err = deploymentapi.UpdateDeployment(p.client, p.cluster.ID, namespace.Name, testDeployment, true)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying host port connectivity after scale up for deployment %s", testDeployment.Name)
			err = networking.VerifyHostPortConnectivity(p.client, p.cluster.ID, hostPort, testDeployment.Name)
			require.NoError(p.T(), err)

			logrus.Infof("Scaling down deployment %s to 2 replicas", testDeployment.Name)
			replicas = 2
			testDeployment.Spec.Replicas = &replicas
			testDeployment, err = deploymentapi.UpdateDeployment(p.client, p.cluster.ID, namespace.Name, testDeployment, true)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying host port connectivity after scale down for deployment %s", testDeployment.Name)
			err = networking.VerifyHostPortConnectivity(p.client, p.cluster.ID, hostPort, testDeployment.Name)
			require.NoError(p.T(), err)

			logrus.Infof("Upgrading deployment %s container", testDeployment.Name)
			testDeployment.Spec.Template.Spec.Containers[0].Name = namegen.AppendRandomString("test-upgrade")
			testDeployment, err = deploymentapi.UpdateDeployment(p.client, p.cluster.ID, namespace.Name, testDeployment, true)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying host port connectivity after upgrade for deployment %s", testDeployment.Name)
			err = networking.VerifyHostPortConnectivity(p.client, p.cluster.ID, hostPort, testDeployment.Name)
			require.NoError(p.T(), err)
		})
	}
}

func (p *PortTestSuite) TestNodePortScaleAndUpgrade() {
	nodePortScaleTests := []struct {
		name string
	}{
		{"Node_Port_Scale_And_Upgrade"},
	}

	for _, tt := range nodePortScaleTests {
		p.Suite.Run(tt.name, func() {
			_, namespace, err := projectsapi.CreateProjectAndNamespace(p.client, p.cluster.ID)
			require.NoError(p.T(), err)

			workloadConfigs := new(workloads.Workloads)
			operations.LoadObjectFromMap(workloads.WorkloadsConfigurationFileKey, p.cattleConfig, workloadConfigs)

			nodePort := rand.Intn(2767) + 30000
			replicas := int32(2)
			workloadConfigs.Deployment.ObjectMeta.Namespace = namespace.Name
			workloadConfigs.Deployment.ObjectMeta.GenerateName = "node-port-scale-"
			workloadConfigs.Deployment.Spec.Replicas = &replicas

			logrus.Infof("Creating deployment with prefix: %s", workloadConfigs.Deployment.ObjectMeta.GenerateName)
			testDeployment, err := deployment.CreateDeploymentFromConfig(p.downstreamClient, p.cluster.ID, workloadConfigs.Deployment)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying deployment %s is running", testDeployment.Name)
			err = deployment.VerifyDeployment(p.client, p.cluster.ID, testDeployment.Namespace, testDeployment.Name)
			require.NoError(p.T(), err)

			serviceName := namegen.AppendRandomString("test-service")
			logrus.Infof("Creating NodePort service %s on port %d", serviceName, nodePort)
			ports := []corev1.ServicePort{
				{
					Protocol: corev1.ProtocolTCP,
					Port:     defaultPort,
					NodePort: int32(nodePort),
				},
			}
			nodePortService := services.NewServiceTemplate(serviceName, namespace.Name, corev1.ServiceTypeNodePort, ports, testDeployment.Spec.Template.Labels)
			serviceResp, err := services.CreateService(p.downstreamClient, nodePortService)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying service %s is ready", serviceResp.Name)
			err = services.VerifyService(p.downstreamClient, serviceResp)
			require.NoError(p.T(), err)

			logrus.Infof("Scaling up deployment %s to 3 replicas", testDeployment.Name)
			replicas = 3
			testDeployment.Spec.Replicas = &replicas
			testDeployment, err = deploymentapi.UpdateDeployment(p.client, p.cluster.ID, namespace.Name, testDeployment, true)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying node port connectivity after scale up for deployment %s", testDeployment.Name)
			err = networking.VerifyNodePortConnectivity(p.client, p.cluster.ID, nodePort, testDeployment.Name)
			require.NoError(p.T(), err)

			logrus.Infof("Scaling down deployment %s to 2 replicas", testDeployment.Name)
			replicas = 2
			testDeployment.Spec.Replicas = &replicas
			testDeployment, err = deploymentapi.UpdateDeployment(p.client, p.cluster.ID, namespace.Name, testDeployment, true)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying node port connectivity after scale down for deployment %s", testDeployment.Name)
			err = networking.VerifyNodePortConnectivity(p.client, p.cluster.ID, nodePort, testDeployment.Name)
			require.NoError(p.T(), err)

			logrus.Infof("Upgrading deployment %s container", testDeployment.Name)
			testDeployment.Spec.Template.Spec.Containers[0].Name = namegen.AppendRandomString("test-upgrade")
			testDeployment, err = deploymentapi.UpdateDeployment(p.client, p.cluster.ID, namespace.Name, testDeployment, true)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying node port connectivity after upgrade for deployment %s", testDeployment.Name)
			err = networking.VerifyNodePortConnectivity(p.client, p.cluster.ID, nodePort, testDeployment.Name)
			require.NoError(p.T(), err)
		})
	}
}

func (p *PortTestSuite) TestLoadBalanceScaleAndUpgrade() {
	lbScaleTests := []struct {
		name string
	}{
		{"Load_Balance_Scale_And_Upgrade"},
	}

	for _, tt := range lbScaleTests {
		p.Suite.Run(tt.name, func() {
			isEnabled, err := cloudprovider.IsCloudProviderEnabled(p.client, p.cluster.ID)
			require.NoError(p.T(), err)

			if !isEnabled {
				p.T().Skip("Load Balance test requires access to cloud provider.")
			}

			_, namespace, err := projectsapi.CreateProjectAndNamespace(p.client, p.cluster.ID)
			require.NoError(p.T(), err)

			workloadConfigs := new(workloads.Workloads)
			operations.LoadObjectFromMap(workloads.WorkloadsConfigurationFileKey, p.cattleConfig, workloadConfigs)

			port := rand.Intn(55283) + 10251
			nodePort := rand.Intn(2767) + 30000
			replicas := int32(2)
			workloadConfigs.Deployment.ObjectMeta.Namespace = namespace.Name
			workloadConfigs.Deployment.ObjectMeta.GenerateName = "load-balance-scale-"
			workloadConfigs.Deployment.Spec.Replicas = &replicas

			logrus.Infof("Creating deployment with prefix: %s", workloadConfigs.Deployment.ObjectMeta.GenerateName)
			testDeployment, err := deployment.CreateDeploymentFromConfig(p.downstreamClient, p.cluster.ID, workloadConfigs.Deployment)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying deployment %s is running", testDeployment.Name)
			err = deployment.VerifyDeployment(p.client, p.cluster.ID, testDeployment.Namespace, testDeployment.Name)
			require.NoError(p.T(), err)

			serviceName := namegen.AppendRandomString("test-service")
			logrus.Infof("Creating LoadBalancer service %s on ports %d/%d", serviceName, port, nodePort)
			ports := []corev1.ServicePort{
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       int32(port),
					TargetPort: intstr.FromInt(defaultPort),
					NodePort:   int32(nodePort),
				},
			}
			lbService := services.NewServiceTemplate(serviceName, namespace.Name, corev1.ServiceTypeLoadBalancer, ports, testDeployment.Spec.Template.Labels)
			serviceResp, err := services.CreateService(p.downstreamClient, lbService)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying service %s is ready", serviceResp.Name)
			err = services.VerifyService(p.downstreamClient, serviceResp)
			require.NoError(p.T(), err)

			logrus.Infof("Scaling up deployment %s to 3 replicas", testDeployment.Name)
			replicas = 3
			testDeployment.Spec.Replicas = &replicas
			testDeployment, err = deploymentapi.UpdateDeployment(p.client, p.cluster.ID, namespace.Name, testDeployment, true)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying load balancer connectivity after scale up for deployment %s", testDeployment.Name)
			err = networking.VerifyNodePortConnectivity(p.client, p.cluster.ID, nodePort, testDeployment.Name)
			require.NoError(p.T(), err)

			logrus.Infof("Scaling down deployment %s to 2 replicas", testDeployment.Name)
			replicas = 2
			testDeployment.Spec.Replicas = &replicas
			testDeployment, err = deploymentapi.UpdateDeployment(p.client, p.cluster.ID, namespace.Name, testDeployment, true)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying load balancer connectivity after scale down for deployment %s", testDeployment.Name)
			err = networking.VerifyNodePortConnectivity(p.client, p.cluster.ID, nodePort, testDeployment.Name)
			require.NoError(p.T(), err)

			logrus.Infof("Upgrading deployment %s container", testDeployment.Name)
			testDeployment.Spec.Template.Spec.Containers[0].Name = namegen.AppendRandomString("test-upgrade")
			testDeployment, err = deploymentapi.UpdateDeployment(p.client, p.cluster.ID, namespace.Name, testDeployment, true)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying load balancer connectivity after upgrade for deployment %s", testDeployment.Name)
			err = networking.VerifyNodePortConnectivity(p.client, p.cluster.ID, nodePort, testDeployment.Name)
			require.NoError(p.T(), err)
		})
	}
}

func TestPortTestSuite(t *testing.T) {
	suite.Run(t, new(PortTestSuite))
}
