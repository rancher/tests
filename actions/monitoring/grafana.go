package monitoring

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"

	"github.com/rancher/shepherd/extensions/defaults"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	GrafanaProxyURLTemplate = "https://%s/k8s/clusters/%s/api/v1/namespaces/cattle-monitoring-system/services/http:rancher-monitoring-grafana:80/proxy"
	LoginURL                = "/login"
	DsQueryURL              = "/api/ds/query"
)

type QueryResponse struct {
	Results struct {
		A struct {
			Frames []struct {
				Data struct {
					Values [][]int `json:"values"`
				} `json:"data"`
			} `json:"frames"`
		} `json:"A"`
	} `json:"results"`
}

func doRequest(client http.Client, method string, url string, payload any) ([]byte, []*http.Cookie, error) {
	req, _ := http.NewRequest(method, url, nil)

	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
		jsonPayload, _ := json.Marshal(payload)
		req.Body = io.NopCloser(bytes.NewBuffer(jsonPayload))
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		return body, resp.Cookies(), nil
	}

	return nil, nil, fmt.Errorf("Request to %s failed with %d: %s\n", url, resp.StatusCode, string(body))
}

// LoginGrafana logs in to a Rancher-proxied Grafana and returns a client embedded with a cookie containing a Ranher user token.
func LoginGrafana(rancherHost string, rancherToken string, clusterID string, skipTLS bool) (*http.Client, error) {
	jar, _ := cookiejar.New(nil)
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: skipTLS},
		},
		Jar: jar,
	}

	parsedBaseURL, err := url.Parse("https://" + rancherHost)
	if err != nil {
		return nil, err
	}

	cookies := []*http.Cookie{{Name: "R_SESS", Value: rancherToken}}
	httpClient.Jar.SetCookies(parsedBaseURL, cookies)

	return httpClient, nil
}

// PrometheusQueryInGrafana runs a Prometheus query using the Grafana API as the best way to check for expected values on Grafana dashboards.
func PrometheusQueryInGrafana(client *http.Client, rancherHost string, clusterID string, query string) (int, error) {
	body := map[string]any{
		"queries": []map[string]any{{
			"refId": "A",
			"datasource": map[string]any{
				"uid": "prometheus",
			},
			"expr":      query,
			"queryType": "instant",
			"format":    "table",
		}},
		"from": "now",
		"to":   "now",
	}

	grafanaProxyURL := fmt.Sprintf(GrafanaProxyURLTemplate, rancherHost, clusterID)
	var value int
	var resp QueryResponse
	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveHundredMillisecondTimeout, 30*time.Second, false, func(context.Context) (bool, error) {
		respBytes, _, err := doRequest(*client, http.MethodPost, grafanaProxyURL+DsQueryURL, body)
		if err != nil {
			return false, err
		}

		if err := json.Unmarshal(respBytes, &resp); err != nil {
			return false, err
		}

		if len(resp.Results.A.Frames) == 0 {
			return false, fmt.Errorf("No frames returned")
		}

		values := resp.Results.A.Frames[0].Data.Values

		if len(values) < 2 || len(values[1]) == 0 {
			return false, nil // If no values are shown, possibly this is because prometheus data takes a second to keep up.
		}

		value = values[1][0]
		return true, nil
	})

	return value, err
}
