package capipnp

import (
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/tests/actions/provisioninginput"
)

type ProviderName string

const (
	CAPA = "capa"
	CAPV = "capv"
)

type ProviderManifestRendererFunc func(documents []map[string]any, config *Config) error
type ProviderPrerequisiteWaiterFunc func(client *rancher.Client) error
type ClusterManifestRendererFunc func(documents []map[string]any, config *Config, machinePools []provisioninginput.MachinePools, clusterName string) error

type Provider struct {
	RenderProviderManifestFunc    ProviderManifestRendererFunc
	WaitProviderPrerequisitesFunc ProviderPrerequisiteWaiterFunc
	RenderClusterManifestFunc     ClusterManifestRendererFunc
}

// CreateCAPIProvider is a helper function that creates a CAPI provider based on the provided name.
func CreateCAPIProvider(name string) Provider {
	var provider Provider

	switch {
	case name == CAPA:
		provider = Provider{
			RenderProviderManifestFunc:    capaProviderManifest,
			WaitProviderPrerequisitesFunc: waitCAPAProviderPrerequisitesReady,
			RenderClusterManifestFunc:     capaCluster,
		}
	case name == CAPV:
		provider = Provider{
			RenderProviderManifestFunc:    capvProviderManifest,
			WaitProviderPrerequisitesFunc: waitCAPVProviderPrerequisitesReady,
			RenderClusterManifestFunc:     capvCluster,
		}
	default:
		panic(fmt.Sprintf("Provider:%v not found", name))
	}

	return provider
}
