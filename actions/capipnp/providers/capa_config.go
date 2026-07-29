package providers

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
