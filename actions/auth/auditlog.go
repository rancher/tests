package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	"github.com/rancher/shepherd/extensions/kubeapi/workloads/deployments"
	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	RancherDeployment  = "rancher"
	RancherAuditLevel  = "2"
	AuditLogVolumeName = "audit-log"
	AuditLogMountPath  = "/var/log/auditlog"
	AuditLogFilePath   = "/var/log/auditlog/rancher-api-audit.log"

	auditLogBodyLevel    = 2
	auditLogEnabledEnv   = "AUDIT_LOG_ENABLED"
	auditLogMaxAgeEnv    = "AUDIT_LOG_MAXAGE"
	auditLogMaxBackupEnv = "AUDIT_LOG_MAXBACKUP"
	auditLogMaxSizeEnv   = "AUDIT_LOG_MAXSIZE"
)

type AuditLogUser struct {
	Name  string              `json:"name,omitempty"`
	Group []string            `json:"group,omitempty"`
	Extra map[string][]string `json:"extra,omitempty"`
}

type AuditLogEntry struct {
	AuditID      string         `json:"auditID,omitempty"`
	RequestURI   string         `json:"requestURI,omitempty"`
	Method       string         `json:"method,omitempty"`
	ResponseCode int            `json:"responseCode,omitempty"`
	User         *AuditLogUser  `json:"user,omitempty"`
	RequestBody  map[string]any `json:"requestBody,omitempty"`
	ResponseBody map[string]any `json:"responseBody,omitempty"`
}

// EnsureRancherAuditLogging turns the Rancher audit log on at the given level with a sidecar that serves it, and returns a function that puts the deployment back as it was
func EnsureRancherAuditLogging(client *rancher.Client, level string) (func() error, error) {
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

		logrus.Infof("Restoring the audit log configuration of the %s deployment", RancherDeployment)
		restored := current.DeepCopy()
		restored.Spec.Template.Spec = *original.Spec.Template.Spec.DeepCopy()

		_, err = deployments.UpdateDeployment(client, extclusterapi.LocalCluster, restored, true)
		return err
	}

	updated := deployment.DeepCopy()
	withAuditLog(updated, level)

	if equality.Semantic.DeepEqual(deployment.Spec.Template.Spec, updated.Spec.Template.Spec) {
		logrus.Info("The Rancher deployment already serves the audit log at the wanted level")
		return func() error { return nil }, nil
	}

	logrus.Infof("Turning the Rancher audit log on at level %s with the %s sidecar", level, RancherAuditLogContainer)
	if _, err := deployments.UpdateDeployment(client, extclusterapi.LocalCluster, updated, true); err != nil {
		return nil, fmt.Errorf("updating the %s/%s deployment: %w", RancherNamespace, RancherDeployment, err)
	}

	return restore, nil
}

// VerifyAuditLoggingEnabled reports whether the Rancher deployment runs the audit log sidecar that the audit tests read
func VerifyAuditLoggingEnabled(client *rancher.Client) (bool, error) {
	deployment, err := rancherDeployment(client)
	if err != nil {
		return false, err
	}

	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name == RancherAuditLogContainer {
			return true, nil
		}
	}

	return false, nil
}

// VerifyAuditLogRecordsBodies reports whether the Rancher deployment runs at an audit level that records request bodies
func VerifyAuditLogRecordsBodies(client *rancher.Client) (bool, error) {
	deployment, err := rancherDeployment(client)
	if err != nil {
		return false, err
	}

	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name != RancherContainer {
			continue
		}

		for _, env := range container.Env {
			if env.Name != RancherAuditLevelEnv {
				continue
			}

			level := 0
			if _, err := fmt.Sscanf(env.Value, "%d", &level); err != nil {
				return false, fmt.Errorf("reading %s=%q of the %s container: %w", RancherAuditLevelEnv, env.Value, RancherContainer, err)
			}

			return level >= auditLogBodyLevel, nil
		}
	}

	return false, nil
}

// FetchAuditLogEntries returns the audit log entries whose request URI contains the given substring
func FetchAuditLogEntries(client *rancher.Client, uriSubstring string) ([]AuditLogEntry, error) {
	logs, err := RancherAuditLogs(client)
	if err != nil {
		return nil, err
	}

	var entries []AuditLogEntry
	for _, line := range strings.Split(logs, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}

		entry := AuditLogEntry{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		if uriSubstring == "" || strings.Contains(entry.RequestURI, uriSubstring) {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

// WaitForAuditLogEntry polls the audit log until an entry whose request URI contains the given substring satisfies match
func WaitForAuditLogEntry(client *rancher.Client, uriSubstring string, match func(AuditLogEntry) bool) (*AuditLogEntry, error) {
	var found *AuditLogEntry

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true,
		func(context.Context) (bool, error) {
			entries, err := FetchAuditLogEntries(client, uriSubstring)
			if err != nil {
				return false, nil
			}

			for i := range entries {
				if match(entries[i]) {
					found = &entries[i]
					return true, nil
				}
			}

			return false, nil
		})
	if err != nil {
		return nil, fmt.Errorf("the audit log never recorded an entry for %s that matched: %w", uriSubstring, err)
	}

	return found, nil
}

func withAuditLog(deployment *appsv1.Deployment, level string) {
	podSpec := &deployment.Spec.Template.Spec

	var serverImage string
	for i := range podSpec.Containers {
		if podSpec.Containers[i].Name != RancherContainer {
			continue
		}

		serverImage = podSpec.Containers[i].Image
		podSpec.Containers[i].Env = withEnv(podSpec.Containers[i].Env, map[string]string{
			auditLogEnabledEnv:   "true",
			RancherAuditLevelEnv: level,
			auditLogMaxAgeEnv:    "1",
			auditLogMaxBackupEnv: "1",
			auditLogMaxSizeEnv:   "100",
		})
		podSpec.Containers[i].VolumeMounts = withVolumeMount(podSpec.Containers[i].VolumeMounts)
	}

	podSpec.Volumes = withVolume(podSpec.Volumes)

	for i := range podSpec.Containers {
		if podSpec.Containers[i].Name == RancherAuditLogContainer {
			podSpec.Containers[i].VolumeMounts = withVolumeMount(podSpec.Containers[i].VolumeMounts)
			return
		}
	}

	podSpec.Containers = append(podSpec.Containers, corev1.Container{
		Name:         RancherAuditLogContainer,
		Image:        serverImage,
		Command:      []string{"tail"},
		Args:         []string{"-F", AuditLogFilePath},
		VolumeMounts: withVolumeMount(nil),
	})
}

func withEnv(env []corev1.EnvVar, wanted map[string]string) []corev1.EnvVar {
	for name, value := range wanted {
		found := false
		for i := range env {
			if env[i].Name == name {
				env[i].Value = value
				env[i].ValueFrom = nil
				found = true
				break
			}
		}
		if !found {
			env = append(env, corev1.EnvVar{Name: name, Value: value})
		}
	}

	return env
}

func withVolumeMount(mounts []corev1.VolumeMount) []corev1.VolumeMount {
	for i := range mounts {
		if mounts[i].Name == AuditLogVolumeName {
			mounts[i].MountPath = AuditLogMountPath
			return mounts
		}
	}

	return append(mounts, corev1.VolumeMount{Name: AuditLogVolumeName, MountPath: AuditLogMountPath})
}

func withVolume(volumes []corev1.Volume) []corev1.Volume {
	for i := range volumes {
		if volumes[i].Name == AuditLogVolumeName {
			return volumes
		}
	}

	return append(volumes, corev1.Volume{
		Name:         AuditLogVolumeName,
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	})
}

func rancherDeployment(client *rancher.Client) (*appsv1.Deployment, error) {
	deployment, err := client.WranglerContext.Apps.Deployment().Get(RancherNamespace, RancherDeployment, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting the %s/%s deployment: %w", RancherNamespace, RancherDeployment, err)
	}

	return deployment, nil
}
