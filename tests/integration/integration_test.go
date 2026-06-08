//go:build integration

package integration_test

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	// Service lifecycle is handled externally by the deployment under test.
	// This TestMain just runs endpoint-driven tests normally.
	os.Exit(m.Run())
}

// BOOT-01: Service starts and reports healthy.
func TestBOOT01_ServiceHealthyAndReady(t *testing.T) {
	// GET /healthz → 200 {"status":"ok"}
	resp, err := http.Get(apiURL() + "/healthz")
	require.NoError(t, err, "GET /healthz request failed")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode, "/healthz status code")

	body := getJSON(t, resp.Body)
	assert.Equal(t, "ok", body["status"], "/healthz response status")

	// GET /readyz → 200 {"status":"ready"}
	resp2, err := http.Get(apiURL() + "/readyz")
	require.NoError(t, err, "GET /readyz request failed")
	defer resp2.Body.Close()

	require.Equal(t, http.StatusOK, resp2.StatusCode, "/readyz status code")

	body2 := getJSON(t, resp2.Body)
	assert.Equal(t, "ready", body2["status"], "/readyz response status")
}

// BOOT-02: Root document returns service metadata.
func TestBOOT02_RootDocumentServiceMetadata(t *testing.T) {
	resp, err := http.Get(apiURL() + "/")
	require.NoError(t, err, "GET / request failed")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode, "root document status code")

	body := getJSON(t, resp.Body)
	assert.Equal(t, "private-charm-registry", body["service-name"], "root document service-name")
}

// BOOT-03: OpenAPI spec served.
func TestBOOT03_OpenAPISpecServed(t *testing.T) {
	resp, err := http.Get(apiURL() + "/openapi.yaml")
	require.NoError(t, err, "GET /openapi.yaml request failed")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode, "/openapi.yaml status code")

	ct := resp.Header.Get("Content-Type")
	assert.Equal(t, "application/yaml", ct, "/openapi.yaml Content-Type")

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "reading /openapi.yaml body")
	assert.True(t, strings.Contains(string(raw), "openapi:"), "/openapi.yaml body contains 'openapi:'")
}

// BOOT-04: Docs page rendered.
func TestBOOT04_DocsPageRendered(t *testing.T) {
	resp, err := http.Get(apiURL() + "/docs")
	require.NoError(t, err, "GET /docs request failed")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode, "/docs status code")

	ct := resp.Header.Get("Content-Type")
	assert.True(t, strings.Contains(ct, "text/html"), "/docs Content-Type contains text/html")

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "reading /docs body")
	assert.True(t, strings.Contains(string(raw), "Charm Registry"), "/docs body contains 'Charm Registry'")
}

// BOOT-05: Security headers present on all responses.
func TestBOOT05_SecurityHeadersPresent(t *testing.T) {
	resp, err := http.Get(apiURL() + "/")
	require.NoError(t, err, "GET / request for security headers check failed")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode, "root endpoint status for header check")

	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"),
		"X-Content-Type-Options header")
	assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"),
		"X-Frame-Options header")
	assert.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"),
		"Referrer-Policy header")

	csp := resp.Header.Get("Content-Security-Policy")
	assert.True(t, strings.Contains(csp, "default-src 'none'"),
		"Content-Security-Policy contains default-src 'none'")
}

// BOOT-06: OCI registry responds to catalog.
func TestBOOT06_OCIRegistryCatalog(t *testing.T) {
	client := ociTLSClient(t)

	req, err := http.NewRequest(http.MethodGet, ociRegistryURL()+"/v2/_catalog", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err, "GET /v2/_catalog request to OCI registry failed")
	defer resp.Body.Close()

	// Accept 200 (anonymous access allowed) or 401 (auth required).
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unexpected OCI catalog status: %d", resp.StatusCode)
	}

	if resp.StatusCode == http.StatusOK {
		body := getJSON(t, resp.Body)
		// The catalog should have a "repositories" key (may be empty list).
		assert.Contains(t, body, "repositories", "OCI catalog has 'repositories' key")
	}
}
