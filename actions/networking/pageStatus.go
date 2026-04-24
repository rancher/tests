package networking

import (
	"crypto/tls"
	"net/http"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/sirupsen/logrus"
)

// GetPageStatus is a function that will attempt to load the Rancher UI's ui.min.js file from both the /v3 and /v1 API endpoints.
func GetPageStatus(rancherConfig *rancher.Config, setting string) error {
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// We want to check both the /v3 and /v1 endpoints. Dynamic and local (true) should be reachable;
	// remote (false) should not be reachable.
	endpoints := []string{"/v3", "/v1"}
	for _, endpoint := range endpoints {
		url := "https://" + rancherConfig.Host + "/api-ui/1.1.11/ui.min.js"
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return err
		}

		req.Header.Set("Authorization", "Bearer "+rancherConfig.AdminToken)
		start := time.Now()
		resp, err := client.Do(req)
		elapsed := time.Since(start)

		defer func(resp *http.Response) {
			if resp != nil {
				resp.Body.Close()
			}
		}(resp)

		if setting == "dynamic" || setting == "true" {
			if err != nil {
				logrus.Errorf("error loading ui.min.js for %s: %v", endpoint, err)
			}

			if resp.StatusCode != 200 {
				logrus.Errorf("unexpected status code %d for %s", resp.StatusCode, endpoint)
			}

			if elapsed >= 10*time.Second {
				logrus.Errorf("request to %s took too long: %s", endpoint, elapsed)
			}
		} else if setting == "false" {
			if err != nil {
				return err
			}

			if resp.StatusCode != 200 {
				logrus.Errorf("unexpected status code %d for %s", resp.StatusCode, endpoint)
			}

			if elapsed < 10*time.Second {
				logrus.Errorf("request to %s was expected to take longer: %s", endpoint, elapsed)
			}
		}
	}

	return nil
}
