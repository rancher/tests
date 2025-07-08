package vai

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/rancher/shepherd/extensions/vai"

	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/charts"
	"github.com/sirupsen/logrus"
	coreV1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	randomStringLength = 8
	uiSQLCacheResource = "ui-sql-cache"
)

type SupportedWithVai interface {
	SupportedWithVai() bool
}

func isVaiEnabled(client *rancher.Client) (bool, error) {
	managementClient := client.Steve.SteveType("management.cattle.io.feature")
	feature, err := managementClient.ByID(uiSQLCacheResource)
	if err != nil {
		return false, err
	}

	// Extract spec and status
	spec, ok := feature.Spec.(map[string]interface{})
	if !ok {
		return false, fmt.Errorf("unable to access Spec field")
	}

	status, statusOk := feature.Status.(map[string]interface{})
	if !statusOk {
		status = map[string]interface{}{} // Prevent nil panics
	}

	logrus.Infof("Feature: %s", feature.Name)
	logrus.Infof("  spec.value: %v", spec["value"])
	logrus.Infof("  status.default: %v", status["default"])
	logrus.Infof("  status.description: %v", status["description"])
	logrus.Infof("  status.dynamic: %v", status["dynamic"])
	logrus.Infof("  status.lockedValue: %v", status["lockedValue"])

	// Determine the effective value
	valueInterface, hasValue := spec["value"]

	// If spec.value is explicitly set (not nil)
	if hasValue && valueInterface != nil {
		value, ok := valueInterface.(bool)
		if !ok {
			return false, fmt.Errorf("value field is not a boolean")
		}
		logrus.Infof("  VAI enabled: %v (using spec.value)", value)
		return value, nil
	}

	// Otherwise, use the default from status
	defaultInterface, hasDefault := status["default"]
	if !hasDefault {
		logrus.Infof("  VAI enabled: false (no value or default found)")
		return false, nil
	}

	defaultValue, ok := defaultInterface.(bool)
	if !ok {
		return false, fmt.Errorf("default field is not a boolean")
	}

	logrus.Infof("  VAI enabled: %v (spec.value is nil, using default)", defaultValue)
	return defaultValue, nil
}

func filterTestCases[T SupportedWithVai](testCases []T, vaiEnabled bool) []T {
	if !vaiEnabled {
		return testCases
	}

	var supported []T
	for _, tc := range testCases {
		if tc.SupportedWithVai() {
			supported = append(supported, tc)
		}
	}
	return supported
}

func ensureVAIState(client *rancher.Client, desiredState bool) error {
	currentState, err := isVaiEnabled(client)
	if err != nil {
		return fmt.Errorf("failed to check VAI state: %v", err)
	}

	if currentState == desiredState {
		return nil
	}

	action := "enabling"
	if !desiredState {
		action = "disabling"
	}

	logrus.Infof("VAI is currently %v, %s it for test...", currentState, action)

	if desiredState {
		err = vai.EnableVaiCaching(client)
	} else {
		err = vai.DisableVaiCaching(client)
	}

	if err != nil {
		return fmt.Errorf("failed to %s VAI: %v", action, err)
	}

	err = waitForRancherStable(client, desiredState)
	if err != nil {
		return fmt.Errorf("failed waiting for Rancher to stabilize after %s VAI: %v", action, err)
	}

	newState, err := isVaiEnabled(client)
	if err != nil {
		return fmt.Errorf("failed to verify VAI state after %s: %v", action, err)
	}

	if newState != desiredState {
		return fmt.Errorf("VAI state verification failed: expected %v, got %v", desiredState, newState)
	}

	logrus.Infof("VAI successfully %s", action+"d")
	return nil
}

func waitForRancherStable(client *rancher.Client, vaiEnabled bool) error {
	logrus.Info("Waiting for Rancher deployment to stabilize after VAI toggle...")

	err := charts.WatchAndWaitDeployments(client, "local", "cattle-system", metav1.ListOptions{
		FieldSelector: "metadata.name=rancher",
	})
	if err != nil {
		return fmt.Errorf("failed waiting for Rancher deployment: %v", err)
	}

	logrus.Info("Verifying Rancher API is responsive...")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	err = kwait.PollUntilContextTimeout(ctx, 2*time.Second, 2*time.Minute, true,
		func(ctx context.Context) (done bool, err error) {
			_, err = client.Steve.SteveType("namespace").List(nil)
			if err != nil {
				logrus.Debugf("API not ready yet: %v", err)
				return false, nil
			}
			return true, nil
		})

	if err != nil {
		return fmt.Errorf("rancher API not responsive: %v", err)
	}

	if vaiEnabled {
		logrus.Info("VAI enabled, waiting 15s for cache initialization...")
		_ = kwait.PollImmediate(time.Second, 15*time.Second, func() (bool, error) {
			return false, nil
		})
	}

	logrus.Info("Rancher deployment is stable and ready")
	return nil
}

func waitForNamespaceActive(namespaceClient interface{}, namespaceName string) error {
	return kwait.PollImmediate(time.Second, 30*time.Second, func() (bool, error) {
		var namespace *steveV1.SteveAPIObject
		var err error

		switch client := namespaceClient.(type) {
		case *steveV1.SteveClient:
			namespace, err = client.ByID(namespaceName)
		case *steveV1.NamespacedSteveClient:
			namespace, err = client.ByID(namespaceName)
		default:
			return false, fmt.Errorf("unsupported client type: %T", namespaceClient)
		}

		if err != nil {
			logrus.Debugf("Namespace %s not ready yet: %v", namespaceName, err)
			return false, nil
		}

		namespaceObj := &coreV1.Namespace{}
		err = steveV1.ConvertToK8sType(namespace.JSONResp, namespaceObj)
		if err != nil {
			return false, nil
		}
		isActive := namespaceObj.Status.Phase == coreV1.NamespaceActive
		if !isActive {
			logrus.Debugf("Namespace %s phase is %s, waiting for Active", namespaceName, namespaceObj.Status.Phase)
		}
		return isActive, nil
	})
}

func waitForResourcesCreated(client interface{}, resourceIDs []string) error {
	return kwait.PollImmediate(time.Second, 30*time.Second, func() (bool, error) {
		for _, id := range resourceIDs {
			var err error
			switch c := client.(type) {
			case *steveV1.SteveClient:
				_, err = c.ByID(id)
			case *steveV1.NamespacedSteveClient:
				_, err = c.ByID(id)
			default:
				return false, fmt.Errorf("unsupported client type: %T", client)
			}

			if err != nil {
				logrus.Debugf("Resource %s not found yet: %v", id, err)
				return false, nil
			}
		}
		return true, nil
	})
}

func waitForResourceCount(client interface{}, listParams url.Values, expectedCount int) (*steveV1.SteveCollection, error) {
	var collection *steveV1.SteveCollection
	err := kwait.PollImmediate(time.Second, 30*time.Second, func() (bool, error) {
		var listErr error

		switch c := client.(type) {
		case *steveV1.SteveClient:
			collection, listErr = c.List(listParams)
		case *steveV1.NamespacedSteveClient:
			collection, listErr = c.List(listParams)
		default:
			return false, fmt.Errorf("unsupported client type: %T", client)
		}

		if listErr != nil {
			return false, listErr
		}
		actualCount := len(collection.Data)
		if actualCount != expectedCount {
			logrus.Debugf("Expected %d resources, but got %d. Retrying...", expectedCount, actualCount)
		}
		return actualCount == expectedCount, nil
	})
	return collection, err
}
