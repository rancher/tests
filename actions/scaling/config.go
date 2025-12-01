package scaling

const (
	AutoScalingConfigurationKey = "autoScalingConfig"
)

type AutoscalingConfig struct {
	ChartRepository string `json:"chartRepository,omitempty" yaml:"chartRepository,omitempty"`
	Image           string `json:"image,omitempty" yaml:"image,omitempty"`
}
