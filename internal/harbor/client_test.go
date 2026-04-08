package harbor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
)

func TestSyncPackageCreatesProjectAndRobots(t *testing.T) {
	t.Parallel()

	var (
		projectCreated bool
		robotCreates   int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/projects":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/projects":
			projectCreated = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && r.URL.Path == "/robots":
			robotCreates++
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"id":     robotCreates,
				"name":   "robot$" + payload["name"].(string),
				"secret": "secret-" + payload["name"].(string),
			}))
		case r.Method == http.MethodGet && r.URL.Path == "/robots/1":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"id": 1, "name": "robot$push", "disable": false}))
		case r.Method == http.MethodGet && r.URL.Path == "/robots/2":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"id": 2, "name": "robot$pull", "disable": false}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client, err := New(config.Config{
		PublicRegistryURL:     "https://oci.example.test",
		HarborURL:             server.URL,
		HarborAPIURL:          server.URL,
		HarborAdminUsername:   "admin",
		HarborAdminPassword:   "secret",
		HarborProjectPrefix:   "charm",
		HarborPullRobotPrefix: "pull",
		HarborPushRobotPrefix: "push",
		HarborSecretKey:       "harbor-secret",
	})
	require.NoError(t, err)

	pkg, err := client.SyncPackage(context.Background(), core.Package{ID: "pkg-1", Name: "my-charm"})
	require.NoError(t, err)

	assert.True(t, projectCreated)
	assert.Equal(t, "charm-my-charm", pkg.HarborProject)
	require.NotNil(t, pkg.HarborPushRobot)
	require.NotNil(t, pkg.HarborPullRobot)
	username, password, err := client.Credentials(pkg, false)
	require.NoError(t, err)
	assert.Equal(t, pkg.HarborPushRobot.Username, username)
	assert.Equal(t, "secret-push-pkg-1", password)
	imageRef, err := client.ImageReference(pkg, "workload-image")
	require.NoError(t, err)
	assert.Equal(t, "oci.example.test/charm-my-charm/workload-image", imageRef)
}

func TestSyncPackageReusesHealthyRobots(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/projects":
			_, _ = w.Write([]byte(`[{"name":"charm-my-charm"}]`))
		case "/robots/10":
			_, _ = w.Write([]byte(`{"id":10,"name":"robot$push-pkg-1","disable":false}`))
		case "/robots/11":
			_, _ = w.Write([]byte(`{"id":11,"name":"robot$pull-pkg-1","disable":false}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client, err := New(config.Config{
		PublicRegistryURL:     "https://oci.example.test",
		HarborURL:             server.URL,
		HarborAPIURL:          server.URL,
		HarborAdminUsername:   "admin",
		HarborAdminPassword:   "secret",
		HarborProjectPrefix:   "charm",
		HarborPullRobotPrefix: "pull",
		HarborPushRobotPrefix: "push",
		HarborSecretKey:       "harbor-secret",
	})
	require.NoError(t, err)

	pkg, err := client.SyncPackage(context.Background(), core.Package{
		ID:            "pkg-1",
		Name:          "my-charm",
		HarborProject: "charm-my-charm",
		HarborPushRobot: &core.RobotCredential{
			ID:              10,
			Username:        "robot$push-pkg-1",
			EncryptedSecret: mustEncrypt(t, client, "push-secret"),
		},
		HarborPullRobot: &core.RobotCredential{
			ID:              11,
			Username:        "robot$pull-pkg-1",
			EncryptedSecret: mustEncrypt(t, client, "pull-secret"),
		},
	})
	require.NoError(t, err)

	username, password, err := client.Credentials(pkg, true)
	require.NoError(t, err)
	assert.Equal(t, "robot$pull-pkg-1", username)
	assert.Equal(t, "pull-secret", password)
}

func mustEncrypt(t *testing.T, client *Client, secret string) string {
	t.Helper()
	encrypted, err := client.encrypt(secret)
	require.NoError(t, err)
	return encrypted
}
