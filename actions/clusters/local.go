package clusters

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
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

// WaitRancherVersion polls the management server-version setting until it equals
// targetVersion (semver-compared) or timeout elapses. Gates post-upgrade assertions
// on Rancher server convergence; transient read errors during the upgrade rollout
// are retried, not returned.
func WaitRancherVersion(client *rancher.Client, targetVersion string, timeout time.Duration) error {
	target, err := parseRancherVersion(targetVersion)
	if err != nil {
		return err
	}

	return wait.PollUntilContextTimeout(context.Background(), defaults.TenSecondTimeout, timeout, true, func(ctx context.Context) (done bool, err error) {
		serverVersionRawValue, err := client.Management.Setting.ByID(serverVersionSetting)
		if err != nil {
			// The server may be mid-restart during the upgrade rollout; retry the read.
			return false, nil
		}

		serverVersion, err := parseRancherVersion(serverVersionRawValue.Value)
		if err != nil {
			return false, nil
		}

		if serverVersion.Equal(target) {
			return true, nil
		}

		logrus.Debugf("Rancher server version is %q, waiting for %q", serverVersionRawValue.Value, targetVersion)
		return false, nil
	})
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
