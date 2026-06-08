//go:build integration

package integration_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- PKG-01: Register a charm package ---

func TestPKG01_RegisterCharm(t *testing.T) {
	auth := devAuthHeader("alice", "alice")
	name := uniqueName("charm-register")

	resp, err := doRequest("POST", "/v1/charm",
		fmt.Sprintf(`{"name":"%s","type":"charm"}`, name), auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	requireStatusCode(t, resp, http.StatusCreated)

	body := readJSON(t, resp)
	id, ok := body["id"].(string)
	require.True(t, ok, "response should have string 'id' field")
	assert.NotEmpty(t, id, "package ID should not be empty")
}

// --- PKG-02: Register a bundle package ---

func TestPKG02_RegisterBundle(t *testing.T) {
	auth := devAuthHeader("alice", "alice")
	name := uniqueName("bundle-register")

	resp, err := doRequest("POST", "/v1/charm",
		fmt.Sprintf(`{"name":"%s","type":"bundle"}`, name), auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	requireStatusCode(t, resp, http.StatusCreated)

	body := readJSON(t, resp)
	id, ok := body["id"].(string)
	require.True(t, ok, "response should have string 'id' field")
	assert.NotEmpty(t, id, "bundle ID should not be empty")

	// Verify the type is "bundle" by fetching the package.
	resp2, err := doRequest("GET", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	defer resp2.Body.Close()
	requireStatusCode(t, resp2, http.StatusOK)

	body2 := readJSON(t, resp2)
	metadata, ok := body2["metadata"].(map[string]any)
	require.True(t, ok, "response should have 'metadata' object")
	assert.Equal(t, "bundle", metadata["type"], "package type should be bundle")
}

// --- PKG-03: Register package with private=true ---

func TestPKG03_RegisterPrivatePackage(t *testing.T) {
	auth := devAuthHeader("alice", "alice")
	name := uniqueName("private-pkg")

	resp, err := doRequest("POST", "/v1/charm",
		fmt.Sprintf(`{"name":"%s","type":"charm","private":true}`, name), auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	requireStatusCode(t, resp, http.StatusCreated)

	// Fetch the package and verify private=true.
	resp2, err := doRequest("GET", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	defer resp2.Body.Close()
	requireStatusCode(t, resp2, http.StatusOK)

	body := readJSON(t, resp2)
	metadata, ok := body["metadata"].(map[string]any)
	require.True(t, ok, "response should have 'metadata' object")
	private, ok := metadata["private"].(bool)
	require.True(t, ok, "metadata should have boolean 'private' field")
	assert.True(t, private, "package should be private")
}

// --- PKG-04: Register package defaults type to "charm" ---

func TestPKG04_RegisterDefaultsToCharmType(t *testing.T) {
	auth := devAuthHeader("alice", "alice")
	name := uniqueName("default-type")

	// Omit "type" field; should default to "charm".
	resp, err := doRequest("POST", "/v1/charm",
		fmt.Sprintf(`{"name":"%s"}`, name), auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	requireStatusCode(t, resp, http.StatusCreated)

	// Fetch and verify type is "charm".
	resp2, err := doRequest("GET", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	defer resp2.Body.Close()
	requireStatusCode(t, resp2, http.StatusOK)

	body := readJSON(t, resp2)
	metadata, ok := body["metadata"].(map[string]any)
	require.True(t, ok, "response should have 'metadata' object")
	assert.Equal(t, "charm", metadata["type"], "default type should be charm")
}

// --- PKG-05: Duplicate registration returns 409 Conflict ---

func TestPKG05_RegisterDuplicateReturnsConflict(t *testing.T) {
	auth := devAuthHeader("alice", "alice")
	name := uniqueName("dup-pkg")

	// First registration succeeds.
	mustRegister(t, name, auth)

	// Second registration with same name fails.
	resp, err := doRequest("POST", "/v1/charm",
		fmt.Sprintf(`{"name":"%s","type":"charm"}`, name), auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	assertStatusCode(t, resp, http.StatusConflict)
}

// --- PKG-06: Registration with invalid name returns 400 ---

func TestPKG06_RegisterInvalidNameReturnsBadRequest(t *testing.T) {
	auth := devAuthHeader("alice", "alice")

	resp, err := doRequest("POST", "/v1/charm",
		`{"name":"   ","type":"charm"}`, auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	assertStatusCode(t, resp, http.StatusBadRequest)
}

// --- PKG-07: Registration without auth returns 401 ---

func TestPKG07_RegisterWithoutAuthReturnsUnauthorized(t *testing.T) {
	name := uniqueName("noauth-pkg")

	resp, err := doRequest("POST", "/v1/charm",
		fmt.Sprintf(`{"name":"%s","type":"charm"}`, name), "")
	require.NoError(t, err)
	defer resp.Body.Close()

	assertStatusCode(t, resp, http.StatusUnauthorized)
}

// --- PKG-08: List packages ---

func TestPKG08_ListPackages(t *testing.T) {
	auth := devAuthHeader("lister", "lister")
	name1 := uniqueName("list-pkg-1")
	name2 := uniqueName("list-pkg-2")

	// Register two packages.
	mustRegister(t, name1, auth)
	mustRegister(t, name2, auth)

	resp, err := doRequest("GET", "/v1/charm", "", auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	results, ok := body["results"].([]any)
	require.True(t, ok, "response should have 'results' array")

	// Should contain at least the two packages we just registered.
	found := 0
	for _, r := range results {
		pkg, ok := r.(map[string]any)
		require.True(t, ok, "each result should be a JSON object")
		pkgName, _ := pkg["name"].(string)
		if pkgName == name1 || pkgName == name2 {
			found++
		}
	}
	assert.Equal(t, 2, found, "list should contain both registered packages")
}

// --- PKG-09: List packages without auth returns 401 ---

func TestPKG09_ListPackagesWithoutAuthReturnsUnauthorized(t *testing.T) {
	resp, err := doRequest("GET", "/v1/charm", "", "")
	require.NoError(t, err)
	defer resp.Body.Close()

	assertStatusCode(t, resp, http.StatusUnauthorized)
}

// --- PKG-10: List packages returns expected metadata fields ---

func TestPKG10_ListPackagesMetadataFields(t *testing.T) {
	auth := devAuthHeader("alice", "alice")
	name := uniqueName("list-fields")

	mustRegister(t, name, auth)

	resp, err := doRequest("GET", "/v1/charm", "", auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	results, ok := body["results"].([]any)
	require.True(t, ok, "response should have 'results' array")

	// Find our package in the list.
	var found map[string]any
	for _, r := range results {
		pkg, _ := r.(map[string]any)
		if pkgName, _ := pkg["name"].(string); pkgName == name {
			found = pkg
			break
		}
	}
	require.NotNil(t, found, "registered package should appear in list")

	// Verify expected metadata fields are present.
	assert.NotEmpty(t, found["id"], "package should have 'id'")
	assert.Equal(t, name, found["name"], "package name should match")
	assert.Contains(t, []string{"charm", "bundle"}, found["type"], "package should have valid type")
	assert.NotEmpty(t, found["status"], "package should have 'status'")
	assert.Contains(t, found, "private", "package should have 'private' field")
	assert.Contains(t, found, "publisher", "package should have 'publisher' field")
	assert.Contains(t, found, "tracks", "package should have 'tracks' field")
}

// --- PKG-11: Get a single package ---

func TestPKG11_GetPackage(t *testing.T) {
	auth := devAuthHeader("getter", "getter")
	name := uniqueName("get-pkg")

	id := mustRegister(t, name, auth)

	resp, err := doRequest("GET", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	metadata, ok := body["metadata"].(map[string]any)
	require.True(t, ok, "response should have 'metadata' object")

	assert.Equal(t, id, metadata["id"], "package ID should match")
	assert.Equal(t, name, metadata["name"], "package name should match")
	assert.Equal(t, "charm", metadata["type"], "package type should be charm")
	assert.Equal(t, "registered", metadata["status"], "package status should be registered")
}

// --- PKG-12: Get package includes all metadata fields ---

func TestPKG12_GetPackageMetadataFields(t *testing.T) {
	auth := devAuthHeader("alice", "alice")
	name := uniqueName("meta-fields")

	mustRegister(t, name, auth)

	resp, err := doRequest("GET", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	metadata, ok := body["metadata"].(map[string]any)
	require.True(t, ok, "response should have 'metadata' object")

	// Verify all documented metadata fields are present.
	expectedFields := []string{
		"id", "name", "type", "status", "private",
		"publisher", "tracks", "default-track",
		"summary", "description", "title",
		"website", "contact", "authority", "store",
		"links", "media", "track-guardrails",
	}
	for _, field := range expectedFields {
		assert.Contains(t, metadata, field, "metadata should contain field '%s'", field)
	}
}

// --- PKG-13: Get nonexistent package returns 404 ---

func TestPKG13_GetNonexistentPackageReturnsNotFound(t *testing.T) {
	auth := devAuthHeader("alice", "alice")

	resp, err := doRequest("GET", "/v1/charm/nonexistent-package", "", auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	assertStatusCode(t, resp, http.StatusNotFound)
}

// --- PKG-14: Get package without auth returns 401 ---

func TestPKG14_GetPackageWithoutAuthReturnsUnauthorized(t *testing.T) {
	resp, err := doRequest("GET", "/v1/charm/some-package", "", "")
	require.NoError(t, err)
	defer resp.Body.Close()

	assertStatusCode(t, resp, http.StatusUnauthorized)
}

// --- PKG-15: Private package is forbidden for different user ---

func TestPKG15_PrivatePackageForbiddenForDifferentUser(t *testing.T) {
	ownerAuth := devAuthHeader("owner", "owner")
	otherAuth := devAuthHeader("other", "other")
	name := uniqueName("private-access")

	mustRegisterWithType(t, name, "charm", true, ownerAuth)

	// Other user cannot access the private package.
	resp, err := doRequest("GET", "/v1/charm/"+name, "", otherAuth)
	require.NoError(t, err)
	defer resp.Body.Close()

	assertStatusCode(t, resp, http.StatusForbidden)
}

// --- PKG-16: Patch package metadata ---

func TestPKG16_PatchPackageMetadata(t *testing.T) {
	auth := devAuthHeader("patcher", "patcher")
	name := uniqueName("patch-pkg")

	mustRegister(t, name, auth)

	// Patch the package metadata.
	patchBody := `{"summary":"Updated summary","description":"Updated description","title":"New Title","website":"https://example.com","contact":"test@example.com"}`
	resp, err := doRequest("PATCH", "/v1/charm/"+name, patchBody, auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	metadata, ok := body["metadata"].(map[string]any)
	require.True(t, ok, "response should have 'metadata' object")

	assert.Equal(t, "Updated summary", metadata["summary"], "summary should be updated")
	assert.Equal(t, "Updated description", metadata["description"], "description should be updated")
	assert.Equal(t, "New Title", metadata["title"], "title should be updated")
	assert.Equal(t, "https://example.com", metadata["website"], "website should be updated")
	assert.Equal(t, "test@example.com", metadata["contact"], "contact should be updated")
}

// --- PKG-17: Patch package persists across GET ---

func TestPKG17_PatchPackagePersistsAcrossGet(t *testing.T) {
	auth := devAuthHeader("alice", "alice")
	name := uniqueName("patch-persist")

	mustRegister(t, name, auth)

	// Patch the summary.
	patchBody := `{"summary":"Persisted summary"}`
	resp, err := doRequest("PATCH", "/v1/charm/"+name, patchBody, auth)
	require.NoError(t, err)
	resp.Body.Close()
	requireStatusCode(t, resp, http.StatusOK)

	// GET the package and verify the patch persisted.
	resp2, err := doRequest("GET", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	defer resp2.Body.Close()
	requireStatusCode(t, resp2, http.StatusOK)

	body := readJSON(t, resp2)
	metadata, ok := body["metadata"].(map[string]any)
	require.True(t, ok, "response should have 'metadata' object")
	assert.Equal(t, "Persisted summary", metadata["summary"], "patched summary should persist")
}

// --- PKG-18: Patch nonexistent package returns 404 ---

func TestPKG18_PatchNonexistentPackageReturnsNotFound(t *testing.T) {
	auth := devAuthHeader("alice", "alice")

	resp, err := doRequest("PATCH", "/v1/charm/nonexistent-pkg",
		`{"summary":"x"}`, auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	assertStatusCode(t, resp, http.StatusNotFound)
}

// --- PKG-19: Patch package without auth returns 401 ---

func TestPKG19_PatchPackageWithoutAuthReturnsUnauthorized(t *testing.T) {
	resp, err := doRequest("PATCH", "/v1/charm/some-pkg",
		`{"summary":"x"}`, "")
	require.NoError(t, err)
	defer resp.Body.Close()

	assertStatusCode(t, resp, http.StatusUnauthorized)
}

// --- PKG-20: Delete package ---

func TestPKG20_DeletePackage(t *testing.T) {
	auth := devAuthHeader("deleter", "deleter")
	name := uniqueName("delete-pkg")

	mustRegister(t, name, auth)

	resp, err := doRequest("DELETE", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	pkgID, ok := body["package-id"].(string)
	require.True(t, ok, "response should have string 'package-id' field")
	assert.NotEmpty(t, pkgID, "package-id should not be empty")
}

// --- PKG-21: Deleted package returns 404 on GET ---

func TestPKG21_DeletedPackageReturnsNotFoundOnGet(t *testing.T) {
	auth := devAuthHeader("deleter2", "deleter2")
	name := uniqueName("deleted-get")

	mustRegister(t, name, auth)

	// Delete the package.
	resp, err := doRequest("DELETE", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	resp.Body.Close()
	requireStatusCode(t, resp, http.StatusOK)

	// GET should now return 404.
	resp2, err := doRequest("GET", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	defer resp2.Body.Close()

	assertStatusCode(t, resp2, http.StatusNotFound)
}

// --- PKG-22: Delete nonexistent package returns 404 ---

func TestPKG22_DeleteNonexistentPackageReturnsNotFound(t *testing.T) {
	auth := devAuthHeader("alice", "alice")

	resp, err := doRequest("DELETE", "/v1/charm/nonexistent-pkg", "", auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	assertStatusCode(t, resp, http.StatusNotFound)
}

// --- PKG-23: Delete package without auth returns 401 ---

func TestPKG23_DeletePackageWithoutAuthReturnsUnauthorized(t *testing.T) {
	resp, err := doRequest("DELETE", "/v1/charm/some-pkg", "", "")
	require.NoError(t, err)
	defer resp.Body.Close()

	assertStatusCode(t, resp, http.StatusUnauthorized)
}

// --- PKG-24: Full package CRUD lifecycle ---

func TestPKG24_PackageCRUDLifecycle(t *testing.T) {
	auth := devAuthHeader("lifecycle", "lifecycle")
	name := uniqueName("crud")

	// Create: register a package.
	id := mustRegister(t, name, auth)
	assert.NotEmpty(t, id, "registered package should have an ID")

	// Read: get the package.
	resp, err := doRequest("GET", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	body := readJSON(t, resp)
	metadata, ok := body["metadata"].(map[string]any)
	require.True(t, ok, "response should have 'metadata' object")
	assert.Equal(t, name, metadata["name"], "GET should return the correct package")
	assert.Equal(t, "registered", metadata["status"], "initial status should be 'registered'")

	// Update: patch the package.
	patchBody := `{"summary":"CRUD test summary","title":"CRUD Test Title"}`
	resp2, err := doRequest("PATCH", "/v1/charm/"+name, patchBody, auth)
	require.NoError(t, err)
	requireStatusCode(t, resp2, http.StatusOK)
	body2 := readJSON(t, resp2)
	patched, ok := body2["metadata"].(map[string]any)
	require.True(t, ok, "PATCH response should have 'metadata' object")
	assert.Equal(t, "CRUD test summary", patched["summary"], "summary should be patched")
	assert.Equal(t, "CRUD Test Title", patched["title"], "title should be patched")

	// Delete: remove the package.
	resp3, err := doRequest("DELETE", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp3, http.StatusOK)
	body3 := readJSON(t, resp3)
	deletedID, ok := body3["package-id"].(string)
	require.True(t, ok, "DELETE response should have 'package-id'")
	assert.Equal(t, id, deletedID, "deleted package ID should match the original")

	// Verify: GET after DELETE returns 404.
	resp4, err := doRequest("GET", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	defer resp4.Body.Close()
	assertStatusCode(t, resp4, http.StatusNotFound)
}

// --- PKG-25: Package appears in list after registration ---

func TestPKG25_PackageAppearsInListAfterRegistration(t *testing.T) {
	auth := devAuthHeader("listcheck", "listcheck")
	name := uniqueName("list-appear")

	mustRegister(t, name, auth)

	resp, err := doRequest("GET", "/v1/charm", "", auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	results, ok := body["results"].([]any)
	require.True(t, ok, "response should have 'results' array")

	found := false
	for _, r := range results {
		pkg, _ := r.(map[string]any)
		if pkgName, _ := pkg["name"].(string); pkgName == name {
			found = true
			break
		}
	}
	assert.True(t, found, "newly registered package should appear in list")
}

// --- PKG-26: Package removed from list after deletion ---

func TestPKG26_PackageRemovedFromListAfterDeletion(t *testing.T) {
	auth := devAuthHeader("listdel", "listdel")
	name := uniqueName("list-remove")

	mustRegister(t, name, auth)

	// Delete the package.
	resp, err := doRequest("DELETE", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	resp.Body.Close()
	requireStatusCode(t, resp, http.StatusOK)

	// List should not contain the deleted package.
	resp2, err := doRequest("GET", "/v1/charm", "", auth)
	require.NoError(t, err)
	defer resp2.Body.Close()
	requireStatusCode(t, resp2, http.StatusOK)

	body := readJSON(t, resp2)
	results, ok := body["results"].([]any)
	require.True(t, ok, "response should have 'results' array")

	for _, r := range results {
		pkg, _ := r.(map[string]any)
		pkgName, _ := pkg["name"].(string)
		assert.NotEqual(t, name, pkgName, "deleted package should not appear in list")
	}
}

// --- PKG-27: New package has "latest" default track ---

func TestPKG27_NewPackageHasLatestDefaultTrack(t *testing.T) {
	auth := devAuthHeader("trackcheck", "trackcheck")
	name := uniqueName("default-track")

	mustRegister(t, name, auth)

	resp, err := doRequest("GET", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	defer resp.Body.Close()
	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	metadata, ok := body["metadata"].(map[string]any)
	require.True(t, ok, "response should have 'metadata' object")

	defaultTrack, ok := metadata["default-track"].(string)
	require.True(t, ok, "metadata should have string 'default-track'")
	assert.Equal(t, "latest", defaultTrack, "default track should be 'latest'")

	tracks, ok := metadata["tracks"].([]any)
	require.True(t, ok, "metadata should have 'tracks' array")
	assert.NotEmpty(t, tracks, "package should have at least one track")
}

// --- PKG-28: Register with empty type defaults to charm ---

func TestPKG28_RegisterEmptyTypeDefaultsToCharm(t *testing.T) {
	auth := devAuthHeader("alice", "alice")
	name := uniqueName("empty-type")

	resp, err := doRequest("POST", "/v1/charm",
		fmt.Sprintf(`{"name":"%s","type":""}`, name), auth)
	require.NoError(t, err)
	defer resp.Body.Close()

	requireStatusCode(t, resp, http.StatusCreated)

	// Verify the type defaults to "charm".
	resp2, err := doRequest("GET", "/v1/charm/"+name, "", auth)
	require.NoError(t, err)
	defer resp2.Body.Close()
	requireStatusCode(t, resp2, http.StatusOK)

	body := readJSON(t, resp2)
	metadata, ok := body["metadata"].(map[string]any)
	require.True(t, ok, "response should have 'metadata' object")
	assert.Equal(t, "charm", metadata["type"], "empty type should default to charm")
}

// --- PKG-29: Patch can toggle private flag ---

func TestPKG29_PatchCanTogglePrivateFlag(t *testing.T) {
	auth := devAuthHeader("alice", "alice")
	name := uniqueName("toggle-private")

	// Register as public.
	mustRegisterWithType(t, name, "charm", false, auth)

	// Patch to private.
	resp, err := doRequest("PATCH", "/v1/charm/"+name,
		`{"private":true}`, auth)
	require.NoError(t, err)
	defer resp.Body.Close()
	requireStatusCode(t, resp, http.StatusOK)

	body := readJSON(t, resp)
	metadata, ok := body["metadata"].(map[string]any)
	require.True(t, ok, "response should have 'metadata' object")
	private, ok := metadata["private"].(bool)
	require.True(t, ok, "metadata should have boolean 'private' field")
	assert.True(t, private, "private flag should be true after patch")
}
