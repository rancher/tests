package hosted

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
)

// DefaultHostedKubernetesVersion is a helper function that returns the latest valid Kubernetes version for the specified hosted provider.
func DefaultHostedKubernetesVersion(client *rancher.Client, provider, cloudCredentialID, projectID, zone, region string) (*string, error) {
	var versions []string
	var err error

	switch provider {
	case "aks":
		versions, err = kubernetesversions.ListAKSAllVersions(client, cloudCredentialID, region)
	case "eks":
		versions, err = kubernetesversions.ListEKSAllVersions(client)
	case "gke":
		versions, err = kubernetesversions.ListGKEAllVersions(client, projectID, cloudCredentialID, zone, region)
	default:
		return nil, fmt.Errorf("invalid hosted provider %q; valid providers: aks, eks, gke", provider)
	}
	if err != nil {
		return nil, err
	}

	var latest *semver.Version
	var latestVersion string
	for _, candidate := range versions {
		version, parseErr := semver.NewVersion(candidate)
		if parseErr != nil {
			continue
		}

		if latest == nil || latest.LessThan(version) {
			latest = version
			latestVersion = candidate
		}
	}

	if latest == nil {
		return nil, fmt.Errorf("no valid Kubernetes versions returned for provider %q", provider)
	}

	return &latestVersion, nil
}
