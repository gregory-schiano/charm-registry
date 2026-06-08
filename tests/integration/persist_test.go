//go:build integration

package integration_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PERSIST-01: Data survives service restart.
// This test verifies that registered packages and tokens persist across
// a charm-registry service restart. A full restart check is orchestrated by
// the deployment-specific suite (Juju unit restart, snap restart, or local
// process manager restart), then re-runs the verification subset.
//
// When run as a single go test invocation, this test can only verify that
// data written earlier in the same session is still readable. A full restart
// persistence test requires external orchestration.

// PERSIST-01a: Package registered in one test is visible later.
func TestPERSIST01a_PackageDataPersists(t *testing.T) {
	auth := devAuthHeader("persist-user-1", "persister1")
	name := uniqueName("persist-pkg")
	pkgID := mustRegister(t, name, auth)
	require.NotEmpty(t, pkgID)

	// Read it back.
	resp, err := doRequest("GET", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	meta, ok := body["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, name, meta["name"])
	assert.Equal(t, pkgID, meta["id"])
}

// PERSIST-01b: Token issued in one test is visible later.
func TestPERSIST01b_TokenDataPersists(t *testing.T) {
	auth := devAuthHeader("persist-user-2", "persister2")

	// Issue a token.
	_, sessionID := mustIssueToken(t, auth, `{"description":"persistence-test"}`)
	require.NotEmpty(t, sessionID)

	// List tokens and verify it's there.
	tokens := mustListTokens(t, auth)
	found := false
	for _, tok := range tokens {
		sid, _ := tok["session-id"].(string)
		if sid == sessionID {
			found = true
		}
	}
	assert.True(t, found, "issued token should appear in token list")
}

// PERSIST-02: Revision data persists after upload.
func TestPERSIST02_RevisionDataPersists(t *testing.T) {
	auth := devAuthHeader("persist-user-3", "persister3")
	name := uniqueName("persist-rev")
	mustRegister(t, name, auth)

	// Upload and push a revision.
	archive := buildTestCharmArchive(t, name)
	uploadID := mustUploadCharm(t, archive, name+".charm", auth)
	mustPushRevision(t, name, uploadID, auth)

	// List revisions and verify.
	resp, err := doRequest("GET", "/v1/charm/"+name+"/revisions", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	revisions, ok := body["revisions"].([]any)
	require.True(t, ok, "response should have revisions array")
	assert.NotEmpty(t, revisions, "should have at least one revision")

	rev0, _ := revisions[0].(map[string]any)
	assert.Equal(t, float64(1), rev0["revision"], "first revision should be revision 1")
}

// PERSIST-03: Release data persists after creation.
func TestPERSIST03_ReleaseDataPersists(t *testing.T) {
	auth := devAuthHeader("persist-user-4", "persister4")
	name := uniqueName("persist-rel")
	mustRegister(t, name, auth)

	archive := buildTestCharmArchive(t, name)
	uploadID := mustUploadCharm(t, archive, name+".charm", auth)
	mustPushRevision(t, name, uploadID, auth)
	mustRelease(t, name, 1, "latest/stable", auth)

	// List releases and verify.
	resp, err := doRequest("GET", "/v1/charm/"+name+"/releases", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	released, ok := body["released"].([]any)
	require.True(t, ok, "response should have released array")
	assert.NotEmpty(t, released, "should have at least one release")
}

// PERSIST-04: Database-backed data visible via v2 info after registration and release.
func TestPERSIST04_V2InfoReflectsPersistedData(t *testing.T) {
	auth := devAuthHeader("persist-user-5", "persister5")
	name := uniqueName("persist-info")
	mustRegister(t, name, auth)

	archive := buildTestCharmArchive(t, name)
	uploadID := mustUploadCharm(t, archive, name+".charm", auth)
	mustPushRevision(t, name, uploadID, auth)
	mustRelease(t, name, 1, "latest/stable", auth)

	// v2 info should reflect the release.
	resp, err := doRequest("GET", "/v2/charms/info/"+name, "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	assert.Equal(t, name, body["name"], "v2 info should return the charm name")
}
