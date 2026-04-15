package charmhub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
