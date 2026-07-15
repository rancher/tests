package prime

const (
	ConfigurationFileKey = "prime"
)

type Config struct {
	Brand               string `json:"brand" yaml:"brand"`
	GitCommit           string `json:"gitCommit" yaml:"gitCommit"`
	IsPrime             bool   `json:"isPrime" yaml:"isPrime" default:"false"`
	Registry            string `json:"registry" yaml:"registry"`
	SCCRegistrationCode string `json:"sccRegistrationCode" yaml:"sccRegistrationCode"`
	SCCRegistrationType string `json:"sccRegistrationType" yaml:"sccRegistrationType"`
}
