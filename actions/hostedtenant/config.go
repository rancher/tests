package hostedtenant

import "github.com/rancher/shepherd/clients/rancher"

const (
	ConfigurationFileKey = "tenantRanchers"
)

type Config struct {
	Clients []rancher.Config `json:"clients" yaml:"clients"`
}
