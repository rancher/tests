package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"sort"
	"sync"
	"syscall"

	"github.com/rancher/shepherd/clients/rancher"
	clusterV3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/kubeconfig"
	"github.com/rancher/shepherd/extensions/rancherversion"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/workloads/pods"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	localClusterName = "local"
	imageReportPath  = "/app/image-report/image-report.txt"
)

var (
	pulledRegex   = regexp.MustCompile(`Successfully pulled image "([^"]+)"`)
	existingRegex = regexp.MustCompile(`Container image "([^"]+)" already present on machine`)
)

// writeImageSet writes a set of image names (in the form of keys on a map) to a file in sorted fashion.
func writeImageSet(imageSet map[string]string, file *os.File) {
	var sortedImages []string

	for image, value := range imageSet {
		sortedImages = append(sortedImages, image+value)
	}

	sort.Slice(sortedImages, func(i, j int) bool {
		return sortedImages[i] < sortedImages[j]
	})

	for _, image := range sortedImages {
		file.WriteString(image + "\n")
	}
}

// Connects to the specified Rancher managed Kubernetes cluster, monitoring and parsing its events while the test
// is run. Stores all unique used image names within a provided map.
func connectAndMonitor(client *rancher.Client, sigChan chan struct{}, clusterID string, imageSet map[string]string) error {
	clientConfig, err := kubeconfig.GetKubeconfig(client, clusterID)
	if err != nil {
		return fmt.Errorf("Failed building client config from string: %v", err)
	}

	restConfig, err := (*clientConfig).ClientConfig()
	if err != nil {
		return fmt.Errorf("Failed building client config from string: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("Failed creating clientset object: %v", err)
	}

	previousEvents, err := clientset.CoreV1().Events("").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("Failed creating previous events: %v", err)
	}

	listOptions := metav1.ListOptions{
		FieldSelector:   "involvedObject.kind=Pod,reason=Pulled",
		ResourceVersion: previousEvents.ResourceVersion,
	}

	eventWatcher, err := clientset.CoreV1().Events("").Watch(context.Background(), listOptions)
	if err != nil {
		return fmt.Errorf("Failed watching events: %v", err)
	}
	defer eventWatcher.Stop()

	log.Println("Listening to events on cluster with ID " + clusterID)

	for {
		select {
		case <-sigChan:
			return nil
		case rawEvent := <-eventWatcher.ResultChan():
			k8sEvent, ok := rawEvent.Object.(*corev1.Event)
			if !ok {
				continue
			}

			matches := pulledRegex.FindStringSubmatch(k8sEvent.Message)

			if len(matches) > 1 {
				imageSet[matches[1]] = "(pulled during test)"
				continue
			}

			matches = existingRegex.FindStringSubmatch(k8sEvent.Message)

			if len(matches) <= 1 {
				continue
			}

			if _, ok := imageSet[matches[1]]; !ok {
				imageSet[matches[1]] = ""
			}
		}
	}
}

func main() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	reportFile, err := os.Create(imageReportPath)
	if err != nil {
		panic(fmt.Errorf("Error creating report file: %w", err))
	}
	defer reportFile.Close()

	client, err := rancher.NewClient("", session.NewSession())
	if err != nil {
		panic(fmt.Errorf("Error creating client: %w", err))
	}

	clusterName := client.RancherConfig.ClusterName

	allClusters, err := client.Management.Cluster.List(nil)
	if err != nil {
		panic(fmt.Errorf("Failed getting cluster objects: %w", err))
	}

	var targetClusters []clusterV3.Cluster
	if clusterName != "" {
		for _, cluster := range allClusters.Data {
			if slices.Contains([]string{localClusterName, clusterName}, cluster.Name) {
				targetClusters = append(targetClusters, cluster)
			}
		}
	} else {
		targetClusters = allClusters.Data
	}

	config, err := rancherversion.RequestRancherVersion(client.RancherConfig.Host)
	if err != nil {
		log.Panicln(fmt.Errorf("Error getting Rancher information: %w", err))
	}

	reportFile.WriteString(fmt.Sprintf("rancher:%s\n", config.RancherVersion))
	reportFile.WriteString(fmt.Sprintf("rancher-commit:%s\n", config.GitCommit))
	reportFile.WriteString(fmt.Sprintf("is-prime:%t\n", config.IsPrime))

	for _, cluster := range targetClusters {
		reportFile.WriteString("Kubernetes version on " + cluster.Name + " cluster: " + cluster.Version.GitVersion + "\n")

		downstreamClient, err := client.Steve.ProxyDownstream(cluster.ID)
		if err != nil {
			panic(fmt.Errorf("Failed creating downstream client for cluster %s: %w", cluster.Name, err))
		}

		steveClient := downstreamClient.SteveType(stevetypes.Pod)
		clusterPods, err := steveClient.List(nil)
		if err != nil {
			panic(fmt.Errorf("Failed getting pods for cluster %s: %w", cluster.Name, err))
		}

		clusterImageSet := make(map[string]string)

		for _, pod := range clusterPods.Data {
			for _, image := range pods.GetPodImages(&pod) {
				clusterImageSet[image] = ""
			}
		}

		reportFile.WriteString("Images present on the " + cluster.Name + " cluster previous to the test run:\n\n")
		writeImageSet(clusterImageSet, reportFile)
		reportFile.WriteString("\n")
	}

	var wg sync.WaitGroup
	wg.Add(len(targetClusters))

	var channelList []chan struct{}
	imageSet := make(map[string]map[string]string)

	for _, clusterInfo := range targetClusters {
		doneChan := make(chan struct{})
		channelList = append(channelList, doneChan)

		imageSet[clusterInfo.Name] = make(map[string]string)

		go func() {
			err := connectAndMonitor(client, doneChan, clusterInfo.ID, imageSet[clusterInfo.Name])
			if err != nil {
				panic(fmt.Errorf("Failed to capture used images: %v", err))
			}

			wg.Done()
		}()
	}

	<-sigChan
	for _, channel := range channelList {
		channel <- struct{}{}
	}
	wg.Wait()

	for clusterName, images := range imageSet {
		reportFile.WriteString("Images used within cluster " + clusterName + ":\n\n")
		writeImageSet(images, reportFile)
		reportFile.WriteString("\n")
	}
}
