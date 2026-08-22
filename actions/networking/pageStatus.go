package networking

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/sirupsen/logrus"
	kwait "k8s.io/apimachinery/pkg/util/wait"
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
		url := "https://" + rancherConfig.Host + endpoint

		doRequest := func() (*http.Response, time.Duration, error) {
			req, err := http.NewRequest("GET", url, nil)
			if err != nil {
				return nil, 0, err
			}

			req.Header.Set("Authorization", "Bearer "+rancherConfig.AdminToken)
			start := time.Now()
			resp, err := client.Do(req)
			elapsed := time.Since(start)
			return resp, elapsed, err
		}

		var resp *http.Response
		var elapsed time.Duration
		var err error

		if setting == "dynamic" || setting == "true" {
			var lastErr error
			err = kwait.PollUntilContextTimeout(context.TODO(), 10*time.Second, 5*time.Minute, true, func(ctx context.Context) (done bool, pollErr error) {
				if resp != nil {
					resp.Body.Close()
					resp = nil
				}

				resp, elapsed, lastErr = doRequest()
				if lastErr != nil {
					return false, nil
				}

				return true, nil
			})
			if err != nil {
				if lastErr != nil {
					return lastErr
				}
				return err
			}
		} else {
			resp, elapsed, err = doRequest()
		}

		defer func(resp *http.Response) {
			if resp != nil {
				resp.Body.Close()
			}
		}(resp)

		if setting == "dynamic" || setting == "true" {
			if err != nil {
				return err
			}

			if resp == nil {
				return fmt.Errorf("nil response received for %s", endpoint)
			}

			logrus.Infof("reachable endpoint %s with status %d in %s", endpoint, resp.StatusCode, elapsed)

			if elapsed >= 10*time.Second {
				logrus.Errorf("request to %s took too long: %s", endpoint, elapsed)
			}
		} else if setting == "false" {
			if err != nil {
				continue
			}

			if resp.StatusCode == http.StatusOK && elapsed < 10*time.Second {
				logrus.Errorf("endpoint %s unexpectedly reachable while remote UI was preferred: status=%d elapsed=%s", endpoint, resp.StatusCode, elapsed)
			}
		}
	}

	return nil
}
