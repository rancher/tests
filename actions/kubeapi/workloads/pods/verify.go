package pods

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/tests/actions/workloads/pods"
	corev1 "k8s.io/api/core/v1"
)

// VerifyPodContainerResources is a helper function to verify the container resource requests and limits of a pod
func VerifyPodContainerResources(client *rancher.Client, clusterID, namespaceName, deploymentName, cpuLimit, cpuReservation, memoryLimit, memoryReservation string) error {
	var errs []string

	podNames, err := pods.GetPodNamesFromDeployment(client, clusterID, namespaceName, deploymentName)
	if err != nil {
		return fmt.Errorf("error fetching pod by deployment name: %w", err)
	}
	if len(podNames) < 1 {
		return errors.New("expected at least one pod, but got " + strconv.Itoa(len(podNames)))
	}

	pod, err := pods.GetPodByName(client, clusterID, namespaceName, podNames[0])
	if err != nil {
		return err
	}
	if len(pod.Spec.Containers) == 0 {
		return errors.New("no containers found in the pod")
	}

	normalizeString := func(s string) string {
		if s == "" {
			return "0"
		}
		return s
	}

	cpuLimit = normalizeString(cpuLimit)
	cpuReservation = normalizeString(cpuReservation)
	memoryLimit = normalizeString(memoryLimit)
	memoryReservation = normalizeString(memoryReservation)

	containerResources := pod.Spec.Containers[0].Resources
	containerCPULimit := containerResources.Limits[corev1.ResourceCPU]
	containerCPURequest := containerResources.Requests[corev1.ResourceCPU]
	containerMemoryLimit := containerResources.Limits[corev1.ResourceMemory]
	containerMemoryRequest := containerResources.Requests[corev1.ResourceMemory]

	if cpuLimit != containerCPULimit.String() {
		errs = append(errs, "CPU limit mismatch")
	}
	if cpuReservation != containerCPURequest.String() {
		errs = append(errs, "CPU reservation mismatch")
	}
	if memoryLimit != containerMemoryLimit.String() {
		errs = append(errs, "Memory limit mismatch")
	}
	if memoryReservation != containerMemoryRequest.String() {
		errs = append(errs, "Memory reservation mismatch")
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "\n"))
	}

	return nil
}
