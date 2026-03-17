package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rancher/shepherd/pkg/config"
	"sigs.k8s.io/yaml"
)

type HarvesterClusterConfig struct {
	Host          string `yaml:"host"`
	User          string `yaml:"user"`
	Password      string `yaml:"password"`
	TargetVersion string `yaml:"targetVersion"`
}

var client = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
}

var (
	upgradeAnotations = map[string]string{
		"harvesterhci.io/skipSingleReplicaDetachedVol": "true",
		"harvesterhci.io/skip-version-check":           "true",
	}
	upgradeTimeLimit     = time.Now().Add(2 * time.Hour)
	errorAlreadyExists   = errors.New("Object already exists")
	errorNodeUnavailable = errors.New("Node unavailable, possibibly upgrading.")
)

const (
	harvesterClusterConfigKey  = "harvesterClusterConfig"
	rancherReleasesHost        = "releases.rancher.com"
	httpsSchema                = "https://"
	upgradeStateLabel          = "harvesterhci.io/upgradeState"
	loginURL                   = "/v3-public/localProviders/local?action=login"
	triggerUpgradeURL          = "/v1/harvester/harvesterhci.io.upgrades/harvester-system"
	createVersionURL           = "/v1/harvester/harvesterhci.io.versions"
	versionManifestURLTemplate = "/harvester/%s/version.yaml"
	getUpgradeURLTemplate      = "/apis/harvesterhci.io/v1beta1/namespaces/harvester-system/upgrades/%s"
)

func makeRequest(host string, method string, url string, authToken string, reqBody any, expectedStatus int) ([]byte, error) {
	var bodyReader io.Reader

	if reqBody != nil {
		reqBodyBytes, err := json.Marshal(reqBody)
		if err != nil {
			return nil, err
		}

		bodyReader = bytes.NewReader(reqBodyBytes)
	}

	req, _ := http.NewRequest(method, httpsSchema+host+url, bodyReader)
	req.Header.Set("Content-Type", "application/json")

	if authToken != "" {
		req.Header.Set("Authorization", authToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		return nil, errorAlreadyExists
	}

	if resp.StatusCode == http.StatusGatewayTimeout {
		return nil, errorNodeUnavailable
	}

	if resp.StatusCode != expectedStatus {
		errorBody, _ := io.ReadAll(resp.Body)
		return nil, errors.New(string(errorBody))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return respBody, nil
}

func authenticateToHarvester(harvesterclusterConfig HarvesterClusterConfig) (string, error) {
	loginPost := struct {
		User     string `json:"username"`
		Password string `json:"password"`
	}{
		User:     harvesterclusterConfig.User,
		Password: harvesterclusterConfig.Password,
	}

	respBody, err := makeRequest(harvesterclusterConfig.Host, http.MethodPost, loginURL, "", loginPost, http.StatusCreated)
	if err != nil {
		return "", err
	}

	var tokenResponse struct { // This ingores the other fields as they aren't useful here.
		Token string `json:"token"`
	}

	err = json.Unmarshal(respBody, &tokenResponse)
	if err != nil {
		return "", err
	}

	return "Bearer " + tokenResponse.Token, nil
}

func createVersionViaAPI(host, token, targetVersion string) error {
	versionManifestURL := fmt.Sprintf(versionManifestURLTemplate, targetVersion)

	versionYaml, err := makeRequest(rancherReleasesHost, http.MethodGet, versionManifestURL, token, nil, http.StatusOK)
	if err != nil {
		return err
	}

	var version struct {
		MetaData struct {
			Name      string `yaml:"name" json:"name"`
			Namespace string `yaml:"namespace" json:"namespace"`
		} `yaml:"metadata" json:"metadata"`
		Spec struct {
			IsoChecksum    string `yaml:"isoChecksum" json:"isoChecksum"`
			IsoURL         string `yaml:"isoURL" json:"isoURL"`
			DisplayVersion string `json:"displayVersion"`
			ReleaseDate    string `yaml:"releaseDate" json:"releaseDate"`
		} `yaml:"spec" json:"spec"`
	}

	err = yaml.Unmarshal(versionYaml, &version)
	if err != nil {
		return err
	}

	_, err = makeRequest(host, http.MethodPost, createVersionURL, token, version, http.StatusCreated)
	return err
}

func triggerUpgrade(host string, targetVersion string, token string) (string, error) {
	upgradeName := "upgrade-" + strings.Replace(targetVersion, ".", "-", -1)

	upgradePayload := map[string]any{
		"type": "harvesterhci.io.upgrade",
		"metadata": map[string]any{
			"name":        upgradeName,
			"namespace":   "harvester-system",
			"annotations": upgradeAnotations,
		},
		"spec": map[string]string{
			"version": targetVersion,
		},
	}

	_, err := makeRequest(host, http.MethodPost, triggerUpgradeURL, token, upgradePayload, http.StatusCreated)
	return upgradeName, err
}

func main() {
	var harvesterClusterConfig HarvesterClusterConfig
	config.LoadConfig(harvesterClusterConfigKey, &harvesterClusterConfig)

	// Authenticate
	token, err := authenticateToHarvester(harvesterClusterConfig)
	if err != nil {
		fmt.Printf("Failed to authenticate to Harvester: %s\n", err.Error())
		os.Exit(1)
	}
	fmt.Println("Successfully authenticated")

	// Create version via API.
	err = createVersionViaAPI(harvesterClusterConfig.Host, token, harvesterClusterConfig.TargetVersion)
	if err == errorAlreadyExists {
		fmt.Println("Version object already existed.")
	} else if err != nil {
		fmt.Printf("Failed during version creation: %v\n", err.Error())
		os.Exit(1)
	} else {
		fmt.Println("Version resource created successfully")
	}

	// Trigger Upgrade via API
	upgradeName, err := triggerUpgrade(harvesterClusterConfig.Host, harvesterClusterConfig.TargetVersion, token)
	if err != nil {
		fmt.Printf("Upgrade trigger failed: %v\n", err.Error())
		os.Exit(1)
	}
	fmt.Printf("Upgrade object name: %s\n", upgradeName)

	time.Sleep(100 * time.Second)
	fmt.Printf("Upgrade to %s initiated successfully\n", harvesterClusterConfig.TargetVersion)

	upgradeURL := fmt.Sprintf(getUpgradeURLTemplate, upgradeName)
	conds := make(map[string]string)

	for time.Now().Before(upgradeTimeLimit) {
		time.Sleep(1 * time.Minute)

		respBytes, err := makeRequest(harvesterClusterConfig.Host, http.MethodGet, upgradeURL, token, nil, http.StatusOK)
		if err == errorNodeUnavailable {
			fmt.Printf("Failed to track update: %v. Trying again in a minute\n", err.Error())
			continue
		} else if err != nil {
			fmt.Printf("Failed to track update: %v\n", err.Error())
		}

		var upgrade struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		}

		err = json.Unmarshal(respBytes, &upgrade)
		if err != nil {
			fmt.Printf("Failed processing upgrade response: %v\n", err.Error())
			os.Exit(1)
		}

		for _, c := range upgrade.Status.Conditions {
			conds[c.Type] = c.Status
		}

		// Check State and Completion
		state := upgrade.Metadata.Labels[upgradeStateLabel]
		if state == "Succeeded" && conds["Completed"] == "True" {
			fmt.Println("Upgrade finished successfully")
			return
		}

		// Check for failures
		for _, status := range conds {
			if status == "False" {
				fmt.Printf("Upgrade failed. Current conditions: %+v\n", conds)
				os.Exit(1)
			}
		}
	}

	fmt.Printf("Upgrade time limit exceeded. Current conditions: %+v\n", conds)
	os.Exit(1)
}
