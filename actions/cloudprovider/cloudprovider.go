package cloudprovider

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/sirupsen/logrus"

	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/cloudcredentials/vsphere"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/secrets"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	harvesterRKE2CloudProviderConfigPath = "/var/lib/rancher/rke2/etc/config-files/cloud-provider-config"
	outOfTreeAWSFilePath                 = "/../resources/out-of-tree/aws.yml"

	kubeletArgKey                     = "kubelet-arg"
	kubeletAPIServerArgKey            = "kubeapi-server-arg"
	kubeControllerManagerArgKey       = "kube-controller-manager-arg"
	cloudProviderConfigAnnotationName = "cloud-provider-config"
	cloudProviderAnnotationName       = "cloud-provider-name"
	disableCloudController            = "disable-cloud-controller"
	protectKernelDefaults             = "protect-kernel-defaults"
	externalCloudProviderString       = "cloud-provider=external"
	etcdRole                          = "etcd-role"
	controlPlaneRole                  = "control-plane-role"
	workerRole                        = "worker-role"
)

func CreateCloudProviderAddOns(client *rancher.Client, clustersConfig *clusters.ClusterConfig, credentials cloudcredentials.CloudCredential, additionalData map[string]interface{}) (*clusters.ClusterConfig, error) {
	currentSelectors := []rkev1.RKESystemConfig{}
	if clustersConfig.Advanced != nil {
		if clustersConfig.Advanced.MachineSelectors != nil {
			currentSelectors = *clustersConfig.Advanced.MachineSelectors
		}
	}

	switch clustersConfig.CloudProvider {
	case provisioninginput.AWSProviderName.String():
		currentSelectors = append(currentSelectors, OutOfTreeSystemConfig(clustersConfig.CloudProvider)...)

		_, filePath, _, ok := runtime.Caller(0)
		if !ok {
			return nil, errors.New("Error retrieving file path")
		}

		filePath, err := filepath.Abs(filePath + outOfTreeAWSFilePath)
		if err != nil {
			return nil, err
		}

		logrus.Info(filePath)
		byteYaml, _ := os.ReadFile(filePath)
		clustersConfig.AddOnConfig = &provisioninginput.AddOnConfig{
			AdditionalManifest: string(byteYaml),
		}

		if clustersConfig.Advanced == nil {
			clustersConfig.Advanced = &provisioninginput.Advanced{}
		}

		if clustersConfig.Advanced.MachineGlobalConfig == nil {
			clustersConfig.Advanced.MachineGlobalConfig = &rkev1.GenericMap{
				Data: map[string]interface{}{},
			}
		}

		clustersConfig.Advanced.MachineGlobalConfig.Data["cloud-provider-name"] = "aws"

	case provisioninginput.VsphereCloudProviderName.String():
		currentSelectors = append(currentSelectors, RKESystemConfigTemplate(map[string]interface{}{
			cloudProviderAnnotationName: provisioninginput.VsphereCloudProviderName.String(),
			protectKernelDefaults:       false,
		},
			nil),
		)

		vcenterCredentials := map[string]interface{}{
			"datacenters": additionalData["datacenter"],
			"host":        credentials.VmwareVsphereConfig.Vcenter,
			"password":    vsphere.GetVspherePassword(),
			"username":    credentials.VmwareVsphereConfig.Username,
		}
		clustersConfig.AddOnConfig = &provisioninginput.AddOnConfig{
			ChartValues: &rkev1.GenericMap{
				Data: map[string]interface{}{
					"rancher-vsphere-cpi": map[string]interface{}{
						"vCenter": vcenterCredentials,
					},
					"rancher-vsphere-csi": map[string]interface{}{
						"storageClass": map[string]interface{}{
							"datastoreURL": additionalData["datastoreUrl"],
						},
						"vCenter": vcenterCredentials,
					},
				},
			},
		}

	case provisioninginput.HarvesterProviderName.String():
	

		data := map[string][]byte{
			"credential": []byte(credentials.HarvesterCredentialConfig.KubeconfigContent),
		}

		annotations := map[string]string{
			"v2prov-secret-authorized-for-cluster":                additionalData["clusterName"].(string),
			"v2prov-authorized-secret-deletes-on-cluster-removal": "true",
		}

		kubeSecret, err := secrets.CreateSecret(client, "local", "fleet-default", data, "secret", nil, annotations)
		if err != nil {
			return clustersConfig, err
		}

		currentSelectors = append(currentSelectors, RKESystemConfigTemplate(map[string]interface{}{
			cloudProviderConfigAnnotationName: "secret://" + kubeSecret.Namespace + ":" + kubeSecret.Name,
			cloudProviderAnnotationName:       provisioninginput.HarvesterProviderName.String(),
			protectKernelDefaults:             false,
		},
			nil),
		)

		clustersConfig.AddOnConfig = &provisioninginput.AddOnConfig{
			ChartValues: &rkev1.GenericMap{
				Data: map[string]interface{}{
					"harvester-cloud-provider": map[string]interface{}{
						"cloudConfigPath": harvesterRKE2CloudProviderConfigPath,
						"global": map[string]interface{}{
							"cattle": map[string]interface{}{
								"clusterName": additionalData["clusterName"],
							},
						},
					},
				},
			},
		}
	}

	// not able to do 'contains' within switch statement cleanly, overwriting any previous changes is intentional
	if strings.Contains(clustersConfig.CloudProvider, "-in-tree") {
		currentSelectors = append(currentSelectors, InTreeSystemConfig(strings.Split(clustersConfig.CloudProvider, "-in-tree")[0])...)
	}

	if clustersConfig.Advanced == nil {
		clustersConfig.Advanced = &provisioninginput.Advanced{}
	}
	clustersConfig.Advanced.MachineSelectors = &currentSelectors

	return clustersConfig, nil
}

// OutOfTreeSystemConfig constructs the proper rkeSystemConfig slice for enabling the aws cloud provider services
func OutOfTreeSystemConfig(providerName string) (rkeConfig []rkev1.RKESystemConfig) {
	roles := []string{etcdRole, controlPlaneRole, workerRole}

	for _, role := range roles {
		selector := &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"rke.cattle.io/" + role: "true",
			},
		}
		configData := map[string]interface{}{}

		configData[kubeletArgKey] = []string{externalCloudProviderString}

		if role == controlPlaneRole {
			configData[kubeletAPIServerArgKey] = []string{externalCloudProviderString}
			configData[kubeControllerManagerArgKey] = []string{externalCloudProviderString}
		}

		if role == workerRole || role == controlPlaneRole {
			configData[disableCloudController] = true
		}

		rkeConfig = append(rkeConfig, RKESystemConfigTemplate(configData, selector))
	}

	configData := map[string]interface{}{
		cloudProviderAnnotationName: providerName,
		protectKernelDefaults:       false,
	}

	rkeConfig = append(rkeConfig, RKESystemConfigTemplate(configData, nil))
	return
}

// InTreeSystemConfig constructs the proper rkeSystemConfig slice for enabling cloud provider in-tree services.
// Vsphere deprecated 1.21+
// AWS deprecated 1.27+
// Azure deprecated 1.28+
func InTreeSystemConfig(providerName string) (rkeConfig []rkev1.RKESystemConfig) {
	configData := map[string]interface{}{
		cloudProviderAnnotationName: providerName,
		protectKernelDefaults:       false,
	}
	rkeConfig = append(rkeConfig, RKESystemConfigTemplate(configData, nil))
	return
}

// RKESYstemConfigTemplate constructs an RKESystemConfig object given config data and a selector
func RKESystemConfigTemplate(config map[string]interface{}, selector *metav1.LabelSelector) rkev1.RKESystemConfig {
	return rkev1.RKESystemConfig{
		Config: rkev1.GenericMap{
			Data: config,
		},
		MachineLabelSelector: selector,
	}
}
