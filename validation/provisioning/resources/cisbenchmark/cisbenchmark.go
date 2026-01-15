package charts

import (
	"time"

	cis "github.com/rancher/cis-operator/pkg/apis/cis.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	extensionscharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/defaults"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/charts"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	System                   = "System"
	pass                     = "pass"
	scan                     = "scan"
	defaultRegistrySettingID = "system-default-registry"
	serverURLSettingID       = "server-url"
	cisBenchmarkSteveType    = "cis.cattle.io.clusterscan"
	ScanProfileName          = "cis-1.11-profile"
)

// SetupHardenedChart installs the CIS Benchmark chart and waits for all resources to be ready.
func SetupHardenedChart(client *rancher.Client, projectClusterID string, chartInstallOptions *charts.InstallOptions, chartName, chartNamespace string) error {
	serverSetting, err := client.Management.Setting.ByID(serverURLSettingID)
	if err != nil {
		return err
	}

	registrySetting, err := client.Management.Setting.ByID(defaultRegistrySettingID)
	if err != nil {
		return err
	}

	benchmarkChartInstallActionPayload := &charts.PayloadOpts{
		InstallOptions:  *chartInstallOptions,
		Name:            chartName,
		Namespace:       chartNamespace,
		Host:            serverSetting.Value,
		DefaultRegistry: registrySetting.Value,
	}

	logrus.Debugf("Installing %s chart...", chartName)
	err = charts.InstallHardenedChart(client, benchmarkChartInstallActionPayload)
	if err != nil {
		return err
	}

	logrus.Debugf("Waiting for %s chart deployments to have expected number of available replicas...", chartName)
	err = extensionscharts.WatchAndWaitDeployments(client, projectClusterID, chartNamespace, metav1.ListOptions{})
	if err != nil {
		return err
	}

	logrus.Debugf("Waiting for %s chart DaemonSets to have expected number of available nodes...", chartName)
	err = extensionscharts.WatchAndWaitDaemonSets(client, projectClusterID, chartNamespace, metav1.ListOptions{})
	if err != nil {
		return err
	}

	logrus.Debugf("Waiting for %s chart StatefulSets to have expected number of ready replicas...", chartName)
	err = extensionscharts.WatchAndWaitStatefulSets(client, projectClusterID, chartNamespace, metav1.ListOptions{})
	if err != nil {
		return err
	}

	logrus.Debugf("Successfully installed %s chart!", chartName)

	return nil
}

// RunCISScan runs the CIS Benchmark scan with the specified profile name.
func RunCISScan(client *rancher.Client, projectClusterID, scanProfileName string) error {
	logrus.Debugf("Running CIS Benchmark scan: %s", scanProfileName)

	cisScan := cis.ClusterScan{
		ObjectMeta: metav1.ObjectMeta{
			Name: namegen.AppendRandomString(scan),
		},
		Spec: cis.ClusterScanSpec{
			ScanProfileName: scanProfileName,
			ScoreWarning:    pass,
		},
	}

	steveclient, err := client.Steve.ProxyDownstream(projectClusterID)
	if err != nil {
		return err
	}

	scan, err := steveclient.SteveType(cisBenchmarkSteveType).Create(cisScan)
	if err != nil {
		return err
	}

	err = kwait.PollUntilContextTimeout(context.TODO(), 1*time.Second, defaults.TenMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		scanResp, err := steveclient.SteveType(cisBenchmarkSteveType).ByID(scan.ID)
		if err != nil {
			return false, err
		}

		if !scanResp.ObjectMeta.State.Transitioning {
			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return err
	}

	return nil
}
