package certificates

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/rancher/norman/types"
	apiv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/sshkeys"
	"github.com/rancher/shepherd/pkg/nodes"
	"github.com/rancher/shepherd/pkg/wait"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

const (
	certFileExtension            = ".crt"
	pemFileExtension             = ".pem"
	privateKeySSHKeyRegExPattern = `-----BEGIN RSA PRIVATE KEY-{3,}\n([\s\S]*?)\n-{3,}END RSA PRIVATE KEY-----`
)

// CertRotationCompleteCheckFunc returns a watch check function that checks if the certificate rotation is complete
func CertRotationCompleteCheckFunc(generation int64) wait.WatchCheckFunc {
	return func(event watch.Event) (bool, error) {
		controlPlane := event.Object.(*rkev1.RKEControlPlane)
		return controlPlane.Status.CertificateRotationGeneration == generation, nil
	}
}

// GetClusterCertificates returns the certificates from a downstream cluster.
func GetClusterCertificates(client *rancher.Client, clusterName string) (map[string]map[string]string, error) {
	clusterID, err := clusters.GetClusterIDByName(client, clusterName)
	if err != nil {
		return nil, err
	}

	steveclient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return nil, err
	}

	nodeList, err := steveclient.SteveType(stevetypes.Node).List(nil)
	if err != nil {
		return nil, err
	}

	nodeCertificates := map[string]map[string]string{}

	for _, node := range nodeList.Data {
		newCertificate, err := getCertificatesFromMachine(client, &node)
		if err != nil {
			return nil, err
		}
		nodeCertificates[node.ID] = newCertificate
	}

	return nodeCertificates, nil
}

// RotateCerts rotates the certificates in a RKE2/K3S downstream cluster.
func RotateCerts(client *rancher.Client, clusterName string) error {
	id, err := clusters.GetV1ProvisioningClusterByName(client, clusterName)
	if err != nil {
		return err
	}

	cluster, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(id)
	if err != nil {
		return err
	}

	clusterSpec := &apiv1.ClusterSpec{}
	err = v1.ConvertToK8sType(cluster.Spec, clusterSpec)
	if err != nil {
		return err
	}

	updatedCluster := *cluster
	generation := int64(1)

	if clusterSpec.RKEConfig.RotateCertificates != nil {
		generation = clusterSpec.RKEConfig.RotateCertificates.Generation + 1
	}

	clusterSpec.RKEConfig.RotateCertificates = &rkev1.RotateCertificates{
		Generation: generation,
	}

	updatedCluster.Spec = *clusterSpec

	_, err = client.Steve.SteveType(stevetypes.Provisioning).Update(cluster, updatedCluster)
	if err != nil {
		return err
	}

	kubeRKEClient, err := client.GetKubeAPIRKEClient()
	if err != nil {
		return err
	}

	result, err := kubeRKEClient.RKEControlPlanes(namespaces.FleetDefault).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + clusterName,
		TimeoutSeconds: &defaults.WatchTimeoutSeconds,
	})
	if err != nil {
		return err
	}

	checkFunc := CertRotationCompleteCheckFunc(generation)
	err = wait.WatchWait(result, checkFunc)
	if err != nil {
		return err
	}

	return nil
}

// getCertificatesFromMachine retrieves the certificates from a given machine node.
func getCertificatesFromMachine(client *rancher.Client, machineNode *v1.SteveAPIObject) (map[string]string, error) {
	certificates := map[string]string{}

	sshNode, err := sshkeys.GetSSHNodeFromMachine(client, machineNode)
	if err != nil {
		return nil, err
	}

	logrus.Debugf("Getting certificates from machine: %s", machineNode.Name)

	clusterType := machineNode.Labels["node.kubernetes.io/instance-type"]
	certsPath := "/var/lib/rancher/" + clusterType + "/server/tls/"

	certsList := []string{
		"client-admin",
		"client-auth-proxy",
		"client-controller",
		"client-kube-apiserver",
		"client-kubelet",
		"client-kube-proxy",

		"client-" + clusterType + "-cloud-controller",
		"client-" + clusterType + "-controller",

		"client-scheduler",
		"client-supervisor",
		"etcd/client",
		"etcd/peer-server-client",
		"kube-controller-manager/kube-controller-manager",
		"kube-scheduler/kube-scheduler",
		"serving-kube-apiserver",
	}

	for _, filename := range certsList {
		// ignoring errors since node roles have different subsets of the certs.
		certString, _ := sshNode.ExecuteCommand("openssl x509 -enddate -noout -in " + certsPath + filename + certFileExtension)

		if certString != "" {
			certificates[filename] = certString
		}
	}

	return certificates, nil
}

// RotateRKE1Certs rotates the certificates in a RKE1 downstream cluster.
func RotateRKE1Certs(client *rancher.Client, clusterName string) error {
	clusterID, err := clusters.GetClusterIDByName(client, clusterName)
	if err != nil {
		return err
	}

	nodeList, err := client.Management.Node.List(&types.ListOpts{Filters: map[string]interface{}{
		"clusterId": clusterID,
	}})
	if err != nil {
		return err
	}

	nodeCertificates := map[string]map[string]string{}

	for _, node := range nodeList.Data {
		newCertificate, err := getCertificatesFromV3Node(client, &node)
		if err != nil {
			return err
		}

		nodeCertificates[node.ID] = newCertificate
	}

	cluster, err := client.Management.Cluster.ByID(clusterID)
	if err != nil {
		return err
	}

	logrus.Infof("Rotating certs...")
	_, err = client.Management.Cluster.ActionRotateCertificates(cluster, &management.RotateCertificateInput{CACertificates: false, Services: ""})
	if err != nil {
		return err
	}

	adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
	if err != nil {
		return err
	}

	err = clusters.WaitClusterToBeUpgraded(adminClient, clusterID)
	if err != nil {
		return err
	}

	postRotatedCertificates := map[string]map[string]string{}

	for _, node := range nodeList.Data {
		newCertificate, err := getCertificatesFromV3Node(client, &node)
		if err != nil {
			return err
		}

		postRotatedCertificates[node.ID] = newCertificate
	}

	isAllCertRotated := VerifyCertificateRotation(nodeCertificates, postRotatedCertificates)
	if !isAllCertRotated {
		return errors.New("Certificates weren't properly rotated")
	}

	return nil
}

// getCertificatesFromV3Node retrieves the certificates from a given RKE1 v3 node.
func getCertificatesFromV3Node(client *rancher.Client, v3Node *management.Node) (map[string]string, error) {
	certificates := map[string]string{}

	sshNode, err := getSSHNodeFromV3Node(client, v3Node)
	if err != nil {
		return nil, err
	}

	logrus.Infof("Getting certificates from machine: %s", v3Node.ID)

	certsPath := "/etc/kubernetes/ssl/"

	certsList := []string{
		"kube-apiserver",
		"kube-apiserver-proxy-client",
		"$(find /etc/kubernetes/ssl -name 'kube-etcd-*' | grep -v \"key\")",
		"kube-controller-manager",
		// Due to GH issue https://github.com/rancher/rancher/issues/44993, kube-node and kube-proxy are commented out.
		//"kube-node",
		//"kube-proxy",
		"kube-scheduler",
	}

	for _, filename := range certsList {
		fullAbsolutePath := certsPath + filename + pemFileExtension
		if strings.Contains(filename, "etcd") {
			fullAbsolutePath = filename
		}

		certString, _ := sshNode.ExecuteCommand("openssl x509 -enddate -noout -in " + fullAbsolutePath)

		if certString != "" {
			certificates[filename] = certString
		}
	}

	return certificates, nil
}

// downloadRKE1SSHKeys downloads the SSH keys for a given RKE1 v3 node.
func downloadRKE1SSHKeys(client *rancher.Client, v3Node *management.Node) ([]byte, error) {
	sshKeyLink := v3Node.Links["nodeConfig"]

	req, err := http.NewRequest("GET", sshKeyLink, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Authorization", "Bearer "+client.RancherConfig.AdminToken)

	resp, err := client.Management.APIBaseClient.Ops.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	privateSSHKeyRegEx := regexp.MustCompile(privateKeySSHKeyRegExPattern)
	privateSSHKey := privateSSHKeyRegEx.FindString(string(bodyBytes))

	return []byte(privateSSHKey), err
}

// getSSHNodeFromV3Node retrieves the SSH node information from a given RKE1 v3 node.
func getSSHNodeFromV3Node(client *rancher.Client, v3Node *management.Node) (*nodes.Node, error) {
	sshkey, err := downloadRKE1SSHKeys(client, v3Node)
	if err != nil {
		return nil, err
	}

	clusterNode := &nodes.Node{
		NodeID:          v3Node.ID,
		PublicIPAddress: v3Node.ExternalIPAddress,
		SSHUser:         v3Node.SshUser,
		SSHKey:          sshkey,
	}

	return clusterNode, nil
}
