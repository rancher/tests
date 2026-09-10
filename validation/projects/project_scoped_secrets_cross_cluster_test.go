//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11

package projects

import (
	"fmt"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extnamespaceapi "github.com/rancher/shepherd/extensions/kubeapi/namespaces"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	secretapi "github.com/rancher/tests/actions/kubeapi/secrets"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var crossClusterOpaqueSecretData = map[string]string{
	"hello": "world",
}

type ProjectScopedSecretCrossClusterTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (pss *ProjectScopedSecretCrossClusterTestSuite) SetupSuite() {
	pss.session = session.NewSession()

	client, err := rancher.NewClient("", pss.session)
	require.NoError(pss.T(), err)
	pss.client = client

	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(pss.T(), clusterName, "Downstream cluster name should be set in the config file")

	clusterID, err := clusters.GetClusterIDByName(pss.client, clusterName)
	require.NoError(pss.T(), err, "Error getting downstream cluster ID")

	pss.cluster, err = pss.client.Management.Cluster.ByID(clusterID)
	require.NoError(pss.T(), err)
}

func (pss *ProjectScopedSecretCrossClusterTestSuite) TearDownSuite() {
	pss.session.Cleanup()
}

func (pss *ProjectScopedSecretCrossClusterTestSuite) TestProjectIDClusterValidation() {
	subSession := pss.session.NewSession()
	defer subSession.Cleanup()

	require.NotEqual(pss.T(), extclusterapi.LocalCluster, pss.cluster.ID, "Test requires a configured downstream cluster")

	log.Info("Create a project and project-scoped secret in the local cluster.")
	sourceProject, err := projectapi.CreateProject(pss.client, extclusterapi.LocalCluster)
	require.NoError(pss.T(), err)

	sourceSecret, err := secretapi.CreateProjectScopedSecret(pss.client, extclusterapi.LocalCluster, sourceProject.Name, crossClusterOpaqueSecretData, corev1.SecretTypeOpaque)
	require.NoError(pss.T(), err)
	require.NoError(pss.T(), secretapi.VerifyProjectScopedSecretLabel(sourceSecret, sourceProject.Name))

	log.Info("Create a standard user with cluster-owner access to the downstream cluster.")
	_, downstreamUserClient, err := rbac.AddUserWithRoleToCluster(pss.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), pss.cluster, nil)
	require.NoError(pss.T(), err)

	log.Info("Create a project and project-scoped secret in the downstream cluster.")
	targetProject, err := projectapi.CreateProject(pss.client, pss.cluster.ID)
	require.NoError(pss.T(), err)

	targetSecret, err := secretapi.CreateProjectScopedSecret(pss.client, pss.cluster.ID, targetProject.Name, crossClusterOpaqueSecretData, corev1.SecretTypeOpaque)
	require.NoError(pss.T(), err)
	require.NoError(pss.T(), secretapi.VerifyProjectScopedSecretLabel(targetSecret, targetProject.Name))

	foreignProjectID := fmt.Sprintf("%s:%s", extclusterapi.LocalCluster, sourceProject.Name)
	targetProjectID := fmt.Sprintf("%s:%s", pss.cluster.ID, targetProject.Name)

	tests := []struct {
		name                  string
		projectID             string
		projectName           string
		setProjectIDOnUpdate  bool
		projectScopedSecret   *corev1.Secret
		secretProjectName     string
		shouldPropagateSecret bool
	}{
		{
			name:                  "same-cluster projectId propagates project secret",
			projectID:             targetProjectID,
			projectName:           targetProject.Name,
			projectScopedSecret:   targetSecret,
			secretProjectName:     targetProject.Name,
			shouldPropagateSecret: true,
		},
		{
			name:                "cross-cluster projectId on namespace creation is rejected",
			projectID:           foreignProjectID,
			projectScopedSecret: sourceSecret,
		},
		{
			name:                 "cross-cluster projectId on namespace update is rejected",
			projectID:            foreignProjectID,
			setProjectIDOnUpdate: true,
			projectScopedSecret:  sourceSecret,
		},
	}

	for _, tt := range tests {
		pss.Run(tt.name, func() {
			var annotations map[string]string
			if !tt.setProjectIDOnUpdate && tt.projectName == "" {
				annotations = map[string]string{namespaceapi.ProjectIDAnnotation: tt.projectID}
			}

			namespace, err := namespaceapi.CreateNamespace(
				downstreamUserClient,
				pss.cluster.ID,
				tt.projectName,
				namegen.AppendRandomString("project-id-cluster-validation-"),
				"",
				nil,
				annotations,
			)
			require.NoError(pss.T(), err)

			if tt.setProjectIDOnUpdate {
				if namespace.Annotations == nil {
					namespace.Annotations = make(map[string]string)
				}
				namespace.Annotations[namespaceapi.ProjectIDAnnotation] = tt.projectID
				namespace, err = extnamespaceapi.UpdateNamespace(downstreamUserClient, pss.cluster.ID, namespace)
				require.NoError(pss.T(), err)
			}

			require.Equal(pss.T(), tt.projectID, namespace.Annotations[namespaceapi.ProjectIDAnnotation])

			if tt.shouldPropagateSecret {
				require.NoError(
					pss.T(),
					secretapi.VerifyPropagatedNamespaceSecrets(pss.client, pss.cluster.ID, tt.secretProjectName, tt.projectScopedSecret, []*corev1.Namespace{namespace}),
					"Same-cluster project-scoped secret was not propagated",
				)
				return
			}

			require.Never(
				pss.T(),
				func() bool {
					_, getErr := secretapi.GetSecretByName(pss.client, pss.cluster.ID, namespace.Name, tt.projectScopedSecret.Name, metav1.GetOptions{})
					return !apierrors.IsNotFound(getErr)
				},
				defaults.OneMinuteTimeout,
				defaults.FiveSecondTimeout,
				"Expected project-scoped secret %q from project %q to remain absent from namespace %q in cluster %q",
				tt.projectScopedSecret.Name,
				tt.projectID,
				namespace.Name,
				pss.cluster.ID,
			)
		})
	}
}

func TestProjectScopedSecretCrossClusterTestSuite(t *testing.T) {
	suite.Run(t, new(ProjectScopedSecretCrossClusterTestSuite))
}
