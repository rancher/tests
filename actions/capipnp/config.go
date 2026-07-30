package capipnp

import (
	"github.com/rancher/tests/actions/capipnp/providers"
)

const (
	ConfigurationFileKey = "capipnp"
)

type ManifestContent struct {
	Path    string
	Content []byte
}

type Config struct {
	AWSTemplate               providers.AWSTemplate        `json:"awsTemplate,omitempty" yaml:"awsTemplate,omitempty"`
	AWSCredentials            providers.AWSCredentials     `json:"awsCredentials,omitempty" yaml:"awsCredentials,omitempty"`
	VSphereTemplate           providers.VSphereTemplate    `json:"vsphereTemplate,omitempty" yaml:"vsphereTemplate,omitempty"`
	VSphereCredentials        providers.VSphereCredentials `json:"vsphereCredentials,omitempty" yaml:"vsphereCredentials,omitempty"`
	CredYAML                  string                       `json:"credYAML,omitempty" yaml:"credYAML,omitempty"`
	ClusterNamePrefix         string                       `json:"clusterNamePrefix,omitempty" yaml:"clusterNamePrefix,omitempty"`
	KubernetesVersion         string                       `json:"kubernetesVersion,omitempty" yaml:"kubernetesVersion,omitempty"`
	ClusterYAML               string                       `json:"clusterYAML,omitempty" yaml:"clusterYAML,omitempty"`
	ClusterStaticIdentityYAML string                       `json:"clusterStaticIdentityYAML,omitempty" yaml:"clusterStaticIdentityYAML,omitempty"`
	Provider                  string                       `json:"provider,omitempty" yaml:"provider,omitempty"`
	ProviderYAML              string                       `json:"providerYAML,omitempty" yaml:"providerYAML,omitempty"`
}
