package main

import (
	"fmt"
	"os"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/token"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

type wrappedConfig struct {
	Configuration *rancher.Config `yaml:"rancher"`
}

func main() {
	// Get config file path from environment or use default
	configPath := os.Getenv("CATTLE_CONFIG_PATH")
	if configPath == "" {
		configPath = "cattle-config.yaml"
	}

	// Get admin credentials from environment
	username := os.Getenv("RANCHER_ADMIN_USERNAME")
	password := os.Getenv("RANCHER_ADMIN_PASSWORD")

	// Validate required environment variables
	if username == "" {
		username = "admin"
		logrus.Infof("RANCHER_ADMIN_USERNAME not set, using default: admin")
	}
	if password == "" {
		logrus.Fatal("RANCHER_ADMIN_PASSWORD environment variable is required")
	}

	// Read existing config file
	logrus.Infof("Reading config from: %s", configPath)
	configData, err := os.ReadFile(configPath)
	if err != nil {
		logrus.Fatalf("Failed to read config file %s: %v", configPath, err)
	}

	// Parse existing config
	var wrapped wrappedConfig
	err = yaml.Unmarshal(configData, &wrapped)
	if err != nil {
		logrus.Fatalf("Failed to parse config file: %v", err)
	}

	if wrapped.Configuration == nil {
		logrus.Fatal("Config file does not contain rancher configuration")
	}

	// Validate that host is set
	if wrapped.Configuration.Host == "" {
		logrus.Fatal("Config file must contain rancher.host")
	}

	logrus.Infof("Generating admin token for: %s@%s", username, wrapped.Configuration.Host)

	// Dynamically create the admin token
	startTime := time.Now()
	adminToken, err := generateAdminToken(username, password, wrapped.Configuration.Host)
	if err != nil {
		logrus.Fatalf("Failed to create admin token: %v", err)
	}
	duration := time.Since(startTime)

	logrus.Infof("Successfully created admin token in %v", duration)

	// Update the config with the generated token
	wrapped.Configuration.AdminToken = adminToken

	// Marshal updated config back to YAML
	updatedConfigData, err := yaml.Marshal(wrapped)
	if err != nil {
		logrus.Fatalf("Failed to marshal updated config: %v", err)
	}

	// Write updated config back to file
	err = os.WriteFile(configPath, updatedConfigData, 0644)
	if err != nil {
		logrus.Fatalf("Failed to write updated config file: %v", err)
	}

	logrus.Infof("Successfully updated config file: %s", configPath)

	// Print summary (with token masked for security)
	fmt.Println("\n=== Configuration Updated ===")
	fmt.Printf("Config File: %s\n", configPath)
	fmt.Printf("Host: %s\n", wrapped.Configuration.Host)
	fmt.Printf("Username: %s\n", username)
	if wrapped.Configuration.ShellImage != "" {
		fmt.Printf("Shell Image: %s\n", wrapped.Configuration.ShellImage)
	}
	if wrapped.Configuration.Insecure != nil {
		fmt.Printf("Insecure: %v\n", *wrapped.Configuration.Insecure)
	}
	if wrapped.Configuration.Cleanup != nil {
		fmt.Printf("Cleanup: %v\n", *wrapped.Configuration.Cleanup)
	}
	maskLength := min(10, len(adminToken))
	fmt.Printf("Token: %s*** (masked for security)\n", adminToken[:maskLength])
	fmt.Println("============================\n")
}

// generateAdminToken creates an admin token using the provided credentials
func generateAdminToken(username, password, host string) (string, error) {
	adminUser := &management.User{
		Username: username,
		Password: password,
	}

	var userToken *management.Token
	err := kwait.Poll(500*time.Millisecond, 5*time.Minute, func() (done bool, err error) {
		userToken, err = token.GenerateUserToken(adminUser, host)
		if err != nil {
			return false, nil
		}
		return true, nil
	})

	if err != nil {
		return "", err
	}

	return userToken.Token, nil
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
