package airgap

const (
	TerraformConfigurationFileKey = "terraform"
)

type TerraformConfig struct {
	StandaloneAirgapConfig StandaloneAirgapConfig `json:"standaloneAirgapConfig" yaml:"standaloneAirgapConfig"`
}

type StandaloneAirgapConfig struct {
	PrivateRegistry string `json:"privateRegistry" yaml:"privateRegistry"`
}
