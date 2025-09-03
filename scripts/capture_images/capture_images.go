package main

import (
	"context"
	"fmt"
	"log"
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

var (
	pulledRegex   = regexp.MustCompile(`Successfully pulled image "([^"]+)"`)
	existingRegex = regexp.MustCompile(`Container image "([^"]+)" already present on machine`)
)

type ClusterInfo struct {
	Name string
	ID   string
}

// Connects to the specified Rancher managed Kubernetes cluster, monitoring and parsing its events while the test
// is run. Writes all pulled image names to a file.
func connectAndMonitor(client *rancher.Client, sigChan chan struct{}, clusterID string) (map[string]struct{}, error) {
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

	previousEvents, err := clientset.CoreV1().Events("").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("Failed creating previous events: %v", err)
	}

	listOptions := metav1.ListOptions{
		FieldSelector:   "involvedObject.kind=Pod,reason=Pulled",
		ResourceVersion: previousEvents.ResourceVersion,
	}

	eventWatcher, err := clientset.CoreV1().Events("").Watch(context.Background(), listOptions)
	if err != nil {
		return nil, fmt.Errorf("Failed watching events: %v", err)
	}
	defer eventWatcher.Stop()

	log.Println("Listening to events on cluster with ID " + clusterID)

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

			matches := pulledRegex.FindStringSubmatch(k8sEvent.Message)

			if len(matches) > 1 {
				imageSet[matches[1]+" (pulled during test)"] = struct{}{}
				continue
			}

			matches = existingRegex.FindStringSubmatch(k8sEvent.Message)

			if len(matches) > 1 {
				imageSet[matches[1]] = struct{}{}
			}
		}
	}
}

func main() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM)

	rancherConfig := new(rancher.Config)
	config.LoadConfig(rancher.ConfigurationFileKey, rancherConfig)

	client, err := rancher.NewClientForConfig("", rancherConfig, session.NewSession())
	if err != nil {
		panic(fmt.Errorf("Error creating client: %w", err))
	}

	clusterList := []ClusterInfo{}

	if rancherConfig.ClusterName != "" {
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
		allClusters, err := client.Management.Cluster.List(nil)
		if err != nil {
			panic(fmt.Errorf("Failed getting local cluster ID: %w", err))
		}

		for _, cluster := range allClusters.Data {
			clusterList = append(clusterList, ClusterInfo{cluster.Name, cluster.ID})
		}
	}

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
