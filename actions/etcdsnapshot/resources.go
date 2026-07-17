package etcdsnapshot

import (
	"errors"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	extensionsingress "github.com/rancher/shepherd/extensions/ingresses"
	"github.com/rancher/shepherd/extensions/workloads"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/services"
	deploy "github.com/rancher/tests/actions/workloads/deployment"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networking "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func createPostBackupWorkloads(client *rancher.Client, clusterID string, podTemplate corev1.PodTemplateSpec, deployment *v1.Deployment) (*steveV1.SteveAPIObject, *steveV1.SteveAPIObject, error) {
	workloadNamePostBackup := namegen.AppendRandomString(postWorkload)

	postBackupDeployment := workloads.NewDeploymentTemplate(workloadNamePostBackup, namespaces.Default, podTemplate, isCattleLabeled, nil)
	postBackupService := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAppendName + workloadNamePostBackup,
			Namespace: namespaces.Default,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name: port,
					Port: 80,
				},
			},
			Selector: deployment.Spec.Template.Labels,
		},
	}

	steveclient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return nil, nil, err
	}

	postDeploymentResp, err := createDeployment(steveclient, workloadNamePostBackup, postBackupDeployment)
	if err != nil {
		return nil, nil, err
	}

	err = deploy.VerifyDeployment(client, clusterID, postDeploymentResp.Namespace, postDeploymentResp.Name)
	if err != nil {
		return nil, nil, err
	}

	if workloadNamePostBackup != postDeploymentResp.ObjectMeta.Name {
		return nil, nil, fmt.Errorf("PostBackup deployment name %s does not match created deployment %s ", workloadNamePostBackup, postDeploymentResp.ObjectMeta.Name)
	}

	postServiceResp, err := services.CreateService(steveclient, postBackupService)
	if err != nil {
		return nil, nil, err
	}

	err = services.VerifyService(steveclient, postServiceResp)
	if err != nil {
		return nil, nil, err
	}

	if serviceAppendName+workloadNamePostBackup != postServiceResp.ObjectMeta.Name {
		return nil, nil, fmt.Errorf("PostBackup service name %s does not match created deployment %s ", serviceAppendName+workloadNamePostBackup, postServiceResp.ObjectMeta.Name)
	}

	return postDeploymentResp, postServiceResp, nil
}

func createAndVerifyResources(client *rancher.Client, clusterID, containerImage string) (*corev1.PodTemplateSpec, *v1.Deployment, *steveV1.SteveAPIObject, *steveV1.SteveAPIObject, *steveV1.SteveAPIObject, error) {
	var containerTemplate corev1.Container
	initialIngressName := namegen.AppendRandomString(InitialIngress)
	initialWorkloadName := namegen.AppendRandomString(InitialWorkload)

	containerTemplate = workloads.NewContainer(containerName, containerImage, corev1.PullAlways, []corev1.VolumeMount{}, []corev1.EnvFromSource{}, nil, nil, nil)

	podTemplate := workloads.NewPodTemplate([]corev1.Container{containerTemplate}, []corev1.Volume{}, []corev1.LocalObjectReference{}, nil, map[string]string{})
	deploymentTemplate := workloads.NewDeploymentTemplate(initialWorkloadName, namespaces.Default, podTemplate, isCattleLabeled, nil)

	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAppendName + initialWorkloadName,
			Namespace: namespaces.Default,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name: port,
					Port: 80,
				},
			},
			Selector: deploymentTemplate.Spec.Template.Labels,
		},
	}

	steveclient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	deploymentResp, err := createDeployment(steveclient, initialWorkloadName, deploymentTemplate)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	err = deploy.VerifyDeployment(client, clusterID, deploymentResp.Namespace, deploymentResp.Name)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	if initialWorkloadName != deploymentResp.ObjectMeta.Name {
		return nil, nil, nil, nil, nil, errors.New("deployment name doesn't match spec")
	}

	serviceResp, err := services.CreateService(steveclient, service)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	err = services.VerifyService(steveclient, serviceResp)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	if serviceAppendName+initialWorkloadName != serviceResp.ObjectMeta.Name {
		return nil, nil, nil, nil, nil, errors.New("service name doesn't match spec")
	}

	path := extensionsingress.NewIngressPathTemplate(networking.PathTypeImplementationSpecific, ingressPath, serviceAppendName+initialWorkloadName, 80)
	ingressTemplate := extensionsingress.NewIngressTemplate(initialIngressName, namespaces.Default, "", []networking.HTTPIngressPath{path})

	ingressResp, err := extensionsingress.CreateIngress(steveclient, initialIngressName, ingressTemplate)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	err = extensionsingress.WaitIngress(steveclient, ingressResp, initialIngressName)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	if initialIngressName != ingressResp.ObjectMeta.Name {
		return nil, nil, nil, nil, nil, errors.New("ingress name doesn't match spec")
	}

	return &podTemplate, deploymentTemplate, deploymentResp, serviceResp, ingressResp, nil
}

func createDeployment(steveclient *steveV1.Client, wlName string, deployment *v1.Deployment) (*steveV1.SteveAPIObject, error) {
	logrus.Infof("Creating deployment: %s", wlName)
	deploymentResp, err := steveclient.SteveType(stevetypes.Deployment).Create(deployment)
	if err != nil {
		return nil, err
	}

	return deploymentResp, err
}
