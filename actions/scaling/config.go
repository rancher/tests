package scaling

const (
	ScalingConfigurationKey = "scalingConfig"
)

type Config struct {
	AutoscalerChartRepository string `json:"autoscalerChartRepository" yaml:"autoscalerChartRepository"`
	AutoscalerImage           string `json:"autoscalerImage" yaml:"autoscalerImage"`
}
