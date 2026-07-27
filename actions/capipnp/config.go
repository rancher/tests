package capipnp

const (
	ConfigurationFileKey = "capipnp"
)

type ManifestContent struct {
	Path    string
	Content []byte
}

type AWSCredentials struct {
	AccessKeyID     string `json:"accessKeyID,omitempty" yaml:"accessKeyID,omitempty"`
	SecretAccessKey string `json:"secretAccessKey,omitempty" yaml:"secretAccessKey,omitempty"`
}

type AWSTemplate struct {
	AMI                       string `json:"ami,omitempty" yaml:"ami,omitempty"`
	ControlPlaneSecurityGroup string `json:"controlPlaneSecurityGroup,omitempty" yaml:"controlPlaneSecurityGroup,omitempty"`
	NodeSecurityGroup         string `json:"nodeSecurityGroup,omitempty" yaml:"nodeSecurityGroup,omitempty"`
	Region                    string `json:"region,omitempty" yaml:"region,omitempty"`
	SSHKeyName                string `json:"sshKeyName,omitempty" yaml:"sshKeyName,omitempty"`
	SubnetID                  string `json:"subnetId,omitempty" yaml:"subnetId,omitempty"`
	VPCID                     string `json:"vpcId,omitempty" yaml:"vpcId,omitempty"`
}

type Config struct {
	AWSTemplate               AWSTemplate    `json:"awsTemplate,omitempty" yaml:"awsTemplate,omitempty"`
	AWSCredentials            AWSCredentials `json:"awsCredentials,omitempty" yaml:"awsCredentials,omitempty"`
	CredYAML                  string         `json:"credYAML,omitempty" yaml:"credYAML,omitempty"`
	ClusterNamePrefix         string         `json:"clusterNamePrefix,omitempty" yaml:"clusterNamePrefix,omitempty"`
	KubernetesVersion         string         `json:"kubernetesVersion,omitempty" yaml:"kubernetesVersion,omitempty"`
	ClusterYAML               string         `json:"clusterYAML,omitempty" yaml:"clusterYAML,omitempty"`
	ClusterStaticIdentityYAML string         `json:"clusterStaticIdentityYAML,omitempty" yaml:"clusterStaticIdentityYAML,omitempty"`
	Provider                  string         `json:"provider,omitempty" yaml:"provider,omitempty"`
	ProviderYAML              string         `json:"providerYAML,omitempty" yaml:"providerYAML,omitempty"`
}
