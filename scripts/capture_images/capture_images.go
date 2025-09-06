package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"sync"
	"syscall"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/kubeconfig"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ClusterInfo struct {
	Name string
	ID   string
}

// Connects to the specified Rancher managed Kubernetes cluster, monitoring and parsing its events while the test
// is run. Writes all pulled image names to a file.
func connectAndMonitor(client *rancher.Client, sigChan chan struct{}, clusterID string) (map[string]struct{}, error) {
	// Get kubeconfig.
	clientConfig, err := kubeconfig.GetKubeconfig(client, clusterID)
	if err != nil {
		return nil, fmt.Errorf("Failed building client config from string: %v", err)
	}

	restConfig, err := (*clientConfig).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("Failed building client config from string: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("Failed creating clientset object: %v", err)
	}

	// Ignore previous events.
	previousEvents, err := clientset.CoreV1().Events("").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("Failed creating previous events: %v", err)
	}

	listOptions := metav1.ListOptions{
		FieldSelector:   "involvedObject.kind=Pod,reason=Pulling",
		ResourceVersion: previousEvents.ResourceVersion,
	}

	// Start watching for events on all namespaces while ignoring previous events.
	eventWatcher, err := clientset.CoreV1().Events("").Watch(context.Background(), listOptions)
	if err != nil {
		return nil, fmt.Errorf("Failed watching events: %v", err)
	}
	defer eventWatcher.Stop()

	// Use map as a set of images to avoid repetition.
	imageSet := make(map[string]struct{})

	for {
		select {
		case <-sigChan:
			return imageSet, nil
		case rawEvent := <-eventWatcher.ResultChan():
			k8sEvent, ok := rawEvent.Object.(*corev1.Event)
			if !ok {
				continue
			}

			// If this is a pulling image event, extract the image name and version.
			re := regexp.MustCompile(`Pulling image "([^"]+)"`)
			matches := re.FindStringSubmatch(k8sEvent.Message)

			if len(matches) > 1 {
				imageSet[matches[1]] = struct{}{}
			}
		}
	}
}

func main() {
	// A signal channel to notify this script that it can stop monitoring the clusters.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM)

	// This uses the rancher config from the file indicated by the path on CATTLE_TEST_CONFIG
	rancherConfig := new(rancher.Config)
	config.LoadConfig(rancher.ConfigurationFileKey, rancherConfig)

	client, err := rancher.NewClientForConfig("", rancherConfig, session.NewSession())
	if err != nil {
		panic(fmt.Errorf("Error creating client: %w", err))
	}

	// List of cluster IDs for the clusters we are interested in.
	clusterList := []ClusterInfo{}

	if rancherConfig.ClusterName != "" {
		// If a downstream cluster name was provided, look into that cluster and the local cluster.
		localClusterID, err := clusters.GetClusterIDByName(client, "local")
		if err != nil {
			panic(fmt.Errorf("Error getting local cluster ID: %w", err))
		}

		clusterList = append(clusterList, ClusterInfo{Name: "local", ID: localClusterID})

		if rancherConfig.ClusterName != "local" {
			clusterID, err := clusters.GetClusterIDByName(client, rancherConfig.ClusterName)
			if err != nil {
				panic(fmt.Errorf("Error getting local cluster ID: %w", err))
			}

			clusterList = append(clusterList, ClusterInfo{rancherConfig.ClusterName, clusterID})
		}
	} else {
		// If no downstream cluster name was provided, look into every existing cluster.
		// The cluster ID we need to provide kubeconfig.GetKubeConfig is the one from the Management API.
		allClusters, err := client.Management.Cluster.List(nil)
		if err != nil {
			panic(fmt.Errorf("Failed getting local cluster ID: %w", err))
		}

		for _, cluster := range allClusters.Data {
			clusterList = append(clusterList, ClusterInfo{cluster.Name, cluster.ID})
		}
	}

	// For each relevant cluster, get their kubeconfigs, monitor and parse their events.
	// Done in parallel, one goroutine per cluster.
	var wg sync.WaitGroup
	wg.Add(len(clusterList))

	var channelList []chan struct{}

	for _, clusterInfo := range clusterList {
		doneChan := make(chan struct{})
		channelList = append(channelList, doneChan)

		go func() {
			imageSet, err := connectAndMonitor(client, doneChan, clusterInfo.ID)
			if err != nil {
				panic(fmt.Errorf("Failed to capture used images: %v", err))
			}

			// Create a file with a unique name to store names for images used in this cluster.
			file, err := os.Create("/app/images/" + clusterInfo.Name)
			if err != nil {
				panic(fmt.Errorf("Failed to create file for image names: %v", err))
			}
			defer file.Close()

			for image := range imageSet {
				file.Write([]byte(image + "\n"))
			}

			wg.Done()
		}()
	}

	<-sigChan
	for _, channel := range channelList {
		channel <- struct{}{}
	}

	wg.Wait()
}
