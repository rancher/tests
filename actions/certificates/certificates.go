package certificates

import (
	"context"
	"strings"
	"time"

	apiv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/sshkeys"
	"github.com/rancher/shepherd/pkg/wait"
	"github.com/sirupsen/logrus"
	kwait "k8s.io/apimachinery/pkg/util/wait"
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

	ctx, cancel := context.WithTimeout(context.Background(), defaults.FifteenMinuteTimeout)
	defer cancel()

	err = kwait.PollUntilContextTimeout(ctx, 10*time.Second, defaults.ThirtyMinuteTimeout, false, func(context.Context) (done bool, err error) {
		cluster, err = client.Steve.SteveType(stevetypes.Provisioning).ByID(cluster.ID)
		clusterStatus := &provv1.ClusterStatus{}
		err = steveV1.ConvertToK8sType(cluster.Status, clusterStatus)
		if err != nil {
			return false, nil
		}

		if !clusterStatus.Ready || cluster.State.Name != "active" || cluster.State.Error == true {
			return false, nil
		}

		return true, nil
	})
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
		// Ignoring if certificate doesn't exist because some certs are not present on some node types
		certString, _ := sshNode.ExecuteCommand("sudo openssl x509 -enddate -noout -in " + certsPath + filename + certFileExtension)
		if strings.Contains(certString, "No such file or directory") {
			logrus.Tracef("Certificate %v does not exist", filename)
		} else if certString != "" {
			certificates[filename] = certString
		}
	}

	return certificates, nil
}
