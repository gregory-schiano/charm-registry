//go:build integration

package integration_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// V2-01: Find returns registered packages matching query.
// Register a charm, upload, push revision, release, then search for it via
// GET /v2/charms/find?q=<prefix> and verify it appears in results.
func TestV201_FindReturnsRegisteredPackagesMatchingQuery(t *testing.T) {
	auth := devAuthHeader("v2find", "v2find")
	name := uniqueName("find-charm")

	// Register, upload, push, and release so the package is published.
	mustRegister(t, name, auth)
	archive := buildTestCharmArchive(t, name)
	uploadID := mustUploadCharm(t, archive, name+".zip", auth)
	mustPushRevision(t, name, uploadID, auth)

	// Get the revision number.
	resp, err := doRequest("GET", "/v1/charm/"+name+"/revisions", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	revBody := readJSON(t, resp)
	revisions := revBody["revisions"].([]any)
	revNum := int(revisions[0].(map[string]any)["revision"].(float64))

	mustRelease(t, name, revNum, "latest/stable", auth)

	// Search by name prefix.
	resp, err = doRequest("GET", "/v2/charms/find?q=itest-find-charm", "", auth)
	require.NoError(t, err)
	defer resp.Body.Close()
	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	results, ok := body["results"].([]any)
	require.True(t, ok, "response should have 'results' array")

	// Find our package in the results.
	var found map[string]any
	for _, r := range results {
		item, _ := r.(map[string]any)
		if itemName, _ := item["name"].(string); itemName == name {
			found = item
			break
		}
	}
	require.NotNil(t, found, "registered package '%s' should appear in find results", name)

	// Verify key fields.
	assert.Equal(t, name, found["name"], "find result name should match")
	assert.NotEmpty(t, found["id"], "find result should have 'id'")
	assert.Equal(t, "charm", found["type"], "find result type should be charm")

	// Verify default-release is present (since we released to latest/stable).
	defaultRelease, ok := found["default-release"].(map[string]any)
	require.True(t, ok, "find result should have 'default-release' object")
	assert.Contains(t, defaultRelease, "channel", "default-release should have 'channel'")
	assert.Contains(t, defaultRelease, "revision", "default-release should have 'revision'")
}

// V2-02: Find with empty query returns all or empty results.
// An empty query string should still return a valid response (possibly empty).
func TestV202_FindWithEmptyQueryReturnsValidResponse(t *testing.T) {
	auth := devAuthHeader("v2findempty", "v2findempty")

	resp, err := doRequest("GET", "/v2/charms/find?q=", "", auth)
	require.NoError(t, err)
	defer resp.Body.Close()
	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	results, ok := body["results"].([]any)
	require.True(t, ok, "response should have 'results' array")
	// Empty query may return all visible packages or an empty list;
	// either is valid — we just verify the response structure is correct.
	assert.NotNil(t, results, "results should not be nil")
}

// V2-03: Info returns package details including channels.
// Register, publish, then call GET /v2/charms/info/{name} and verify
// the info object contains name, id, type, default-release, and channel-map.
func TestV203_InfoReturnsPackageDetailsIncludingChannels(t *testing.T) {
	auth := devAuthHeader("v2info", "v2info")
	name := uniqueName("info-charm")

	// Register, upload, push, and release.
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

	mustRelease(t, name, revNum, "latest/stable", auth)

	// Fetch info.
	resp, err = doRequest("GET", "/v2/charms/info/"+name, "", auth)
	require.NoError(t, err)
	defer resp.Body.Close()
	requireStatusCode(t, resp, http.StatusOK)

	info := readJSON(t, resp)

	// Verify top-level fields.
	assert.Equal(t, name, info["name"], "info name should match")
	assert.NotEmpty(t, info["id"], "info should have 'id'")
	assert.Equal(t, "charm", info["type"], "info type should be charm")

	// Verify default-release present with channel info.
	defaultRelease, ok := info["default-release"].(map[string]any)
	require.True(t, ok, "info should have 'default-release' object")
	channel, ok := defaultRelease["channel"].(map[string]any)
	require.True(t, ok, "default-release should have 'channel' object")
	assert.Equal(t, "latest/stable", channel["name"], "default-release channel should be latest/stable")
	assert.Equal(t, "latest", channel["track"], "channel track should be 'latest'")
	assert.Equal(t, "stable", channel["risk"], "channel risk should be 'stable'")

	// Verify channel-map present (should contain at least one entry for latest/stable).
	channelMap, ok := info["channel-map"].([]any)
	require.True(t, ok, "info should have 'channel-map' array")
	require.NotEmpty(t, channelMap, "channel-map should have at least one entry")

	// Verify result object with metadata.
	result, ok := info["result"].(map[string]any)
	require.True(t, ok, "info should have 'result' object")
	assert.Contains(t, result, "summary", "result should contain 'summary'")
	assert.Contains(t, result, "publisher", "result should contain 'publisher'")
}

// V2-04: Info for specific channel returns revision info.
// Release to latest/edge, then call GET /v2/charms/info/{name}?channel=latest/edge
// and verify the response reflects the edge channel revision.
func TestV204_InfoForSpecificChannelReturnsRevisionInfo(t *testing.T) {
	auth := devAuthHeader("v2chan", "v2chan")
	name := uniqueName("chan-info-charm")

	// Register, upload, push, and release to both stable and edge.
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

	mustRelease(t, name, revNum, "latest/stable", auth)
	mustRelease(t, name, revNum, "latest/edge", auth)

	// Fetch info for the edge channel.
	resp, err = doRequest("GET", "/v2/charms/info/"+name+"?channel=latest/edge", "", auth)
	require.NoError(t, err)
	defer resp.Body.Close()
	requireStatusCode(t, resp, http.StatusOK)

	info := readJSON(t, resp)

	// Verify the default-release reflects the requested channel.
	defaultRelease, ok := info["default-release"].(map[string]any)
	require.True(t, ok, "info should have 'default-release'")
	channel, ok := defaultRelease["channel"].(map[string]any)
	require.True(t, ok, "default-release should have 'channel'")
	assert.Equal(t, "latest/edge", channel["name"], "default-release channel should be latest/edge")
	assert.Equal(t, "edge", channel["risk"], "channel risk should be 'edge'")

	// Verify revision number in the default-release.
	revisionObj, ok := defaultRelease["revision"].(map[string]any)
	require.True(t, ok, "default-release should have 'revision' object")
	assert.Equal(t, float64(revNum), revisionObj["revision"], "revision number should match")
}

// V2-05: Info for nonexistent package returns 404.
func TestV205_InfoForNonexistentPackageReturnsNotFound(t *testing.T) {
	auth := devAuthHeader("v2noexist", "v2noexist")

	resp, err := doRequest("GET", "/v2/charms/info/nonexistent-charm", "", auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	assertStatusCode(t, resp, http.StatusNotFound)
}

// V2-06: Refresh resolves a known package.
// Register and publish a charm, then POST /v2/charms/refresh with
// an action referencing the charm name and channel, and verify the
// response includes the charm details.
func TestV206_RefreshResolvesKnownPackage(t *testing.T) {
	auth := devAuthHeader("v2refresh", "v2refresh")
	name := uniqueName("refresh-charm")

	// Register, upload, push, and release.
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

	mustRelease(t, name, revNum, "latest/stable", auth)

	// Refresh request body.
	refreshBody := fmt.Sprintf(`{
		"context": [],
		"actions": [{
			"action": "refresh",
			"instance-key": "itest-unit/0",
			"name": "%s",
			"channel": "latest/stable"
		}]
	}`, name)

	resp, err = doRequest("POST", "/v2/charms/refresh", refreshBody, auth)
	require.NoError(t, err)
	defer resp.Body.Close()
	requireStatusCode(t, resp, http.StatusOK)

	result := readJSON(t, resp)

	// Verify top-level response structure.
	errorList, ok := result["error-list"].([]any)
	require.True(t, ok, "response should have 'error-list'")
	assert.Empty(t, errorList, "error-list should be empty on success")

	results, ok := result["results"].([]any)
	require.True(t, ok, "response should have 'results' array")
	require.Len(t, results, 1, "should have exactly 1 result")

	actionResult := results[0].(map[string]any)

	// Verify the action result fields.
	assert.Equal(t, "itest-unit/0", actionResult["instance-key"], "instance-key should match request")
	assert.Equal(t, name, actionResult["name"], "name should match request")
	assert.Equal(t, "refresh", actionResult["result"], "result should be 'refresh'")
	assert.NotEmpty(t, actionResult["id"], "result should have 'id'")

	// Verify the charm object is present with key fields.
	charm, ok := actionResult["charm"].(map[string]any)
	require.True(t, ok, "result should have 'charm' object")
	assert.Equal(t, name, charm["name"], "charm name should match")
	assert.NotEmpty(t, charm["id"], "charm should have 'id'")
	assert.NotEmpty(t, charm["revision"], "charm should have 'revision'")
	assert.Contains(t, charm, "download", "charm should have 'download'")
}

// V2-07: No auth returns 401 on all v2 endpoints.
// All v2 endpoints require authentication; verify that requests without
// an Authorization header receive a 401 Unauthorized response.
func TestV207_NoAuthReturns401OnAllV2Endpoints(t *testing.T) {
	// Table-driven test for all v2 endpoints.
	endpoints := []struct {
		method string
		path   string
		body   string
	}{
		{"GET", "/v2/charms/find?q=test", ""},
		{"GET", "/v2/charms/info/some-charm", ""},
		{"POST", "/v2/charms/refresh", `{"context":[],"actions":[{"action":"refresh","instance-key":"u/0","name":"x","channel":"latest/stable"}]}`},
		{"GET", "/v2/charms/resources/some-charm/config/revisions", ""},
	}

	for _, ep := range endpoints {
		t.Run(strings.ReplaceAll(ep.method+"_"+ep.path, "/", "_"), func(t *testing.T) {
			resp, err := doRequest(ep.method, ep.path, ep.body, "")
			require.NoError(t, err, "%s %s request should not fail", ep.method, ep.path)
			defer resp.Body.Close()
			assertStatusCode(t, resp, http.StatusUnauthorized)
		})
	}
}
