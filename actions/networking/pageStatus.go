package networking

import (
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/sirupsen/logrus"
)

var errNotLoaded = errors.New("not loaded")

// GetPageStatus is a function that will attempt to load the /v3 and /v1 API-UI pages and verify
// whether they load based on the ui-offline-preferred setting.
func GetPageStatus(rancherConfig *rancher.Config, setting string) error {
	client := &http.Client{
		Timeout:   20 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}

	for _, endpoint := range []string{"/v3", "/v1"} {
		err := loadAPIUI(client, rancherConfig, endpoint)

		switch {
		case setting == "false" && err == nil:
			return fmt.Errorf("API-UI %s unexpectedly loaded while remote UI was preferred", endpoint)
		case setting == "false" && errors.Is(err, errNotLoaded):
			logrus.Infof("API-UI %s did not load with ui-offline-preferred=false: %v", endpoint, err)
		case err != nil:
			return err
		}
	}

	return nil
}

func loadAPIUI(client *http.Client, rancherConfig *rancher.Config, endpoint string) error {
	pageURL, err := url.Parse("https://" + rancherConfig.Host + endpoint)
	if err != nil {
		return err
	}

	body, err := fetch(client, pageURL.String(), rancherConfig.AdminToken)
	if err != nil {
		return err
	}

	var assetPattern = regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["']([^"']+\.(?:js|css)(?:\?[^"']*)?)["']`)

	matches := assetPattern.FindAllStringSubmatch(html.UnescapeString(string(body)), -1)
	if len(matches) == 0 {
		return fmt.Errorf("%s page references no scripts or stylesheets", endpoint)
	}

	for _, match := range matches {
		ref, err := url.Parse(match[1])
		if err != nil {
			return err
		}

		asset := pageURL.ResolveReference(ref)

		token := ""
		if asset.Host == pageURL.Host {
			token = rancherConfig.AdminToken
		}

		if _, err := fetch(client, asset.String(), token); err != nil {
			return err
		}
	}

	return nil
}

func fetch(client *http.Client, target, token string) ([]byte, error) {
	req, err := http.NewRequest("GET", target, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s after %s: %v", errNotLoaded, target, time.Since(start), err)
	}
	defer resp.Body.Close()

	logrus.Debugf("%s status=%d elapsed=%s", target, resp.StatusCode, time.Since(start))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s returned status %d", errNotLoaded, target, resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}
