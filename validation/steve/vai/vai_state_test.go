package vai

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rancher/norman/types"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/pkg/clientbase"
	"github.com/stretchr/testify/require"
)

type vaiVersionSettings struct {
	management.SettingOperations
	version string
	err     error
}

func (s vaiVersionSettings) ByID(id string) (*management.Setting, error) {
	if id != "server-version" {
		return nil, errors.New("unexpected setting: " + id)
	}
	return &management.Setting{Value: s.version}, s.err
}

type vaiFeatureTransport func(*http.Request) (*http.Response, error)

func (f vaiFeatureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestVaiAlwaysEnabledVersions(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    bool
	}{
		{"v2.12.0", false},
		{"v2.14.4", false},
		{"v2.15.0", false},
		{"v2.15-head", false},
		{"v2.15.99", false},
		{"v2.16.0-alpha1", true},
		{"v2.16-head", true},
		{"v2.16.0-rc1", true},
		{"v2.16.0", true},
		{"v2.17.0", true},
	} {
		t.Run(tc.version, func(t *testing.T) {
			client := &rancher.Client{Management: &management.Client{
				Setting: vaiVersionSettings{version: tc.version},
			}}
			alwaysEnabled, err := isVaiAlwaysEnabled(client)
			require.NoError(t, err)
			require.Equal(t, tc.want, alwaysEnabled)
			if tc.want {
				enabled, err := isVaiEnabled(client)
				require.NoError(t, err)
				require.True(t, enabled)
				require.NoError(t, ensureVAIState(client, true))
				require.ErrorContains(t, ensureVAIState(client, false), "cannot be disabled")
			}
		})
	}
}

func TestVaiVersionErrors(t *testing.T) {
	for _, settings := range []vaiVersionSettings{
		{version: "unknown"},
		{err: errors.New("version lookup failed")},
	} {
		client := &rancher.Client{Management: &management.Client{Setting: settings}}
		_, err := isVaiEnabled(client)
		require.Error(t, err)
		require.Error(t, ensureVAIState(client, true))
		require.Error(t, ensureVAIState(client, false))
	}
}

func TestVaiLegacyFeatureState(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       string
		statusCode int
		want       bool
	}{
		{"explicit enabled", `{"spec":{"value":true},"status":{"default":false}}`, 200, true},
		{"explicit disabled", `{"spec":{"value":false},"status":{"default":true}}`, 200, false},
		{"default enabled", `{"spec":{"value":null},"status":{"default":true}}`, 200, true},
		{"default disabled", `{"spec":{},"status":{"default":false}}`, 200, false},
		{"missing flag remains an error", `{}`, 404, false},
		{"forbidden remains an error", `{}`, 403, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := vaiFeatureTransport(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/v1/management.cattle.io.features/ui-sql-cache", r.URL.Path)
				return &http.Response{
					StatusCode: tc.statusCode,
					Body:       io.NopCloser(strings.NewReader(tc.body)),
					Header:     make(http.Header),
				}, nil
			})
			client := &rancher.Client{
				Management: &management.Client{Setting: vaiVersionSettings{version: "v2.15.1"}},
				Steve: &steveV1.Client{APIBaseClient: clientbase.APIBaseClient{
					Ops: &clientbase.APIOperations{
						Opts:   &clientbase.ClientOpts{},
						Client: &http.Client{Transport: transport},
						Types: map[string]types.Schema{
							"management.cattle.io.feature": {
								Links: map[string]string{
									"collection": "https://rancher.example/v1/management.cattle.io.features",
								},
								ResourceMethods: []string{http.MethodGet},
							},
						},
					},
				}},
			}
			enabled, err := isVaiEnabled(client)
			if tc.statusCode != http.StatusOK {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, enabled)
		})
	}
}
