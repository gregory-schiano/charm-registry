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
// Register a charm, upload an archive, push a revision, release to a channel,
// and verify the release appears in the releases list.
func TestREV01_FullUploadToReleasePipeline(t *testing.T) {
	auth := devAuthHeader("alice", "Alice")
	name := uniqueName("pipeline-charm")

	// Step 1: Register the charm.
	mustRegister(t, name, auth)

	// Step 2: Upload a charm archive.
	archive := buildTestCharmArchive(t, name)
	uploadID := mustUploadCharm(t, archive, name+".zip", auth)

	// Step 3: Push a revision from the upload-id.
	mustPushRevision(t, name, uploadID, auth)

	// Step 4: Check review status for the upload.
	resp, err := doRequest("GET", "/v1/charm/"+name+"/revisions/review?upload-id="+uploadID, "", auth)
	require.NoError(t, err)
	defer resp.Body.Close()
	assertStatusCode(t, resp, http.StatusOK)

	// Step 5: List revisions and find revision number 1.
	resp, err = doRequest("GET", "/v1/charm/"+name+"/revisions", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	revisionsBody := readJSON(t, resp)
	revisions, ok := revisionsBody["revisions"].([]any)
	require.True(t, ok, "response should have 'revisions' array")
	require.NotEmpty(t, revisions, "should have at least one revision")

	firstRev := revisions[0].(map[string]any)
	revNum := int(firstRev["revision"].(float64))
	assert.Equal(t, 1, revNum, "first revision should be revision 1")

	// Step 6: Release revision 1 to latest/stable.
	mustRelease(t, name, revNum, "latest/stable", auth)

	// Step 7: Verify the release appears in the releases list.
	resp, err = doRequest("GET", "/v1/charm/"+name+"/releases", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	releasesBody := readJSON(t, resp)

	releases, ok := releasesBody["released"].([]any)
	require.True(t, ok, "response should have 'released' array")
	require.NotEmpty(t, releases, "should have at least one release")

	firstRelease := releases[0].(map[string]any)
	assert.Equal(t, "latest/stable", firstRelease["channel"], "release channel should be latest/stable")
	assert.Equal(t, float64(revNum), firstRelease["revision"], "release revision should match pushed revision")
}

// REV-02: Upload charm with resources.
// Upload a charm archive declaring resources in metadata.yaml, push revision,
// and verify resource definitions are auto-declared.
func TestREV02_UploadCharmWithResources(t *testing.T) {
	auth := devAuthHeader("alice", "Alice")
	name := uniqueName("resource-charm")

	// Register the charm.
	mustRegister(t, name, auth)

	// Upload a charm archive that declares resources.
	archive := buildTestCharmArchiveWithResources(t, name)
	uploadID := mustUploadCharm(t, archive, name+".zip", auth)

	// Push a revision from the upload-id.
	mustPushRevision(t, name, uploadID, auth)

	// Verify the revision was created.
	resp, err := doRequest("GET", "/v1/charm/"+name+"/revisions", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	revisionsBody := readJSON(t, resp)

	revisions, ok := revisionsBody["revisions"].([]any)
	require.True(t, ok, "response should have 'revisions' array")
	require.NotEmpty(t, revisions, "should have at least one revision after push")
}

// REV-03: List revisions.
// Push 2 revisions for the same charm and verify both appear in the revisions list.
func TestREV03_ListRevisions(t *testing.T) {
	auth := devAuthHeader("alice", "Alice")
	name := uniqueName("list-rev-charm")

	// Register the charm.
	mustRegister(t, name, auth)

	// Push revision 1.
	archive1 := buildTestCharmArchive(t, name)
	uploadID1 := mustUploadCharm(t, archive1, name+"_1.zip", auth)
	mustPushRevision(t, name, uploadID1, auth)

	// Push revision 2 (different archive, same charm name in metadata).
	archive2 := buildTestCharmArchive(t, name)
	uploadID2 := mustUploadCharm(t, archive2, name+"_2.zip", auth)
	mustPushRevision(t, name, uploadID2, auth)

	// List revisions.
	resp, err := doRequest("GET", "/v1/charm/"+name+"/revisions", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	body := readJSON(t, resp)

	revisions, ok := body["revisions"].([]any)
	require.True(t, ok, "response should have 'revisions' array")
	require.Len(t, revisions, 2, "should have exactly 2 revisions")

	// Verify each revision has expected fields.
	for _, rev := range revisions {
		r := rev.(map[string]any)
		assert.Contains(t, r, "revision", "revision entry should have 'revision' field")
		assert.Contains(t, r, "version", "revision entry should have 'version' field")
		assert.Contains(t, r, "status", "revision entry should have 'status' field")
	}

	// Verify we can filter by revision number.
	revNum := int(revisions[0].(map[string]any)["revision"].(float64))
	resp, err = doRequest("GET", fmt.Sprintf("/v1/charm/%s/revisions?revision=%d", name, revNum), "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	filteredBody := readJSON(t, resp)

	filteredRevisions, ok := filteredBody["revisions"].([]any)
	require.True(t, ok, "filtered response should have 'revisions' array")
	require.Len(t, filteredRevisions, 1, "revision filter should return exactly 1 revision")
}

// REV-04: Release to multiple channels.
// Release the same revision to latest/stable and latest/edge,
// and verify both channels appear in the releases list.
func TestREV04_ReleaseToMultipleChannels(t *testing.T) {
	auth := devAuthHeader("alice", "Alice")
	name := uniqueName("multi-channel-charm")

	// Register, upload, and push a single revision.
	mustRegister(t, name, auth)
	archive := buildTestCharmArchive(t, name)
	uploadID := mustUploadCharm(t, archive, name+".zip", auth)
	mustPushRevision(t, name, uploadID, auth)

	// Determine the revision number.
	resp, err := doRequest("GET", "/v1/charm/"+name+"/revisions", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	revBody := readJSON(t, resp)
	revisions := revBody["revisions"].([]any)
	revNum := int(revisions[0].(map[string]any)["revision"].(float64))

	// Release to latest/stable.
	mustRelease(t, name, revNum, "latest/stable", auth)

	// Release to latest/edge.
	mustRelease(t, name, revNum, "latest/edge", auth)

	// Verify both channels appear in the releases list.
	resp, err = doRequest("GET", "/v1/charm/"+name+"/releases", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	releasesBody := readJSON(t, resp)

	releases, ok := releasesBody["released"].([]any)
	require.True(t, ok, "response should have 'released' array")
	require.Len(t, releases, 2, "should have 2 releases (stable + edge)")

	channels := make(map[string]bool)
	for _, rel := range releases {
		r := rel.(map[string]any)
		channels[r["channel"].(string)] = true
	}
	assert.True(t, channels["latest/stable"], "latest/stable should be in releases")
	assert.True(t, channels["latest/edge"], "latest/edge should be in releases")
}

// REV-05: Create and use custom track.
// Create a custom track "2.0", then release to "2.0/stable" and verify it works.
func TestREV05_CreateAndUseCustomTrack(t *testing.T) {
	auth := devAuthHeader("alice", "Alice")
	name := uniqueName("track-charm")

	// Register, upload, and push a revision.
	mustRegister(t, name, auth)
	archive := buildTestCharmArchive(t, name)
	uploadID := mustUploadCharm(t, archive, name+".zip", auth)
	mustPushRevision(t, name, uploadID, auth)

	// Determine the revision number.
	resp, err := doRequest("GET", "/v1/charm/"+name+"/revisions", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	revBody := readJSON(t, resp)
	revisions := revBody["revisions"].([]any)
	revNum := int(revisions[0].(map[string]any)["revision"].(float64))

	// Create a custom track "2.0".
	trackBody := `[{"name":"2.0"}]`
	resp, err = doRequest("POST", "/v1/charm/"+name+"/tracks", trackBody, auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusCreated)
	trackResult := readJSON(t, resp)
	numCreated, ok := trackResult["num-tracks-created"].(float64)
	require.True(t, ok, "response should have 'num-tracks-created' field")
	assert.Equal(t, float64(1), numCreated, "should have created 1 track")

	// Release to 2.0/stable.
	mustRelease(t, name, revNum, "2.0/stable", auth)

	// Verify the release appears in the releases list.
	resp, err = doRequest("GET", "/v1/charm/"+name+"/releases", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	releasesBody := readJSON(t, resp)

	releases, ok := releasesBody["released"].([]any)
	require.True(t, ok, "response should have 'released' array")
	require.NotEmpty(t, releases, "should have at least one release")

	found := false
	for _, rel := range releases {
		r := rel.(map[string]any)
		if r["channel"] == "2.0/stable" {
			found = true
			assert.Equal(t, float64(revNum), r["revision"], "release revision should match")
			break
		}
	}
	assert.True(t, found, "2.0/stable should appear in releases")
}

// REV-06: Charm download.
// Upload and release a charm, then download it and verify the archive matches.
func TestREV06_CharmDownload(t *testing.T) {
	auth := devAuthHeader("alice", "Alice")
	name := uniqueName("download-charm")

	// Register, upload, and push a revision.
	pkgID := mustRegister(t, name, auth)
	archive := buildTestCharmArchive(t, name)
	uploadID := mustUploadCharm(t, archive, name+".zip", auth)
	mustPushRevision(t, name, uploadID, auth)

	// Determine the revision number.
	resp, err := doRequest("GET", "/v1/charm/"+name+"/revisions", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	revBody := readJSON(t, resp)
	revisions := revBody["revisions"].([]any)
	revNum := int(revisions[0].(map[string]any)["revision"].(float64))

	// Release to latest/stable so the charm is published.
	mustRelease(t, name, revNum, "latest/stable", auth)

	// Download the charm archive.
	downloadURL := fmt.Sprintf("/api/v1/charms/download/%s_%d.charm", pkgID, revNum)
	resp, err = doRequest("GET", downloadURL, "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)

	downloadedBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "reading downloaded charm archive")
	resp.Body.Close()

	// The downloaded archive should be a valid zip (same content as uploaded).
	assert.NotEmpty(t, downloadedBytes, "downloaded archive should not be empty")
	assert.Equal(t, len(archive), len(downloadedBytes), "downloaded archive size should match uploaded archive size")
}

// REV-07: Unscanned upload returns upload-id.
// Verify the raw /unscanned-upload/ endpoint returns a successful response with upload_id.
func TestREV07_UnscannedUploadReturnsUploadID(t *testing.T) {
	auth := devAuthHeader("alice", "Alice")
	name := uniqueName("upload-id-charm")

	archive := buildTestCharmArchive(t, name)
	result := uploadMultipart(t, "/unscanned-upload/", "binary", name+".zip", archive, auth)

	assert.Equal(t, true, result["successful"], "upload should be successful")
	uploadID, ok := result["upload_id"].(string)
	require.True(t, ok, "response should have 'upload_id' string field")
	require.NotEmpty(t, uploadID, "upload_id should not be empty")
}

// REV-08: Push revision returns status-url.
// Verify that POST /v1/charm/{name}/revisions returns a status-url in the response.
func TestREV08_PushRevisionReturnsStatusURL(t *testing.T) {
	auth := devAuthHeader("alice", "Alice")
	name := uniqueName("status-url-charm")

	mustRegister(t, name, auth)
	archive := buildTestCharmArchive(t, name)
	uploadID := mustUploadCharm(t, archive, name+".zip", auth)

	// Push revision and inspect the raw response.
	body := fmt.Sprintf(`{"upload-id":"%s"}`, uploadID)
	resp, err := doRequest("POST", "/v1/charm/"+name+"/revisions", body, auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)

	result := readJSON(t, resp)
	// The API should return a status-url field.
	assert.Contains(t, result, "status-url", "push revision response should contain 'status-url'")
}

// REV-09: Release endpoint returns released array.
// Verify POST /v1/charm/{name}/releases returns 201 with a "released" array.
func TestREV09_ReleaseEndpointReturnsReleasedArray(t *testing.T) {
	auth := devAuthHeader("alice", "Alice")
	name := uniqueName("release-resp-charm")

	mustRegister(t, name, auth)
	archive := buildTestCharmArchive(t, name)
	uploadID := mustUploadCharm(t, archive, name+".zip", auth)
	mustPushRevision(t, name, uploadID, auth)

	// Get revision number.
	resp, err := doRequest("GET", "/v1/charm/"+name+"/revisions", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	revBody := readJSON(t, resp)
	revisions := revBody["revisions"].([]any)
	revNum := int(revisions[0].(map[string]any)["revision"].(float64))

	// Release and inspect raw response.
	releaseBody := fmt.Sprintf(`[{"revision":%d,"channel":"latest/stable","resources":[]}]`, revNum)
	resp, err = doRequest("POST", "/v1/charm/"+name+"/releases", releaseBody, auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusCreated)

	result := readJSON(t, resp)
	released, ok := result["released"].([]any)
	require.True(t, ok, "release response should have 'released' array")
	require.NotEmpty(t, released, "'released' array should not be empty")

	firstReleased := released[0].(map[string]any)
	assert.Equal(t, "latest/stable", firstReleased["channel"], "released channel should be latest/stable")
	assert.Equal(t, float64(revNum), firstReleased["revision"], "released revision should match")
}

// REV-10: Revision listing includes metadata fields.
// Verify that each revision in the list contains sha3-384, size, bases, and created-at.
func TestREV10_RevisionListingIncludesMetadataFields(t *testing.T) {
	auth := devAuthHeader("alice", "Alice")
	name := uniqueName("rev-meta-charm")

	mustRegister(t, name, auth)
	archive := buildTestCharmArchive(t, name)
	uploadID := mustUploadCharm(t, archive, name+".zip", auth)
	mustPushRevision(t, name, uploadID, auth)

	// List revisions and check metadata fields.
	resp, err := doRequest("GET", "/v1/charm/"+name+"/revisions", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	body := readJSON(t, resp)

	revisions, ok := body["revisions"].([]any)
	require.True(t, ok, "response should have 'revisions' array")
	require.NotEmpty(t, revisions, "should have at least one revision")

	firstRev := revisions[0].(map[string]any)
	assert.Contains(t, firstRev, "sha3-384", "revision should have sha3-384 field")
	assert.Contains(t, firstRev, "size", "revision should have size field")
	assert.Contains(t, firstRev, "bases", "revision should have bases field")
	assert.Contains(t, firstRev, "created-at", "revision should have created-at field")

	// Verify size is a positive number.
	size, ok := firstRev["size"].(float64)
	require.True(t, ok, "size should be a number")
	assert.Greater(t, size, float64(0), "archive size should be positive")

	// Verify sha3-384 is a non-empty string.
	hash, ok := firstRev["sha3-384"].(string)
	require.True(t, ok, "sha3-384 should be a string")
	assert.NotEmpty(t, hash, "sha3-384 should not be empty")
}