package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/kubeconfig"
	"github.com/rancher/shepherd/extensions/kubectl"
	"github.com/rancher/shepherd/extensions/rancherversion"
	"github.com/rancher/shepherd/pkg/session"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	logBufferSize            = "2MB"
	kubernetesVersionCommand = "kubectl version -o json | jq -r '\"kubernetes:\" + .serverVersion.gitVersion'"
	imagesVersionsCommand    = "kubectl get pods --all-namespaces -o jsonpath=\"{..image}\" |tr -s '[[:space:]]' '\n' |sort |uniq"
	imagesPath               = "/app/images/"
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
func connectAndMonitor(client *rancher.Client, sigChan chan struct{}, clusterID string, file *os.File) error {
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
				file.WriteString(fmt.Sprintf("%s (pulled during test)\n", matches[1]))
				continue
			}

			matches = existingRegex.FindStringSubmatch(k8sEvent.Message)

			if len(matches) > 1 {
				file.WriteString(fmt.Sprintf("%s \n", matches[1]))
			}
		}
	}
}

func main() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM)

	client, err := rancher.NewClient("", session.NewSession())
	if err != nil {
		panic(fmt.Errorf("Error creating client: %w", err))
	}

	clusterName := client.RancherConfig.ClusterName
	clusterList := []ClusterInfo{}

	if clusterName != "" {
		localClusterID, err := clusters.GetClusterIDByName(client, "local")
		if err != nil {
			panic(fmt.Errorf("Error getting local cluster ID: %w", err))
		}

		clusterList = append(clusterList, ClusterInfo{Name: "local", ID: localClusterID})

		if clusterName != "local" {
			clusterID, err := clusters.GetClusterIDByName(client, clusterName)
			if err != nil {
				panic(fmt.Errorf("Error getting local cluster ID: %w", err))
			}

			clusterList = append(clusterList, ClusterInfo{clusterName, clusterID})
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

	c, err := clusters.NewClusterMeta(client, clusterName)
	if err != nil {
		panic(fmt.Errorf("Failed to create file for versions: %v", err))
	}

	versions, err := captureVersions(client, c.ID)
	if err != nil {
		panic(fmt.Errorf("Failed to create file for versions: %v", err))
	}

	file, err := os.Create(imagesPath + "version-information")
	if err != nil {
		panic(fmt.Errorf("Failed to create file for version-information: %v", err))
	}
	defer file.Close()
	file.Write([]byte(versions + "\n"))

	filesMap := make(map[string]*os.File)
	for _, clusterInfo := range clusterList {
		file, err := os.Create(imagesPath + clusterInfo.Name)
		if err != nil {
			panic(fmt.Errorf("Failed to create file for image names: %v", err))
		}
		defer file.Close()
		filesMap[clusterInfo.Name] = file
	}

	var wg sync.WaitGroup
	wg.Add(len(clusterList))

	var channelList []chan struct{}

	for _, clusterInfo := range clusterList {
		doneChan := make(chan struct{})
		channelList = append(channelList, doneChan)

		go func() {
			file = filesMap[clusterInfo.Name]
			err := connectAndMonitor(client, doneChan, clusterInfo.ID, file)
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
}

// captureVersions gets the images, rancher and kubernetes versions on the cluster
func captureVersions(client *rancher.Client, clusterID string) (string, error) {
	var b strings.Builder

	config, err := rancherversion.RequestRancherVersion(client.RancherConfig.Host)
	if err != nil {
		return "", err
	}

	b.WriteString(fmt.Sprintf("rancher:%s\n", config.RancherVersion))
	b.WriteString(fmt.Sprintf("rancher-commit:%s\n", config.GitCommit))
	b.WriteString(fmt.Sprintf("is-prime:%t\n", config.IsPrime))

	versionsCommand := []string{
		"sh", "-c", fmt.Sprintf("%s && %s", kubernetesVersionCommand, imagesVersionsCommand),
	}

	log, err := kubectl.Command(client, nil, clusterID, versionsCommand, logBufferSize)
	if err != nil {
		return "", err
	}

	b.WriteString(log)
	return b.String(), nil
}
