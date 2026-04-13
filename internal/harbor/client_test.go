package harbor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
)

func TestSyncPackageCreatesProjectAndRobots(t *testing.T) {
	t.Parallel()

	// Act + Assert

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

	// Act
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

	// Assert
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

func TestErrorAsMatchesWrappedHarborAPIError(t *testing.T) {
	t.Parallel()

	// Act
	wrapped := fmt.Errorf("wrapped: %w", &harborAPIError{StatusCode: http.StatusConflict, Body: "conflict"})
	var target *harborAPIError

	// Assert
	require.True(t, errorAs(wrapped, &target))
	require.NotNil(t, target)
	assert.Equal(t, http.StatusConflict, target.StatusCode)

}

func TestDoJSONUsesRequestTimeout(t *testing.T) {
	t.Parallel()

	// Act
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	// Assert
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
	client.requestTimeout = 20 * time.Millisecond

	start := time.Now()
	err = client.doJSON(context.Background(), http.MethodGet, "/projects", nil, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 500*time.Millisecond)

}

func TestNewReturnsContextForMissingHarborCAFile(t *testing.T) {
	t.Parallel()

	// Act
	_, err := New(config.Config{
		PublicRegistryURL:     "https://oci.example.test",
		HarborURL:             "https://harbor.example.test",
		HarborAPIURL:          "https://harbor.example.test/api/v2.0",
		HarborAdminUsername:   "admin",
		HarborAdminPassword:   "secret",
		HarborProjectPrefix:   "charm",
		HarborPullRobotPrefix: "pull",
		HarborPushRobotPrefix: "push",
		HarborSecretKey:       "harbor-secret",
		HarborCAFile:          filepath.Join(t.TempDir(), "missing.pem"),
	})

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot read Harbor CA file")

}

func TestNewReturnsContextForInvalidHarborCAFile(t *testing.T) {
	t.Parallel()

	// Arrange
	tempDir := t.TempDir()
	caFile := filepath.Join(tempDir, "invalid.pem")
	require.NoError(t, os.WriteFile(caFile, []byte("not-a-certificate"), 0o600))

	// Act
	_, err := New(config.Config{
		PublicRegistryURL:     "https://oci.example.test",
		HarborURL:             "https://harbor.example.test",
		HarborAPIURL:          "https://harbor.example.test/api/v2.0",
		HarborAdminUsername:   "admin",
		HarborAdminPassword:   "secret",
		HarborProjectPrefix:   "charm",
		HarborPullRobotPrefix: "pull",
		HarborPushRobotPrefix: "push",
		HarborSecretKey:       "harbor-secret",
		HarborCAFile:          caFile,
	})

	// Assert
	require.Error(t, err)
	assert.EqualError(t, err, "cannot append Harbor CA file: no certificates found")

}
