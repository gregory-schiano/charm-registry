//go:build integration

package integration_test

import (
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RES-01: List resources returns declared resources for a charm with resources.
// Register a charm with resource declarations, push a revision, and verify
// GET /v1/charm/{name}/resources returns the declared resource(s).
func TestRES01_ListResourcesReturnsDeclaredResources(t *testing.T) {
	auth := devAuthHeader("alice", "Alice")
	name := uniqueName("res-list-charm")

	// Register the charm.
	mustRegister(t, name, auth)

	// Upload a charm archive that declares resources.
	archive := buildTestCharmArchiveWithResources(t, name)
	uploadID := mustUploadCharm(t, archive, name+".zip", auth)
	mustPushRevision(t, name, uploadID, auth)

	// List declared resources.
	resp, err := doRequest("GET", "/v1/charm/"+name+"/resources", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	resources, ok := body["resources"].([]any)
	require.True(t, ok, "response should have 'resources' array")
	require.NotEmpty(t, resources, "should have at least one declared resource")

	// The charm declares a "config" resource of type "file".
	first := resources[0].(map[string]any)
	assert.Equal(t, "config", first["name"], "resource name should be 'config'")
	assert.Equal(t, "file", first["type"], "resource type should be 'file'")
	assert.Contains(t, first, "optional", "resource should have 'optional' field")
	assert.Contains(t, first, "revision", "resource should have 'revision' field")
}

// RES-02: List resources returns empty for charm without resources.
// Register a charm without resource declarations and verify the resources list is empty.
func TestRES02_ListResourcesReturnsEmptyForCharmWithoutResources(t *testing.T) {
	auth := devAuthHeader("alice", "Alice")
	name := uniqueName("res-norestlist-charm")

	// Register and push a charm without resources.
	mustRegister(t, name, auth)
	archive := buildTestCharmArchive(t, name)
	uploadID := mustUploadCharm(t, archive, name+".zip", auth)
	mustPushRevision(t, name, uploadID, auth)

	// List declared resources.
	resp, err := doRequest("GET", "/v1/charm/"+name+"/resources", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	resources, ok := body["resources"].([]any)
	require.True(t, ok, "response should have 'resources' array")
	assert.Empty(t, resources, "charm without resources should return empty resources array")
}

// RES-03: List resource revisions returns revisions after push.
// Push a resource revision, then list revisions for that resource
// and verify the revision appears.
func TestRES03_ListResourceRevisionsReturnsRevisionsAfterPush(t *testing.T) {
	auth := devAuthHeader("alice", "Alice")
	name := uniqueName("res-revlist-charm")

	// Register the charm with resource declarations.
	mustRegister(t, name, auth)
	archive := buildTestCharmArchiveWithResources(t, name)
	uploadID := mustUploadCharm(t, archive, name+".zip", auth)
	mustPushRevision(t, name, uploadID, auth)

	// Upload a resource file and push a resource revision.
	resourceContent := []byte("key: value\n")
	resourceUploadResult := uploadMultipart(t, "/unscanned-upload/", "binary", "config.yaml", resourceContent, auth)
	resourceUploadID, ok := resourceUploadResult["upload_id"].(string)
	require.True(t, ok, "resource upload should return 'upload_id'")
	require.NotEmpty(t, resourceUploadID, "resource upload_id should not be empty")

	pushBody := fmt.Sprintf(`{"upload-id":"%s"}`, resourceUploadID)
	resp, err := doRequest("POST", "/v1/charm/"+name+"/resources/config/revisions", pushBody, auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusCreated)
	// Drain body so connection can be reused.
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	// List resource revisions.
	resp, err = doRequest("GET", "/v1/charm/"+name+"/resources/config/revisions", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	revisions, ok := body["revisions"].([]any)
	require.True(t, ok, "response should have 'revisions' array")
	require.NotEmpty(t, revisions, "should have at least one resource revision after push")

	first := revisions[0].(map[string]any)
	assert.Contains(t, first, "revision", "resource revision should have 'revision' field")
	assert.Contains(t, first, "filename", "resource revision should have 'filename' field")
	assert.Contains(t, first, "created-at", "resource revision should have 'created-at' field")
	assert.Contains(t, first, "download", "resource revision should have 'download' field")
}

// RES-04: Push resource revision returns status-url with 201.
// Push a resource revision and verify the response is 201 with a status-url field.
func TestRES04_PushResourceRevisionReturnsStatusURL(t *testing.T) {
	auth := devAuthHeader("alice", "Alice")
	name := uniqueName("res-push-charm")

	// Register the charm with resource declarations.
	mustRegister(t, name, auth)
	archive := buildTestCharmArchiveWithResources(t, name)
	uploadID := mustUploadCharm(t, archive, name+".zip", auth)
	mustPushRevision(t, name, uploadID, auth)

	// Upload a resource file.
	resourceContent := []byte("key: value\n")
	resourceUploadResult := uploadMultipart(t, "/unscanned-upload/", "binary", "config.yaml", resourceContent, auth)
	resourceUploadID, ok := resourceUploadResult["upload_id"].(string)
	require.True(t, ok, "resource upload should return 'upload_id'")
	require.NotEmpty(t, resourceUploadID, "resource upload_id should not be empty")

	// Push resource revision and inspect raw response.
	pushBody := fmt.Sprintf(`{"upload-id":"%s"}`, resourceUploadID)
	resp, err := doRequest("POST", "/v1/charm/"+name+"/resources/config/revisions", pushBody, auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusCreated)

	result := readJSON(t, resp)
	assert.Contains(t, result, "status-url", "push resource response should contain 'status-url'")
	statusURL, ok := result["status-url"].(string)
	require.True(t, ok, "status-url should be a string")
	assert.NotEmpty(t, statusURL, "status-url should not be empty")
}

// RES-05: Resource download endpoint returns content.
// Push a resource revision, then download it via /api/v1/resources/download/{filename}
// and verify the content matches.
func TestRES05_ResourceDownloadEndpointReturnsContent(t *testing.T) {
	auth := devAuthHeader("alice", "Alice")
	name := uniqueName("res-download-charm")

	// Register the charm with resource declarations.
	pkgID := mustRegister(t, name, auth)
	archive := buildTestCharmArchiveWithResources(t, name)
	uploadID := mustUploadCharm(t, archive, name+".zip", auth)
	mustPushRevision(t, name, uploadID, auth)

	// Upload a resource file and push a resource revision.
	resourceContent := []byte("key: value\n")
	resourceUploadResult := uploadMultipart(t, "/unscanned-upload/", "binary", "config.yaml", resourceContent, auth)
	resourceUploadID, ok := resourceUploadResult["upload_id"].(string)
	require.True(t, ok, "resource upload should return 'upload_id'")
	require.NotEmpty(t, resourceUploadID)

	pushBody := fmt.Sprintf(`{"upload-id":"%s"}`, resourceUploadID)
	resp, err := doRequest("POST", "/v1/charm/"+name+"/resources/config/revisions", pushBody, auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusCreated)
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	// Determine the resource revision number from the revisions list.
	resp, err = doRequest("GET", "/v1/charm/"+name+"/resources/config/revisions", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	revBody := readJSON(t, resp)
	revisions := revBody["revisions"].([]any)
	require.NotEmpty(t, revisions, "should have at least one resource revision")
	revNum := int(revisions[0].(map[string]any)["revision"].(float64))

	// Download the resource via /api/v1/resources/download/charm_{pkgID}.{resourceName}_{revNum}.
	downloadFilename := fmt.Sprintf("charm_%s.config_%d", pkgID, revNum)
	resp, err = doRequest("GET", "/api/v1/resources/download/"+downloadFilename, "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)

	downloadedBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "reading downloaded resource")
	resp.Body.Close()

	// The downloaded content should match what was uploaded.
	assert.NotEmpty(t, downloadedBytes, "downloaded resource should not be empty")
	assert.Equal(t, string(resourceContent), string(downloadedBytes),
		"downloaded resource content should match uploaded content")
}

// RES-06: Nonexistent resource returns 404 or empty revisions.
// Query a resource that does not exist and verify the API returns 404 or empty list.
func TestRES06_NonexistentResourceReturns404OrEmptyRevisions(t *testing.T) {
	auth := devAuthHeader("alice", "Alice")
	name := uniqueName("res-nonexistent-charm")

	// Register the charm with resource declarations.
	mustRegister(t, name, auth)
	archive := buildTestCharmArchiveWithResources(t, name)
	uploadID := mustUploadCharm(t, archive, name+".zip", auth)
	mustPushRevision(t, name, uploadID, auth)

	// Query revisions for a resource name that does not exist.
	resp, err := doRequest("GET", "/v1/charm/"+name+"/resources/nonexistent-resource/revisions", "", auth)
	require.NoError(t, err)

	// The API should return 404 for a nonexistent resource definition.
	if resp.StatusCode == http.StatusNotFound {
		// Expected: 404 not found.
		_, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		return
	}

	// Alternatively, the API may return 200 with an empty revisions list.
	assertStatusCode(t, resp, http.StatusOK)
	body := readJSON(t, resp)
	revisions, ok := body["revisions"].([]any)
	require.True(t, ok, "response should have 'revisions' array")
	assert.Empty(t, revisions, "nonexistent resource should return empty revisions list")
}
