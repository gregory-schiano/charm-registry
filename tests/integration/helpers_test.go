//go:build integration

package integration_test

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baseURL returns the charm-registry API base URL from the environment,
// defaulting to http://localhost:8080.
func baseURL() string {
	if u := os.Getenv("CHARM_REGISTRY_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://localhost:8080"
}

// devAuthEnabled returns whether insecure dev auth is enabled.
func devAuthEnabled() bool {
	return os.Getenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH") == "true"
}

// devAuthHeader returns an Authorization header value that triggers the
// insecure dev auth path. The format is: Bearer dev:<subject>:<displayName>.
func devAuthHeader(subject, displayName string) string {
	return "Bearer dev:" + subject + ":" + displayName
}

// uniqueName returns a unique package name suitable for test isolation.
func uniqueName(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%x-%d", prefix, b, time.Now().UnixMilli()%10000)
}

// doRequest performs an HTTP request against the charm-registry API.
// method is the HTTP method, path is appended to baseURL, body is the request
// body (may be empty), and auth is the Authorization header value (may be empty).
func doRequest(method, path, body, auth string) (*http.Response, error) {
	url := baseURL() + path
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return http.DefaultClient.Do(req)
}

// assertStatusCode asserts that the response status code matches expected.
func assertStatusCode(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	assert.Equal(t, expected, resp.StatusCode, "expected status %d, got %d", expected, resp.StatusCode)
}

// requireStatusCode requires that the response status code matches expected.
func requireStatusCode(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	require.Equal(t, expected, resp.StatusCode, "expected status %d, got %d", expected, resp.StatusCode)
}

// readJSON reads and JSON-decodes the response body into a map.
func readJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var result map[string]any
	err := json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err, "response body should be valid JSON")
	return result
}

// mustRegister registers a charm package and requires success.
func mustRegister(t *testing.T, name, auth string) {
	t.Helper()
	body := fmt.Sprintf(`{"name":"%s","type":"charm"}`, name)
	resp, err := doRequest("POST", "/v1/charm", body, auth)
	require.NoError(t, err)
	defer resp.Body.Close()
	requireStatusCode(t, resp, http.StatusCreated)
}

// buildTestCharmArchiveWithResources creates a charm ZIP archive with resource declarations.
func buildTestCharmArchiveWithResources(t *testing.T, name string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	metadata := fmt.Sprintf(
		"name: %s\nsummary: test charm with resources\ndescription: integration test charm\nresources:\n  test-image:\n    type: oci-image\n    description: test OCI image\n",
		name,
	)
	mf, err := w.Create("metadata.yaml")
	require.NoError(t, err)
	_, err = mf.Write([]byte(metadata))
	require.NoError(t, err)

	manf, err := w.Create("manifest.yaml")
	require.NoError(t, err)
	_, err = manf.Write([]byte("bases:\n  - name: ubuntu\n    channel: '22.04'\n"))
	require.NoError(t, err)

	require.NoError(t, w.Close())
	return &buf
}

// uploadMultipart uploads a file via multipart form POST and returns the JSON response.
func uploadMultipart(t *testing.T, path, fieldName, filename string, content *bytes.Buffer, auth string) map[string]any {
	t.Helper()
	var body bytes.Buffer
	mpw := multipart.NewWriter(&body)
	part, err := mpw.CreateFormFile(fieldName, filename)
	require.NoError(t, err)
	_, err = io.Copy(part, content)
	require.NoError(t, err)
	require.NoError(t, mpw.Close())

	url := baseURL() + path
	req, err := http.NewRequest("POST", url, &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mpw.FormDataContentType())
	req.Header.Set("Authorization", auth)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	requireStatusCode(t, resp, http.StatusOK)
	return readJSON(t, resp)
}

// buildTestCharmArchive creates a minimal valid charm ZIP archive in memory.
func buildTestCharmArchive(t *testing.T, name string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// metadata.yaml is required by the charm format.
	metadata := fmt.Sprintf(
		"name: %s\nsummary: test charm\ndescription: integration test charm\n",
		name,
	)
	mf, err := w.Create("metadata.yaml")
	require.NoError(t, err)
	_, err = mf.Write([]byte(metadata))
	require.NoError(t, err)

	// manifest.yaml
	manf, err := w.Create("manifest.yaml")
	require.NoError(t, err)
	_, err = manf.Write([]byte("bases:\n  - name: ubuntu\n    channel: '22.04'\n"))
	require.NoError(t, err)

	require.NoError(t, w.Close())
	return &buf
}

// mustUploadCharm uploads a charm archive and returns the upload-id.
func mustUploadCharm(t *testing.T, archive *bytes.Buffer, filename, auth string) string {
	t.Helper()
	var body bytes.Buffer
	mpw := multipart.NewWriter(&body)
	part, err := mpw.CreateFormFile("charm", filename)
	require.NoError(t, err)
	_, err = io.Copy(part, archive)
	require.NoError(t, err)
	require.NoError(t, mpw.Close())

	url := baseURL() + "/v1/charm/upload"
	req, err := http.NewRequest("POST", url, &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mpw.FormDataContentType())
	req.Header.Set("Authorization", auth)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	requireStatusCode(t, resp, http.StatusOK)

	result := readJSON(t, resp)
	uploadID, ok := result["upload-id"].(string)
	require.True(t, ok, "response should contain upload-id string, got %v", result)
	return uploadID
}

// mustPushRevision pushes a revision from an upload-id and returns the revision URL.
func mustPushRevision(t *testing.T, charmName, uploadID, auth string) string {
	t.Helper()
	path := fmt.Sprintf("/v1/charm/%s/revisions?upload-id=%s", charmName, uploadID)
	resp, err := doRequest("POST", path, "", auth)
	require.NoError(t, err)
	defer resp.Body.Close()
	requireStatusCode(t, resp, http.StatusOK)

	result := readJSON(t, resp)
	revURL, ok := result["revision-url"].(string)
	require.True(t, ok, "response should contain revision-url string, got %v", result)
	return revURL
}

// mustRelease releases a revision to a channel and requires success.
func mustRelease(t *testing.T, charmName string, revision int, channel, auth string) {
	t.Helper()
	body := fmt.Sprintf(`{"revision":%d,"channel":"%s"}`, revision, channel)
	path := fmt.Sprintf("/v1/charm/%s/release", charmName)
	resp, err := doRequest("POST", path, body, auth)
	require.NoError(t, err)
	defer resp.Body.Close()
	requireStatusCode(t, resp, http.StatusOK)
}

// mustCreateTrack creates a custom track and requires success.
func mustCreateTrack(t *testing.T, charmName, trackName, auth string) {
	t.Helper()
	body := fmt.Sprintf(`{"tracks":[{"track":"%s"}]}`, trackName)
	path := fmt.Sprintf("/v1/charm/%s/tracks", charmName)
	resp, err := doRequest("POST", path, body, auth)
	require.NoError(t, err)
	defer resp.Body.Close()
	requireStatusCode(t, resp, http.StatusOK)
}

// mustDownload downloads a charm archive and requires success, returning the response body.
func mustDownload(t *testing.T, charmName string, revision int, auth string) []byte {
	t.Helper()
	path := fmt.Sprintf("/v1/charm/%s/download/%d", charmName, revision)
	resp, err := doRequest("GET", path, "", auth)
	require.NoError(t, err)
	defer resp.Body.Close()
	requireStatusCode(t, resp, http.StatusOK)
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return data
}

// dumpResponse logs the full HTTP response for debugging.
func dumpResponse(t *testing.T, resp *http.Response) {
	t.Helper()
	d, _ := httputil.DumpResponse(resp, true)
	t.Logf("Response:\n%s", string(d))
}
