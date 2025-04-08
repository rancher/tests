//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package certificates

import (
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	"github.com/rancher/tests/actions/kubeapi/ingresses"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/secrets"
	"github.com/rancher/tests/actions/workloads/deployment"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	testHost   = "test.example.com"
	testHost1  = "test1.example.com"
	testHost2  = "test2.example.com"
	testHost3  = "test3.example.com"
	sharedHost = "shared.example.com"
	rotateHost = "rotate.example.com"
)

type CertificateTestSuite struct {
	suite.Suite
	client    *rancher.Client
	session   *session.Session
	cluster   *management.Cluster
	certData1 string
	keyData1  string
	certData2 string
	keyData2  string
}

func (c *CertificateTestSuite) SetupSuite() {
	c.session = session.NewSession()

	client, err := rancher.NewClient("", c.session)
	require.NoError(c.T(), err)
	c.client = client

	log.Info("Getting cluster name from the config file")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(c.T(), clusterName, "Cluster name should be set")

	clusterID, err := clusters.GetClusterIDByName(c.client, clusterName)
	require.NoError(c.T(), err, "Error getting cluster ID")

	c.cluster, err = c.client.Management.Cluster.ByID(clusterID)
	require.NoError(c.T(), err)

	log.Info("Generating first self-signed certificate and key for certificate operations")
	c.certData1, c.keyData1, err = secrets.GenerateSelfSignedCert()
	require.NoError(c.T(), err)

	log.Info("Generating second self-signed certificate and key for certificate operations")
	c.certData2, c.keyData2, err = secrets.GenerateSelfSignedCert()
	require.NoError(c.T(), err)
}

func (c *CertificateTestSuite) TearDownSuite() {
	c.session.Cleanup()
}

func (c *CertificateTestSuite) createTestCertAndNamespace(certData, keyData string) (*v3.Project, *corev1.Namespace, *corev1.Secret, error) {
	project, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(c.client, c.cluster.ID)
	require.NoError(c.T(), err, "Error creating project and namespace")

	tlsSecret, err := c.createCertWithData(namespace, certData, keyData)
	require.NoError(c.T(), err, "Error creating tls secret")

	return project, namespace, tlsSecret, nil
}

func (c *CertificateTestSuite) createCertWithData(namespace *corev1.Namespace, certData, keyData string) (*corev1.Secret, error) {
	secretData := map[string][]byte{
		corev1.TLSCertKey:       []byte(certData),
		corev1.TLSPrivateKeyKey: []byte(keyData),
	}
	return secrets.CreateSecret(c.client, c.cluster.ID, namespace.Name, secretData, corev1.SecretTypeTLS)
}

func (c *CertificateTestSuite) setupIngressWithCert(namespace *corev1.Namespace, tlsSecret *corev1.Secret, hosts []string, deploymentName string) (*netv1.Ingress, error) {
	deploymentForIngress, err := deployment.CreateDeployment(c.client, c.cluster.ID, namespace.Name, 1, tlsSecret.Name, "", false, false, false, true)
	require.NoError(c.T(), err)

	ingressTemplate, err := ingresses.CreateServiceAndIngressTemplateForDeployment(c.client, c.cluster.ID, namespace.Name, deploymentForIngress)
	require.NoError(c.T(), err, "Error creating ingress template")

	ingressTemplate.Spec.TLS = []netv1.IngressTLS{
		{
			Hosts:      hosts,
			SecretName: tlsSecret.Name,
		},
	}

	return ingresses.CreateIngress(c.client, c.cluster.ID, ingressTemplate.Name, namespace.Name, &ingressTemplate.Spec)
}

func (c *CertificateTestSuite) TestCertificateScopes() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a certificate in the namespace")
	_, namespace, tlsSecret, err := c.createTestCertAndNamespace(c.certData1, c.keyData1)
	require.NoError(c.T(), err)

	log.Info("Verifying the certificate exists in the namespace")
	adminContext, err := clusterapi.GetClusterWranglerContext(c.client, c.cluster.ID)
	require.NoError(c.T(), err)

	retrievedSecret, err := adminContext.Core.Secret().Get(namespace.Name, tlsSecret.Name, metav1.GetOptions{})
	require.NoError(c.T(), err)
	require.Equal(c.T(), corev1.SecretTypeTLS, retrievedSecret.Type)

	log.Info("Creating a second namespace")
	_, namespace2, err := projects.CreateProjectAndNamespaceUsingWrangler(c.client, c.cluster.ID)
	require.NoError(c.T(), err)

	log.Info("Verifying the certificate is not accessible in the second namespace")
	_, err = adminContext.Core.Secret().Get(namespace2.Name, tlsSecret.Name, metav1.GetOptions{})

	require.Error(c.T(), err, "Should return an error when accessing certificate from another namespace")
	require.True(c.T(), errors.IsNotFound(err) || errors.IsForbidden(err), "Error should be 'not found' or 'forbidden' when accessing certificate from another namespace")
}

func (c *CertificateTestSuite) TestMultipleCertificateTypes() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating first certificate")
	_, namespace, cert1, err := c.createTestCertAndNamespace(c.certData1, c.keyData1)
	require.NoError(c.T(), err)

	log.Info("Creating second certificate")
	cert2, err := c.createCertWithData(namespace, c.certData2, c.keyData2)
	require.NoError(c.T(), err)

	log.Info("Verifying both certificates were created successfully")
	adminContext, err := clusterapi.GetClusterWranglerContext(c.client, c.cluster.ID)
	require.NoError(c.T(), err)

	retrievedCert1, err := adminContext.Core.Secret().Get(namespace.Name, cert1.Name, metav1.GetOptions{})
	require.NoError(c.T(), err)
	require.Equal(c.T(), corev1.SecretTypeTLS, retrievedCert1.Type)

	retrievedCert2, err := adminContext.Core.Secret().Get(namespace.Name, cert2.Name, metav1.GetOptions{})
	require.NoError(c.T(), err)
	require.Equal(c.T(), corev1.SecretTypeTLS, retrievedCert2.Type)
}

func (c *CertificateTestSuite) TestUpdateCertificate() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, tlsSecret, err := c.createTestCertAndNamespace(c.certData1, c.keyData1)
	require.NoError(c.T(), err)

	log.Info("Getting the certificate for updating")
	adminContext, err := clusterapi.GetClusterWranglerContext(c.client, c.cluster.ID)
	require.NoError(c.T(), err)

	retrievedSecret, err := adminContext.Core.Secret().Get(namespace.Name, tlsSecret.Name, metav1.GetOptions{})
	require.NoError(c.T(), err)

	log.Info("Updating the certificate with new data")
	secretData2 := map[string][]byte{
		corev1.TLSCertKey:       []byte(c.certData2),
		corev1.TLSPrivateKeyKey: []byte(c.keyData2),
	}

	updatedSecret := retrievedSecret.DeepCopy()
	updatedSecret.Data = secretData2

	_, err = adminContext.Core.Secret().Update(updatedSecret)
	require.NoError(c.T(), err)

	log.Info("Verifying the certificate was updated")
	retrievedUpdatedSecret, err := adminContext.Core.Secret().Get(namespace.Name, tlsSecret.Name, metav1.GetOptions{})
	require.NoError(c.T(), err)

	require.Equal(c.T(), secretData2[corev1.TLSCertKey], retrievedUpdatedSecret.Data[corev1.TLSCertKey])
	require.Equal(c.T(), secretData2[corev1.TLSPrivateKeyKey], retrievedUpdatedSecret.Data[corev1.TLSPrivateKeyKey])
}

func (c *CertificateTestSuite) TestCrossProjectAccess() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating first project and namespace with TLS secret")
	_, namespace1, tlsSecret, err := c.createTestCertAndNamespace(c.certData1, c.keyData1)
	require.NoError(c.T(), err)

	log.Info("Creating second project and namespace")
	project2, _, _, err := c.createTestCertAndNamespace(c.certData2, c.keyData2)
	require.NoError(c.T(), err)

	log.Info("Creating user with access to second project only")
	user, userClient, err := rbac.AddUserWithRoleToCluster(c.client, rbac.StandardUser.String(), rbac.ProjectOwner.String(), c.cluster, project2)
	require.NoError(c.T(), err)
	log.Infof("Created user: %v", user.Username)

	log.Infof("As user %s, attempting to access the TLS secret in the first project", user.Username)
	userContext, err := clusterapi.GetClusterWranglerContext(userClient, c.cluster.ID)
	require.NoError(c.T(), err)

	_, err = userContext.Core.Secret().Get(namespace1.Name, tlsSecret.Name, metav1.GetOptions{})
	require.True(c.T(), errors.IsForbidden(err), "User should not be able to access TLS secret in another project")
}

func (c *CertificateTestSuite) TestCertificateWithIngressSingleNS() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, tlsSecret, err := c.createTestCertAndNamespace(c.certData1, c.keyData1)
	require.NoError(c.T(), err)

	ingress, err := c.setupIngressWithCert(namespace, tlsSecret, []string{testHost}, "")
	require.NoError(c.T(), err)

	require.Len(c.T(), ingress.Spec.TLS, 1)
	require.Equal(c.T(), tlsSecret.Name, ingress.Spec.TLS[0].SecretName)
}

func (c *CertificateTestSuite) TestCertificateWithIngressMultiNS() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating first tls secret and namespace")
	_, namespace1, tlsSecret1, err := c.createTestCertAndNamespace(c.certData1, c.keyData1)
	require.NoError(c.T(), err)

	log.Info("Creating second tls secret and namespace")
	_, namespace2, tlsSecret2, err := c.createTestCertAndNamespace(c.certData2, c.keyData2)
	require.NoError(c.T(), err)

	log.Info("Creating ingress with first tls secret")
	ingress1, err := c.setupIngressWithCert(namespace1, tlsSecret1, []string{testHost1}, "")
	require.NoError(c.T(), err)
	require.Len(c.T(), ingress1.Spec.TLS, 1)
	require.Equal(c.T(), tlsSecret1.Name, ingress1.Spec.TLS[0].SecretName)

	log.Info("Creating ingress with second tls secret")
	ingress2, err := c.setupIngressWithCert(namespace2, tlsSecret2, []string{testHost2}, "")
	require.NoError(c.T(), err)
	require.Len(c.T(), ingress2.Spec.TLS, 1)
	require.Equal(c.T(), tlsSecret2.Name, ingress2.Spec.TLS[0].SecretName)
}

func (c *CertificateTestSuite) TestUpdateCertificateWithIngress() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, tlsSecret, err := c.createTestCertAndNamespace(c.certData1, c.keyData1)
	require.NoError(c.T(), err)

	ingress, err := c.setupIngressWithCert(namespace, tlsSecret, []string{testHost}, "")
	require.NoError(c.T(), err)

	log.Info("Updating the certificate")
	adminContext, err := clusterapi.GetClusterWranglerContext(c.client, c.cluster.ID)
	require.NoError(c.T(), err)

	retrievedSecret, err := adminContext.Core.Secret().Get(namespace.Name, tlsSecret.Name, metav1.GetOptions{})
	require.NoError(c.T(), err)

	secretData2 := map[string][]byte{
		corev1.TLSCertKey:       []byte(c.certData2),
		corev1.TLSPrivateKeyKey: []byte(c.keyData2),
	}

	updatedSecret := retrievedSecret.DeepCopy()
	updatedSecret.Data = secretData2

	_, err = adminContext.Core.Secret().Update(updatedSecret)
	require.NoError(c.T(), err)

	updatedIngress, err := ingresses.GetIngressByName(c.client, c.cluster.ID, namespace.Name, ingress.Name)
	require.NoError(c.T(), err)
	require.Len(c.T(), updatedIngress.Spec.TLS, 1)
	require.Equal(c.T(), tlsSecret.Name, updatedIngress.Spec.TLS[0].SecretName)
}

func (c *CertificateTestSuite) TestSharedCertificateBetweenIngresses() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, tlsSecret, err := c.createTestCertAndNamespace(c.certData1, c.keyData1)
	require.NoError(c.T(), err)

	log.Info("Creating first ingress with certificate")
	ingress1, err := c.setupIngressWithCert(namespace, tlsSecret, []string{sharedHost}, "app1")
	require.NoError(c.T(), err)
	require.NotNil(c.T(), ingress1)

	log.Info("Creating second ingress with same certificate")
	ingress2, err := c.setupIngressWithCert(namespace, tlsSecret, []string{sharedHost}, "app2")
	require.NoError(c.T(), err)
	require.NotNil(c.T(), ingress2)

	log.Info("Verify both ingresses use the same certificate")
	require.Len(c.T(), ingress1.Spec.TLS, 1)
	require.Equal(c.T(), tlsSecret.Name, ingress1.Spec.TLS[0].SecretName)

	require.Len(c.T(), ingress2.Spec.TLS, 1)
	require.Equal(c.T(), tlsSecret.Name, ingress2.Spec.TLS[0].SecretName)
}

func (c *CertificateTestSuite) TestDeleteCertificateUsedByIngress() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, tlsSecret, err := c.createTestCertAndNamespace(c.certData1, c.keyData1)
	require.NoError(c.T(), err)

	log.Info("Creating ingress with tls secret")
	ingress, err := c.setupIngressWithCert(namespace, tlsSecret, []string{testHost}, "")
	require.NoError(c.T(), err)

	log.Info("Deleting the tls secret")
	adminContext, err := clusterapi.GetClusterWranglerContext(c.client, c.cluster.ID)
	require.NoError(c.T(), err)
	err = adminContext.Core.Secret().Delete(namespace.Name, tlsSecret.Name, &metav1.DeleteOptions{})
	require.NoError(c.T(), err)

	log.Info("Verifying that the tls secret was deleted")
	_, err = adminContext.Core.Secret().Get(namespace.Name, tlsSecret.Name, metav1.GetOptions{})
	require.True(c.T(), errors.IsNotFound(err), "tls secret should be deleted")

	updatedIngress, err := ingresses.GetIngressByName(c.client, c.cluster.ID, namespace.Name, ingress.Name)
	require.NoError(c.T(), err)
	require.Len(c.T(), updatedIngress.Spec.TLS, 1)
	require.Equal(c.T(), tlsSecret.Name, updatedIngress.Spec.TLS[0].SecretName)
}

func (c *CertificateTestSuite) TestCertificateWithMultipleHosts() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, tlsSecret, err := c.createTestCertAndNamespace(c.certData1, c.keyData1)
	require.NoError(c.T(), err)

	hosts := []string{testHost1, testHost2, testHost3}
	ingress, err := c.setupIngressWithCert(namespace, tlsSecret, hosts, "")
	require.NoError(c.T(), err)

	require.Len(c.T(), ingress.Spec.TLS, 1)
	require.Equal(c.T(), tlsSecret.Name, ingress.Spec.TLS[0].SecretName)
	require.Len(c.T(), ingress.Spec.TLS[0].Hosts, 3)
	require.Contains(c.T(), ingress.Spec.TLS[0].Hosts, testHost1)
	require.Contains(c.T(), ingress.Spec.TLS[0].Hosts, testHost2)
	require.Contains(c.T(), ingress.Spec.TLS[0].Hosts, testHost3)
}

func (c *CertificateTestSuite) TestCertificateWithAnnotations() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating project and namespace")
	_, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(c.client, c.cluster.ID)
	require.NoError(c.T(), err)

	log.Info("Creating certificate with annotations")

	adminContext, err := clusterapi.GetClusterWranglerContext(c.client, c.cluster.ID)
	require.NoError(c.T(), err)

	secretData := map[string][]byte{
		corev1.TLSCertKey:       []byte(c.certData1),
		corev1.TLSPrivateKeyKey: []byte(c.keyData1),
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "cert-with-annotations-",
			Namespace:    namespace.Name,
			Annotations: map[string]string{
				"cert-manager.io/issuer":      "test-issuer",
				"cert-manager.io/issuer-kind": "ClusterIssuer",
				"custom-annotation":           "test-value",
			},
		},
		Type: corev1.SecretTypeTLS,
		Data: secretData,
	}

	createdSecret, err := adminContext.Core.Secret().Create(secret)
	require.NoError(c.T(), err)

	retrievedSecret, err := adminContext.Core.Secret().Get(namespace.Name, createdSecret.Name, metav1.GetOptions{})
	require.NoError(c.T(), err)
	require.Equal(c.T(), "test-issuer", retrievedSecret.Annotations["cert-manager.io/issuer"])
	require.Equal(c.T(), "ClusterIssuer", retrievedSecret.Annotations["cert-manager.io/issuer-kind"])
	require.Equal(c.T(), "test-value", retrievedSecret.Annotations["custom-annotation"])

	_, err = c.setupIngressWithCert(namespace, createdSecret, []string{testHost}, "")
	require.NoError(c.T(), err)

}

func (c *CertificateTestSuite) TestCertificateRotation() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, tlsSecret, err := c.createTestCertAndNamespace(c.certData1, c.keyData1)
	require.NoError(c.T(), err)

	ingress, err := c.setupIngressWithCert(namespace, tlsSecret, []string{rotateHost}, "")
	require.NoError(c.T(), err)

	log.Info("Creating new rotated certificate")
	rotatedCert, err := c.createCertWithData(namespace, c.certData2, c.keyData2)
	require.NoError(c.T(), err)

	log.Info("Deleting old ingress")
	err = ingresses.DeleteIngress(c.client, c.cluster.ID, namespace.Name, ingress.Name)
	require.NoError(c.T(), err)

	log.Info("Creating new ingress with rotated certificate")
	updatedIngress, err := c.setupIngressWithCert(namespace, rotatedCert, []string{rotateHost}, "rotated")
	require.NoError(c.T(), err)
	require.NotNil(c.T(), updatedIngress)

	require.Len(c.T(), updatedIngress.Spec.TLS, 1)
	require.Equal(c.T(), rotatedCert.Name, updatedIngress.Spec.TLS[0].SecretName)
}

func TestCertificateTestSuite(t *testing.T) {
	suite.Run(t, new(CertificateTestSuite))
}
