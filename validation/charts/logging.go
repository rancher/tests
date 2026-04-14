package charts

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	shepherdCharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/defaults/stevestates"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/charts"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	clusterOutputSteveType = "logging.banzaicloud.io.clusteroutput"
	clusterFlowSteveType   = "logging.banzaicloud.io.clusterflow"

	loggingPipelinePollInterval = 5 * time.Second
	loggingPipelinePollTimeout  = 2 * time.Minute
)

var daemonSetLoggingGVR = schema.GroupVersionResource{
	Group:    "apps",
	Version:  "v1",
	Resource: "daemonsets",
}

// loggingClusterOutputSpec defines the spec for a ClusterOutput resource.
type loggingClusterOutputSpec struct {
	Stdout map[string]interface{} `json:"stdout"`
}

// loggingClusterOutput is an inline struct for creating ClusterOutput CRs without importing the banzaicloud library.
type loggingClusterOutput struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              loggingClusterOutputSpec `json:"spec"`
}

// loggingClusterFlowSpec defines the spec for a ClusterFlow resource.
type loggingClusterFlowSpec struct {
	GlobalOutputRefs []string `json:"globalOutputRefs"`
}

// loggingClusterFlow is an inline struct for creating ClusterFlow CRs without importing the banzaicloud library.
type loggingClusterFlow struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              loggingClusterFlowSpec `json:"spec"`
}

// verifyLoggingCollectorsRunning ensures the FluentBit DaemonSets and Fluentd StatefulSets
// in the logging namespace are fully available, confirming that log collection is active.
func verifyLoggingCollectorsRunning(client *rancher.Client, clusterID string) error {
	logrus.Infof("Verifying FluentBit DaemonSets are ready in namespace [%s]", charts.RancherLoggingNamespace)
	err := shepherdCharts.WatchAndWaitDaemonSets(client, clusterID, charts.RancherLoggingNamespace, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("FluentBit DaemonSets not ready: %w", err)
	}

	logrus.Infof("Verifying Fluentd StatefulSets are ready in namespace [%s]", charts.RancherLoggingNamespace)
	err = shepherdCharts.WatchAndWaitStatefulSets(client, clusterID, charts.RancherLoggingNamespace, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("Fluentd StatefulSets not ready: %w", err)
	}

	return nil
}

// createLoggingPipeline creates a ClusterOutput (stdout) and a ClusterFlow referencing it
// to validate end-to-end logging pipeline configuration. It returns the names of the created resources.
func createLoggingPipeline(client *rancher.Client, clusterID string) (outputName, flowName string, err error) {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return "", "", err
	}

	outputName = namegen.AppendRandomString("test-output")
	flowName = namegen.AppendRandomString("test-flow")

	logrus.Infof("Creating ClusterOutput [%s] in namespace [%s]", outputName, charts.RancherLoggingNamespace)
	clusterOutput := &loggingClusterOutput{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "logging.banzaicloud.io/v1beta1",
			Kind:       "ClusterOutput",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      outputName,
			Namespace: charts.RancherLoggingNamespace,
		},
		Spec: loggingClusterOutputSpec{
			Stdout: map[string]interface{}{},
		},
	}

	_, err = steveClient.SteveType(clusterOutputSteveType).Create(clusterOutput)
	if err != nil {
		return "", "", fmt.Errorf("failed to create ClusterOutput [%s]: %w", outputName, err)
	}

	logrus.Infof("Creating ClusterFlow [%s] in namespace [%s]", flowName, charts.RancherLoggingNamespace)
	clusterFlow := &loggingClusterFlow{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "logging.banzaicloud.io/v1beta1",
			Kind:       "ClusterFlow",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      flowName,
			Namespace: charts.RancherLoggingNamespace,
		},
		Spec: loggingClusterFlowSpec{
			GlobalOutputRefs: []string{outputName},
		},
	}

	_, err = steveClient.SteveType(clusterFlowSteveType).Create(clusterFlow)
	if err != nil {
		return outputName, "", fmt.Errorf("failed to create ClusterFlow [%s]: %w", flowName, err)
	}

	return outputName, flowName, nil
}

// verifyLoggingPipelineActive polls until both the ClusterOutput and ClusterFlow
// reach the active state, confirming the logging pipeline is fully operational.
func verifyLoggingPipelineActive(client *rancher.Client, clusterID, outputName, flowName string) error {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	outputID := charts.RancherLoggingNamespace + "/" + outputName
	logrus.Infof("Waiting for ClusterOutput [%s] to become active", outputID)
	err = kwait.PollUntilContextTimeout(context.TODO(), loggingPipelinePollInterval, loggingPipelinePollTimeout, true, func(ctx context.Context) (bool, error) {
		obj, err := steveClient.SteveType(clusterOutputSteveType).ByID(outputID)
		if err != nil {
			return false, err
		}
		return obj.ObjectMeta.State != nil && obj.ObjectMeta.State.Name == stevestates.Active, nil
	})
	if err != nil {
		return fmt.Errorf("ClusterOutput [%s] did not reach active state: %w", outputName, err)
	}

	flowID := charts.RancherLoggingNamespace + "/" + flowName
	logrus.Infof("Waiting for ClusterFlow [%s] to become active", flowID)
	err = kwait.PollUntilContextTimeout(context.TODO(), loggingPipelinePollInterval, loggingPipelinePollTimeout, true, func(ctx context.Context) (bool, error) {
		obj, err := steveClient.SteveType(clusterFlowSteveType).ByID(flowID)
		if err != nil {
			return false, err
		}
		return obj.ObjectMeta.State != nil && obj.ObjectMeta.State.Name == stevestates.Active, nil
	})
	if err != nil {
		return fmt.Errorf("ClusterFlow [%s] did not reach active state: %w", flowName, err)
	}

	return nil
}

// verifyAdditionalLoggingSourcesDaemonsets checks that more than one DaemonSet is deployed in
// the logging namespace, confirming that the AdditionalLoggingSources option deployed extra
// collectors (e.g. systemd/containerd log collectors for RKE/RKE2/K3s).
func verifyAdditionalLoggingSourcesDaemonsets(client *rancher.Client, clusterID string) error {
	adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
	if err != nil {
		return err
	}

	adminDynamicClient, err := adminClient.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return err
	}

	daemonSetResource := adminDynamicClient.Resource(daemonSetLoggingGVR).Namespace(charts.RancherLoggingNamespace)

	daemonSets, err := daemonSetResource.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}

	logrus.Infof("Found %d DaemonSet(s) in namespace [%s]", len(daemonSets.Items), charts.RancherLoggingNamespace)

	if len(daemonSets.Items) <= 1 {
		return fmt.Errorf("expected more than 1 DaemonSet for AdditionalLoggingSources in namespace [%s], got %d",
			charts.RancherLoggingNamespace, len(daemonSets.Items))
	}

	return nil
}
