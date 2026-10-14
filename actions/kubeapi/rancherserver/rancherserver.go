package rancherserver

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extdeployments "github.com/rancher/shepherd/extensions/kubeapi/workloads/deployments"
	"github.com/rancher/shepherd/extensions/kubeconfig"
	"github.com/rancher/shepherd/extensions/rancherversion"
	pods "github.com/rancher/tests/actions/kubeapi/workloads/pods"
	corev1 "k8s.io/api/core/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	RancherContainer = "rancher"

	BuildTypePrime     = "prime"
	BuildTypeCommunity = "community"

	rancherAppLabel   = "app=rancher"
	rancherErrorLevel = `\[ERROR\]`
	rancherLogBuffer  = "8MB"
)

// ListRancherServerPods returns the Rancher server pods running on the local cluster
func ListRancherServerPods(client *rancher.Client) ([]corev1.Pod, error) {
	podList, err := pods.GetPodsByLabelSelector(client, extclusterapi.LocalCluster, extdeployments.RancherDeploymentNamespace, rancherAppLabel)
	if err != nil {
		return nil, fmt.Errorf("listing the Rancher server pods: %w", err)
	}

	if len(podList) == 0 {
		return nil, fmt.Errorf("the local cluster runs no pod matching %s in %s", rancherAppLabel, extdeployments.RancherDeploymentNamespace)
	}

	return podList, nil
}

// Logs returns what every Rancher server container has logged
func Logs(client *rancher.Client) (string, error) {
	return ContainerLogs(client, RancherContainer)
}

// FindRancherServerLogs returns the Rancher server log lines that contain the given substring
func FindRancherServerLogs(client *rancher.Client, substring string) ([]string, error) {
	logs, err := Logs(client)
	if err != nil {
		return nil, err
	}

	var matches []string
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, substring) {
			matches = append(matches, line)
		}
	}

	return matches, nil
}

// FetchRancherServerRestartCounts returns how often the Rancher server container has restarted, per pod
func FetchRancherServerRestartCounts(client *rancher.Client) (map[string]int32, error) {
	podList, err := ListRancherServerPods(client)
	if err != nil {
		return nil, err
	}

	counts := map[string]int32{}
	for _, pod := range podList {
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name == RancherContainer {
				counts[pod.Name] = status.RestartCount
			}
		}
	}

	return counts, nil
}

// VerifyRancherServerRestarted reports whether the Rancher server has restarted since the given per pod restart counts were taken
func VerifyRancherServerRestarted(client *rancher.Client, previous map[string]int32) (bool, error) {
	current, err := FetchRancherServerRestartCounts(client)
	if err != nil {
		return false, err
	}

	for name, count := range current {
		before, known := previous[name]
		if !known || count > before {
			return true, nil
		}
	}

	return len(current) != len(previous), nil
}

// VerifyRancherServerStaysStable reports whether the Rancher server pods avoid a restart for the whole of the given window
func VerifyRancherServerStaysStable(client *rancher.Client, previous map[string]int32, window time.Duration) (bool, error) {
	var restarted bool

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, window, true, func(context.Context) (bool, error) {
		var pollErr error

		restarted, pollErr = VerifyRancherServerRestarted(client, previous)
		if pollErr != nil {
			return false, pollErr
		}

		return restarted, nil
	})
	if err != nil && !kwait.Interrupted(err) {
		return false, err
	}

	return !restarted, nil
}

// VerifyNoRancherServerErrors errors when a Rancher server pod has logged an error matching the pattern since it started
func VerifyNoRancherServerErrors(client *rancher.Client, term string) error {
	podList, err := ListRancherServerPods(client)
	if err != nil {
		return err
	}

	pattern := fmt.Sprintf(`%s.*%s`, rancherErrorLevel, regexp.QuoteMeta(term))

	for _, pod := range podList {
		since := time.Time{}
		if pod.Status.StartTime != nil {
			since = pod.Status.StartTime.Time
		}

		if err := pods.CheckPodLogsForErrors(client, extclusterapi.LocalCluster, pod.Name, extdeployments.RancherDeploymentNamespace, pattern, since); err != nil {
			return err
		}
	}

	return nil
}

// IsRancherPrimeBuild reports whether the Rancher server answers /rancherversion as a Prime build
func IsRancherPrimeBuild(client *rancher.Client) (bool, error) {
	version, err := rancherversion.RequestRancherVersion(client.RancherConfig.Host)
	if err != nil {
		return false, fmt.Errorf("requesting the Rancher version: %w", err)
	}

	return version.IsPrime, nil
}

// ContainerLogs returns what the named container has logged on every Rancher server pod
func ContainerLogs(client *rancher.Client, container string) (string, error) {
	podList, err := ListRancherServerPods(client)
	if err != nil {
		return "", err
	}

	combined := strings.Builder{}

	for _, pod := range podList {
		logs, err := kubeconfig.GetPodContainerLogs(client, extclusterapi.LocalCluster, pod.Name, extdeployments.RancherDeploymentNamespace, container, rancherLogBuffer)
		if err != nil {
			return "", fmt.Errorf("reading the %s logs of %s/%s: %w", container, extdeployments.RancherDeploymentNamespace, pod.Name, err)
		}

		combined.WriteString(logs)
		combined.WriteString("\n")
	}

	return combined.String(), nil
}

// VerifyBuildType errors when the Rancher server does not report the expected build type
func VerifyBuildType(client *rancher.Client, expected string) error {
	if expected == "" {
		return nil
	}

	if expected != BuildTypePrime && expected != BuildTypeCommunity {
		return fmt.Errorf("buildType is %q, want %q or %q", expected, BuildTypePrime, BuildTypeCommunity)
	}

	prime, err := IsRancherPrimeBuild(client)
	if err != nil {
		return err
	}

	actual := BuildTypeCommunity
	if prime {
		actual = BuildTypePrime
	}

	if actual != expected {
		return fmt.Errorf("expected a %s Rancher build but the server reports %s", expected, actual)
	}

	return nil
}
