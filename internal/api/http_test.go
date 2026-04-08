package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gschiano/charm-registry/internal/auth"
	"github.com/gschiano/charm-registry/internal/blob"
	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
	"github.com/gschiano/charm-registry/internal/service"
)

var testCfg = config.Config{
	PublicAPIURL:          "https://registry.test",
	PublicStorageURL:      "https://storage.test",
	PublicRegistryURL:     "https://oci.test",
	EnableInsecureDevAuth: true,
	HarborURL:             "https://harbor.test",
	HarborAPIURL:          "https://harbor.test/api/v2.0",
	HarborAdminUsername:   "admin",
	HarborAdminPassword:   "admin-secret",
	HarborProjectPrefix:   "charm",
	HarborPullRobotPrefix: "pull",
	HarborPushRobotPrefix: "push",
	HarborSecretKey:       "test-harbor-secret",
	MaxJSONBodyBytes:      1 << 20,
	MaxUploadBytes:        64 << 20,
}

func TestRootIncludesSecurityHeaders(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", recorder.Header().Get("X-Frame-Options"))
	assert.Equal(t, "no-referrer", recorder.Header().Get("Referrer-Policy"))
	assert.Contains(t, recorder.Header().Get("Content-Security-Policy"), "default-src 'none'")
}

func TestRootReturnsJSON(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	resp := doRequest(t, handler, "GET", "/", nil, "")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	assert.Equal(t, "private-charm-registry", body["service-name"])
}

func TestHealthz(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	resp := doRequest(t, handler, "GET", "/healthz", nil, "")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	assert.Equal(t, "ok", body["status"])
}

func TestReadyz(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	resp := doRequest(t, handler, "GET", "/readyz", nil, "")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	assert.Equal(t, "ready", body["status"])
}

func TestOpenAPIEndpoint(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	resp := doRequest(t, handler, "GET", "/openapi.yaml", nil, "")

	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, "application/yaml", resp.Header().Get("Content-Type"))
	assert.Contains(t, resp.Body.String(), "openapi")
}

func TestDocsEndpoint(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	resp := doRequest(t, handler, "GET", "/docs", nil, "")

	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, resp.Body.String(), "Charm Registry")
}

func TestGetTokensUnauthenticated(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	resp := doRequest(t, handler, "GET", "/v1/tokens", nil, "")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	assert.Equal(t, "oidc-login-required", body["macaroon"])
}

func TestIssueTokenRejectsOversizedJSONBody(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, config.Config{
		EnableInsecureDevAuth: true,
		MaxJSONBodyBytes:      8,
		MaxUploadBytes:        1024,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/tokens", strings.NewReader(`{"description":"this is too large"}`))
	req.Header.Set("Authorization", "Bearer dev:alice:alice")
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "request-too-large")
}

func TestIssueAndListTokens(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	// Act: issue token
	resp := doRequest(t, handler, "POST", "/v1/tokens", map[string]any{
		"description": "test token",
		"ttl":         3600,
	}, "Bearer dev:alice:alice")
	require.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	assert.NotEmpty(t, body["macaroon"])

	// Act: list tokens
	resp = doRequest(t, handler, "GET", "/v1/tokens", nil, "Bearer dev:alice:alice")
	require.Equal(t, http.StatusOK, resp.Code)
	body = decodeJSON(t, resp)
	macaroons := body["macaroons"].([]any)
	assert.GreaterOrEqual(t, len(macaroons), 1)
}

func TestIssueTokenRateLimitedPerAccount(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	for range 5 {
		resp := doRequest(t, handler, "POST", "/v1/tokens", map[string]any{
			"description": "test token",
			"ttl":         3600,
		}, "Bearer dev:alice:alice")
		require.Equal(t, http.StatusOK, resp.Code)
	}

	resp := doRequest(t, handler, "POST", "/v1/tokens", map[string]any{
		"description": "too many",
		"ttl":         3600,
	}, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusTooManyRequests, resp.Code)
	assert.Contains(t, resp.Body.String(), "rate-limit-exceeded")
}

func TestExchangeToken(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "POST", "/v1/tokens/exchange", nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	assert.NotEmpty(t, body["macaroon"])
}

func TestOfflineExchangeToken(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "POST", "/v1/tokens/offline/exchange", nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	assert.NotEmpty(t, body["macaroon"])
}

func TestDashboardExchange(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "POST", "/v1/tokens/dashboard/exchange",
		map[string]any{"client-description": "web UI"}, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	assert.NotEmpty(t, body["macaroon"])
}

func TestDashboardExchangeEmptyBody(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "POST", "/v1/tokens/dashboard/exchange", nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusOK, resp.Code)
}

func TestRevokeToken(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	// Issue a token first
	resp := doRequest(t, handler, "POST", "/v1/tokens", map[string]any{}, "Bearer dev:alice:alice")
	require.Equal(t, http.StatusOK, resp.Code)

	// List to get the session-id
	resp = doRequest(t, handler, "GET", "/v1/tokens?include-inactive=true", nil, "Bearer dev:alice:alice")
	require.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	macaroons := body["macaroons"].([]any)
	require.GreaterOrEqual(t, len(macaroons), 1)
	sessionID := macaroons[0].(map[string]any)["session-id"].(string)

	// Revoke it
	resp = doRequest(t, handler, "POST", "/v1/tokens/revoke",
		map[string]any{"session-id": sessionID}, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusOK, resp.Code)
}

func TestWhoAmI(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "GET", "/v1/whoami", nil, "Bearer dev:alice:Alice")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	assert.Equal(t, "Alice", body["username"])
}

func TestTokenWhoAmI(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	// Issue a store token first
	resp := doRequest(t, handler, "POST", "/v1/tokens/exchange", nil, "Bearer dev:alice:Alice")
	require.Equal(t, http.StatusOK, resp.Code)
	macaroon := decodeJSON(t, resp)["macaroon"].(string)

	resp = doRequest(t, handler, "GET", "/v1/tokens/whoami", nil, "Bearer "+macaroon)

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	account := body["account"].(map[string]any)
	assert.Equal(t, "Alice", account["username"])
}

func TestRegisterAndGetPackage(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	// Act: register
	resp := doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "test-charm", "type": "charm"}, "Bearer dev:alice:alice")
	require.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	assert.NotEmpty(t, body["id"])

	// Act: get
	resp = doRequest(t, handler, "GET", "/v1/charm/test-charm", nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusOK, resp.Code)
	body = decodeJSON(t, resp)
	metadata := body["metadata"].(map[string]any)
	assert.Equal(t, "test-charm", metadata["name"])
}

func TestListPackages(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "list-charm-1"}, "Bearer dev:alice:alice")
	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "list-charm-2"}, "Bearer dev:alice:alice")

	resp := doRequest(t, handler, "GET", "/v1/charm", nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	results := body["results"].([]any)
	assert.Len(t, results, 2)
}

func TestPatchPackage(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "patch-charm"}, "Bearer dev:alice:alice")

	resp := doRequest(t, handler, "PATCH", "/v1/charm/patch-charm",
		map[string]any{"title": "Patched Title", "summary": "New summary"}, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	metadata := body["metadata"].(map[string]any)
	assert.Equal(t, "Patched Title", metadata["title"])
	assert.Equal(t, "New summary", metadata["summary"])
}

func TestDeletePackage(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "doomed-charm"}, "Bearer dev:alice:alice")

	resp := doRequest(t, handler, "DELETE", "/v1/charm/doomed-charm", nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	assert.NotEmpty(t, body["package-id"])

	// Assert: gone
	resp = doRequest(t, handler, "GET", "/v1/charm/doomed-charm", nil, "Bearer dev:alice:alice")
	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestGetPackageNotFoundReturns404(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "GET", "/v1/charm/nonexistent", nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestCreateTracks(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "track-charm"}, "Bearer dev:alice:alice")

	resp := doJSONRequest(t, handler, "POST", "/v1/charm/track-charm/tracks",
		`[{"name":"2.0"},{"name":"3.0"}]`, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	assert.Equal(t, float64(2), body["num-tracks-created"])
}

func TestFindEndpoint(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "GET", "/v2/charms/find?q=nonexistent", nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	results := body["results"].([]any)
	assert.Empty(t, results)
}

func TestFullPublishAndRefreshViaHTTP(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	authHeader := "Bearer dev:publisher:publisher"

	// Register
	resp := doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "http-charm"}, authHeader)
	require.Equal(t, http.StatusOK, resp.Code)

	// Upload charm archive
	resp = doMultipartUpload(t, handler, buildTestCharmArchive(t, "http-charm"), "http-charm.charm", authHeader)
	require.Equal(t, http.StatusOK, resp.Code)
	uploadBody := decodeJSON(t, resp)
	require.Equal(t, true, uploadBody["successful"])
	uploadID := uploadBody["upload_id"].(string)

	// Push revision
	resp = doRequest(t, handler, "POST", "/v1/charm/http-charm/revisions",
		map[string]any{"upload-id": uploadID}, authHeader)
	require.Equal(t, http.StatusOK, resp.Code)

	// List revisions
	resp = doRequest(t, handler, "GET", "/v1/charm/http-charm/revisions", nil, authHeader)
	require.Equal(t, http.StatusOK, resp.Code)
	revBody := decodeJSON(t, resp)
	revisions := revBody["revisions"].([]any)
	assert.Len(t, revisions, 1)

	// Review upload
	resp = doRequest(t, handler, "GET",
		"/v1/charm/http-charm/revisions/review?upload-id="+uploadID, nil, authHeader)
	assert.Equal(t, http.StatusOK, resp.Code)

	// List resources
	resp = doRequest(t, handler, "GET", "/v1/charm/http-charm/resources", nil, authHeader)
	assert.Equal(t, http.StatusOK, resp.Code)

	// Release
	resp = doJSONRequest(t, handler, "POST", "/v1/charm/http-charm/releases",
		`[{"channel":"latest/stable","revision":1}]`, authHeader)
	require.Equal(t, http.StatusOK, resp.Code)

	// List releases
	resp = doRequest(t, handler, "GET", "/v1/charm/http-charm/releases", nil, authHeader)
	assert.Equal(t, http.StatusOK, resp.Code)

	// Info
	resp = doRequest(t, handler, "GET", "/v2/charms/info/http-charm", nil, authHeader)
	assert.Equal(t, http.StatusOK, resp.Code)
	infoBody := decodeJSON(t, resp)
	assert.Equal(t, "http-charm", infoBody["name"])

	// Refresh
	resp = doRequest(t, handler, "POST", "/v2/charms/refresh", map[string]any{
		"context": []any{},
		"actions": []any{map[string]any{
			"action":       "refresh",
			"instance-key": "unit/0",
			"name":         "http-charm",
			"channel":      "latest/stable",
		}},
	}, authHeader)
	assert.Equal(t, http.StatusOK, resp.Code)
}

func TestCharmDownloadInvalidFilename(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	tests := []struct {
		name     string
		filename string
	}{
		{"no underscore", "invalidfilename.charm"},
		{"too many parts", "a_b_c.charm"},
		{"non-numeric revision", "pkg_abc.charm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := doRequest(t, handler, "GET", "/api/v1/charms/download/"+tt.filename,
				nil, "Bearer dev:alice:alice")
			assert.Equal(t, http.StatusBadRequest, resp.Code)
		})
	}
}

func TestResourceDownloadInvalidFilename(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	tests := []struct {
		name     string
		filename string
	}{
		{"no dot", "charm_nodot"},
		{"no underscore in resource part", "charm_pkg.resourceonly"},
		{"non-numeric revision", "charm_pkg.res_abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := doRequest(t, handler, "GET", "/api/v1/resources/download/"+tt.filename,
				nil, "Bearer dev:alice:alice")
			assert.Equal(t, http.StatusBadRequest, resp.Code)
		})
	}
}

func TestListRevisionsInvalidRevisionParam(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "rev-charm"}, "Bearer dev:alice:alice")

	resp := doRequest(t, handler, "GET", "/v1/charm/rev-charm/revisions?revision=abc", nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestMultipleJSONDocumentsRejected(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	req := httptest.NewRequest("POST", "/v1/charm",
		strings.NewReader(`{"name":"a"}{"name":"b"}`))
	req.Header.Set("Authorization", "Bearer dev:alice:alice")
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestUnscannedUploadMissingFile(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	req := httptest.NewRequest("POST", "/unscanned-upload/", strings.NewReader("not multipart"))
	req.Header.Set("Authorization", "Bearer dev:alice:alice")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestUnscannedUploadSuccess(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	resp := doMultipartUpload(t, handler, []byte("archive data"), "test.charm", "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	assert.Equal(t, true, body["successful"])
	assert.NotEmpty(t, body["upload_id"])
}

func TestListPackagesWithCollaborationsParam(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "collab-charm"}, "Bearer dev:alice:alice")

	resp := doRequest(t, handler, "GET", "/v1/charm?include-collaborations=true", nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusOK, resp.Code)
}

func TestResourceEndpoints(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	authHeader := "Bearer dev:alice:alice"

	// Setup: register, upload (with resources declared), push revision, push resource
	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "res-charm"}, authHeader)
	resp := doMultipartUpload(t, handler, buildTestCharmArchiveWithResources(t, "res-charm"), "res-charm.charm", authHeader)
	require.Equal(t, http.StatusOK, resp.Code)
	uploadID := decodeJSON(t, resp)["upload_id"].(string)
	doRequest(t, handler, "POST", "/v1/charm/res-charm/revisions",
		map[string]any{"upload-id": uploadID}, authHeader)

	// Upload a resource file
	resp = doMultipartUpload(t, handler, []byte("config: true\n"), "config.yaml", authHeader)
	require.Equal(t, http.StatusOK, resp.Code)
	resUploadID := decodeJSON(t, resp)["upload_id"].(string)

	// Push resource
	resp = doRequest(t, handler, "POST", "/v1/charm/res-charm/resources/config/revisions",
		map[string]any{"upload-id": resUploadID, "type": "file"}, authHeader)
	assert.Equal(t, http.StatusOK, resp.Code)

	// List resource revisions
	resp = doRequest(t, handler, "GET", "/v1/charm/res-charm/resources/config/revisions", nil, authHeader)
	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	revisions := body["revisions"].([]any)
	assert.Len(t, revisions, 1)

	// Update resource revision metadata
	resp = doRequest(t, handler, "PATCH", "/v1/charm/res-charm/resources/config/revisions",
		map[string]any{"resource-revision-updates": []any{
			map[string]any{"revision": 1, "bases": []any{
				map[string]any{"name": "ubuntu", "channel": "22.04", "architecture": "amd64"},
			}, "architectures": []any{"amd64"}},
		}}, authHeader)
	assert.Equal(t, http.StatusOK, resp.Code)
	body = decodeJSON(t, resp)
	assert.Equal(t, float64(1), body["num-resource-revisions-updated"])
}

func TestOCIEndpoints(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	authHeader := "Bearer dev:alice:alice"

	// Setup: register, upload, push revision (which auto-generates OCI resource from containers)
	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "oci-charm"}, authHeader)
	charmArchive := buildTestCharmArchiveWithContainers(t, "oci-charm")
	resp := doMultipartUpload(t, handler, charmArchive, "oci-charm.charm", authHeader)
	require.Equal(t, http.StatusOK, resp.Code)
	uploadID := decodeJSON(t, resp)["upload_id"].(string)
	resp = doRequest(t, handler, "POST", "/v1/charm/oci-charm/revisions",
		map[string]any{"upload-id": uploadID}, authHeader)
	require.Equal(t, http.StatusOK, resp.Code)

	// OCI upload credentials
	resp = doRequest(t, handler, "GET",
		"/v1/charm/oci-charm/resources/workload-image/oci-image/upload-credentials", nil, authHeader)
	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	assert.Contains(t, body["image-name"], "oci-charm/workload-image")
	assert.Contains(t, body["username"], "robot$push-")

	// OCI image blob
	resp = doRequest(t, handler, "POST",
		"/v1/charm/oci-charm/resources/workload-image/oci-image/blob",
		map[string]any{"image-digest": "sha256:abc123"}, authHeader)
	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), "sha256:abc123")
}

func TestCharmDownloadSuccess(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	authHeader := "Bearer dev:pub:pub"

	// Setup: register, upload, push, release
	resp := doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "dl-charm"}, authHeader)
	require.Equal(t, http.StatusOK, resp.Code)
	pkgID := decodeJSON(t, resp)["id"].(string)

	archiveData := buildTestCharmArchive(t, "dl-charm")
	resp = doMultipartUpload(t, handler, archiveData, "dl-charm.charm", authHeader)
	require.Equal(t, http.StatusOK, resp.Code)
	uploadID := decodeJSON(t, resp)["upload_id"].(string)
	doRequest(t, handler, "POST", "/v1/charm/dl-charm/revisions",
		map[string]any{"upload-id": uploadID}, authHeader)

	// Download
	resp = doRequest(t, handler, "GET",
		"/api/v1/charms/download/"+pkgID+"_1.charm", nil, authHeader)

	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, "application/octet-stream", resp.Header().Get("Content-Type"))
	assert.Equal(t, archiveData, resp.Body.Bytes())
}

func TestResourceDownloadSuccess(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	authHeader := "Bearer dev:pub:pub"

	// Setup: register, upload charm, push revision, upload resource, push resource
	resp := doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "resdl-charm"}, authHeader)
	require.Equal(t, http.StatusOK, resp.Code)
	pkgID := decodeJSON(t, resp)["id"].(string)

	resp = doMultipartUpload(t, handler, buildTestCharmArchiveWithResources(t, "resdl-charm"), "resdl-charm.charm", authHeader)
	require.Equal(t, http.StatusOK, resp.Code)
	uploadID := decodeJSON(t, resp)["upload_id"].(string)
	doRequest(t, handler, "POST", "/v1/charm/resdl-charm/revisions",
		map[string]any{"upload-id": uploadID}, authHeader)

	resourceData := []byte("resource content here")
	resp = doMultipartUpload(t, handler, resourceData, "config.yaml", authHeader)
	require.Equal(t, http.StatusOK, resp.Code)
	resUploadID := decodeJSON(t, resp)["upload_id"].(string)
	doRequest(t, handler, "POST", "/v1/charm/resdl-charm/resources/config/revisions",
		map[string]any{"upload-id": resUploadID, "type": "file"}, authHeader)

	// Download resource
	resp = doRequest(t, handler, "GET",
		"/api/v1/resources/download/charm_"+pkgID+".config_1", nil, authHeader)

	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, resourceData, resp.Body.Bytes())
}

func TestWriteErrorInternalError(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	// Trigger an internal error by requesting a download with valid filename format but nonexistent package
	resp := doRequest(t, handler, "GET",
		"/api/v1/charms/download/nonexistent_1.charm", nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
	body := decodeJSON(t, resp)
	errorList := body["error-list"].([]any)
	assert.Len(t, errorList, 1)
}

func TestInfoEndpointNotFound(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "GET", "/v2/charms/info/nonexistent", nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
	body := decodeJSON(t, resp)
	assert.Equal(t, "not-found", body["code"])
	assert.NotEmpty(t, body["message"])
}

func TestRefreshEndpointInvalidBody(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doJSONRequest(t, handler, "POST", "/v2/charms/refresh",
		`not json`, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestPushRevisionInvalidBody(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "bad-body-charm"}, "Bearer dev:alice:alice")

	resp := doJSONRequest(t, handler, "POST", "/v1/charm/bad-body-charm/revisions",
		`not json`, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestReleaseInvalidBody(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "bad-rel-charm"}, "Bearer dev:alice:alice")

	resp := doJSONRequest(t, handler, "POST", "/v1/charm/bad-rel-charm/releases",
		`not json`, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestPatchInvalidBody(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "bad-patch-charm"}, "Bearer dev:alice:alice")

	resp := doJSONRequest(t, handler, "PATCH", "/v1/charm/bad-patch-charm",
		`not json`, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestRegisterInvalidBody(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doJSONRequest(t, handler, "POST", "/v1/charm",
		`not json`, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestRevokeTokenInvalidBody(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doJSONRequest(t, handler, "POST", "/v1/tokens/revoke",
		`not json`, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestIssueTokenInvalidBody(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doJSONRequest(t, handler, "POST", "/v1/tokens",
		`not json`, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestOCIImageBlobInvalidBody(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "oci-bad"}, "Bearer dev:alice:alice")

	resp := doJSONRequest(t, handler, "POST",
		"/v1/charm/oci-bad/resources/foo/oci-image/blob",
		`not json`, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestPushResourceInvalidBody(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "res-bad"}, "Bearer dev:alice:alice")

	resp := doJSONRequest(t, handler, "POST",
		"/v1/charm/res-bad/resources/config/revisions",
		`not json`, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestUpdateResourceRevisionsInvalidBody(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "resupd-bad"}, "Bearer dev:alice:alice")

	resp := doJSONRequest(t, handler, "PATCH",
		"/v1/charm/resupd-bad/resources/config/revisions",
		`not json`, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestCreateTracksInvalidBody(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "track-bad"}, "Bearer dev:alice:alice")

	resp := doJSONRequest(t, handler, "POST",
		"/v1/charm/track-bad/tracks",
		`not json`, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestHandlersRejectBadAuth(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	badAuth := "Basic dXNlcjpwYXNz"

	endpoints := []struct {
		method string
		path   string
		body   string
	}{
		{"POST", "/v1/tokens", `{}`},
		{"POST", "/v1/tokens/exchange", ""},
		{"POST", "/v1/tokens/offline/exchange", ""},
		{"POST", "/v1/tokens/dashboard/exchange", ""},
		{"POST", "/v1/tokens/revoke", `{"session-id":"x"}`},
		{"GET", "/v1/tokens/whoami", ""},
		{"GET", "/v1/whoami", ""},
		{"POST", "/v1/charm", `{"name":"x"}`},
		{"GET", "/v1/charm", ""},
		{"GET", "/v1/charm/x", ""},
		{"PATCH", "/v1/charm/x", `{}`},
		{"DELETE", "/v1/charm/x", ""},
		{"GET", "/v1/charm/x/revisions", ""},
		{"POST", "/v1/charm/x/revisions", `{}`},
		{"GET", "/v1/charm/x/revisions/review?upload-id=y", ""},
		{"GET", "/v1/charm/x/resources", ""},
		{"GET", "/v1/charm/x/resources/r/revisions", ""},
		{"POST", "/v1/charm/x/resources/r/revisions", `{}`},
		{"PATCH", "/v1/charm/x/resources/r/revisions", `{}`},
		{"GET", "/v1/charm/x/resources/r/oci-image/upload-credentials", ""},
		{"POST", "/v1/charm/x/resources/r/oci-image/blob", `{}`},
		{"GET", "/v1/charm/x/releases", ""},
		{"POST", "/v1/charm/x/releases", `[]`},
		{"POST", "/v1/charm/x/tracks", `[]`},
		{"GET", "/v2/charms/find?q=x", ""},
		{"GET", "/v2/charms/info/x", ""},
		{"POST", "/v2/charms/refresh", `{}`},
		{"GET", "/api/v1/charms/download/x_1.charm", ""},
		{"GET", "/api/v1/resources/download/charm_x.r_1", ""},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			t.Parallel()
			var resp *httptest.ResponseRecorder
			if ep.body != "" {
				resp = doJSONRequest(t, handler, ep.method, ep.path, ep.body, badAuth)
			} else {
				req := httptest.NewRequest(ep.method, ep.path, nil)
				req.Header.Set("Authorization", badAuth)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				resp = rec
			}
			assert.Equal(t, http.StatusUnauthorized, resp.Code,
				"expected 401 for %s %s, got %d: %s", ep.method, ep.path, resp.Code, resp.Body.String())
		})
	}
}

func TestGetTokensWithInactiveFilter(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "GET", "/v1/tokens?include-inactive=true", nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusOK, resp.Code)
}

func TestDashboardExchangeInvalidBody(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doJSONRequest(t, handler, "POST", "/v1/tokens/dashboard/exchange",
		`not json`, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestDeletePackageNotFound(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "DELETE", "/v1/charm/nonexistent", nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestPatchPackageNotFound(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "PATCH", "/v1/charm/nonexistent",
		map[string]any{"title": "x"}, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestListRevisionsNotFoundPackage(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "GET", "/v1/charm/nonexistent/revisions", nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestListReleasesNotFoundPackage(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "GET", "/v1/charm/nonexistent/releases", nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestListResourcesNotFoundPackage(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "GET", "/v1/charm/nonexistent/resources", nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestListResourceRevisionsNotFoundPackage(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "GET", "/v1/charm/nonexistent/resources/config/revisions",
		nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestReviewUploadNotFoundPackage(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "GET", "/v1/charm/nonexistent/revisions/review?upload-id=x",
		nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestPushRevisionNotFoundPackage(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "POST", "/v1/charm/nonexistent/revisions",
		map[string]any{"upload-id": "bogus"}, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestPushResourceNotFoundPackage(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "POST", "/v1/charm/nonexistent/resources/config/revisions",
		map[string]any{"upload-id": "bogus", "type": "file"}, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestUpdateResourceRevisionsNotFoundPackage(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "PATCH", "/v1/charm/nonexistent/resources/config/revisions",
		map[string]any{"resource-revision-updates": []any{}}, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestOCIUploadCredentialsNotFoundPackage(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "GET",
		"/v1/charm/nonexistent/resources/config/oci-image/upload-credentials",
		nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestOCIImageBlobAssemblesPayload(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	authHeader := "Bearer dev:alice:alice"

	doRequest(t, handler, "POST", "/v1/charm", map[string]any{"name": "oci-charm"}, authHeader)
	resp := doMultipartUpload(
		t,
		handler,
		buildTestCharmArchiveWithContainers(t, "oci-charm"),
		"oci-charm.charm",
		authHeader,
	)
	require.Equal(t, http.StatusOK, resp.Code)
	uploadID := decodeJSON(t, resp)["upload_id"].(string)
	resp = doRequest(t, handler, "POST", "/v1/charm/oci-charm/revisions", map[string]any{"upload-id": uploadID}, authHeader)
	require.Equal(t, http.StatusOK, resp.Code)

	resp = doRequest(t, handler, "POST",
		"/v1/charm/oci-charm/resources/workload-image/oci-image/blob",
		map[string]any{"image-digest": "sha256:abc"}, authHeader)

	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), "sha256:abc")
}

func TestReleaseNotFoundPackage(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doJSONRequest(t, handler, "POST", "/v1/charm/nonexistent/releases",
		`[{"channel":"latest/stable","revision":1}]`, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestCreateTracksNotFoundPackage(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doJSONRequest(t, handler, "POST", "/v1/charm/nonexistent/tracks",
		`[{"name":"2.0"}]`, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestRefreshMissingIDAndName(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	// Per the Charmhub refresh contract the HTTP response is always 200; the
	// error for this specific action is embedded in the per-action result.
	resp := doRequest(t, handler, "POST", "/v2/charms/refresh", map[string]any{
		"context": []any{},
		"actions": []any{map[string]any{
			"action":       "refresh",
			"instance-key": "unit/0",
		}},
	}, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	results := body["results"].([]any)
	require.Len(t, results, 1)
	item := results[0].(map[string]any)
	assert.Equal(t, "error", item["result"])
	apiErr := item["error"].(map[string]any)
	assert.Equal(t, "invalid-request", apiErr["code"])
}

func TestResourceDownloadNotFoundPackage(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "GET",
		"/api/v1/resources/download/charm_nonexistent.config_1",
		nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestCharmDownloadNotFoundPackage(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "GET",
		"/api/v1/charms/download/nonexistent_1.charm",
		nil, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestWhoAmIUnauthenticated(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "GET", "/v1/whoami", nil, "")

	assert.Equal(t, http.StatusUnauthorized, resp.Code)
}

func TestTokenWhoAmIUnauthenticated(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "GET", "/v1/tokens/whoami", nil, "")

	assert.Equal(t, http.StatusUnauthorized, resp.Code)
}

// TestIssueTokenDevAutoLogin verifies that POST /v1/tokens with no credentials
// succeeds in dev mode by auto-provisioning a "developer" identity, so that
// `charmcraft login` completes without an OIDC provider.
func TestIssueTokenDevAutoLogin(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg) // EnableInsecureDevAuth: true

	resp := doRequest(t, handler, "POST", "/v1/tokens",
		map[string]any{"description": "charmcraft@dev", "ttl": 108000}, "")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	macaroonJSON, ok := body["macaroon"].(string)
	require.True(t, ok, "response should contain a macaroon string")
	// The value must be a JSON string that bakery.Macaroon.from_dict() can parse.
	var macaroonDict map[string]any
	require.NoError(t, json.Unmarshal([]byte(macaroonJSON), &macaroonDict),
		"macaroon field must itself be a JSON string")
	identifier, _ := macaroonDict["identifier"].(string)
	assert.True(t, strings.HasPrefix(identifier, "cr_"), "macaroon identifier should use cr_ prefix")
	assert.NotEmpty(t, macaroonDict["signature"], "macaroon should have a signature")
	assert.Equal(t, testCfg.PublicAPIURL, macaroonDict["location"])
}

// TestIssueTokenDevAutoLoginDisabled verifies that POST /v1/tokens with no
// credentials still returns 401 when dev auth is disabled.
func TestIssueTokenDevAutoLoginDisabled(t *testing.T) {
	t.Parallel()

	cfg := testCfg
	cfg.EnableInsecureDevAuth = false
	handler := newTestHandler(t, cfg)

	resp := doRequest(t, handler, "POST", "/v1/tokens",
		map[string]any{}, "")

	assert.Equal(t, http.StatusUnauthorized, resp.Code)
}

// TestIssueTokenDevAutoLoginExchange simulates the full charmcraft login flow:
//  1. POST /v1/tokens (no auth, dev mode) → bakery macaroon JSON
//  2. POST /v1/tokens/exchange with Macaroons header → plain cr_xxx token
//  3. Subsequent request with Authorization: Macaroon cr_xxx → authenticated
func TestIssueTokenDevAutoLoginExchange(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	// Step 1: charmcraft calls POST /v1/tokens with no auth.
	resp := doRequest(t, handler, "POST", "/v1/tokens",
		map[string]any{"description": "charmcraft@dev", "ttl": 108000}, "")
	require.Equal(t, http.StatusOK, resp.Code)
	macaroonJSON := decodeJSON(t, resp)["macaroon"].(string)

	// Step 2: simulate what craft-store does — build the Macaroons header.
	// craft-store serializes the macaroon array and base64url-encodes it.
	macaroonsArray := "[" + macaroonJSON + "]"
	macaroonsHeader := base64.URLEncoding.EncodeToString([]byte(macaroonsArray))

	resp = doRequest(t, handler, "POST", "/v1/tokens/exchange",
		map[string]any{}, "")
	// Without the Macaroons header this should still be 401.
	assert.Equal(t, http.StatusUnauthorized, resp.Code)

	req, _ := http.NewRequest("POST", "/v1/tokens/exchange", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Macaroons", macaroonsHeader)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusOK, recorder.Code)
	finalToken := decodeJSON(t, recorder)["macaroon"].(string)
	assert.True(t, strings.HasPrefix(finalToken, "cr_"),
		"exchange should return a plain cr_ token")

	// Step 3: use the final token with Authorization: Macaroon <cr_xxx>.
	resp = doRequest(t, handler, "GET", "/v1/tokens/whoami", nil, "Macaroon "+finalToken)
	assert.Equal(t, http.StatusOK, resp.Code)
	account := decodeJSON(t, resp)["account"].(map[string]any)
	assert.Equal(t, "developer", account["username"])
}

func TestRevokeTokenUnauthenticated(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "POST", "/v1/tokens/revoke",
		map[string]any{"session-id": "x"}, "")

	assert.Equal(t, http.StatusUnauthorized, resp.Code)
}

func TestExchangeTokenUnauthenticated(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "POST", "/v1/tokens/exchange", nil, "")

	assert.Equal(t, http.StatusUnauthorized, resp.Code)
}

func TestDeletePackageWithRevisions(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	authHeader := "Bearer dev:alice:alice"

	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "del-charm"}, authHeader)
	resp := doMultipartUpload(t, handler, buildTestCharmArchive(t, "del-charm"), "del-charm.charm", authHeader)
	require.Equal(t, http.StatusOK, resp.Code)
	uploadID := decodeJSON(t, resp)["upload_id"].(string)
	doRequest(t, handler, "POST", "/v1/charm/del-charm/revisions",
		map[string]any{"upload-id": uploadID}, authHeader)

	resp = doRequest(t, handler, "DELETE", "/v1/charm/del-charm", nil, authHeader)

	// The caller is authorised; the business rule prevents deletion.
	// This is 400 invalid-request, not 403 (which is reserved for auth).
	assert.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestReleaseEmptyChannelReturns400(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	authHeader := "Bearer dev:alice:alice"

	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "rel-err-charm"}, authHeader)
	resp := doMultipartUpload(t, handler, buildTestCharmArchive(t, "rel-err-charm"), "rel-err-charm.charm", authHeader)
	require.Equal(t, http.StatusOK, resp.Code)
	uploadID := decodeJSON(t, resp)["upload_id"].(string)
	doRequest(t, handler, "POST", "/v1/charm/rel-err-charm/revisions",
		map[string]any{"upload-id": uploadID}, authHeader)

	resp = doJSONRequest(t, handler, "POST", "/v1/charm/rel-err-charm/releases",
		`[{"channel":"","revision":1}]`, authHeader)

	assert.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestUnscannedUploadMissingBinaryField(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("wrong-field", "test.charm")
	require.NoError(t, err)
	_, err = part.Write([]byte("data"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest("POST", "/unscanned-upload/", &buf)
	req.Header.Set("Authorization", "Bearer dev:alice:alice")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	body := decodeJSON(t, recorder)
	assert.Equal(t, false, body["successful"])
}

func TestUnscannedUploadRequiresAuthentication(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doMultipartUpload(t, handler, []byte("archive data"), "test.charm", "")

	assert.Equal(t, http.StatusUnauthorized, resp.Code)
}

func TestListRevisionsWithRevisionParam(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	authHeader := "Bearer dev:alice:alice"

	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "revparam-charm"}, authHeader)
	resp := doMultipartUpload(t, handler, buildTestCharmArchive(t, "revparam-charm"), "revparam-charm.charm", authHeader)
	require.Equal(t, http.StatusOK, resp.Code)
	uploadID := decodeJSON(t, resp)["upload_id"].(string)
	doRequest(t, handler, "POST", "/v1/charm/revparam-charm/revisions",
		map[string]any{"upload-id": uploadID}, authHeader)

	resp = doRequest(t, handler, "GET", "/v1/charm/revparam-charm/revisions?revision=1",
		nil, authHeader)

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	revisions := body["revisions"].([]any)
	assert.Len(t, revisions, 1)
}

func TestRefreshNotFoundChannel(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	authHeader := "Bearer dev:alice:alice"

	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "norel-charm"}, authHeader)

	// The charm exists but has no release on the requested channel.
	// Per the Charmhub refresh contract the HTTP response is 200; the
	// not-found error is embedded in the per-action result.
	resp := doRequest(t, handler, "POST", "/v2/charms/refresh", map[string]any{
		"context": []any{},
		"actions": []any{map[string]any{
			"action":       "refresh",
			"instance-key": "unit/0",
			"name":         "norel-charm",
			"channel":      "latest/stable",
		}},
	}, authHeader)

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	results := body["results"].([]any)
	require.Len(t, results, 1)
	item := results[0].(map[string]any)
	assert.Equal(t, "error", item["result"])
	apiErr := item["error"].(map[string]any)
	assert.Equal(t, "not-found", apiErr["code"])
}

func TestInfoEndpointNoRelease(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	authHeader := "Bearer dev:alice:alice"

	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "norel-info"}, authHeader)

	resp := doRequest(t, handler, "GET", "/v2/charms/info/norel-info", nil, authHeader)

	assert.Equal(t, http.StatusNotFound, resp.Code)
	body := decodeJSON(t, resp)
	assert.Equal(t, "not-found", body["code"])
	assert.Equal(t, "no released revisions found", body["message"])
}

// TestLibrariesBulkNoAuth verifies that POST /v1/charm/libraries/bulk works
// without credentials (charmcraft uses its anonymous client for this call)
// and always returns an empty libraries list.
func TestLibrariesBulkNoAuth(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	// Empty request (no local libs with known IDs).
	resp := doRequest(t, handler, "POST", "/v1/charm/libraries/bulk", []any{}, "")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	libs, ok := body["libraries"].([]any)
	require.True(t, ok)
	assert.Empty(t, libs)
}

func TestLibrariesBulkWithPayload(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	// charmcraft sends a list of {library-id: "..."} objects.
	payload := []any{
		map[string]any{"library-id": "some-uuid-1"},
		map[string]any{"library-id": "some-uuid-2"},
	}
	resp := doRequest(t, handler, "POST", "/v1/charm/libraries/bulk", payload, "")

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	libs, ok := body["libraries"].([]any)
	require.True(t, ok)
	assert.Empty(t, libs, "unknown library IDs should return empty list, not an error")
}

func TestListReleasesEmpty(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	authHeader := "Bearer dev:alice:alice"

	doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "norel-list"}, authHeader)

	resp := doRequest(t, handler, "GET", "/v1/charm/norel-list/releases", nil, authHeader)

	assert.Equal(t, http.StatusOK, resp.Code)
	body := decodeJSON(t, resp)
	channelMap := body["channel-map"].([]any)
	assert.Empty(t, channelMap)
}

func TestFullPublishAndDownloadWithResources(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	authHeader := "Bearer dev:alice:alice"

	// Register
	resp := doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "full-charm"}, authHeader)
	require.Equal(t, http.StatusOK, resp.Code)
	pkgID := decodeJSON(t, resp)["id"].(string)

	// Upload and push charm with resources declared
	resp = doMultipartUpload(t, handler, buildTestCharmArchiveWithResources(t, "full-charm"), "full-charm.charm", authHeader)
	require.Equal(t, http.StatusOK, resp.Code)
	uploadID := decodeJSON(t, resp)["upload_id"].(string)
	resp = doRequest(t, handler, "POST", "/v1/charm/full-charm/revisions",
		map[string]any{"upload-id": uploadID}, authHeader)
	require.Equal(t, http.StatusOK, resp.Code)

	// Upload and push resource
	resp = doMultipartUpload(t, handler, []byte("resource-data"), "config.yaml", authHeader)
	require.Equal(t, http.StatusOK, resp.Code)
	resUploadID := decodeJSON(t, resp)["upload_id"].(string)
	resp = doRequest(t, handler, "POST", "/v1/charm/full-charm/resources/config/revisions",
		map[string]any{"upload-id": resUploadID, "type": "file"}, authHeader)
	require.Equal(t, http.StatusOK, resp.Code)

	// Release with resource ref
	resp = doJSONRequest(t, handler, "POST", "/v1/charm/full-charm/releases",
		`[{"channel":"latest/stable","revision":1,"resources":[{"name":"config","revision":1}]}]`, authHeader)
	require.Equal(t, http.StatusOK, resp.Code)

	// Info - should show resource downloads
	resp = doRequest(t, handler, "GET", "/v2/charms/info/full-charm", nil, authHeader)
	assert.Equal(t, http.StatusOK, resp.Code)
	infoBody := decodeJSON(t, resp)
	defaultRelease := infoBody["default-release"].(map[string]any)
	resources := defaultRelease["resources"].([]any)
	assert.Len(t, resources, 1)

	// Download charm
	resp = doRequest(t, handler, "GET",
		"/api/v1/charms/download/"+pkgID+"_1.charm", nil, authHeader)
	assert.Equal(t, http.StatusOK, resp.Code)

	// Download resource
	resp = doRequest(t, handler, "GET",
		"/api/v1/resources/download/charm_"+pkgID+".config_1", nil, authHeader)
	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, []byte("resource-data"), resp.Body.Bytes())

	// Find
	resp = doRequest(t, handler, "GET", "/v2/charms/find?q=full", nil, authHeader)
	assert.Equal(t, http.StatusOK, resp.Code)
	findBody := decodeJSON(t, resp)
	results := findBody["results"].([]any)
	assert.Len(t, results, 1)

	// Refresh with resource override
	resp = doRequest(t, handler, "POST", "/v2/charms/refresh", map[string]any{
		"context": []any{},
		"actions": []any{map[string]any{
			"action":       "refresh",
			"instance-key": "unit/0",
			"name":         "full-charm",
			"channel":      "latest/stable",
			"resource-revisions": []any{
				map[string]any{"name": "config", "revision": 1},
			},
		}},
	}, authHeader)
	assert.Equal(t, http.StatusOK, resp.Code)
}

func TestRegisterPackageWithPrivateFlag(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "private-charm", "private": true}, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusOK, resp.Code)

	resp = doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "public-charm", "private": false}, "Bearer dev:alice:alice")

	assert.Equal(t, http.StatusOK, resp.Code)
}

func TestGetPrivatePackageForbiddenForDifferentUser(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "alice-private", "private": true}, "Bearer dev:alice:alice")
	require.Equal(t, http.StatusOK, resp.Code)

	resp = doRequest(t, handler, "GET", "/v1/charm/alice-private", nil, "Bearer dev:bob:bob")

	assert.Equal(t, http.StatusForbidden, resp.Code)
	assert.Contains(t, resp.Body.String(), "forbidden")
}

func TestPatchPackageForbiddenForDifferentUser(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)

	resp := doRequest(t, handler, "POST", "/v1/charm",
		map[string]any{"name": "alice-owned"}, "Bearer dev:alice:alice")
	require.Equal(t, http.StatusOK, resp.Code)

	resp = doRequest(t, handler, "PATCH", "/v1/charm/alice-owned",
		map[string]any{"title": "bob edit"}, "Bearer dev:bob:bob")

	assert.Equal(t, http.StatusForbidden, resp.Code)
	assert.Contains(t, resp.Body.String(), "forbidden")
}

// --- helpers ---

func newTestHandler(t *testing.T, cfg config.Config) http.Handler {
	t.Helper()

	repository := repo.NewMemory()
	authenticator, err := auth.New(context.Background(), cfg, repository)
	require.NoError(t, err)

	svc := service.New(cfg, repository, blob.NewMemoryStore(), apiTestOCIRegistry{})
	return New(cfg, svc, authenticator)
}

type apiTestOCIRegistry struct{}

func (apiTestOCIRegistry) SyncPackage(_ context.Context, pkg core.Package) (core.Package, error) {
	if pkg.HarborProject == "" {
		pkg.HarborProject = "charm-" + pkg.Name
	}
	if pkg.HarborPushRobot == nil {
		pkg.HarborPushRobot = &core.RobotCredential{ID: 1, Username: "robot$push-" + pkg.ID, EncryptedSecret: "push"}
	}
	if pkg.HarborPullRobot == nil {
		pkg.HarborPullRobot = &core.RobotCredential{ID: 2, Username: "robot$pull-" + pkg.ID, EncryptedSecret: "pull"}
	}
	return pkg, nil
}

func (apiTestOCIRegistry) ImageReference(pkg core.Package, resourceName string) (string, error) {
	return "oci.test/" + pkg.HarborProject + "/" + resourceName, nil
}

func (apiTestOCIRegistry) Credentials(pkg core.Package, pull bool) (string, string, error) {
	if pull {
		return pkg.HarborPullRobot.Username, "pull-secret", nil
	}
	return pkg.HarborPushRobot.Username, "push-secret", nil
}

func doRequest(t *testing.T, handler http.Handler, method, path string, body any, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func doJSONRequest(t *testing.T, handler http.Handler, method, path, body, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func doMultipartUpload(t *testing.T, handler http.Handler, data []byte, filename, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("binary", filename)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest("POST", "/unscanned-upload/", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func decodeJSON(t *testing.T, resp *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	err := json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err, "response body: %s", resp.Body.String())
	return body
}

func buildTestCharmArchiveWithContainers(t *testing.T, name string) []byte {
	t.Helper()
	var payload bytes.Buffer
	writer := zip.NewWriter(&payload)
	metadata := fmt.Sprintf(
		"name: %s\nsummary: Test\ndescription: Test charm\ncontainers:\n  workload:\n    resource: workload-image\n",
		name,
	)
	entry, err := writer.Create("metadata.yaml")
	require.NoError(t, err)
	_, err = entry.Write([]byte(metadata))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return payload.Bytes()
}

func buildTestCharmArchive(t *testing.T, name string) []byte {
	t.Helper()
	return buildTestCharmArchiveFrom(t, name, fmt.Sprintf("name: %s\nsummary: Test\ndescription: Test charm\n", name))
}

func buildTestCharmArchiveWithResources(t *testing.T, name string) []byte {
	t.Helper()
	metadata := fmt.Sprintf(
		"name: %s\nsummary: Test\ndescription: Test charm\nresources:\n  config:\n    type: file\n    filename: config.yaml\n    description: Config file\n",
		name,
	)
	return buildTestCharmArchiveFrom(t, name, metadata)
}

func buildTestCharmArchiveFrom(t *testing.T, _ string, metadata string) []byte {
	t.Helper()
	var payload bytes.Buffer
	writer := zip.NewWriter(&payload)
	entry, err := writer.Create("metadata.yaml")
	require.NoError(t, err)
	_, err = entry.Write([]byte(metadata))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return payload.Bytes()
}
