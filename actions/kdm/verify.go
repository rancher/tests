package kdm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Release struct {
	Version string `json:"version"`
}

type DistroMetadata struct {
	Releases []Release `json:"releases"`
}

type Metadata struct {
	RKE2 DistroMetadata `json:"rke2"`
	K3S  DistroMetadata `json:"k3s"`
}

// VerifyKDMUrl validates the KDM URL and returns Kubernetes versions.
func VerifyKDMUrl(url, rancherVersion string) (map[string][]string, error) {
	if strings.Contains(rancherVersion, "-alpha") || strings.Contains(rancherVersion, "-head") {
		if !strings.Contains(url, "dev-v") {
			return nil, fmt.Errorf("expected KDM URL to point to dev branch, url: %s", url)
		}
	} else {
		if !strings.Contains(url, "release-v") {
			return nil, fmt.Errorf("expected KDM URL to point to release branch, url: %s", url)
		}
	}

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed reading KDM response body: %w", err)
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("KDM response body is empty")
	}

	var meta Metadata
	err = json.Unmarshal(body, &meta)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal KDM JSON: %w", err)
	}

	versions := make(map[string][]string)
	for _, rel := range meta.RKE2.Releases {
		if rel.Version != "" {
			versions["rke2"] = append(versions["rke2"], rel.Version)
		}
	}

	for _, rel := range meta.K3S.Releases {
		if rel.Version != "" {
			versions["k3s"] = append(versions["k3s"], rel.Version)
		}
	}

	return versions, nil
}

// VerifyKDMVersions checks that the available Rancher Kubernetes versions are listed in the KDM version list
func VerifyKDMVersions(kdmVersions map[string][]string, dropdownVersions []string, distro string) error {
	var kdmList []string

	switch {
	case strings.Contains(distro, "rke2"):
		kdmList = kdmVersions["rke2"]
	case strings.Contains(distro, "k3s"):
		kdmList = kdmVersions["k3s"]
	default:
		panic(fmt.Sprintf("Unsupported distro: %s", distro))
	}

	if len(kdmList) == 0 {
		return fmt.Errorf("KDM version list for %s should not be empty", distro)
	}

	if len(dropdownVersions) == 0 {
		return fmt.Errorf("Dropdown version list for %s should not be empty", distro)
	}

	for _, version := range dropdownVersions {
		found := false
		for _, kdmVersion := range kdmList {
			if version == kdmVersion {
				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("%s dropdown version %s not found in KDM versions: %v", distro, version, kdmList)
		}
	}

	return nil
}
