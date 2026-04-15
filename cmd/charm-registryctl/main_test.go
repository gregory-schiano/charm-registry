package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunSyncList(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/admin/charmhub-sync", r.URL.Path)
		require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rules":[{"name":"demo","track":"latest","status":"ok","created-at":"2026-04-13T00:00:00Z","updated-at":"2026-04-13T00:00:00Z"}]}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--url", server.URL, "--token", "test-token", "sync", "list"}, &stdout, &stderr)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "demo")
	assert.Contains(t, stdout.String(), "latest")
}

func TestRunSyncAdd(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/admin/charmhub-sync", r.URL.Path)
		require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"demo","track":"2.0","status":"pending","created-at":"2026-04-13T00:00:00Z","updated-at":"2026-04-13T00:00:00Z"}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := run(
		context.Background(),
		[]string{"--url", server.URL, "--token", "test-token", "sync", "add", "demo", "--track", "2.0"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "scheduled sync for demo track 2.0")
}

func TestRunSyncRemove(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/v1/admin/charmhub-sync/demo/2.0", r.URL.Path)
		require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := run(
		context.Background(),
		[]string{"--url", server.URL, "--token", "test-token", "sync", "remove", "demo", "--track", "2.0"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "scheduled removal for demo track 2.0")
}

func TestRunSyncRun(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/admin/charmhub-sync/demo/run", r.URL.Path)
		require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := run(
		context.Background(),
		[]string{"--url", server.URL, "--token", "test-token", "sync", "run", "demo"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "triggered sync for demo")
}

func TestRunReportsAPIConflict(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error-list":[{"code":"package-exists","message":"already exists"}]}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := run(
		context.Background(),
		[]string{"--url", server.URL, "--token", "test-token", "sync", "add", "demo", "--track", "latest"},
		&stdout,
		&stderr,
	)
	require.Error(t, err)
	assert.EqualError(t, err, "package-exists: already exists")
}
