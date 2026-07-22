package clusters

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/rancher/shepherd/clients/rancher"
)

const (
	serverVersionSetting = "server-version"
	rancherVersionRegex  = `\d+\.\d+(?:\.\d+)?(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?`
)

var rancherVersionPattern = regexp.MustCompile(rancherVersionRegex)

// IsRancherVersionAbove returns true when the Rancher server version is greater than minVersion.
func IsRancherVersionAbove(client *rancher.Client, minVersion string) (bool, error) {
	serverVersionRawValue, err := client.Management.Setting.ByID(serverVersionSetting)
	if err != nil {
		return false, err
	}

	serverVersion, err := parseRancherVersion(serverVersionRawValue.Value)
	if err != nil {
		return false, err
	}

	minimumVersion, err := parseRancherVersion(minVersion)
	if err != nil {
		return false, err
	}

	return serverVersion.GreaterThan(minimumVersion), nil
}

func parseRancherVersion(versionInput string) (*semver.Version, error) {
	trimmedVersion := strings.TrimSpace(strings.TrimPrefix(versionInput, "v"))
	if trimmedVersion == "" {
		return nil, errors.New("version cannot be empty")
	}

	// Rancher version values can include extra text; extract the first semver-looking token.
	match := rancherVersionPattern.FindString(trimmedVersion)
	if match == "" {
		return nil, fmt.Errorf("unable to parse Rancher version from input %q", versionInput)
	}

	parsedVersion, err := semver.NewVersion(match)
	if err != nil {
		return nil, err
	}

	return parsedVersion, nil
}
