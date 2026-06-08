//go:build integration

package integration_test

import (
	"archive/zip"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------- Configuration ----------

// apiURL returns the base URL for the charm-registry API server.
func apiURL() string {
	if u := os.Getenv("ITEST_API_URL"); u != "" {
		return u
	}
	return "http://localhost:18080"
}

// ociRegistryURL returns the base URL for the OCI registry.
func ociRegistryURL() string {
	if u := os.Getenv("ITEST_OCI_URL"); u != "" {
		return u
	}
	return "https://localhost:15000"
}

// ---------- TLS helpers ----------

// loadCertPool loads a TLS certificate pool from the given file path.
func loadCertPool(t *testing.T, certPath string) *x509.CertPool {
	t.Helper()
	certData, err := os.ReadFile(certPath)
	require.NoError(t, err, "reading TLS certificate from %s", certPath)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(certData), "adding certificate to pool")
	return pool
}

// ---------- HTTP request helpers ----------

// doRequest performs an HTTP request against the integration server.
// authHeader is set as the Authorization header when non-empty.
// body is sent as the request body when non-empty.
func doRequest(method, path, body, authHeader string) (*http.Response, error) {
	// Split path from query string so url.JoinPath doesn't encode '?' as '%3F'.
	rawPath, query, _ := strings.Cut(path, "?")
	u, _ := url.JoinPath(apiURL(), rawPath)
	if query != "" {
		u += "?" + query
	}
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, u, bodyReader)
	if err != nil {
		return nil, err
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	if body != "" && method != "GET" {
		req.Header.Set("Content-Type", "application/json")
	}
	return http.DefaultClient.Do(req)
}

// doRequestRaw is like doRequest but lets the caller set all headers and body.
func doRequestRaw(method, path string, headers map[string]string, body io.Reader) (*http.Response, error) {
	u, _ := url.JoinPath(apiURL(), path)
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return http.DefaultClient.Do(req)
}

// ---------- Dev auth helpers ----------

// devAuthHeader returns a Bearer Authorization header for the dev: namespace.
// Format: "Bearer dev:<subject>:<username>"
func devAuthHeader(subject, username string) string {
	return "Bearer dev:" + subject + ":" + username
}

// ---------- Response parsing helpers ----------

// readJSON decodes the response body into a map.
func readJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var result map[string]any
	err := json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err, "failed to decode JSON response")
	return result
}

// assertStatusCode checks that the response has the expected status code (non-fatal).
// Only consumes the body when the status does not match, and resets the body
// so subsequent reads still work.
func assertStatusCode(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	if resp.StatusCode == expected {
		return
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	assert.Equal(t, expected, resp.StatusCode, "unexpected status code; body was: %s", string(bodyBytes))
}

// requireStatusCode checks that the response has the expected status code (fatal on mismatch).
// Only consumes the body on mismatch for the error message; on success the body is untouched.
func requireStatusCode(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	if resp.StatusCode != expected {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.Equal(t, expected, resp.StatusCode, "unexpected status code; body was: %s", string(bodyBytes))
	}
}

// ---------- Token action helpers ----------

// extractRawToken parses the issue-token response and extracts the raw token (identifier)
// from the macaroon structure.
func extractRawToken(t *testing.T, body map[string]any) string {
	t.Helper()
	macaroonStr, ok := body["macaroon"].(string)
	require.True(t, ok, "response should have 'macaroon' string field, got: %v", body)

	var mac map[string]any
	require.NoError(t, json.Unmarshal([]byte(macaroonStr), &mac), "parsing macaroon JSON")
	rawToken, ok := mac["identifier"].(string)
	require.True(t, ok, "macaroon should have 'identifier' field")
	require.NotEmpty(t, rawToken, "raw token should not be empty")
	return rawToken
}

// mustIssueToken issues a store token and returns the raw token string and session ID.
func mustIssueToken(t *testing.T, authHeader string, reqBody string) (rawToken string, sessionID string) {
	t.Helper()
	resp, err := doRequest("POST", "/v1/tokens", reqBody, authHeader)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	result := readJSON(t, resp)
	rawToken = extractRawToken(t, result)

	// Session ID is not in the issue response; list tokens to find it.
	sessionID = mustGetLatestSessionID(t, authHeader)
	return rawToken, sessionID
}

// mustGetLatestSessionID finds the session ID for the most recently issued token.
func mustGetLatestSessionID(t *testing.T, authHeader string) string {
	t.Helper()
	tokens := mustListTokens(t, authHeader)
	require.NotEmpty(t, tokens, "should have at least one token")
	last := tokens[len(tokens)-1]
	sid, ok := last["session-id"].(string)
	require.True(t, ok, "token entry should have 'session-id'")
	return sid
}

// mustListTokens calls GET /v1/tokens and returns the macaroons array.
func mustListTokens(t *testing.T, authHeader string) []map[string]any {
	t.Helper()
	resp, err := doRequest("GET", "/v1/tokens", "", authHeader)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	result := readJSON(t, resp)
	macaroons, ok := result["macaroons"].([]any)
	require.True(t, ok, "response should have 'macaroons' field")
	out := make([]map[string]any, len(macaroons))
	for i, m := range macaroons {
		out[i] = m.(map[string]any)
	}
	return out
}

// mustExchangeToken exchanges a token via Bearer auth and returns the raw exchanged token.
func mustExchangeToken(t *testing.T, rawToken string) string {
	t.Helper()
	resp, err := doRequest("POST", "/v1/tokens/exchange", "", "Bearer "+rawToken)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	result := readJSON(t, resp)
	exchanged, ok := result["macaroon"].(string)
	require.True(t, ok, "response should have 'macaroon' field")
	require.NotEmpty(t, exchanged, "exchanged token should not be empty")
	return exchanged
}

// mustRevokeToken revokes a token by session ID.
func mustRevokeToken(t *testing.T, authHeader string, sessionID string) {
	t.Helper()
	body := fmt.Sprintf(`{"session-id":"%s"}`, sessionID)
	resp, err := doRequest("POST", "/v1/tokens/revoke", body, authHeader)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
}

// mustWhoami calls /v1/whoami and returns the response.
func mustWhoami(t *testing.T, authHeader string) map[string]any {
	t.Helper()
	resp, err := doRequest("GET", "/v1/whoami", "", authHeader)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	return readJSON(t, resp)
}

// ---------- Package action helpers ----------

// mustRegister creates a charm package and asserts success.
// Returns the package ID.
func mustRegister(t *testing.T, name, authHeader string) string {
	t.Helper()
	return mustRegisterWithType(t, name, "charm", false, authHeader)
}

// mustRegisterWithType creates a package with the specified type and privacy.
func mustRegisterWithType(t *testing.T, name, pkgType string, private bool, authHeader string) string {
	t.Helper()
	body := fmt.Sprintf(`{"name":"%s","type":"%s"}`, name, pkgType)
	if private {
		body = fmt.Sprintf(`{"name":"%s","type":"%s","private":true}`, name, pkgType)
	}
	resp, err := doRequest("POST", "/v1/charm", body, authHeader)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusCreated)
	result := readJSON(t, resp)
	id, ok := result["id"].(string)
	require.True(t, ok, "response should have string 'id' field")
	return id
}

// ---------- Upload helpers ----------

// uploadMultipart sends a multipart form upload to the given path.
func uploadMultipart(t *testing.T, path, fieldName, filename string, data []byte, authHeader string) map[string]any {
	t.Helper()
	u, _ := url.JoinPath(apiURL(), path)

	reqBody := &bytes.Buffer{}
	mpw := multipart.NewWriter(reqBody)
	part, err := mpw.CreateFormFile(fieldName, filename)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, mpw.Close())

	req, err := http.NewRequest("POST", u, reqBody)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mpw.FormDataContentType())
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return readJSON(t, resp)
}

// mustUploadCharm pushes a charm archive via /unscanned-upload/ and returns the upload-id.
func mustUploadCharm(t *testing.T, archive []byte, filename string, authHeader string) string {
	t.Helper()
	result := uploadMultipart(t, "/unscanned-upload/", "binary", filename, archive, authHeader)
	uploadID, ok := result["upload_id"].(string)
	require.True(t, ok, "response should have 'upload_id' field, got: %v", result)
	return uploadID
}

// mustPushRevision creates a revision from an upload-id.
func mustPushRevision(t *testing.T, charmName, uploadID, authHeader string) {
	t.Helper()
	body := fmt.Sprintf(`{"upload-id":"%s"}`, uploadID)
	resp, err := doRequest("POST", "/v1/charm/"+charmName+"/revisions", body, authHeader)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	// Drain body to ensure connection reuse.
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
}

// mustRelease releases a revision to a channel.
func mustRelease(t *testing.T, charmName string, revision int, channel string, authHeader string) {
	t.Helper()
	body := fmt.Sprintf(`[{"revision":%d,"channel":"%s"}]`, revision, channel)
	resp, err := doRequest("POST", "/v1/charm/"+charmName+"/releases", body, authHeader)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusCreated)
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
}

// ---------- Charm archive helpers ----------

// buildTestCharmArchive creates a minimal valid charm .charm archive.
func buildTestCharmArchive(t *testing.T, name string) []byte {
	t.Helper()
	metadata := fmt.Sprintf(`name: %s
summary: Integration test charm
description: A charm for integration testing
`, name)

	manifest := `bases:
  - name: ubuntu
    channel: "22.04"
    architectures:
      - amd64
`

	return buildZipArchive(t, map[string]string{
		"metadata.yaml": metadata,
		"manifest.yaml": manifest,
	})
}

// buildZipArchive creates a zip file with the given files.
func buildZipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)
	for name, content := range files {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

// ---------- Encoding helpers ----------

// base64URLEncode encodes a string as base64url with padding.
func base64URLEncode(s string) string {
	encoded := new(strings.Builder)
	encoder := base64.NewEncoder(base64.URLEncoding, encoded)
	_, _ = encoder.Write([]byte(s))
	encoder.Close()
	return encoded.String()
}

// readAllBytes reads the response body and returns it as a byte slice.
func readAllBytes(t *testing.T, resp *http.Response) ([]byte, error) {
	t.Helper()
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// getJSON reads an io.Reader and decodes the JSON into a map.
func getJSON(t *testing.T, r io.Reader) map[string]any {
	t.Helper()
	var result map[string]any
	err := json.NewDecoder(r).Decode(&result)
	require.NoError(t, err, "failed to decode JSON response")
	return result
}

// ociTLSClient creates an HTTP client that trusts the OCI TLS certificate.
// Skips the test if the certificate file does not exist.
func ociTLSClient(t *testing.T) *http.Client {
	t.Helper()
	certPath := os.Getenv("ITEST_OCI_CERT_PATH")
	if certPath == "" {
		root := projectRoot()
		certPath = root + "/certs/oci.crt"
	}
	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		t.Skipf("OCI TLS certificate not found at %s; run 'make generate-cert' first", certPath)
	}
	pool := loadCertPool(t, certPath)
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}
}

// projectRoot returns the project root directory, checking ITEST_PROJECT_ROOT first.
func projectRoot() string {
	if root := os.Getenv("ITEST_PROJECT_ROOT"); root != "" {
		return root
	}
	// Walk up from the test file to find go.mod.
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(dir + "/go.mod"); err == nil {
			return dir
		}
		parent := dir + "/.."
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}

// buildTestCharmArchiveWithResources creates a charm archive that declares resources.
func buildTestCharmArchiveWithResources(t *testing.T, name string) []byte {
	t.Helper()
	metadata := fmt.Sprintf(`name: %s
summary: Integration test charm with resources
description: A charm for integration testing
resources:
  config:
    type: file
    filename: config.yaml
    description: Config file
`, name)

	manifest := `bases:
  - name: ubuntu
    channel: "22.04"
    architectures:
      - amd64
`
	return buildZipArchive(t, map[string]string{
		"metadata.yaml":  metadata,
		"manifest.yaml":   manifest,
	})
}

// ---------- Unique name helper ----------

// uniqueName generates a unique charm name using a timestamp suffix to avoid collisions.
func uniqueName(base string) string {
	return fmt.Sprintf("itest-%s-%d", base, time.Now().UnixNano())
}
