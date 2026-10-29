package imported

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	rancherv1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// WaitForOperation is a helper function that waits for a specific operation to complete for a given resource type and operation ID.
func WaitForOperation(client *rancher.Client, resourceType, operationID string) error {
	var lastStatus any

	operationClient := client.Steve.SteveType(resourceType)

	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.TenSecondTimeout, defaults.ThirtyMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		current, err := operationClient.ByID(operationID)
		if err != nil {
			return false, fmt.Errorf("get operation %s: %w", operationID, err)
		}

		lastStatus = current.Status

		return OperationResults(operationID, current.Status)
	})
	if err != nil {
		return fmt.Errorf("waiting for %s operation %s (last status: %v): %w", resourceType, operationID, lastStatus, err)
	}

	return nil
}

// OperationResults evaluates the status of an operation and returns whether it has completed successfully or encountered an error.
func OperationResults(operationID string, rawStatus any) (bool, error) {
	var status struct {
		Phase      string          `json:"phase"`
		Step       string          `json:"step"`
		Conditions json.RawMessage `json:"conditions"`
	}

	if err := rancherv1.ConvertToK8sType(rawStatus, &status); err != nil {
		return false, fmt.Errorf("decode operation results for %s status: %w", operationID, err)
	}

	switch status.Phase {
	case "Succeeded":
		return true, nil
	case "Failed", "Canceled":
		return false, fmt.Errorf("operation %s ended with phase %q at step %q; conditions: %s", operationID, status.Phase, status.Step, status.Conditions)
	default:
		return false, nil
	}
}
