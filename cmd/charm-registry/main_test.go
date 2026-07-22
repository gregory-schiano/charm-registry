package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/gschiano/charm-registry/internal/config"
)

func TestNewAPIServerUsesConfiguredWriteTimeout(t *testing.T) {
	cfg := config.Config{
		ListenAddress:           ":8080",
		ServerReadHeaderTimeout: 10 * time.Second,
		ServerReadTimeout:       30 * time.Second,
		ServerWriteTimeout:      30 * time.Second,
		ServerIdleTimeout:       120 * time.Second,
		ServerMaxHeaderBytes:    1 << 20,
	}

	server := newAPIServer(cfg, http.NewServeMux())

	assert.Equal(t, 30*time.Second, server.WriteTimeout)
}

func TestNewOCIServerDefaultsWriteTimeoutToZero(t *testing.T) {
	cfg := config.Config{
		OCIListenAddress:     ":5000",
		OCIReadHeaderTimeout: 10 * time.Second,
		OCIReadTimeout:       0,
		OCIWriteTimeout:      0,
		OCIIdleTimeout:       300 * time.Second,
		ServerMaxHeaderBytes: 1 << 20,
	}

	server := newOCIServer(cfg, http.NewServeMux())

	assert.Zero(t, server.WriteTimeout, "OCI write timeout must default to 0 (disabled) for large blob transfers")
	assert.Zero(t, server.ReadTimeout, "OCI read timeout must default to 0 (disabled) for large blob pushes")
	assert.Equal(t, 10*time.Second, server.ReadHeaderTimeout)
	assert.Equal(t, 300*time.Second, server.IdleTimeout)
}

func TestNewOCIServerUsesExplicitTimeouts(t *testing.T) {
	cfg := config.Config{
		OCIListenAddress:     ":5000",
		OCIReadHeaderTimeout: 15 * time.Second,
		OCIReadTimeout:       5 * time.Minute,
		OCIWriteTimeout:      10 * time.Minute,
		OCIIdleTimeout:       600 * time.Second,
		ServerMaxHeaderBytes: 1 << 20,
	}

	server := newOCIServer(cfg, http.NewServeMux())

	assert.Equal(t, 10*time.Minute, server.WriteTimeout)
	assert.Equal(t, 5*time.Minute, server.ReadTimeout)
	assert.Equal(t, 15*time.Second, server.ReadHeaderTimeout)
	assert.Equal(t, 600*time.Second, server.IdleTimeout)
}

func TestNewOCIServerDoesNotShareAPIWriteTimeout(t *testing.T) {
	// The key fix: API server gets 30s WriteTimeout, OCI server gets 0.
	cfg := config.Config{
		ListenAddress:           ":8080",
		OCIListenAddress:        ":5000",
		ServerReadHeaderTimeout: 10 * time.Second,
		ServerReadTimeout:       30 * time.Second,
		ServerWriteTimeout:      30 * time.Second,
		ServerIdleTimeout:       120 * time.Second,
		OCIReadHeaderTimeout:    10 * time.Second,
		OCIReadTimeout:          0,
		OCIWriteTimeout:         0,
		OCIIdleTimeout:          300 * time.Second,
		ServerMaxHeaderBytes:    1 << 20,
	}

	apiServer := newAPIServer(cfg, http.NewServeMux())
	ociServer := newOCIServer(cfg, http.NewServeMux())

	assert.Equal(t, 30*time.Second, apiServer.WriteTimeout, "API keeps 30s write timeout")
	assert.Zero(t, ociServer.WriteTimeout, "OCI has no write timeout for large blobs")
	assert.Equal(t, 30*time.Second, apiServer.ReadTimeout, "API keeps 30s read timeout")
	assert.Zero(t, ociServer.ReadTimeout, "OCI has no read timeout for large blob pushes")
}
