//go:build integration

package integration_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ACL-01: Owner can access their own private package.
func TestACL01_OwnerCanAccessOwnPrivatePackage(t *testing.T) {
	auth := devAuthHeader("acl-owner-1", "owner1")
	name := uniqueName("private-pkg")
	pkgID := mustRegisterWithType(t, name, "charm", true, auth)
	require.NotEmpty(t, pkgID, "package ID should not be empty")

	// GET should succeed for the owner.
	resp, err := doRequest("GET", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	meta, ok := body["metadata"].(map[string]any)
	require.True(t, ok, "response should have metadata field")
	assert.Equal(t, name, meta["name"], "package name should match")
	assert.Equal(t, true, meta["private"], "package should be private")
}

// ACL-02: Different user gets 403 Forbidden on private package GET.
func TestACL02_DifferentUserGets403OnPrivatePackage(t *testing.T) {
	ownerAuth := devAuthHeader("acl-owner-2", "owner2")
	name := uniqueName("private-pkg")
	mustRegisterWithType(t, name, "charm", true, ownerAuth)

	otherAuth := devAuthHeader("acl-other-2", "other2")
	resp, err := doRequest("GET", "/v1/charm/"+name, "", otherAuth)
	require.NoError(t, err)
	assertStatusCode(t, resp, http.StatusForbidden)
}

// ACL-03: Private package hidden from different user's package list.
func TestACL03_PrivatePackageHiddenFromOtherUserList(t *testing.T) {
	ownerAuth := devAuthHeader("acl-owner-3", "owner3")
	name := uniqueName("private-pkg")
	mustRegisterWithType(t, name, "charm", true, ownerAuth)

	otherAuth := devAuthHeader("acl-other-3", "other3")
	resp, err := doRequest("GET", "/v1/charm", "", otherAuth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	results, ok := body["results"].([]any)
	require.True(t, ok, "response should have results array")

	for _, r := range results {
		pkg, _ := r.(map[string]any)
		assert.NotEqual(t, name, pkg["name"], "private package should not appear in other user's list")
	}
}

// ACL-04: Different user can access public package from another user.
func TestACL04_DifferentUserCanAccessPublicPackage(t *testing.T) {
	ownerAuth := devAuthHeader("acl-owner-4", "owner4")
	name := uniqueName("public-pkg")
	mustRegister(t, name, ownerAuth) // public by default

	otherAuth := devAuthHeader("acl-other-4", "other4")
	resp, err := doRequest("GET", "/v1/charm/"+name, "", otherAuth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	meta, ok := body["metadata"].(map[string]any)
	require.True(t, ok, "response should have metadata field")
	assert.Equal(t, name, meta["name"], "public package should be visible to other users")
}

// ACL-05: Unauthenticated access returns 401.
func TestACL05_UnauthenticatedAccessReturns401(t *testing.T) {
	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/v1/charm"},
		{"POST", "/v1/charm"},
		{"GET", "/v1/charm/nonexistent"},
		{"PATCH", "/v1/charm/nonexistent"},
		{"DELETE", "/v1/charm/nonexistent"},
		{"GET", "/v1/whoami"},
		{"GET", "/v2/charms/find"},
		{"GET", "/v2/charms/info/nonexistent"},
		// Note: GET /v1/tokens and POST /v1/tokens are intentionally
		// excluded — they implement the charmcraft OIDC bootstrap protocol
		// and must return 200/201 (with oidc-login-required / auto-provision)
		// rather than 401 for unauthenticated requests.
	}

	for _, ep := range endpoints {
		t.Run(fmt.Sprintf("%s %s", ep.method, ep.path), func(t *testing.T) {
			resp, err := doRequest(ep.method, ep.path, "", "")
			require.NoError(t, err)
			assertStatusCode(t, resp, http.StatusUnauthorized)
		})
	}
}

// ACL-06: Owner can list, patch, delete their own private package.
func TestACL06_OwnerFullCRUDOnPrivatePackage(t *testing.T) {
	auth := devAuthHeader("acl-owner-6", "owner6")
	name := uniqueName("private-crud")
	pkgID := mustRegisterWithType(t, name, "charm", true, auth)
	require.NotEmpty(t, pkgID)

	// List: should include the private package.
	resp, err := doRequest("GET", "/v1/charm", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	body := readJSON(t, resp)
	results, _ := body["results"].([]any)
	found := false
	for _, r := range results {
		pkg, _ := r.(map[string]any)
		if pkg["name"] == name {
			found = true
		}
	}
	assert.True(t, found, "owner should see their own private package in list")

	// Patch: update summary.
	patchBody := `{"summary":"updated summary","description":"updated description"}`
	resp, err = doRequest("PATCH", "/v1/charm/"+name, patchBody, auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)

	// Delete.
	resp, err = doRequest("DELETE", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	delBody := readJSON(t, resp)
	assert.Equal(t, pkgID, delBody["package-id"], "delete should return package-id")
}

// ACL-07: Different user cannot release to private package.
func TestACL07_DifferentUserCannotReleaseToPrivatePackage(t *testing.T) {
	ownerAuth := devAuthHeader("acl-owner-7", "owner7")
	name := uniqueName("private-pkg")
	mustRegisterWithType(t, name, "charm", true, ownerAuth)

	// Owner uploads and pushes a revision so there's something to release.
	archive := buildTestCharmArchive(t, name)
	uploadID := mustUploadCharm(t, archive, name+".charm", ownerAuth)
	mustPushRevision(t, name, uploadID, ownerAuth)

	// Other user tries to release.
	otherAuth := devAuthHeader("acl-other-7", "other7")
	releaseBody := `[{"revision":1,"channel":"latest/stable"}]`
	resp, err := doRequest("POST", "/v1/charm/"+name+"/releases", releaseBody, otherAuth)
	require.NoError(t, err)
	assertStatusCode(t, resp, http.StatusForbidden)
}
