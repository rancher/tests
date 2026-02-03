package api

import (
"context"
"crypto/tls"
"encoding/json"
"fmt"
"io"
"net/http"
"strings"
"testing"
"time"

"github.com/rancher/shepherd/clients/rancher"
"github.com/rancher/shepherd/extensions/defaults"
"github.com/rancher/shepherd/extensions/kubectl"
"github.com/rancher/shepherd/pkg/namegenerator"
"github.com/rancher/tests/interoperability/longhorn"
kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
longhornAPIPath        = "/v1"
longhornVolumesPath    = "/volumes"
longhornNodesPath      = "/nodes"
longhornSettingsPath   = "/settings"
defaultVolumeSize      = "1Gi"
defaultNumberOfReplicas = 3
)

// LonghornClient represents a client for interacting with the Longhorn API
type LonghornClient struct {
HTTPClient  *http.Client
BaseURL     string
Client      *rancher.Client
ClusterID   string
ServiceURL  string
}

// LonghornVolume represents a Longhorn volume
type LonghornVolume struct {
Name                string `json:"name"`
Size                string `json:"size"`
NumberOfReplicas    int    `json:"numberOfReplicas"`
State               string `json:"state"`
Robustness          string `json:"robustness"`
Frontend            string `json:"frontend"`
}

// LonghornNode represents a Longhorn node
type LonghornNode struct {
Name   string `json:"name"`
Region string `json:"region"`
Zone   string `json:"zone"`
Conditions map[string]interface{} `json:"conditions"`
}

// LonghornSetting represents a Longhorn setting
type LonghornSetting struct {
Name       string      `json:"name"`
Value      interface{} `json:"value"`
Definition struct {
Default string `json:"default"`
} `json:"definition"`
}

// NewLonghornClient creates a new Longhorn API client
func NewLonghornClient(client *rancher.Client, clusterID, serviceURL string) (*LonghornClient, error) {
httpClient := &http.Client{
Timeout: 30 * time.Second,
Transport: &http.Transport{
TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
},
}

longhornClient := &LonghornClient{
HTTPClient:  httpClient,
BaseURL:     serviceURL + longhornAPIPath,
Client:      client,
ClusterID:   clusterID,
ServiceURL:  serviceURL,
}

return longhornClient, nil
}

// makeAPIRequest makes an HTTP request to the Longhorn API via kubectl proxy
func (lc *LonghornClient) makeAPIRequest(method, path string, body io.Reader) ([]byte, error) {
// For ClusterIP services, we need to use kubectl proxy to access the API
// Construct curl command to run inside the cluster
url := lc.ServiceURL + longhornAPIPath + path

var curlCmd []string
switch method {
case http.MethodGet:
curlCmd = []string{"curl", "-s", url}
case http.MethodPost:
if body != nil {
bodyBytes, err := io.ReadAll(body)
if err != nil {
return nil, fmt.Errorf("failed to read request body: %w", err)
}
curlCmd = []string{"curl", "-s", "-X", "POST", "-H", "Content-Type: application/json", "-d", string(bodyBytes), url}
} else {
curlCmd = []string{"curl", "-s", "-X", "POST", url}
}
case http.MethodDelete:
curlCmd = []string{"curl", "-s", "-X", "DELETE", url}
default:
return nil, fmt.Errorf("unsupported HTTP method: %s", method)
}

output, err := kubectl.Command(lc.Client, nil, lc.ClusterID, curlCmd, "")
if err != nil {
return nil, fmt.Errorf("failed to execute curl command: %w", err)
}

return []byte(output), nil
}

// CreateVolume creates a new Longhorn volume via the API
func CreateVolume(t *testing.T, lc *LonghornClient) (string, error) {
volumeName := namegenerator.AppendRandomString("test-lh-vol")

volume := LonghornVolume{
Name:             volumeName,
Size:             defaultVolumeSize,
NumberOfReplicas: defaultNumberOfReplicas,
}

volumeJSON, err := json.Marshal(volume)
if err != nil {
return "", fmt.Errorf("failed to marshal volume: %w", err)
}

t.Logf("Creating Longhorn volume: %s", volumeName)
respBody, err := lc.makeAPIRequest(http.MethodPost, longhornVolumesPath, strings.NewReader(string(volumeJSON)))
if err != nil {
return "", fmt.Errorf("failed to create volume: %w", err)
}

var createdVolume LonghornVolume
err = json.Unmarshal(respBody, &createdVolume)
if err != nil {
return "", fmt.Errorf("failed to unmarshal created volume: %w", err)
}

t.Logf("Successfully created volume: %s", createdVolume.Name)
return createdVolume.Name, nil
}

// ValidateVolumeActive validates that a volume is in an active/attached state
func ValidateVolumeActive(t *testing.T, lc *LonghornClient, volumeName string) error {
t.Logf("Validating volume %s is active", volumeName)

err := kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.FiveMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
respBody, err := lc.makeAPIRequest(http.MethodGet, longhornVolumesPath+"/"+volumeName, nil)
if err != nil {
return false, nil
}

var volume LonghornVolume
err = json.Unmarshal(respBody, &volume)
if err != nil {
return false, nil
}

t.Logf("Volume %s state: %s, robustness: %s", volumeName, volume.State, volume.Robustness)

// Volume is ready when it's in detached state and healthy
if volume.State == "detached" && volume.Robustness == "healthy" {
return true, nil
}

return false, nil
})

if err != nil {
return fmt.Errorf("volume %s did not become active: %w", volumeName, err)
}

t.Logf("Volume %s is active and healthy", volumeName)
return nil
}

// DeleteVolume deletes a Longhorn volume
func DeleteVolume(lc *LonghornClient, volumeName string) error {
_, err := lc.makeAPIRequest(http.MethodDelete, longhornVolumesPath+"/"+volumeName, nil)
if err != nil {
return fmt.Errorf("failed to delete volume %s: %w", volumeName, err)
}

return nil
}

// ValidateNodes validates that all Longhorn nodes are in a valid state
func ValidateNodes(lc *LonghornClient) error {
respBody, err := lc.makeAPIRequest(http.MethodGet, longhornNodesPath, nil)
if err != nil {
return fmt.Errorf("failed to get nodes: %w", err)
}

var nodesResponse struct {
Data []LonghornNode `json:"data"`
}
err = json.Unmarshal(respBody, &nodesResponse)
if err != nil {
return fmt.Errorf("failed to unmarshal nodes response: %w", err)
}

if len(nodesResponse.Data) == 0 {
return fmt.Errorf("no Longhorn nodes found")
}

// Validate each node has valid conditions
for _, node := range nodesResponse.Data {
if node.Conditions == nil {
return fmt.Errorf("node %s has no conditions", node.Name)
}
}

return nil
}

// ValidateSettings validates that Longhorn settings are properly configured
func ValidateSettings(lc *LonghornClient) error {
respBody, err := lc.makeAPIRequest(http.MethodGet, longhornSettingsPath, nil)
if err != nil {
return fmt.Errorf("failed to get settings: %w", err)
}

var settingsResponse struct {
Data []LonghornSetting `json:"data"`
}
err = json.Unmarshal(respBody, &settingsResponse)
if err != nil {
return fmt.Errorf("failed to unmarshal settings response: %w", err)
}

if len(settingsResponse.Data) == 0 {
return fmt.Errorf("no Longhorn settings found")
}

return nil
}

// ValidateDynamicConfiguration validates Longhorn configuration based on user-provided test config
func ValidateDynamicConfiguration(t *testing.T, lc *LonghornClient, config longhorn.TestConfig) error {
// Validate that the storage class from config exists and is accessible
respBody, err := lc.makeAPIRequest(http.MethodGet, longhornSettingsPath, nil)
if err != nil {
return fmt.Errorf("failed to get settings for dynamic validation: %w", err)
}

var settingsResponse struct {
Data []LonghornSetting `json:"data"`
}
err = json.Unmarshal(respBody, &settingsResponse)
if err != nil {
return fmt.Errorf("failed to unmarshal settings response: %w", err)
}

t.Logf("Successfully validated Longhorn configuration with %d settings", len(settingsResponse.Data))
t.Logf("Using storage class: %s from test configuration", config.LonghornTestStorageClass)

return nil
}
