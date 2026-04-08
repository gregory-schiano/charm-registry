package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gschiano/charm-registry/internal/auth"
	"github.com/gschiano/charm-registry/internal/blob"
	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
)

func TestPublishInfoAndRefreshFlow(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("owner-1", "owner")

	// Act: register package
	pkg, err := svc.RegisterPackage(ctx, owner, "demo-charm", "charm", true)
	require.NoError(t, err)

	// Assert: private package not visible before release
	findResult, err := svc.Find(ctx, owner, "demo")
	require.NoError(t, err)
	assert.Len(t, findResult["results"], 0)

	// Act: upload and push revision
	upload, err := svc.CreateUpload(ctx, "demo-charm.charm", buildCharmArchive(t, "demo-charm"))
	require.NoError(t, err)
	statusURL, err := svc.PushRevision(ctx, owner, pkg.Name, PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	// Assert
	assert.Contains(t, statusURL, "/v1/charm/demo-charm/revisions/review")

	// Act: push resource
	resourceUpload, err := svc.CreateUpload(ctx, "config.yaml", []byte("debug: true\n"))
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, pkg.Name, "config", PushResourceRequest{
		UploadID: resourceUpload.ID,
		Type:     "file",
	})
	require.NoError(t, err)

	// Act: release to channel
	released, err := svc.Release(ctx, owner, pkg.Name, []core.Release{{
		Channel:  "latest/stable",
		Revision: 1,
		Resources: []core.ReleaseResourceRef{{
			Name:     "config",
			Revision: intPtr(1),
		}},
	}})
	require.NoError(t, err)
	// Assert
	assert.Len(t, released, 1)

	// Act: fetch package info
	info, err := svc.Info(ctx, owner, pkg.Name)
	require.NoError(t, err)

	// Assert: info reflects released revision and resources
	assert.Equal(t, pkg.ID, info["id"])
	defaultRelease := info["default-release"].(map[string]any)
	defaultRevision := defaultRelease["revision"].(map[string]any)
	assert.Equal(t, 1, defaultRevision["revision"])
	defaultResources := defaultRelease["resources"].([]map[string]any)
	require.Len(t, defaultResources, 1) // guards index access below
	assert.Equal(t, "config", defaultResources[0]["name"])

	// Act: refresh
	refresh, err := svc.Refresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			Name:        stringPtr("demo-charm"),
			Channel:     stringPtr("latest/stable"),
		}},
	})
	require.NoError(t, err)

	// Assert: refresh returns the released revision
	results := refresh["results"].([]map[string]any)
	require.Len(t, results, 1) // guards index access below
	assert.Equal(t, pkg.ID, results[0]["id"])
	charmEntity := results[0]["charm"].(map[string]any)
	assert.Equal(t, "demo-charm", charmEntity["name"])
	assert.Equal(t, 1, charmEntity["revision"])
	assert.Len(t, charmEntity["resources"].([]map[string]any), 1)

	// Act: OCI image operations
	creds, err := svc.OCIImageUploadCredentials(ctx, owner, pkg.Name, "workload-image")
	require.NoError(t, err)
	blobPayload, err := svc.OCIImageBlob(ctx, owner, pkg.Name, "workload-image", "sha256:deadbeef")
	require.NoError(t, err)

	// Assert
	assert.Contains(t, creds["image-name"], "demo-charm/workload-image")
	assert.Contains(t, blobPayload, `"Digest":"sha256:deadbeef"`)
}

func TestPrivatePackagesRequireAuthentication(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("owner-2", "owner")
	_, err := svc.RegisterPackage(ctx, owner, "secret-charm", "charm", true)
	require.NoError(t, err)

	// Act: unauthenticated find
	findResult, err := svc.Find(ctx, core.Identity{}, "secret")
	require.NoError(t, err)
	// Assert
	assert.Len(t, findResult["results"], 0)

	// Act: unauthenticated get
	_, err = svc.GetPackage(ctx, core.Identity{}, "secret-charm", false)
	// Assert
	require.Error(t, err)
	var svcErr *Error
	require.ErrorAs(t, err, &svcErr) // guards svcErr field access below
	assert.Equal(t, ErrorKindUnauthorized, svcErr.Kind)
}

func TestRegisterPackageOCISyncFailureDoesNotPersistPackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, repository := newTestServiceWithOCI(failingOCIRegistry{
		syncErr: fmt.Errorf("harbor unavailable"),
	})
	owner := newIdentity("owner-oci", "owner-oci")

	_, err := svc.RegisterPackage(ctx, owner, "broken-charm", "charm", true)
	require.Error(t, err)

	_, err = repository.GetPackageByName(ctx, "broken-charm")
	require.ErrorIs(t, err, repo.ErrNotFound)
}

func TestOCIImageUploadCredentialsPropagatesCredentialFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestServiceWithOCI(failingOCIRegistry{
		credentialsErr: fmt.Errorf("robot credentials unavailable"),
	})
	owner := newIdentity("owner-creds", "owner-creds")

	pkg, err := svc.RegisterPackage(ctx, owner, "cred-charm", "charm", true)
	require.NoError(t, err)

	upload, err := svc.CreateUpload(ctx, "cred-charm.charm", buildCharmArchive(t, "cred-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, pkg.Name, PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	_, err = svc.OCIImageUploadCredentials(ctx, owner, pkg.Name, "workload-image")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "robot credentials unavailable")
}

func TestIssueStoreTokenAndAuthenticate(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	svc, repository := newTestService()
	identity, err := svc.ResolveIdentity(ctx, auth.Claims{
		Subject:     "oidc|alice",
		Username:    "alice",
		DisplayName: "Alice Example",
		Email:       "alice@example.com",
	}, nil)
	require.NoError(t, err)

	// Act: issue a scoped store token
	raw, token, err := svc.IssueStoreToken(ctx, identity, IssueTokenRequest{
		Description: stringPtr("test token"),
		TTL:         intPtr(3600),
		Packages: []core.PackageSelector{{
			Name: "demo-charm",
			Type: "charm",
		}},
		Channels:    []string{"latest/stable"},
		Permissions: []string{permPackageView},
	})
	require.NoError(t, err)
	// Assert
	assert.NotEmpty(t, raw)
	assert.Equal(t, identity.Account.ID, token.AccountID)

	// Act: authenticate with the issued token
	authenticator, err := auth.New(ctx, testConfig(), repository)
	require.NoError(t, err)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	claims, storeToken, err := authenticator.Authenticate(req)
	require.NoError(t, err)
	require.NotNil(t, storeToken) // guards storeToken field access below

	// Assert: resolved identity and token match what was issued
	assert.Equal(t, identity.Account.Username, claims.Username)
	assert.Equal(t, token.SessionID, storeToken.SessionID)

	// Act: fetch token info
	whoami, err := svc.MacaroonInfo(core.Identity{
		Account:       identity.Account,
		Token:         storeToken,
		Authenticated: true,
	})
	require.NoError(t, err)

	// Assert: scoped permissions and channels are preserved
	assert.Equal(t, []string{"latest/stable"}, whoami["channels"])
	assert.Equal(t, []string{permPackageView}, whoami["permissions"])
}

func TestResolveIdentityEmptyClaims(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()

	identity, err := svc.ResolveIdentity(ctx, auth.Claims{}, nil)

	require.NoError(t, err)
	assert.False(t, identity.Authenticated)
	assert.Empty(t, identity.Account.ID)
}

func TestRootDocument(t *testing.T) {
	t.Parallel()

	svc, _ := newTestService()

	doc := svc.RootDocument()

	assert.Equal(t, "private-charm-registry", doc["service-name"])
	assert.Equal(t, "v1", doc["version"])
	assert.Equal(t, "https://registry.example.test", doc["api-url"])
}

func TestIssueStoreTokenUnauthenticated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()

	_, _, err := svc.IssueStoreToken(ctx, core.Identity{}, IssueTokenRequest{})

	require.Error(t, err)
	assertServiceError(t, err, 401)
}

func TestIssueStoreTokenDefaultPermissions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, token, err := svc.IssueStoreToken(ctx, owner, IssueTokenRequest{})

	require.NoError(t, err)
	assert.Equal(t, defaultPermissions, token.Permissions)
}

func TestExchangeStoreToken(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	raw, err := svc.ExchangeStoreToken(ctx, owner, nil)

	require.NoError(t, err)
	assert.NotEmpty(t, raw)
}

func TestExchangeStoreTokenPreservesScope(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	owner.Token = &core.StoreToken{
		Channels:    []string{"latest/edge"},
		Permissions: []string{permPackageView},
		Packages:    []core.PackageSelector{{Name: "my-charm", Type: "charm"}},
	}

	raw, err := svc.ExchangeStoreToken(ctx, owner, stringPtr("refreshed"))

	require.NoError(t, err)
	assert.NotEmpty(t, raw)
}

func TestResolveIdentityMarksConfiguredAdmin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repository := repo.NewMemory()
	cfg := testConfig()
	cfg.AdminSubjects = []string{"oidc|admin"}
	svc := New(cfg, repository, blob.NewMemoryStore(), testOCIRegistry{})

	identity, err := svc.ResolveIdentity(ctx, auth.Claims{
		Subject:     "oidc|admin",
		Username:    "admin",
		DisplayName: "Admin User",
		Email:       "admin@example.com",
	}, nil)

	require.NoError(t, err)
	assert.True(t, identity.Account.IsAdmin)
}

func TestAdminListsAllPackages(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("owner-1", "owner")
	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true

	_, err := svc.RegisterPackage(ctx, owner, "private-charm", "charm", true)
	require.NoError(t, err)
	_, err = svc.RegisterPackage(ctx, admin, "admin-charm", "charm", true)
	require.NoError(t, err)

	packages, err := svc.ListRegisteredPackages(ctx, admin, false)

	require.NoError(t, err)
	assert.Len(t, packages, 2)
}

func TestReleaseRejectsResourceForDifferentPackageRevision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("owner-1", "owner")

	pkg, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	uploadOne, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, pkg.Name, PushRevisionRequest{UploadID: uploadOne.ID})
	require.NoError(t, err)

	uploadTwo, err := svc.CreateUpload(ctx, "my-charm-v2.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, pkg.Name, PushRevisionRequest{UploadID: uploadTwo.ID})
	require.NoError(t, err)

	resourceUpload, err := svc.CreateUpload(ctx, "config.yaml", []byte("debug: true\n"))
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, pkg.Name, "config", PushResourceRequest{
		UploadID:        resourceUpload.ID,
		Type:            "file",
		PackageRevision: intPtr(1),
	})
	require.NoError(t, err)

	_, err = svc.Release(ctx, owner, pkg.Name, []core.Release{{
		Channel:  "latest/stable",
		Revision: 2,
		Resources: []core.ReleaseResourceRef{{
			Name:     "config",
			Revision: intPtr(1),
		}},
	}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not compatible")
}

func TestListStoreTokensUnauthenticated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()

	_, err := svc.ListStoreTokens(ctx, core.Identity{}, false)

	require.Error(t, err)
	assertServiceError(t, err, 401)
}

func TestRevokeStoreToken(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, token, err := svc.IssueStoreToken(ctx, owner, IssueTokenRequest{})
	require.NoError(t, err)

	err = svc.RevokeStoreToken(ctx, owner, token.SessionID)

	require.NoError(t, err)

	// Assert: revoked token no longer in active list
	tokens, err := svc.ListStoreTokens(ctx, owner, false)
	require.NoError(t, err)
	assert.Empty(t, tokens)

	// Assert: revoked token in inactive list
	all, err := svc.ListStoreTokens(ctx, owner, true)
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestRevokeStoreTokenUnauthenticated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()

	err := svc.RevokeStoreToken(ctx, core.Identity{}, "any-session")

	require.Error(t, err)
	assertServiceError(t, err, 401)
}

func TestMacaroonInfoUnauthenticated(t *testing.T) {
	t.Parallel()

	svc, _ := newTestService()

	_, err := svc.MacaroonInfo(core.Identity{})

	require.Error(t, err)
	assertServiceError(t, err, 401)
}

func TestMacaroonInfoWithoutToken(t *testing.T) {
	t.Parallel()

	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	info, err := svc.MacaroonInfo(owner)

	require.NoError(t, err)
	account := info["account"].(map[string]any)
	assert.Equal(t, "alice", account["username"])
	assert.Equal(t, []string{}, info["permissions"])
	assert.Equal(t, []string{}, info["channels"])
}

func TestDeprecatedWhoAmI(t *testing.T) {
	t.Parallel()

	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	result, err := svc.DeprecatedWhoAmI(owner)

	require.NoError(t, err)
	assert.Equal(t, "alice", result["username"])
	assert.Equal(t, "acc-1", result["id"])
}

func TestDeprecatedWhoAmIUnauthenticated(t *testing.T) {
	t.Parallel()

	svc, _ := newTestService()

	_, err := svc.DeprecatedWhoAmI(core.Identity{})

	assertServiceError(t, err, 401)
}

func TestRegisterPackageUnauthenticated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()

	_, err := svc.RegisterPackage(ctx, core.Identity{}, "charm-name", "charm", true)

	assertServiceError(t, err, 401)
}

func TestRegisterPackageDuplicate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", false)
	require.NoError(t, err)

	_, err = svc.RegisterPackage(ctx, owner, "my-charm", "charm", false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestRegisterPackageDefaultsToCharmType(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	pkg, err := svc.RegisterPackage(ctx, owner, "my-charm", "", false)

	require.NoError(t, err)
	assert.Equal(t, "charm", pkg.Type)
}

func TestRegisterPackageWithInsufficientPermission(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	identity := newIdentity("acc-1", "alice")
	identity.Token = &core.StoreToken{
		Permissions: []string{permPackageView}, // no register permission
	}

	_, err := svc.RegisterPackage(ctx, identity, "my-charm", "charm", false)

	assertServiceError(t, err, 403)
}

func TestListRegisteredPackages(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "charm-a", "charm", false)
	require.NoError(t, err)
	_, err = svc.RegisterPackage(ctx, owner, "charm-b", "charm", true)
	require.NoError(t, err)

	packages, err := svc.ListRegisteredPackages(ctx, owner, false)

	require.NoError(t, err)
	assert.Len(t, packages, 2)
}

func TestGetPackageNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, err := svc.GetPackage(ctx, owner, "nonexistent", true)

	assertServiceError(t, err, 404)
}

func TestUpdatePackageMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	updated, err := svc.UpdatePackage(ctx, owner, "my-charm", MetadataPatch{
		Title:       stringPtr("My Charm"),
		Description: stringPtr("A charm"),
		Summary:     stringPtr("Summary"),
		Contact:     stringPtr("admin@example.com"),
		Website:     stringPtr("https://example.com"),
		Private:     boolPtr(false),
	})

	require.NoError(t, err)
	assert.Equal(t, "My Charm", *updated.Title)
	assert.Equal(t, "A charm", *updated.Description)
	assert.Equal(t, "Summary", *updated.Summary)
	assert.Equal(t, "admin@example.com", *updated.Contact)
	assert.Equal(t, "https://example.com", *updated.Website)
	assert.False(t, updated.Private)
}

func TestUpdatePackageNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, err := svc.UpdatePackage(ctx, owner, "nonexistent", MetadataPatch{})

	assertServiceError(t, err, 404)
}

func TestUpdatePackageUnauthenticated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.UpdatePackage(ctx, core.Identity{}, "my-charm", MetadataPatch{})

	assertServiceError(t, err, 401)
}

func TestUnregisterEmptyPackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	pkg, err := svc.RegisterPackage(ctx, owner, "empty-charm", "charm", true)
	require.NoError(t, err)

	id, err := svc.UnregisterPackage(ctx, owner, "empty-charm")

	require.NoError(t, err)
	assert.Equal(t, pkg.ID, id)

	// Assert: package is gone
	_, err = svc.GetPackage(ctx, owner, "empty-charm", true)
	assertServiceError(t, err, 404)
}

func TestUnregisterPackageWithRevisions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "has-revisions", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "has-revisions.charm", buildCharmArchive(t, "has-revisions"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "has-revisions", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	_, err = svc.UnregisterPackage(ctx, owner, "has-revisions")

	// The caller is authorised — the business rule (not an auth check) prevents
	// deletion.  Expect 400 invalid-request, not 403.
	assertServiceError(t, err, 400)
}

func TestUnregisterPackageNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, err := svc.UnregisterPackage(ctx, owner, "nonexistent")

	assertServiceError(t, err, 404)
}

func TestUnregisterPackageUnauthenticated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.UnregisterPackage(ctx, core.Identity{}, "my-charm")

	assertServiceError(t, err, 401)
}

func TestCreateUploadDetectsKind(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()

	charmUpload, err := svc.CreateUpload(ctx, "test.charm", []byte("data"))
	require.NoError(t, err)
	assert.Equal(t, "revision", charmUpload.Kind)

	resourceUpload, err := svc.CreateUpload(ctx, "config.yaml", []byte("data"))
	require.NoError(t, err)
	assert.Equal(t, "resource", resourceUpload.Kind)
}

func TestCreateUploadComputesHashes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()

	upload, err := svc.CreateUpload(ctx, "test.charm", []byte("test"))

	require.NoError(t, err)
	assert.NotEmpty(t, upload.SHA256)
	assert.NotEmpty(t, upload.SHA384)
	assert.Equal(t, int64(4), upload.Size)
	assert.Equal(t, "pending", upload.Status)
}

func TestReviewUpload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	result, err := svc.ReviewUpload(ctx, owner, "my-charm", upload.ID)

	require.NoError(t, err)
	revisions := result["revisions"].([]map[string]any)
	require.Len(t, revisions, 1)
	assert.Equal(t, "approved", revisions[0]["status"])
}

func TestReviewUploadNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.ReviewUpload(ctx, owner, "my-charm", "nonexistent")

	assertServiceError(t, err, 404)
}

func TestListRevisions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	revisions, err := svc.ListRevisions(ctx, owner, "my-charm", nil)

	require.NoError(t, err)
	assert.Len(t, revisions, 1)
	assert.Equal(t, 1, revisions[0].Revision)
}

func TestListRevisionsFilterByNumber(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	rev := 1
	revisions, err := svc.ListRevisions(ctx, owner, "my-charm", &rev)

	require.NoError(t, err)
	require.Len(t, revisions, 1)
	assert.Equal(t, 1, revisions[0].Revision)
}

func TestListResources(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	resources, err := svc.ListResources(ctx, owner, "my-charm")

	require.NoError(t, err)
	assert.NotEmpty(t, resources)
}

func TestListResourceRevisions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	resourceUpload, err := svc.CreateUpload(ctx, "config.yaml", []byte("data"))
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, "my-charm", "config", PushResourceRequest{
		UploadID: resourceUpload.ID, Type: "file",
	})
	require.NoError(t, err)

	revisions, err := svc.ListResourceRevisions(ctx, owner, "my-charm", "config")

	require.NoError(t, err)
	require.Len(t, revisions, 1)
	assert.Equal(t, 1, revisions[0].Revision)
	assert.NotEmpty(t, revisions[0].Download.URL)
}

func TestUpdateResourceRevisions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	resourceUpload, err := svc.CreateUpload(ctx, "config.yaml", []byte("data"))
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, "my-charm", "config", PushResourceRequest{
		UploadID: resourceUpload.ID, Type: "file",
	})
	require.NoError(t, err)

	updated, err := svc.UpdateResourceRevisions(ctx, owner, "my-charm", "config", UpdateResourceRevisionRequest{
		ResourceRevisionUpdates: []struct {
			Revision      int         `json:"revision"`
			Bases         []core.Base `json:"bases"`
			Architectures []string    `json:"architectures"`
		}{{
			Revision:      1,
			Bases:         []core.Base{{Name: "ubuntu", Channel: "22.04", Architecture: "arm64"}},
			Architectures: []string{"arm64"},
		}},
	})

	require.NoError(t, err)
	assert.Equal(t, 1, updated)
}

func TestReleaseEmptyChannelFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	_, err = svc.Release(ctx, owner, "my-charm", []core.Release{{
		Channel: "", Revision: 1,
	}})

	assertServiceError(t, err, 400)
}

func TestReleaseNonExistentRevisionFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.Release(ctx, owner, "my-charm", []core.Release{{
		Channel: "latest/stable", Revision: 999,
	}})

	assertServiceError(t, err, 404)
}

func TestReleaseChannelRestriction(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	// Now restrict the token to edge channel only
	owner.Token = &core.StoreToken{
		Channels:    []string{"latest/edge"},
		Permissions: []string{permPackageManageReleases},
	}

	_, err = svc.Release(ctx, owner, "my-charm", []core.Release{{
		Channel: "latest/stable", Revision: 1,
	}})

	assertServiceError(t, err, 403)
}

func TestCreateTracks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	created, err := svc.CreateTracks(ctx, owner, "my-charm", []core.Track{
		{Name: "2.0"},
		{Name: "3.0"},
	})

	require.NoError(t, err)
	assert.Equal(t, 2, created)
}

func TestCreateTracksDuplicate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	// "latest" already exists from registration
	created, err := svc.CreateTracks(ctx, owner, "my-charm", []core.Track{
		{Name: "latest"},
		{Name: "2.0"},
	})

	require.NoError(t, err)
	assert.Equal(t, 1, created) // only "2.0" is new
}

func TestListReleases(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	_, err = svc.Release(ctx, owner, "my-charm", []core.Release{{
		Channel: "latest/stable", Revision: 1,
	}})
	require.NoError(t, err)

	result, err := svc.ListReleases(ctx, owner, "my-charm")

	require.NoError(t, err)
	channelMap := result["channel-map"].([]map[string]any)
	assert.Len(t, channelMap, 1)
}

func TestDownloadCharm(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	pkg, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	archiveData := buildCharmArchive(t, "my-charm")
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", archiveData)
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	payload, err := svc.DownloadCharm(ctx, owner, pkg.ID, 1)

	require.NoError(t, err)
	assert.Equal(t, archiveData, payload)
}

func TestDownloadCharmNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, err := svc.DownloadCharm(ctx, owner, "nonexistent-id", 1)

	assertServiceError(t, err, 404)
}

func TestDownloadResource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	pkg, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	resourceUpload, err := svc.CreateUpload(ctx, "config.yaml", []byte("debug: true\n"))
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, "my-charm", "config", PushResourceRequest{
		UploadID: resourceUpload.ID, Type: "file",
	})
	require.NoError(t, err)

	payload, err := svc.DownloadResource(ctx, owner, pkg.ID, "config", 1)

	require.NoError(t, err)
	assert.Equal(t, []byte("debug: true\n"), payload)
}

func TestDownloadResourceNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, err := svc.DownloadResource(ctx, owner, "nonexistent", "config", 1)

	assertServiceError(t, err, 404)
}

func TestPushRevisionNonExistentPackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, err := svc.PushRevision(ctx, owner, "nonexistent", PushRevisionRequest{UploadID: "some-id"})

	assertServiceError(t, err, 404)
}

func TestPushRevisionInvalidArchive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", []byte("not a valid archive"))
	require.NoError(t, err)

	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})

	assertServiceError(t, err, 400)
}

func TestPushResourceNonExistentPackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, err := svc.PushResource(ctx, owner, "nonexistent", "config", PushResourceRequest{UploadID: "id"})

	assertServiceError(t, err, 404)
}

func TestTokenPackageScoping(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "charm-a", "charm", true)
	require.NoError(t, err)
	_, err = svc.RegisterPackage(ctx, owner, "charm-b", "charm", true)
	require.NoError(t, err)

	// Act: scope token to charm-a only
	scopedIdentity := newIdentity("acc-1", "alice")
	scopedIdentity.Token = &core.StoreToken{
		Packages:    []core.PackageSelector{{Name: "charm-a", Type: "charm"}},
		Permissions: []string{permPackageManage},
	}

	// Assert: can access charm-a
	_, err = svc.GetPackage(ctx, scopedIdentity, "charm-a", true)
	require.NoError(t, err)

	// Assert: cannot access charm-b
	_, err = svc.GetPackage(ctx, scopedIdentity, "charm-b", true)
	assertServiceError(t, err, 403)
}

func TestFindPublicPackages(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	// Arrange: create a public package, push a revision, and release it
	_, err := svc.RegisterPackage(ctx, owner, "public-charm", "charm", false)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "public-charm.charm", buildCharmArchive(t, "public-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "public-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	_, err = svc.Release(ctx, owner, "public-charm", []core.Release{{
		Channel: "latest/stable", Revision: 1,
	}})
	require.NoError(t, err)

	// Act: unauthenticated find
	result, err := svc.Find(ctx, core.Identity{}, "public")

	require.NoError(t, err)
	results := result["results"].([]map[string]any)
	assert.Len(t, results, 1)
	assert.Equal(t, "public-charm", results[0]["name"])
}

func TestRefreshByID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	pkg, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	_, err = svc.Release(ctx, owner, "my-charm", []core.Release{{
		Channel: "latest/stable", Revision: 1,
	}})
	require.NoError(t, err)

	// Act: refresh by package ID instead of name
	result, err := svc.Refresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			ID:          &pkg.ID,
			Channel:     stringPtr("latest/stable"),
		}},
	})

	require.NoError(t, err)
	results := result["results"].([]map[string]any)
	require.Len(t, results, 1)
	assert.Equal(t, pkg.ID, results[0]["id"])
}

func TestRefreshByRevision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	// Act: refresh by specific revision
	result, err := svc.Refresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			Name:        stringPtr("my-charm"),
			Revision:    intPtr(1),
		}},
	})

	require.NoError(t, err)
	results := result["results"].([]map[string]any)
	require.Len(t, results, 1)
}

func TestRefreshMissingIDAndName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	// Per the Charmhub refresh contract, action-level errors are embedded
	// inside the results array — the top-level call succeeds (no error).
	result, err := svc.Refresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
		}},
	})

	require.NoError(t, err)
	results := result["results"].([]map[string]any)
	require.Len(t, results, 1)
	assert.Equal(t, "error", results[0]["result"])
	apiErr, ok := results[0]["error"].(core.APIError)
	require.True(t, ok)
	assert.Equal(t, "invalid-request", apiErr.Code)
}

func TestOCIImageUploadCredentialsNonExistentPackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, err := svc.OCIImageUploadCredentials(ctx, owner, "nonexistent", "resource")

	assertServiceError(t, err, 404)
}

func TestMultipleRevisions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	// Push two revisions
	upload1, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload1.ID})
	require.NoError(t, err)
	upload2, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload2.ID})
	require.NoError(t, err)

	// Assert: two revisions exist
	revisions, err := svc.ListRevisions(ctx, owner, "my-charm", nil)
	require.NoError(t, err)
	assert.Len(t, revisions, 2)
	assert.Equal(t, 1, revisions[0].Revision)
	assert.Equal(t, 2, revisions[1].Revision)
}

func TestPackageManagePermissionDenied(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	other := newIdentity("acc-2", "bob")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	// Bob cannot manage Alice's package
	_, err = svc.UpdatePackage(ctx, other, "my-charm", MetadataPatch{
		Title: stringPtr("Hacked"),
	})

	assertServiceError(t, err, 403)
}

func TestUpdatePackageLinks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	updated, err := svc.UpdatePackage(ctx, owner, "my-charm", MetadataPatch{
		Links: map[string][]string{
			"docs":   {"https://docs.example.com"},
			"issues": {"https://github.com/example/issues"},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"https://docs.example.com"}, updated.Links["docs"])
}

func TestUpdatePackageDefaultTrack(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	updated, err := svc.UpdatePackage(ctx, owner, "my-charm", MetadataPatch{
		DefaultTrack: stringPtr("2.0"),
	})

	require.NoError(t, err)
	assert.Equal(t, "2.0", *updated.DefaultTrack)
}

func TestServiceErrorString(t *testing.T) {
	t.Parallel()

	err := &Error{Kind: ErrorKindNotFound, Code: "not-found", Message: "package not found"}

	assert.Equal(t, "not-found: package not found", err.Error())
}

func TestPublicPackageAccessibleAnonymously(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "public-charm", "charm", false)
	require.NoError(t, err)

	// Unauthenticated user can view public packages
	pkg, err := svc.GetPackage(ctx, core.Identity{}, "public-charm", true)

	require.NoError(t, err)
	assert.Equal(t, "public-charm", pkg.Name)
}

func TestRefreshDefaultRelease(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	_, err = svc.Release(ctx, owner, "my-charm", []core.Release{{
		Channel: "latest/stable", Revision: 1,
	}})
	require.NoError(t, err)

	// Act: refresh without specifying channel (should resolve to default)
	result, err := svc.Refresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			Name:        stringPtr("my-charm"),
		}},
	})

	require.NoError(t, err)
	results := result["results"].([]map[string]any)
	require.Len(t, results, 1)
	assert.Equal(t, "latest/stable", results[0]["effective-channel"])
}

func TestListResourcesNoResources(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "bare-charm", "charm", true)
	require.NoError(t, err)

	resources, err := svc.ListResources(ctx, owner, "bare-charm")

	require.NoError(t, err)
	assert.Empty(t, resources)
}

func TestListResourcesNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, err := svc.ListResources(ctx, owner, "nonexistent")

	assertServiceError(t, err, 404)
}

func TestListRevisionsNotFoundPackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, err := svc.ListRevisions(ctx, owner, "nonexistent", nil)

	assertServiceError(t, err, 404)
}

func TestListReleasesNotFoundPackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, err := svc.ListReleases(ctx, owner, "nonexistent")

	assertServiceError(t, err, 404)
}

func TestInfoNotFoundPackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, err := svc.Info(ctx, owner, "nonexistent")

	assertServiceError(t, err, 404)
}

func TestListResourceRevisionsNotFoundPackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, err := svc.ListResourceRevisions(ctx, owner, "nonexistent", "config")

	assertServiceError(t, err, 404)
}

func TestUpdateResourceRevisionsNotFoundPackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, err := svc.UpdateResourceRevisions(ctx, owner, "nonexistent", "config",
		UpdateResourceRevisionRequest{})

	assertServiceError(t, err, 404)
}

func TestCreateTracksNotFoundPackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, err := svc.CreateTracks(ctx, owner, "nonexistent", []core.Track{{Name: "2.0"}})

	assertServiceError(t, err, 404)
}

func TestReviewUploadUnauthenticated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.ReviewUpload(ctx, core.Identity{}, "my-charm", "upload-id")

	assertServiceError(t, err, 401)
}

func TestOCIImageUploadCredentialsUnauthenticated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.OCIImageUploadCredentials(ctx, core.Identity{}, "my-charm", "resource")

	assertServiceError(t, err, 401)
}

func TestPushResourceUndeclaredResource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	resUpload, err := svc.CreateUpload(ctx, "file.bin", []byte("data"))
	require.NoError(t, err)

	_, err = svc.PushResource(ctx, owner, "my-charm", "nonexistent-resource", PushResourceRequest{
		UploadID: resUpload.ID, Type: "file",
	})

	assertServiceError(t, err, 404)
}

func TestDownloadResourceOCIImage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	// Push an OCI image blob as a resource
	ociBlob := []byte(`{"ImageName":"oci.example.test/charm-my-charm/workload-image","Digest":"sha256:test"}`)
	ociUpload, err := svc.CreateUpload(ctx, "blob.json", ociBlob)
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, "my-charm", "workload-image", PushResourceRequest{
		UploadID: ociUpload.ID, Type: "oci-image",
	})
	require.NoError(t, err)

	pkg, err := svc.GetPackage(ctx, owner, "my-charm", true)
	require.NoError(t, err)
	payload, err := svc.DownloadResource(ctx, owner, pkg.ID, "workload-image", 1)

	require.NoError(t, err)
	assert.Contains(t, string(payload), `"Digest":"sha256:test"`)
	assert.Contains(t, string(payload), `"Username":"robot$pull-`)
}

func TestReleaseMultipleChannels(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	released, err := svc.Release(ctx, owner, "my-charm", []core.Release{
		{Channel: "latest/stable", Revision: 1},
		{Channel: "latest/edge", Revision: 1},
	})

	require.NoError(t, err)
	assert.Len(t, released, 2)
}

func TestRefreshWithResourceRevisionOverride(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	resUpload, err := svc.CreateUpload(ctx, "config.yaml", []byte("v1"))
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, "my-charm", "config", PushResourceRequest{
		UploadID: resUpload.ID, Type: "file",
	})
	require.NoError(t, err)
	_, err = svc.Release(ctx, owner, "my-charm", []core.Release{{
		Channel: "latest/stable", Revision: 1,
		Resources: []core.ReleaseResourceRef{{Name: "config", Revision: intPtr(1)}},
	}})
	require.NoError(t, err)

	// Refresh with resource revision override
	result, err := svc.Refresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			Name:        stringPtr("my-charm"),
			Channel:     stringPtr("latest/stable"),
			ResourceRevisions: []core.ReleaseResourceRef{
				{Name: "config", Revision: intPtr(1)},
			},
		}},
	})

	require.NoError(t, err)
	results := result["results"].([]map[string]any)
	assert.Len(t, results, 1)
}

func TestListRegisteredPackagesUnauthenticated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()

	_, err := svc.ListRegisteredPackages(ctx, core.Identity{}, false)

	assertServiceError(t, err, 401)
}

func TestListRegisteredPackagesWithCollaborations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "charm-a", "charm", false)
	require.NoError(t, err)

	packages, err := svc.ListRegisteredPackages(ctx, owner, true)

	require.NoError(t, err)
	assert.Len(t, packages, 1)
}

func TestListRegisteredPackagesInsufficientPermission(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	identity := newIdentity("acc-1", "alice")
	identity.Token = &core.StoreToken{
		Permissions: []string{permPackageView},
	}

	_, err := svc.ListRegisteredPackages(ctx, identity, false)

	assertServiceError(t, err, 403)
}

func TestDownloadCharmRevisionNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	pkg, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.DownloadCharm(ctx, owner, pkg.ID, 999)

	assertServiceError(t, err, 404)
}

func TestDownloadResourceResourceNotDeclared(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	pkg, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.DownloadResource(ctx, owner, pkg.ID, "nonexistent-res", 1)

	assertServiceError(t, err, 404)
}

func TestDownloadResourceRevisionNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	pkg, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	_, err = svc.DownloadResource(ctx, owner, pkg.ID, "config", 999)

	assertServiceError(t, err, 404)
}

func TestListResourceRevisionsUndeclaredResource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.ListResourceRevisions(ctx, owner, "my-charm", "nonexistent")

	assertServiceError(t, err, 404)
}

func TestUpdateResourceRevisionsUndeclaredResource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.UpdateResourceRevisions(ctx, owner, "my-charm", "nonexistent",
		UpdateResourceRevisionRequest{})

	assertServiceError(t, err, 404)
}

func TestOCIImageUploadCredentialsUndeclaredResource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.OCIImageUploadCredentials(ctx, owner, "my-charm", "nonexistent")

	assertServiceError(t, err, 404)
}

func TestPushRevisionNonExistentUpload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: "bogus"})

	assertServiceError(t, err, 404)
}

func TestPushRevisionUnauthenticated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.PushRevision(ctx, core.Identity{}, "my-charm", PushRevisionRequest{UploadID: "id"})

	assertServiceError(t, err, 401)
}

func TestPushResourceNonExistentUpload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	_, err = svc.PushResource(ctx, owner, "my-charm", "config", PushResourceRequest{
		UploadID: "bogus", Type: "file",
	})

	assertServiceError(t, err, 404)
}

func TestReleaseUnauthenticated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.Release(ctx, core.Identity{}, "my-charm", []core.Release{{
		Channel: "latest/stable", Revision: 1,
	}})

	assertServiceError(t, err, 401)
}

func TestReleaseNotFoundPackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, err := svc.Release(ctx, owner, "nonexistent", []core.Release{{
		Channel: "latest/stable", Revision: 1,
	}})

	assertServiceError(t, err, 404)
}

func TestInfoNoRelease(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.Info(ctx, owner, "my-charm")

	assertServiceError(t, err, 404)
}

func TestRefreshByChannelNotReleased(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	// Per the Charmhub refresh contract, the top-level call succeeds (200);
	// the not-found error is embedded in the per-action result.
	result, err := svc.Refresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			Name:        stringPtr("my-charm"),
			Channel:     stringPtr("latest/stable"),
		}},
	})

	require.NoError(t, err)
	assertRefreshActionError(t, result, "app/0", "not-found")
}

func TestRefreshByRevisionNonExistent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	result, err := svc.Refresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			Name:        stringPtr("my-charm"),
			Revision:    intPtr(999),
		}},
	})

	require.NoError(t, err)
	assertRefreshActionError(t, result, "app/0", "not-found")
}

func TestTokenPackageScopingByID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	pkg, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	scopedIdentity := newIdentity("acc-1", "alice")
	scopedIdentity.Token = &core.StoreToken{
		Packages:    []core.PackageSelector{{ID: pkg.ID}},
		Permissions: []string{permPackageManage},
	}

	_, err = svc.GetPackage(ctx, scopedIdentity, "my-charm", true)

	require.NoError(t, err)
}

func TestTokenDoesNotAllowPackageManage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	_, err = svc.RegisterPackage(ctx, owner, "other-charm", "charm", true)
	require.NoError(t, err)

	scopedIdentity := newIdentity("acc-1", "alice")
	scopedIdentity.Token = &core.StoreToken{
		Packages:    []core.PackageSelector{{Name: "other-charm"}},
		Permissions: []string{permPackageManageMetadata},
	}

	_, err = svc.UpdatePackage(ctx, scopedIdentity, "my-charm", MetadataPatch{
		Title: stringPtr("Hacked"),
	})

	assertServiceError(t, err, 403)
}

func TestListReleasesWithChannelInfo(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	_, err = svc.Release(ctx, owner, "my-charm", []core.Release{
		{Channel: "latest/stable", Revision: 1},
		{Channel: "latest/edge", Revision: 1},
	})
	require.NoError(t, err)

	result, err := svc.ListReleases(ctx, owner, "my-charm")

	require.NoError(t, err)
	channelMap := result["channel-map"].([]map[string]any)
	assert.Len(t, channelMap, 2)
	revisions := result["revisions"].([]map[string]any)
	assert.Len(t, revisions, 1)
	assert.Equal(t, []any{}, revisions[0]["errors"])
	pkgInfo := result["package"].(map[string]any)
	channels := pkgInfo["channels"].([]map[string]any)
	assert.NotEmpty(t, channels)
}

func TestFindNoMatchingPackages(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()

	result, err := svc.Find(ctx, core.Identity{}, "nonexistent-query-xyz")

	require.NoError(t, err)
	results := result["results"].([]map[string]any)
	assert.Empty(t, results)
}

func TestPushResourceMultipleRevisions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	// Push two resource revisions
	resUpload1, err := svc.CreateUpload(ctx, "config.yaml", []byte("v1"))
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, "my-charm", "config", PushResourceRequest{
		UploadID: resUpload1.ID, Type: "file",
	})
	require.NoError(t, err)
	resUpload2, err := svc.CreateUpload(ctx, "config.yaml", []byte("v2"))
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, "my-charm", "config", PushResourceRequest{
		UploadID: resUpload2.ID, Type: "file",
	})
	require.NoError(t, err)

	revisions, err := svc.ListResourceRevisions(ctx, owner, "my-charm", "config")

	require.NoError(t, err)
	assert.Len(t, revisions, 2)
}

func TestReleaseWithResourceRefs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	resUpload, err := svc.CreateUpload(ctx, "config.yaml", []byte("data"))
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, "my-charm", "config", PushResourceRequest{
		UploadID: resUpload.ID, Type: "file",
	})
	require.NoError(t, err)

	released, err := svc.Release(ctx, owner, "my-charm", []core.Release{{
		Channel:  "latest/stable",
		Revision: 1,
		Resources: []core.ReleaseResourceRef{
			{Name: "config", Revision: intPtr(1)},
		},
	}})

	require.NoError(t, err)
	assert.Len(t, released, 1)

	// Assert: info shows the resource
	info, err := svc.Info(ctx, owner, "my-charm")
	require.NoError(t, err)
	defaultRelease := info["default-release"].(map[string]any)
	resources := defaultRelease["resources"].([]map[string]any)
	assert.Len(t, resources, 1)
}

func TestReleaseWithNilResourceRevision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	// Release with resource ref that has nil revision (should be skipped)
	released, err := svc.Release(ctx, owner, "my-charm", []core.Release{{
		Channel:  "latest/stable",
		Revision: 1,
		Resources: []core.ReleaseResourceRef{
			{Name: "config", Revision: nil},
		},
	}})

	require.NoError(t, err)
	assert.Len(t, released, 1)
}

func TestListReleasesUnauthenticated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.ListReleases(ctx, core.Identity{}, "my-charm")

	assertServiceError(t, err, 401)
}

func TestOCIImageBlobPayload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	blob, err := svc.OCIImageBlob(ctx, owner, "my-charm", "workload-image", "sha256:abc123")

	require.NoError(t, err)
	assert.Contains(t, blob, `"Digest":"sha256:abc123"`)
	assert.Contains(t, blob, "oci.example.test")
}

func TestOCIImageUploadCredentialsSuccess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	creds, err := svc.OCIImageUploadCredentials(ctx, owner, "my-charm", "workload-image")

	require.NoError(t, err)
	assert.Contains(t, creds["image-name"].(string), "workload-image")
	assert.NotEmpty(t, creds["username"])
	assert.NotEmpty(t, creds["password"])
}

func TestGetPackagePublicWithTokenPermission(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "public-charm", "charm", false)
	require.NoError(t, err)

	// Authenticated user with token needs package-view permission for requireTokenPermission=true
	viewer := newIdentity("acc-2", "bob")
	viewer.Token = &core.StoreToken{
		Permissions: []string{permPackageView},
	}
	_, err = svc.GetPackage(ctx, viewer, "public-charm", true)

	require.NoError(t, err)
}

func TestGetPackagePublicWithInsufficientTokenPermission(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "public-charm", "charm", false)
	require.NoError(t, err)

	// Token with wrong permission should fail requirePermissionOrAnonymous
	viewer := newIdentity("acc-2", "bob")
	viewer.Token = &core.StoreToken{
		Permissions: []string{permAccountRegisterPackage},
	}
	_, err = svc.GetPackage(ctx, viewer, "public-charm", true)

	assertServiceError(t, err, 403)
}

func TestPrivatePackageTokenDoesNotAllowPackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "private-a", "charm", true)
	require.NoError(t, err)
	_, err = svc.RegisterPackage(ctx, owner, "private-b", "charm", true)
	require.NoError(t, err)

	// Token scoped to private-a cannot see private-b
	scopedIdentity := newIdentity("acc-1", "alice")
	scopedIdentity.Token = &core.StoreToken{
		Packages:    []core.PackageSelector{{Name: "private-a"}},
		Permissions: []string{permPackageView},
	}

	_, err = svc.GetPackage(ctx, scopedIdentity, "private-b", true)

	assertServiceError(t, err, 403)
}

func TestEnforceChannelRestrictionAllowed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	// Token restricted to edge — release to edge should succeed
	owner.Token = &core.StoreToken{
		Channels:    []string{"latest/edge"},
		Permissions: []string{permPackageManageReleases},
	}

	released, err := svc.Release(ctx, owner, "my-charm", []core.Release{{
		Channel: "latest/edge", Revision: 1,
	}})

	require.NoError(t, err)
	assert.Len(t, released, 1)
}

func TestRefreshNoChannelNoRelease(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	// Refresh without channel and no releases: default release not found.
	// Per the Charmhub refresh contract this becomes a per-action error.
	result, err := svc.Refresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			Name:        stringPtr("my-charm"),
		}},
	})

	require.NoError(t, err)
	assertRefreshActionError(t, result, "app/0", "not-found")
}

func TestListRevisionsUnauthenticated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.ListRevisions(ctx, core.Identity{}, "my-charm", nil)

	assertServiceError(t, err, 401)
}

func TestListResourcesUnauthenticated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.ListResources(ctx, core.Identity{}, "my-charm")

	assertServiceError(t, err, 401)
}

func TestCreateTracksUnauthenticated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.CreateTracks(ctx, core.Identity{}, "my-charm", []core.Track{{Name: "2.0"}})

	assertServiceError(t, err, 401)
}

func TestDownloadCharmUnauthenticatedPrivate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	pkg, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	_, err = svc.DownloadCharm(ctx, core.Identity{}, pkg.ID, 1)

	assertServiceError(t, err, 401)
}

func TestUpdatePackageTokenDoesNotAllowPackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "charm-a", "charm", true)
	require.NoError(t, err)
	_, err = svc.RegisterPackage(ctx, owner, "charm-b", "charm", true)
	require.NoError(t, err)

	// Token scoped to charm-b, trying to update charm-a
	scopedIdentity := newIdentity("acc-1", "alice")
	scopedIdentity.Token = &core.StoreToken{
		Packages:    []core.PackageSelector{{Name: "charm-b"}},
		Permissions: []string{permPackageManageMetadata},
	}

	_, err = svc.UpdatePackage(ctx, scopedIdentity, "charm-a", MetadataPatch{Title: stringPtr("x")})

	assertServiceError(t, err, 403)
}

func TestListResourcesWithPushedRevisions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	// Push a resource revision
	resUpload, err := svc.CreateUpload(ctx, "config.yaml", []byte("debug: true\n"))
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, "my-charm", "config", PushResourceRequest{
		UploadID: resUpload.ID, Type: "file",
	})
	require.NoError(t, err)

	// Act: list resources — should show current revision > 0
	resources, err := svc.ListResources(ctx, owner, "my-charm")

	require.NoError(t, err)
	require.Len(t, resources, 2) // config + workload-image (auto-generated)
	found := false
	for _, res := range resources {
		if res["name"] == "config" {
			assert.Equal(t, 1, res["revision"])
			found = true
		}
	}
	assert.True(t, found, "config resource should be in the list")
}

func TestReleaseSinglePartChannel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	// Release to "stable" (single-part channel) to exercise splitChannel with one part
	released, err := svc.Release(ctx, owner, "my-charm", []core.Release{{
		Channel: "stable", Revision: 1,
	}})

	require.NoError(t, err)
	assert.Len(t, released, 1)

	// Info should parse the single-part channel correctly
	info, err := svc.Info(ctx, owner, "my-charm")
	require.NoError(t, err)
	defaultRelease := info["default-release"].(map[string]any)
	channel := defaultRelease["channel"].(map[string]any)
	assert.Equal(t, "latest", channel["track"])
	assert.Equal(t, "stable", channel["risk"])
}

func TestFindPackageWithoutRelease(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	// Create public package without releasing — should be filtered from find results
	_, err := svc.RegisterPackage(ctx, owner, "unreleased-charm", "charm", false)
	require.NoError(t, err)

	result, err := svc.Find(ctx, owner, "unreleased")

	require.NoError(t, err)
	results := result["results"].([]map[string]any)
	assert.Empty(t, results)
}

func TestPushResourceOCIImage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	// Push an OCI image blob
	ociBlob := []byte(`{"ImageName":"oci.example.test/charm-my-charm/workload-image","Digest":"sha256:test"}`)
	ociUpload, err := svc.CreateUpload(ctx, "blob.json", ociBlob)
	require.NoError(t, err)
	statusURL, err := svc.PushResource(ctx, owner, "my-charm", "workload-image", PushResourceRequest{
		UploadID: ociUpload.ID, Type: "oci-image",
	})
	require.NoError(t, err)
	assert.Contains(t, statusURL, "/v1/charm/my-charm/revisions/review")

	// Verify: resource revision should have empty ObjectKey (OCI stored as blob text)
	revisions, err := svc.ListResourceRevisions(ctx, owner, "my-charm", "workload-image")
	require.NoError(t, err)
	require.Len(t, revisions, 1)
	assert.Equal(t, "oci-image", revisions[0].Type)
}

func TestInfoWithMultipleReleasesAndResources(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	resUpload, err := svc.CreateUpload(ctx, "config.yaml", []byte("data"))
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, "my-charm", "config", PushResourceRequest{
		UploadID: resUpload.ID, Type: "file",
	})
	require.NoError(t, err)
	_, err = svc.Release(ctx, owner, "my-charm", []core.Release{
		{Channel: "latest/stable", Revision: 1, Resources: []core.ReleaseResourceRef{
			{Name: "config", Revision: intPtr(1)},
		}},
		{Channel: "latest/edge", Revision: 1},
	})
	require.NoError(t, err)

	info, err := svc.Info(ctx, owner, "my-charm")

	require.NoError(t, err)
	channelMap := info["channel-map"].([]map[string]any)
	assert.Len(t, channelMap, 2)
	defaultRelease := info["default-release"].(map[string]any)
	resources := defaultRelease["resources"].([]map[string]any)
	assert.Len(t, resources, 1)
}

func TestListReleasesRevisionsSorted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	// Push two revisions
	upload1, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload1.ID})
	require.NoError(t, err)
	upload2, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload2.ID})
	require.NoError(t, err)

	// Release both
	_, err = svc.Release(ctx, owner, "my-charm", []core.Release{
		{Channel: "latest/stable", Revision: 2},
		{Channel: "latest/edge", Revision: 1},
	})
	require.NoError(t, err)

	result, err := svc.ListReleases(ctx, owner, "my-charm")

	require.NoError(t, err)
	revisions := result["revisions"].([]map[string]any)
	assert.Len(t, revisions, 2)
	assert.Equal(t, 1, revisions[0]["revision"])
	assert.Equal(t, 2, revisions[1]["revision"])
}

func TestResolveIdentityWithMinimalClaims(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()

	// Only subject, no username/email/display
	identity, err := svc.ResolveIdentity(ctx, auth.Claims{
		Subject: "oidc|bob",
	}, nil)

	require.NoError(t, err)
	assert.True(t, identity.Authenticated)
	// Username falls back to subject with | replaced
	assert.Contains(t, identity.Account.Username, "oidc")
	// Email falls back to sanitized subject
	assert.Contains(t, identity.Account.Email, "@example.invalid")
}

func TestPushResourceWithFilenameOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	// Push resource without specifying type — should inherit from resource def
	resUpload, err := svc.CreateUpload(ctx, "config.yaml", []byte("data"))
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, "my-charm", "config", PushResourceRequest{
		UploadID:      resUpload.ID,
		Bases:         []core.Base{{Name: "ubuntu", Channel: "22.04", Architecture: "arm64"}},
		Architectures: []string{"arm64"},
	})

	require.NoError(t, err)
}

// --- helper function tests ---

func TestStringPtr(t *testing.T) {
	t.Parallel()

	assert.Nil(t, stringPtr(""))
	assert.Equal(t, "hello", *stringPtr("hello"))
}

func TestStringValue(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", stringValue(nil))
	v := "hello"
	assert.Equal(t, "hello", stringValue(&v))
}

func TestFirstNonEmpty(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", firstNonEmpty())
	assert.Equal(t, "", firstNonEmpty("", "", ""))
	assert.Equal(t, "a", firstNonEmpty("", "a", "b"))
	assert.Equal(t, "first", firstNonEmpty("first"))
}

func TestFirstLink(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", firstLink(nil))
	assert.Equal(t, "", firstLink([]string{}))
	assert.Equal(t, "a", firstLink([]string{"a", "b"}))
}

func TestNullIfEmpty(t *testing.T) {
	t.Parallel()

	assert.Nil(t, nullIfEmpty([]string{}))
	assert.Nil(t, nullIfEmpty[int](nil))
	result := nullIfEmpty([]string{"a"})
	assert.Equal(t, []string{"a"}, result)
}

func TestEmptySliceIfNil(t *testing.T) {
	t.Parallel()

	assert.NotNil(t, emptySliceIfNil[string](nil))
	assert.Empty(t, emptySliceIfNil[string](nil))
	assert.Equal(t, []string{"a"}, emptySliceIfNil([]string{"a"}))
}

func TestTranslateRepoError(t *testing.T) {
	t.Parallel()

	// Nil error returns nil
	assert.NoError(t, translateRepoError(nil, "msg"))

	// ErrNotFound becomes 404 not-found
	err := translateRepoError(repo.ErrNotFound, "not found message")
	assertServiceError(t, err, 404)
	var notFound *Error
	require.ErrorAs(t, err, &notFound)
	assert.Equal(t, "not-found", notFound.Code)

	// ErrConflict (wrapped) becomes 409 already-registered
	wrapped := fmt.Errorf("dup: %w", repo.ErrConflict)
	err = translateRepoError(wrapped, "already registered")
	assertServiceError(t, err, 409)
	var conflict *Error
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, "already-registered", conflict.Code)

	// Other errors pass through unchanged
	other := fmt.Errorf("some other error")
	assert.Equal(t, other, translateRepoError(other, "msg"))
}

func TestSplitChannel(t *testing.T) {
	t.Parallel()

	// Two-part channel
	parts := splitChannel("2.0/edge")
	assert.Equal(t, "2.0", parts.track)
	assert.Equal(t, "edge", parts.risk)

	// Single-part channel defaults to latest track
	parts = splitChannel("stable")
	assert.Equal(t, "latest", parts.track)
	assert.Equal(t, "stable", parts.risk)
}

func TestPackageChannels(t *testing.T) {
	t.Parallel()

	// Nil tracks defaults to "latest"
	channels := packageChannels(nil)
	assert.Len(t, channels, 4) // stable, candidate, beta, edge

	// Custom tracks
	channels = packageChannels([]core.Track{{Name: "2.0"}, {Name: "3.0"}})
	assert.Len(t, channels, 8) // 4 per track
}

func TestExtractBases(t *testing.T) {
	t.Parallel()

	// Empty manifest returns default base
	bases := extractBases(core.CharmManifest{})
	require.Len(t, bases, 1)
	assert.Equal(t, "ubuntu", bases[0].Name)
	assert.Equal(t, "22.04", bases[0].Channel)
	assert.Equal(t, "amd64", bases[0].Architecture)
}

func TestDetectUploadKind(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "revision", detectUploadKind("test.charm"))
	assert.Equal(t, "resource", detectUploadKind("config.yaml"))
	assert.Equal(t, "resource", detectUploadKind("image.tar"))
}

func TestChannelOrDefault(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", channelOrDefault(nil))
	empty := ""
	assert.Equal(t, "", channelOrDefault(&empty))
	stable := "latest/stable"
	assert.Equal(t, "latest/stable", channelOrDefault(&stable))
}

func TestTokenAllowsPackage(t *testing.T) {
	t.Parallel()

	token := &core.StoreToken{
		Packages: []core.PackageSelector{
			{ID: "pkg-id-1"},
			{Name: "charm-a"},
		},
	}
	pkg1 := core.Package{ID: "pkg-id-1", Name: "other-name"}
	pkg2 := core.Package{ID: "other-id", Name: "charm-a"}
	pkg3 := core.Package{ID: "other-id", Name: "charm-b"}

	assert.True(t, tokenAllowsPackage(token, pkg1))  // by ID
	assert.True(t, tokenAllowsPackage(token, pkg2))  // by name
	assert.False(t, tokenAllowsPackage(token, pkg3)) // neither
}

func TestMergeLinks(t *testing.T) {
	t.Parallel()

	existing := map[string][]string{"docs": {"https://a.com"}}
	merged := mergeLinks(existing, "https://b.com", "https://issues.com", "https://src.com", []string{"https://web.com"})

	assert.Equal(t, []string{"https://a.com", "https://b.com"}, merged["docs"])
	assert.Equal(t, []string{"https://issues.com"}, merged["issues"])
	assert.Equal(t, []string{"https://src.com"}, merged["source"])
	assert.Equal(t, []string{"https://web.com"}, merged["website"])

	// Existing links are not modified
	assert.Equal(t, []string{"https://a.com"}, existing["docs"])
}

func TestMergeLinksDeduplication(t *testing.T) {
	t.Parallel()

	existing := map[string][]string{"docs": {"https://a.com"}}
	merged := mergeLinks(existing, "https://a.com", "", "", nil)

	assert.Equal(t, []string{"https://a.com"}, merged["docs"])
}

func TestMergeLinksEmpty(t *testing.T) {
	t.Parallel()

	merged := mergeLinks(nil, "", "", "", nil)

	assert.Empty(t, merged)
}

func TestCompactID(t *testing.T) {
	t.Parallel()

	id := compactID()
	assert.Len(t, id, 32)
	assert.NotContains(t, id, "-")
}

func TestSanitizeSubject(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "oidc-google-123", sanitizeSubject("oidc|google/123"))
	assert.Equal(t, "simple", sanitizeSubject("simple"))
}

func TestInfoWithNilResourceRevision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	// Release with nil resource revision — exercises resolveReleaseResources nil-revision skip
	_, err = svc.Release(ctx, owner, "my-charm", []core.Release{{
		Channel:  "latest/stable",
		Revision: 1,
		Resources: []core.ReleaseResourceRef{
			{Name: "config", Revision: nil},
		},
	}})
	require.NoError(t, err)

	// Info calls resolveReleaseResources → nil revision branch
	info, err := svc.Info(ctx, owner, "my-charm")

	require.NoError(t, err)
	assert.NotNil(t, info["default-release"])
}

func TestRefreshWithDefaultReleaseAndResources(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	resUpload, err := svc.CreateUpload(ctx, "config.yaml", []byte("v1"))
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, "my-charm", "config", PushResourceRequest{
		UploadID: resUpload.ID, Type: "file",
	})
	require.NoError(t, err)
	_, err = svc.Release(ctx, owner, "my-charm", []core.Release{{
		Channel: "latest/stable", Revision: 1,
		Resources: []core.ReleaseResourceRef{
			{Name: "config", Revision: intPtr(1)},
		},
	}})
	require.NoError(t, err)

	// Refresh WITHOUT specifying channel — uses default release path with resource resolution
	result, err := svc.Refresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			Name:        stringPtr("my-charm"),
		}},
	})

	require.NoError(t, err)
	results := result["results"].([]map[string]any)
	require.Len(t, results, 1)
	charmEntity := results[0]["charm"].(map[string]any)
	resources := charmEntity["resources"].([]map[string]any)
	assert.Len(t, resources, 1)
}

func TestFindPublicPackageWithMultipleTracks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "trackcharm", "charm", false)
	require.NoError(t, err)
	_, err = svc.CreateTracks(ctx, owner, "trackcharm", []core.Track{{Name: "2.0"}})
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "trackcharm.charm", buildCharmArchive(t, "trackcharm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "trackcharm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	_, err = svc.Release(ctx, owner, "trackcharm", []core.Release{{
		Channel: "latest/stable", Revision: 1,
	}})
	require.NoError(t, err)

	result, err := svc.Find(ctx, core.Identity{}, "trackcharm")

	require.NoError(t, err)
	results := result["results"].([]map[string]any)
	require.Len(t, results, 1)
	assert.Equal(t, "trackcharm", results[0]["name"])
}

func TestNewError(t *testing.T) {
	t.Parallel()

	err := newError(ErrorKindInvalidRequest, "bad-request", "invalid input")
	var svcErr *Error
	require.ErrorAs(t, err, &svcErr)
	assert.Equal(t, ErrorKindInvalidRequest, svcErr.Kind)
	assert.Equal(t, "bad-request", svcErr.Code)
	assert.Equal(t, "invalid input", svcErr.Message)
	assert.Equal(t, "bad-request: invalid input", svcErr.Error())
}

func assertServiceError(t *testing.T, err error, expectedStatus int) {
	t.Helper()
	require.Error(t, err)
	var svcErr *Error
	require.ErrorAs(t, err, &svcErr)
	expectedKind := ErrorKindInvalidRequest
	switch expectedStatus {
	case 401:
		expectedKind = ErrorKindUnauthorized
	case 403:
		expectedKind = ErrorKindForbidden
	case 404:
		expectedKind = ErrorKindNotFound
	case 409:
		expectedKind = ErrorKindConflict
	}
	assert.Equal(t, expectedKind, svcErr.Kind)
}

// assertRefreshActionError verifies that a per-action error is embedded in the
// Refresh results map for the given instanceKey with the expected error code.
func assertRefreshActionError(t *testing.T, result map[string]any, instanceKey, expectedCode string) {
	t.Helper()
	results, ok := result["results"].([]map[string]any)
	require.True(t, ok, "results should be []map[string]any")
	var found bool
	for _, item := range results {
		if item["instance-key"] != instanceKey {
			continue
		}
		found = true
		assert.Equal(t, "error", item["result"], "result should be 'error'")
		apiErr, ok := item["error"].(core.APIError)
		require.True(t, ok, "error should be core.APIError")
		assert.Equal(t, expectedCode, apiErr.Code)
	}
	require.True(t, found, "no result found for instance-key %q", instanceKey)
}

func boolPtr(v bool) *bool {
	return &v
}

func newTestService() (*Service, repo.Repository) {
	return newTestServiceWithOCI(testOCIRegistry{})
}

func newTestServiceWithOCI(oci OCIRegistry) (*Service, repo.Repository) {
	repository := repo.NewMemory()
	return New(testConfig(), repository, blob.NewMemoryStore(), oci), repository
}

func newIdentity(id, username string) core.Identity {
	return core.Identity{
		Account: core.Account{
			ID:          id,
			Subject:     username,
			Username:    username,
			DisplayName: username,
			Email:       username + "@example.com",
			Validation:  "verified",
		},
		Authenticated: true,
	}
}

func testConfig() config.Config {
	return config.Config{
		PublicAPIURL:          "https://registry.example.test",
		PublicStorageURL:      "https://storage.example.test",
		PublicRegistryURL:     "https://oci.example.test",
		EnableInsecureDevAuth: true,
		HarborURL:             "https://harbor.example.test",
		HarborAPIURL:          "https://harbor.example.test/api/v2.0",
		HarborAdminUsername:   "admin",
		HarborAdminPassword:   "admin-secret",
		HarborProjectPrefix:   "charm",
		HarborPullRobotPrefix: "pull",
		HarborPushRobotPrefix: "push",
		HarborSecretKey:       "test-harbor-secret",
	}
}

type testOCIRegistry struct{}

func (testOCIRegistry) SyncPackage(_ context.Context, pkg core.Package) (core.Package, error) {
	if pkg.HarborProject == "" {
		pkg.HarborProject = "charm-" + pkg.Name
	}
	if pkg.HarborPushRobot == nil {
		pkg.HarborPushRobot = &core.RobotCredential{ID: 1, Username: "robot$push-" + pkg.ID, EncryptedSecret: "push"}
	}
	if pkg.HarborPullRobot == nil {
		pkg.HarborPullRobot = &core.RobotCredential{ID: 2, Username: "robot$pull-" + pkg.ID, EncryptedSecret: "pull"}
	}
	now := time.Now().UTC()
	pkg.HarborSyncedAt = &now
	return pkg, nil
}

func (testOCIRegistry) ImageReference(pkg core.Package, resourceName string) (string, error) {
	return "oci.example.test/" + pkg.HarborProject + "/" + resourceName, nil
}

func (testOCIRegistry) Credentials(pkg core.Package, pull bool) (string, string, error) {
	if pull {
		return pkg.HarborPullRobot.Username, "pull-secret", nil
	}
	return pkg.HarborPushRobot.Username, "push-secret", nil
}

type failingOCIRegistry struct {
	testOCIRegistry
	syncErr        error
	imageRefErr    error
	credentialsErr error
}

func (o failingOCIRegistry) SyncPackage(ctx context.Context, pkg core.Package) (core.Package, error) {
	if o.syncErr != nil {
		return core.Package{}, o.syncErr
	}
	return o.testOCIRegistry.SyncPackage(ctx, pkg)
}

func (o failingOCIRegistry) ImageReference(pkg core.Package, resourceName string) (string, error) {
	if o.imageRefErr != nil {
		return "", o.imageRefErr
	}
	return o.testOCIRegistry.ImageReference(pkg, resourceName)
}

func (o failingOCIRegistry) Credentials(pkg core.Package, pull bool) (string, string, error) {
	if o.credentialsErr != nil {
		return "", "", o.credentialsErr
	}
	return o.testOCIRegistry.Credentials(pkg, pull)
}

func buildCharmArchive(t *testing.T, name string) []byte {
	t.Helper()

	var payload bytes.Buffer
	writer := zip.NewWriter(&payload)

	files := map[string]string{
		"metadata.yaml": "name: " + name + "\n" +
			"display-name: Demo Charm\n" +
			"summary: Demo summary\n" +
			"description: Demo description\n" +
			"docs: https://example.com/docs\n" +
			"issues: https://example.com/issues\n" +
			"source: https://example.com/source\n" +
			"website:\n" +
			"  - https://example.com\n" +
			"resources:\n" +
			"  config:\n" +
			"    type: file\n" +
			"    filename: config.yaml\n" +
			"    description: Config file\n" +
			"containers:\n" +
			"  workload:\n" +
			"    resource: workload-image\n" +
			"provides:\n" +
			"  db:\n" +
			"    interface: postgresql_client\n",
		"config.yaml": "options: {}\n",
		"README.md":   "# Demo\n",
	}

	for name, content := range files {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(content))
		require.NoError(t, err)
	}

	require.NoError(t, writer.Close())
	return payload.Bytes()
}

func intPtr(value int) *int {
	return &value
}
