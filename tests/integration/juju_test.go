//go:build integration

package integration_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// JUJU-01: Charm download endpoint returns charm archive.
func TestJUJU01_CharmDownloadEndpoint(t *testing.T) {
	auth := devAuthHeader("juju-user-1", "jujuuser1")
	name := uniqueName("juju-charm")
	pkgID := mustRegister(t, name, auth)
	require.NotEmpty(t, pkgID)

	// Upload and push a revision.
	archive := buildTestCharmArchive(t, name)
	uploadID := mustUploadCharm(t, archive, name+".charm", auth)
	mustPushRevision(t, name, uploadID, auth)

	// Download via /api/v1/charms/download/{pkgID}_1.charm
	downloadPath := "/api/v1/charms/download/" + pkgID + "_1.charm"
	resp, err := doRequest("GET", downloadPath, "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)

	// Verify Content-Disposition header.
	disp := resp.Header.Get("Content-Disposition")
	assert.Contains(t, disp, "attachment", "download should have Content-Disposition attachment")

	// Read body and verify it's non-empty (charm archive).
	bodyBytes, err := readAllBytes(t, resp)
	require.NoError(t, err)
	assert.True(t, len(bodyBytes) > 0, "downloaded charm archive should be non-empty")

	// Verify size matches original.
	assert.Equal(t, len(archive), len(bodyBytes), "downloaded charm size should match uploaded archive")
}

// JUJU-02: Charm download for nonexistent revision returns 404.
func TestJUJU02_CharmDownloadNonexistentRevision404(t *testing.T) {
	auth := devAuthHeader("juju-user-2", "jujuuser2")
	name := uniqueName("juju-charm")
	pkgID := mustRegister(t, name, auth)

	// Try to download revision 99 (doesn't exist).
	downloadPath := "/api/v1/charms/download/" + pkgID + "_99.charm"
	resp, err := doRequest("GET", downloadPath, "", auth)
	require.NoError(t, err)
	assertStatusCode(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

// JUJU-03: Charm download without auth returns 401.
func TestJUJU03_CharmDownloadWithoutAuth401(t *testing.T) {
	downloadPath := "/api/v1/charms/download/nonexistent_1.charm"
	resp, err := doRequest("GET", downloadPath, "", "")
	require.NoError(t, err)
	assertStatusCode(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

// JUJU-04: Charm download for different user's public charm succeeds.
func TestJUJU04_DownloadPublicCharmAsOtherUser(t *testing.T) {
	ownerAuth := devAuthHeader("juju-owner-4", "jujuowner4")
	name := uniqueName("juju-public")
	pkgID := mustRegister(t, name, ownerAuth)

	archive := buildTestCharmArchive(t, name)
	uploadID := mustUploadCharm(t, archive, name+".charm", ownerAuth)
	mustPushRevision(t, name, uploadID, ownerAuth)

	// Different authenticated user can download a public charm.
	otherAuth := devAuthHeader("juju-other-4", "jujuother4")
	downloadPath := "/api/v1/charms/download/" + pkgID + "_1.charm"
	resp, err := doRequest("GET", downloadPath, "", otherAuth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	resp.Body.Close()
}

// JUJU-05: Unscanned upload endpoint works for charm push flow.
func TestJUJU05_UnscannedUploadEndpoint(t *testing.T) {
	auth := devAuthHeader("juju-user-5", "jujuuser5")
	name := uniqueName("juju-upload")

	// Verify the unscanned-upload endpoint exists and returns proper response.
	archive := buildTestCharmArchive(t, name)
	result := uploadMultipart(t, "/unscanned-upload/", "binary", name+".charm", archive, auth)

	assert.Equal(t, true, result["successful"], "upload should be successful")
	uploadID, ok := result["upload_id"].(string)
	assert.True(t, ok, "response should have upload_id field")
	assert.NotEmpty(t, uploadID, "upload_id should not be empty")
}