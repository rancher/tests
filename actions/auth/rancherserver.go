package auth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	"github.com/rancher/shepherd/extensions/kubeapi/workloads/deployments"
	"github.com/rancher/shepherd/extensions/kubeconfig"
	pods "github.com/rancher/tests/actions/kubeapi/workloads/pods"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	RancherNamespace         = "cattle-system"
	RancherContainer         = "rancher"
	RancherAuditLogContainer = "rancher-audit-log"
	RancherAuditLevelEnv     = "AUDIT_LEVEL"

	rancherAppLabel     = "app=rancher"
	rancherErrorLevel   = "level=error"
	rancherLogBuffer    = "8MB"
	rancherVersionPath  = "rancherversion"
	rancherUsersPath    = "v3/users"
	rancherPrimeAnswer  = "true"
	rancherVersionReqTO = 30 * time.Second
)

type rancherVersionInfo struct {
	Version      string
	GitCommit    string
	RancherPrime string
}

// ListRancherServerPods returns the Rancher server pods running on the local cluster
func ListRancherServerPods(client *rancher.Client) ([]corev1.Pod, error) {
	podList, err := pods.GetPodsByLabelSelector(client, extclusterapi.LocalCluster, RancherNamespace, rancherAppLabel)
	if err != nil {
		return nil, fmt.Errorf("listing the Rancher server pods: %w", err)
	}

	if len(podList) == 0 {
		return nil, fmt.Errorf("the local cluster runs no pod matching %s in %s", rancherAppLabel, RancherNamespace)
	}

	return podList, nil
}

// RancherServerLogs returns what every Rancher server container has logged
func RancherServerLogs(client *rancher.Client) (string, error) {
	return rancherContainerLogs(client, RancherContainer)
}

// RancherAuditLogs returns what every Rancher audit log sidecar has logged
func RancherAuditLogs(client *rancher.Client) (string, error) {
	return rancherContainerLogs(client, RancherAuditLogContainer)
}

// FindRancherServerLogs returns the Rancher server log lines that contain the given substring
func FindRancherServerLogs(client *rancher.Client, substring string) ([]string, error) {
	logs, err := RancherServerLogs(client)
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

// FindRancherServerErrors returns the Rancher server log lines that are logged at error level and contain the given substring
func FindRancherServerErrors(client *rancher.Client, substring string) ([]string, error) {
	lines, err := FindRancherServerLogs(client, substring)
	if err != nil {
		return nil, err
	}

	var errorLines []string
	for _, line := range lines {
		if strings.Contains(line, rancherErrorLevel) {
			errorLines = append(errorLines, line)
		}
	}

	return errorLines, nil
}

// SetRancherServerEnv writes an environment variable on the Rancher server container and returns a function that puts the deployment back as it was
func SetRancherServerEnv(client *rancher.Client, name, value string) (func() error, error) {
	deployment, err := deployments.GetDeploymentByName(client, extclusterapi.LocalCluster, RancherNamespace, RancherDeployment)
	if err != nil {
		return nil, fmt.Errorf("getting the %s/%s deployment: %w", RancherNamespace, RancherDeployment, err)
	}

	original := deployment.DeepCopy()
	restore := func() error {
		current, err := deployments.GetDeploymentByName(client, extclusterapi.LocalCluster, RancherNamespace, RancherDeployment)
		if err != nil {
			return fmt.Errorf("getting the %s/%s deployment: %w", RancherNamespace, RancherDeployment, err)
		}

		logrus.Infof("Restoring the environment of the %s deployment", RancherDeployment)
		restored := current.DeepCopy()
		restored.Spec.Template.Spec = *original.Spec.Template.Spec.DeepCopy()

		_, err = deployments.UpdateDeployment(client, extclusterapi.LocalCluster, restored, true)
		return err
	}

	logrus.Infof("Setting %s=%s on the %s deployment", name, value, RancherDeployment)
	updated := deployment.DeepCopy()
	for i := range updated.Spec.Template.Spec.Containers {
		if updated.Spec.Template.Spec.Containers[i].Name == RancherContainer {
			updated.Spec.Template.Spec.Containers[i].Env = withEnv(updated.Spec.Template.Spec.Containers[i].Env, map[string]string{name: value})
		}
	}

	if _, err := deployments.UpdateDeployment(client, extclusterapi.LocalCluster, updated, true); err != nil {
		return nil, fmt.Errorf("updating the %s/%s deployment: %w", RancherNamespace, RancherDeployment, err)
	}

	return restore, nil
}

// VerifyFeatureEffectivelyEnabled reports the value the Rancher server resolves for the named feature, which accounts for its default and for Prime builds
func VerifyFeatureEffectivelyEnabled(client *rancher.Client, featureName string) (bool, error) {
	feature, err := client.Management.Feature.ByID(featureName)
	if err != nil {
		return false, fmt.Errorf("getting the %s feature: %w", featureName, err)
	}

	if feature.Value == nil {
		return false, nil
	}

	return *feature.Value, nil
}

// FetchRancherAPIStatusWithToken reports the status the Rancher API answers a request carrying the given bearer token
func FetchRancherAPIStatusWithToken(client *rancher.Client, token string) (int, error) {
	url := fmt.Sprintf("https://%s/%s", client.RancherConfig.Host, rancherUsersPath)

	insecure := client.RancherConfig.Insecure == nil || *client.RancherConfig.Insecure
	httpClient := &http.Client{
		Timeout: rancherVersionReqTO,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec
		},
	}

	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("building the request for %s: %w", url, err)
	}

	request.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("requesting %s: %w", url, err)
	}
	defer resp.Body.Close()

	return resp.StatusCode, nil
}

// IsRancherPrimeBuild reports whether the Rancher server answers /rancherversion as a Prime build
func IsRancherPrimeBuild(client *rancher.Client) (bool, error) {
	url := fmt.Sprintf("https://%s/%s", client.RancherConfig.Host, rancherVersionPath)

	insecure := client.RancherConfig.Insecure == nil || *client.RancherConfig.Insecure
	httpClient := &http.Client{
		Timeout: rancherVersionReqTO,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec
		},
	}

	resp, err := httpClient.Get(url)
	if err != nil {
		return false, fmt.Errorf("requesting %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%s answered %d, want 200", url, resp.StatusCode)
	}

	info := rancherVersionInfo{}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return false, fmt.Errorf("decoding the answer of %s: %w", url, err)
	}

	return info.RancherPrime == rancherPrimeAnswer, nil
}

func rancherContainerLogs(client *rancher.Client, container string) (string, error) {
	podList, err := ListRancherServerPods(client)
	if err != nil {
		return "", err
	}

	combined := strings.Builder{}

	for _, pod := range podList {
		logs, err := kubeconfig.GetPodContainerLogs(client, extclusterapi.LocalCluster, pod.Name, RancherNamespace, container, rancherLogBuffer)
		if err != nil {
			return "", fmt.Errorf("reading the %s logs of %s/%s: %w", container, RancherNamespace, pod.Name, err)
		}

		combined.WriteString(logs)
		combined.WriteString("\n")
	}

	return combined.String(), nil
}
