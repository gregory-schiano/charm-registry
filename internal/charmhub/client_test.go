package charmhub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gschiano/charm-registry/internal/core"
)

func TestGetChannelAcceptsTimezoneLessTimestamps(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v2/charms/info/postgresql-k8s", r.URL.Path)
		require.Equal(t, "default-release,result", r.URL.Query().Get("fields"))
		require.Equal(t, "14/stable", r.URL.Query().Get("channel"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "postgresql-k8s",
			"name": "postgresql-k8s",
			"type": "charm",
			"result": {
				"description": "PostgreSQL",
				"links": {},
				"media": [],
				"summary": "database",
				"title": "PostgreSQL",
				"website": "https://example.com"
			},
			"default-release": {
				"channel": {
					"name": "14/stable",
					"released-at": "2026-03-21T16:25:13.716831",
					"risk": "stable",
					"track": "14"
				},
				"resources": [
					{
						"created-at": "2026-03-21T16:25:13.716831",
						"description": "OCI image",
						"download": {"url": "https://example.com/resource"},
						"filename": "postgresql.rock",
						"name": "postgresql-image",
						"revision": 3,
						"type": "oci-image"
					}
				],
				"revision": {
					"actions-yaml": "",
					"attributes": {},
					"bases": [],
					"bundle-yaml": "",
					"config-yaml": "",
					"created-at": "2026-03-21T16:25:13.716831",
					"download": {"url": "https://example.com/charm"},
					"metadata-yaml": "name: postgresql-k8s",
					"readme-md": "",
					"relations": {},
					"revision": 42,
					"subordinate": false,
					"version": "14.0"
				}
			}
		}`))
	}))
	defer server.Close()

	client := New(server.URL)
	channel, err := client.GetChannel(context.Background(), "postgresql-k8s", "14/stable")
	require.NoError(t, err)

	expected := time.Date(2026, 3, 21, 16, 25, 13, 716831000, time.UTC)
	require.Equal(t, expected, channel.DefaultRelease.Channel.ReleasedAt)
	require.Equal(t, expected, channel.DefaultRelease.Resources[0].CreatedAt)
	require.Equal(t, expected, channel.DefaultRelease.Revision.CreatedAt)
}

func TestGetInfoParsesChannelMapVariants(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v2/charms/info/postgresql-k8s", r.URL.Path)
		require.Equal(t, "channel-map,result", r.URL.Query().Get("fields"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "postgresql-k8s-id",
			"name": "postgresql-k8s",
			"type": "charm",
			"result": {"summary": "database"},
			"channel-map": [
				{
					"channel": {
						"base": {"architecture": "amd64", "channel": "24.04", "name": "ubuntu"},
						"name": "14/stable",
						"released-at": "2026-03-21T16:25:13.716831",
						"risk": "stable",
						"track": "14"
					},
					"revision": {"created-at": "2026-03-21T16:25:13.716831", "revision": 42}
				},
				{
					"channel": {
						"base": {"architecture": "arm64", "channel": "24.04", "name": "ubuntu"},
						"name": "14/stable",
						"released-at": "2026-03-21T16:25:13.716831",
						"risk": "stable",
						"track": "14"
					},
					"revision": {"created-at": "2026-03-21T16:25:13.716831", "revision": 43}
				}
			]
		}`))
	}))
	defer server.Close()

	client := New(server.URL)
	info, err := client.GetInfo(context.Background(), "postgresql-k8s")
	require.NoError(t, err)

	require.Len(t, info.ChannelMap, 2)
	require.NotNil(t, info.ChannelMap[0].Channel.Base)
	require.Equal(t, "amd64", info.ChannelMap[0].Channel.Base.Architecture)
	require.Equal(t, 43, info.ChannelMap[1].Revision.Revision)
}

func TestGetInfoRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.Repeat("x", 6)))
	}))
	defer server.Close()

	client := NewWithLimits(server.URL, 5, 1024)
	_, err := client.GetInfo(context.Background(), "postgresql-k8s")

	require.ErrorContains(t, err, "Charmhub API response exceeds 5 bytes")
}

func TestDownloadRejectsOversizedArtifact(t *testing.T) {
	// Use a TLS server so the SSRF host-allowlist and HTTPS checks pass.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 6)))
	}))
	defer server.Close()

	client := NewWithLimits(server.URL, 1024, 5)
	// Wire the test server's CA so the HTTPS client trusts it.
	client.http.Transport = server.Client().Transport
	_, err := client.Download(context.Background(), server.URL+"/artifact.charm")

	require.ErrorContains(t, err, "Charmhub artifact exceeds 5 bytes")
}

func TestDownloadToStreamsArtifactWithoutBuffering(t *testing.T) {
	// Use a TLS server so the SSRF host-allowlist and HTTPS checks pass.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("streamed artifact"))
	}))
	defer server.Close()

	client := NewWithLimits(server.URL, 1024, 64)
	// Wire the test server's CA so the HTTPS client trusts it.
	client.http.Transport = server.Client().Transport

	var dst strings.Builder
	size, err := client.DownloadTo(context.Background(), server.URL+"/artifact.charm", &dst)

	require.NoError(t, err)
	require.EqualValues(t, len("streamed artifact"), size)
	require.Equal(t, "streamed artifact", dst.String())
}

func TestDownloadToRejectsOversizedArtifact(t *testing.T) {
	// Use a TLS server so the SSRF host-allowlist and HTTPS checks pass.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 6)))
	}))
	defer server.Close()

	client := NewWithLimits(server.URL, 1024, 5)
	// Wire the test server's CA so the HTTPS client trusts it.
	client.http.Transport = server.Client().Transport

	var dst strings.Builder
	_, err := client.DownloadTo(context.Background(), server.URL+"/artifact.charm", &dst)

	require.ErrorContains(t, err, "Charmhub artifact exceeds 5 bytes")
}

func TestRefreshChannelResolvesBaseVariant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v2/charms/refresh", r.URL.Path)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		actions := body["actions"].([]any)
		action := actions[0].(map[string]any)
		require.Equal(t, "install", action["action"])
		require.Equal(t, "postgresql-k8s", action["name"])
		require.Equal(t, "14/stable", action["channel"])
		require.Equal(t, map[string]any{
			"architecture": "amd64",
			"name":         "ubuntu",
			"channel":      "24.04",
		}, action["base"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [{
				"effective-channel": "14/stable",
				"id": "postgresql-k8s-id",
				"instance-key": "charmhub-sync",
				"name": "postgresql-k8s",
				"released-at": "2026-03-21T16:25:13.716831",
				"result": "refresh",
				"charm": {
					"bases": [{"architecture": "amd64", "channel": "24.04", "name": "ubuntu"}],
					"created-at": "2026-03-21T16:25:13.716831",
					"download": {"url": "https://example.com/charm"},
					"id": "postgresql-k8s-id",
					"metadata-yaml": "name: postgresql-k8s",
					"name": "postgresql-k8s",
					"resources": [{"name": "image", "revision": 7, "type": "oci-image", "download": {"url": "https://example.com/image"}}],
					"revision": 42,
					"type": "charm",
					"version": "42"
				}
			}]
		}`))
	}))
	defer server.Close()

	client := New(server.URL)
	channel, err := client.RefreshChannel(
		context.Background(),
		"postgresql-k8s",
		"14/stable",
		core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"},
	)
	require.NoError(t, err)

	require.Equal(t, "postgresql-k8s-id", channel.ID)
	require.Equal(t, 42, channel.DefaultRelease.Revision.Revision)
	require.Len(t, channel.DefaultRelease.Resources, 1)
	require.Equal(t, "image", channel.DefaultRelease.Resources[0].Name)
}

func TestGetChannelAcceptsRFC3339Timestamps(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "postgresql-k8s",
			"name": "postgresql-k8s",
			"type": "charm",
			"result": {
				"description": "PostgreSQL",
				"links": {},
				"media": [],
				"summary": "database",
				"title": "PostgreSQL",
				"website": "https://example.com"
			},
			"default-release": {
				"channel": {
					"name": "14/stable",
					"released-at": "2026-03-21T16:25:13.716831Z",
					"risk": "stable",
					"track": "14"
				},
				"resources": [],
				"revision": {
					"actions-yaml": "",
					"attributes": {},
					"bases": [],
					"bundle-yaml": "",
					"config-yaml": "",
					"created-at": "2026-03-21T16:25:13.716831Z",
					"download": {"url": "https://example.com/charm"},
					"metadata-yaml": "name: postgresql-k8s",
					"readme-md": "",
					"relations": {},
					"revision": 42,
					"subordinate": false,
					"version": "14.0"
				}
			}
		}`))
	}))
	defer server.Close()

	client := New(server.URL)
	channel, err := client.GetChannel(context.Background(), "postgresql-k8s", "14/stable")
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 3, 21, 16, 25, 13, 716831000, time.UTC), channel.DefaultRelease.Channel.ReleasedAt)
}

func TestAPIErrorMessage(t *testing.T) {
	t.Parallel()
	err := &APIError{StatusCode: 404, Body: "not found"}
	assert.Contains(t, err.Error(), "404")
	assert.Contains(t, err.Error(), "not found")
}

func TestDefaultReleasePresent(t *testing.T) {
	t.Parallel()
	// Empty → not present
	dr := DefaultRelease{}
	assert.False(t, dr.Present())

	// With channel name but no revision → not present
	dr.Channel.Name = "latest/stable"
	assert.False(t, dr.Present())

	// Both set → present
	dr.Revision.Revision = 5
	assert.True(t, dr.Present())
}
