//go:build (validation || pit.weekly) && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

// Throwaway evidence harness: dumps both chart namespaces' state, the Rancher
// server pods' recent logs, and the catalog index ConfigMap chunks (reassembled,
// chart entry names listed) through the proxied k8s client only, so it works
// against degraded envs whose management API no longer answers. Only ever
// dispatched explicitly by name; delete before final review.
package rancherupgrade

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"regexp"
	"sort"
	"strconv"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/session"
	interoperableCharts "github.com/rancher/tests/interoperability/charts"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ObservabilityEvidenceSuite struct {
	suite.Suite
}

var chunkNamePattern = regexp.MustCompile(`^(.*)-(\d+)-([0-9a-f-]+)$`)

func (s *ObservabilityEvidenceSuite) TestDumpEvidence() {
	testSession := session.NewSession()
	defer testSession.Cleanup()

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)

	k8s, err := clusterK8sClient(client, localClusterName)
	require.NoError(s.T(), err)

	logNamespaceWorkloadState(k8s, interoperableCharts.StackStateServerNamespace)
	logNamespaceWorkloadState(k8s, interoperableCharts.StackstateNamespace)
	dumpRepoIndexChunks(k8s)
	dumpRancherLogs(k8s)
}

// dumpRepoIndexChunks reassembles the catalog index ConfigMap chunks (the
// controller splits gzip'd indexes over 1MiB) and logs each repo's chart
// entry names.
func dumpRepoIndexChunks(k8s *kubernetes.Clientset) {
	cms, err := k8s.CoreV1().ConfigMaps(cattleSystemNamespace).List(context.TODO(), meta.ListOptions{})
	if err != nil {
		log.Warnf("diagnostics: listing configmaps failed: %v", err)
		return
	}
	groups := map[string]map[int][]byte{}
	for i := range cms.Items {
		cm := cms.Items[i]
		if cm.Annotations["catalog.cattle.io/size"] == "" {
			continue
		}
		m := chunkNamePattern.FindStringSubmatch(cm.Name)
		if m == nil {
			continue
		}
		idx, _ := strconv.Atoi(m[2])
		if groups[m[1]] == nil {
			groups[m[1]] = map[int][]byte{}
		}
		groups[m[1]][idx] = cm.BinaryData["content"]
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		chunks := groups[name]
		order := make([]int, 0, len(chunks))
		for idx := range chunks {
			order = append(order, idx)
		}
		sort.Ints(order)
		var all []byte
		for _, idx := range order {
			all = append(all, chunks[idx]...)
		}
		gz, err := gzip.NewReader(bytes.NewReader(all))
		if err != nil {
			log.Warnf("diagnostics: repo index %s (%d chunks, %d bytes): gzip: %v", name, len(order), len(all), err)
			continue
		}
		data, err := io.ReadAll(gz)
		gz.Close()
		if err != nil {
			log.Warnf("diagnostics: repo index %s (%d chunks, %d bytes): gunzip: %v", name, len(order), len(all), err)
			continue
		}
		index := map[string]interface{}{}
		if err := json.Unmarshal(data, &index); err != nil {
			log.Warnf("diagnostics: repo index %s (%d chunks, %d bytes): json: %v", name, len(order), len(all), err)
			continue
		}
		entries := map[string]interface{}{}
		if e, ok := index["entries"].(map[string]interface{}); ok {
			entries = e
		}
		keys := make([]string, 0, len(entries))
		for k := range entries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		log.Infof("diagnostics: repo index %s: %d chunks, %d bytes, %d chart entries: %v", name, len(order), len(all), len(keys), keys)
	}
}

func dumpRancherLogs(k8s *kubernetes.Clientset) {
	tail := int64(rancherLogTailLines)
	pods, err := k8s.CoreV1().Pods(cattleSystemNamespace).List(context.TODO(), meta.ListOptions{LabelSelector: rancherPodLabel})
	if err != nil {
		log.Warnf("diagnostics: listing rancher pods failed: %v", err)
		return
	}
	for i := range pods.Items {
		logs, logErr := k8s.CoreV1().Pods(cattleSystemNamespace).GetLogs(pods.Items[i].Name, &corev1.PodLogOptions{TailLines: &tail}).DoRaw(context.TODO())
		if logErr != nil {
			log.Warnf("diagnostics: fetching rancher logs from %s failed: %v", pods.Items[i].Name, logErr)
			continue
		}
		log.Infof("diagnostics: rancher server log tail from pod %s:\n%s", pods.Items[i].Name, string(logs))
	}
}

func TestObservabilityEvidenceSuite(t *testing.T) {
	suite.Run(t, new(ObservabilityEvidenceSuite))
}
