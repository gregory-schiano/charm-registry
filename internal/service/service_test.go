package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
	"github.com/gschiano/charm-registry/internal/testutil"
)

func TestPackagePublishedSupportsInfoAndRefresh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("owner-1", "owner")
	pkg, err := svc.RegisterPackage(ctx, owner, "demo-charm", "charm", true)
	require.NoError(t, err)
	findResult, err := svc.SearchPackages(ctx, owner, "demo")
	require.NoError(t, err)
	assert.Len(t, findResult.Results, 0)
	upload, err := svc.CreateUpload(ctx, "demo-charm.charm", buildCharmArchive(t, "demo-charm"))
	require.NoError(t, err)
	statusURL, err := svc.PushRevision(ctx, owner, pkg.Name, PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	assert.Contains(t, statusURL, "/v1/charm/demo-charm/revisions/review")
	resourceUpload, err := svc.CreateUpload(ctx, "config.yaml", []byte("debug: true\n"))
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, pkg.Name, "config", PushResourceRequest{
		UploadID: resourceUpload.ID,
		Type:     "file",
	})
	require.NoError(t, err)
	released, err := svc.CreateRelease(ctx, owner, pkg.Name, []core.Release{{
		Channel:  "latest/stable",
		Revision: 1,
		Resources: []core.ReleaseResourceRef{{
			Name:     "config",
			Revision: intPtr(1),
		}},
	}})
	require.NoError(t, err)
	assert.Len(t, released, 1)
	info, err := svc.GetPackageInfo(ctx, owner, pkg.Name)
	require.NoError(t, err)
	assert.Equal(t, pkg.ID, info.ID)
	defaultRelease := info.DefaultRelease
	defaultRevision := defaultRelease.Revision
	assert.Equal(t, 1, defaultRevision.Revision)
	defaultResources := defaultRelease.Resources
	require.Len(t, defaultResources, 1) // guards index access below
	assert.Equal(t, "config", defaultResources[0].Name)
	refresh, err := svc.ResolveRefresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			Name:        stringPtr("demo-charm"),
			Channel:     stringPtr("latest/stable"),
		}},
	})
	require.NoError(t, err)
	results := refresh.Results
	require.Len(t, results, 1) // guards index access below
	assert.Equal(t, pkg.ID, results[0].ID)
	require.NotNil(t, results[0].Charm)
	charmEntity := results[0].Charm
	assert.Equal(t, "demo-charm", charmEntity.Name)
	assert.Equal(t, 1, charmEntity.Revision)
	assert.Len(t, charmEntity.Resources, 1)
	// the package default track instead of looking for a literal "stable"
	// release.
	refresh, err = svc.ResolveRefresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "install",
			InstanceKey: "app/0",
			Name:        stringPtr("demo-charm"),
			Channel:     stringPtr("stable"),
		}},
	})
	require.NoError(t, err)
	results = refresh.Results
	require.Len(t, results, 1)
	require.Nil(t, results[0].Error)
	assert.Equal(t, "latest/stable", results[0].EffectiveChannel)
	require.NotNil(t, results[0].Charm)
	assert.Equal(t, 1, results[0].Charm.Revision)
	// force base-specific release lookup for a normal manually published charm.
	refresh, err = svc.ResolveRefresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "install",
			InstanceKey: "app/0",
			Name:        stringPtr("demo-charm"),
			Channel:     stringPtr("stable"),
			Base:        &core.Base{Name: "NA", Channel: "NA", Architecture: "amd64"},
		}},
	})
	require.NoError(t, err)
	results = refresh.Results
	require.Len(t, results, 1)
	require.Nil(t, results[0].Error)
	assert.Equal(t, "latest/stable", results[0].EffectiveChannel)
	require.NotNil(t, results[0].Charm)
	assert.Equal(t, 1, results[0].Charm.Revision)
	// revision base. Manually published releases are channel-scoped, so this
	// should fall back to the channel release when no per-base release exists.
	refresh, err = svc.ResolveRefresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "download",
			InstanceKey: "app/0",
			Name:        stringPtr("demo-charm"),
			Channel:     stringPtr("latest/stable"),
			Base:        &core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"},
		}},
	})
	require.NoError(t, err)
	results = refresh.Results
	require.Len(t, results, 1)
	require.Nil(t, results[0].Error)
	assert.Equal(t, "latest/stable", results[0].EffectiveChannel)
	require.NotNil(t, results[0].Charm)
	assert.Equal(t, 1, results[0].Charm.Revision)
	creds, err := svc.OCIImageUploadCredentials(ctx, owner, pkg.Name, "workload-image")
	require.NoError(t, err)
	blobPayload, err := svc.OCIImageBlob(ctx, owner, pkg.Name, "workload-image", "sha256:deadbeef")
	require.NoError(t, err)
	assert.Contains(t, creds.ImageName, "demo-charm/workload-image")
	assert.Contains(t, blobPayload, `"Digest":"sha256:deadbeef"`)
}

func TestPrivatePackagesRequireAuthentication(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("owner-2", "owner")
	pkg, err := svc.RegisterPackage(ctx, owner, "secret-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "secret-charm.charm", buildCharmArchive(t, "secret-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, pkg.Name, PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	_, err = svc.CreateRelease(ctx, owner, pkg.Name, []core.Release{{
		Channel:  "latest/stable",
		Revision: 1,
	}})
	require.NoError(t, err)

	// Anonymous search is public but must omit private packages.
	results, err := svc.SearchPackages(ctx, core.Identity{}, "secret")
	require.NoError(t, err)
	assert.Empty(t, results.Results)

	// Direct private-package access still requires authentication.
	_, err = svc.GetPackage(ctx, core.Identity{}, "secret-charm", false)
	require.Error(t, err)
	var svcErr *Error
	require.ErrorAs(t, err, &svcErr)
	assert.Equal(t, ErrorKindUnauthorized, svcErr.Kind)
}

func TestAnonymousPublicConsumerServiceAccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("public-owner", "public-owner")
	anonymous := core.Identity{}

	pkg, err := svc.RegisterPackage(ctx, owner, "anonymous-public-charm", "charm", false)
	require.NoError(t, err)
	charmPayload := buildCharmArchive(t, "anonymous-public-charm")
	charmUpload, err := svc.CreateUpload(ctx, "anonymous-public-charm.charm", charmPayload)
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, pkg.Name, PushRevisionRequest{UploadID: charmUpload.ID})
	require.NoError(t, err)

	resourcePayload := []byte("anonymous resource")
	resourceUpload, err := svc.CreateUpload(ctx, "config.yaml", resourcePayload)
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, pkg.Name, "config", PushResourceRequest{
		UploadID: resourceUpload.ID,
		Type:     "file",
	})
	require.NoError(t, err)
	_, err = svc.CreateRelease(ctx, owner, pkg.Name, []core.Release{{
		Channel:  "latest/stable",
		Revision: 1,
		Resources: []core.ReleaseResourceRef{{
			Name:     "config",
			Revision: intPtr(1),
		}},
	}})
	require.NoError(t, err)

	search, err := svc.SearchPackages(ctx, anonymous, "anonymous-public")
	require.NoError(t, err)
	require.Len(t, search.Results, 1)

	info, err := svc.GetPackageInfo(ctx, anonymous, pkg.Name)
	require.NoError(t, err)
	assert.Equal(t, pkg.ID, info.ID)

	refresh, err := svc.ResolveRefresh(ctx, anonymous, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "anonymous/0",
			Name:        stringPtr(pkg.Name),
			Channel:     stringPtr("latest/stable"),
		}},
	})
	require.NoError(t, err)
	require.Len(t, refresh.Results, 1)
	require.Nil(t, refresh.Results[0].Error)

	resourceRevisions, err := svc.ListResourceRevisions(ctx, anonymous, pkg.Name, "config")
	require.NoError(t, err)
	require.Len(t, resourceRevisions, 1)

	charmReader, _, err := svc.DownloadCharmStream(ctx, anonymous, pkg.ID, 1)
	require.NoError(t, err)
	charmContent, err := io.ReadAll(charmReader)
	require.NoError(t, err)
	require.NoError(t, charmReader.Close())
	assert.Equal(t, charmPayload, charmContent)

	resourceReader, _, err := svc.DownloadResourceStream(ctx, anonymous, pkg.ID, "config", 1)
	require.NoError(t, err)
	resourceContent, err := io.ReadAll(resourceReader)
	require.NoError(t, err)
	require.NoError(t, resourceReader.Close())
	assert.Equal(t, resourcePayload, resourceContent)
}

func TestRegisterPackageDoesNotRequireOCIProvisioning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, repository := newTestServiceWithOCI(failingOCIRegistry{
		syncErr: fmt.Errorf("oci unavailable"),
	})
	owner := newIdentity("owner-oci", "owner-oci")
	pkg, err := svc.RegisterPackage(ctx, owner, "broken-charm", "charm", true)
	require.NoError(t, err)

	stored, err := repository.GetPackageByName(ctx, "broken-charm")
	require.NoError(t, err)
	assert.Equal(t, pkg.ID, stored.ID)
	assert.Empty(t, stored.OCIProject)

}

func TestServiceUsesInjectedClockForTimestamps(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fixed := time.Date(2024, time.March, 4, 5, 6, 7, 0, time.FixedZone("UTC+2", 2*60*60))
	expected := fixed.UTC()
	svc, _ := newTestServiceWithClock(func() time.Time { return fixed }, testutil.OCIRegistry{})
	owner := newIdentity("owner-clock", "owner-clock")

	pkg, err := svc.RegisterPackage(ctx, owner, "clocked-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "clocked-charm.charm", buildCharmArchive(t, "clocked-charm"))
	require.NoError(t, err)
	_, token, err := svc.IssueStoreToken(ctx, owner, IssueTokenRequest{TTL: intPtr(3600)})
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, pkg.Name, PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	revisions, err := svc.ListRevisions(ctx, owner, pkg.Name, nil)
	require.NoError(t, err)
	require.Len(t, revisions, 1)
	require.Len(t, pkg.Tracks, 1)

	assert.True(t, pkg.CreatedAt.Equal(expected))
	assert.True(t, pkg.UpdatedAt.Equal(expected))
	assert.True(t, pkg.Tracks[0].CreatedAt.Equal(expected))
	assert.True(t, upload.CreatedAt.Equal(expected))
	assert.True(t, token.ValidSince.Equal(expected))
	assert.True(t, token.ValidUntil.Equal(expected.Add(time.Hour)))
	assert.True(t, revisions[0].CreatedAt.Equal(expected))
}

func TestCreateUploadStreamProvidesSeekableBlobPayload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repository := repo.NewMemory()
	store := &seekablePayloadStore{}
	svc := New(testConfig(), repository, store, testutil.OCIRegistry{})

	upload, err := svc.CreateUploadStream(ctx, "seekable.charm", bytes.NewBufferString("payload"))
	require.NoError(t, err)

	assert.Equal(t, int64(len("payload")), upload.Size)
	assert.Equal(t, []byte("payload"), store.payload)
}

func TestRefreshSelectsReleaseVariantByArchitecture(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "multiarch-charm", "charm", true)
	require.NoError(t, err)

	uploadAMD64, err := svc.CreateUpload(ctx, "multiarch-charm.charm", buildCharmArchive(t, "multiarch-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "multiarch-charm", PushRevisionRequest{UploadID: uploadAMD64.ID})
	require.NoError(t, err)

	uploadARM64, err := svc.CreateUpload(ctx, "multiarch-charm.charm", buildCharmArchive(t, "multiarch-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "multiarch-charm", PushRevisionRequest{UploadID: uploadARM64.ID})
	require.NoError(t, err)

	_, err = svc.CreateRelease(ctx, owner, "multiarch-charm", []core.Release{
		{
			Channel:  "16/edge",
			Revision: 1,
			Base:     &core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"},
		},
		{
			Channel:  "16/edge",
			Revision: 2,
			Base:     &core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "arm64"},
		},
	})
	require.NoError(t, err)
	result, err := svc.ResolveRefresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "install",
			InstanceKey: "app/0",
			Name:        stringPtr("multiarch-charm"),
			Channel:     stringPtr("16/edge"),
			Base:        &core.Base{Name: "NA", Channel: "NA", Architecture: "amd64"},
		}},
	})
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	require.Nil(t, result.Results[0].Error)
	require.NotNil(t, result.Results[0].Charm)
	assert.Equal(t, 1, result.Results[0].Charm.Revision)
}

func TestOCIImageUploadCredentialsHidesProvisioningCauseFromMessage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	expectedErr := errors.New("harbor backend: robot account quota exceeded")
	svc, _ := newTestServiceWithOCI(failingOCIRegistry{
		syncErr: expectedErr,
	})
	owner := newIdentity("owner-provision", "owner-provision")

	pkg, err := svc.RegisterPackage(ctx, owner, "provision-charm", "charm", true)
	require.NoError(t, err)

	upload, err := svc.CreateUpload(ctx, "provision-charm.charm", buildCharmArchive(t, "provision-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, pkg.Name, PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)

	_, err = svc.OCIImageUploadCredentials(ctx, owner, pkg.Name, "workload-image")
	require.Error(t, err)
	assert.ErrorIs(t, err, expectedErr)

	var svcErr *Error
	require.ErrorAs(t, err, &svcErr)
	assert.Equal(t, "oci-provisioning-unavailable", svcErr.Code)
	assert.Equal(t, "OCI package provisioning is temporarily unavailable", svcErr.Message)
	assert.NotContains(t, svcErr.Error(), "robot account quota")
}

func TestOCIImageUploadCredentialsPropagatesCredentialFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	expectedErr := errors.New("robot credentials unavailable")
	svc, _ := newTestServiceWithOCI(failingOCIRegistry{
		credentialsErr: expectedErr,
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
	assert.ErrorIs(t, err, expectedErr)

}

func TestIssueStoreTokenAndAuthenticate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, repository := newTestService()
	identity, err := svc.ResolveIdentity(ctx, auth.Claims{
		Subject:     "oidc|alice",
		Username:    "alice",
		DisplayName: "Alice Example",
		Email:       "alice@example.com",
	}, nil)
	require.NoError(t, err)
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
	assert.NotEmpty(t, raw)
	assert.Equal(t, identity.Account.ID, token.AccountID)
	authenticator, err := auth.New(ctx, testConfig(), repository)
	require.NoError(t, err)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	claims, storeToken, err := authenticator.Authenticate(req)
	require.NoError(t, err)
	require.NotNil(t, storeToken) // guards storeToken field access below
	assert.Equal(t, identity.Account.Username, claims.Username)
	assert.Equal(t, token.SessionID, storeToken.SessionID)
	whoami, err := svc.MacaroonInfo(core.Identity{
		Account:       identity.Account,
		Token:         storeToken,
		Authenticated: true,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"latest/stable"}, whoami.Channels)
	assert.Equal(t, []string{permPackageView}, whoami.Permissions)
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

func TestGetRootDocumentReturnsServiceMetadata(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService()
	doc := svc.GetRootDocument()
	assert.Equal(t, "private-charm-registry", doc.ServiceName)
	assert.Equal(t, "v1", doc.Version)
	assert.Equal(t, "https://registry.example.test", doc.APIURL)
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
	svc := New(cfg, repository, blob.NewMemoryStore(), testutil.OCIRegistry{})
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
	_, err = svc.CreateRelease(ctx, owner, pkg.Name, []core.Release{{
		Channel:  "latest/stable",
		Revision: 2,
		Resources: []core.ReleaseResourceRef{{
			Name:     "config",
			Revision: intPtr(1),
		}},
	}})

	svcErr := serviceError(t, err)
	assert.Equal(t, ErrorKindInvalidRequest, svcErr.Kind)
	assert.Equal(t, "invalid-request", svcErr.Code)
	assert.Equal(t, `resource "config" revision 1 is not compatible with package revision 2`, svcErr.Message)

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
	tokens, err := svc.ListStoreTokens(ctx, owner, false)
	require.NoError(t, err)
	assert.Empty(t, tokens)
	all, err := svc.ListStoreTokens(ctx, owner, true)
	require.NoError(t, err)
	assert.Len(t, all, 1)

}

func TestMacaroonInfoWithoutToken(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	info, err := svc.MacaroonInfo(owner)
	require.NoError(t, err)
	assert.Equal(t, "alice", info.Account.Username)
	assert.Equal(t, []string{}, info.Permissions)
	assert.Equal(t, []string{}, info.Channels)

}

func TestDeprecatedWhoAmI(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	result, err := svc.DeprecatedWhoAmI(owner)
	require.NoError(t, err)
	assert.Equal(t, "alice", result.Username)
	assert.Equal(t, "acc-1", result.ID)

}

func TestRegisterPackageDuplicate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", false)
	require.NoError(t, err)
	_, err = svc.RegisterPackage(ctx, owner, "my-charm", "charm", false)

	svcErr := serviceError(t, err)
	assert.Equal(t, ErrorKindConflict, svcErr.Kind)
	assert.Equal(t, "already-registered", svcErr.Code)
	assert.Equal(t, messagePackageAlreadyExists, svcErr.Message)
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

func TestRegisterPackageInvalidNameReturnsServiceError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	_, err := svc.RegisterPackage(ctx, owner, "   ", "charm", false)

	svcErr := serviceError(t, err)
	assert.Equal(t, ErrorKindInvalidRequest, svcErr.Kind)
	assert.Equal(t, "invalid-request", svcErr.Code)
	assert.Equal(t, "package name is required", svcErr.Message)
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
	assertServiceError(t, err, ErrorKindForbidden)

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
	_, err = svc.GetPackage(ctx, owner, "empty-charm", true)
	assertServiceError(t, err, ErrorKindNotFound)

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
	assertServiceError(t, err, ErrorKindInvalidRequest)

}

func TestCreateUploadSetsKindFromFilename(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	charmUpload, err := svc.CreateUpload(ctx, "test.charm", []byte("data"))
	require.NoError(t, err)
	resourceUpload, err := svc.CreateUpload(ctx, "config.yaml", []byte("data"))
	require.NoError(t, err)
	assert.Equal(t, "revision", charmUpload.Kind)
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
	require.Len(t, result.Revisions, 1)
	assert.Equal(t, "approved", result.Revisions[0].Status)

}

func TestReviewUploadNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	_, err = svc.ReviewUpload(ctx, owner, "my-charm", "nonexistent")
	assertServiceError(t, err, ErrorKindNotFound)

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
	_, err = svc.CreateRelease(ctx, owner, "my-charm", []core.Release{{
		Channel: "", Revision: 1,
	}})
	assertServiceError(t, err, ErrorKindInvalidRequest)

}

func TestCreateReleaseRejectsIncompleteBase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		base        core.Base
		errContains string
	}{
		{
			name:        "missing name",
			base:        core.Base{Channel: "24.04", Architecture: "amd64"},
			errContains: "base name is required",
		},
		{
			name:        "missing channel",
			base:        core.Base{Name: "ubuntu", Architecture: "amd64"},
			errContains: "base channel is required",
		},
		{
			name:        "missing architecture",
			base:        core.Base{Name: "ubuntu", Channel: "24.04"},
			errContains: "base architecture is required",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
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

			_, err = svc.CreateRelease(ctx, owner, "my-charm", []core.Release{{
				Channel: "latest/stable", Revision: 1, Base: &tt.base,
			}})

			svcErr := serviceError(t, err)
			assert.Equal(t, ErrorKindInvalidRequest, svcErr.Kind)
			assert.Contains(t, svcErr.Message, tt.errContains)
		})
	}
}

func TestCreateReleaseAcceptsValidBase(t *testing.T) {
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
	base := core.Base{Name: " ubuntu ", Channel: " 24.04 ", Architecture: " amd64 "}

	released, err := svc.CreateRelease(ctx, owner, "my-charm", []core.Release{{
		Channel: "latest/stable", Revision: 1, Base: &base,
	}})

	require.NoError(t, err)
	require.Len(t, released, 1)
	require.NotNil(t, released[0].Base)
	assert.Equal(t, core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"}, *released[0].Base)
}

func TestReleaseNonExistentRevisionFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	_, err = svc.CreateRelease(ctx, owner, "my-charm", []core.Release{{
		Channel: "latest/stable", Revision: 999,
	}})
	assertServiceError(t, err, ErrorKindNotFound)

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
	_, err = svc.CreateRelease(ctx, owner, "my-charm", []core.Release{{
		Channel: "latest/stable", Revision: 1,
	}})
	assertServiceError(t, err, ErrorKindForbidden)

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
	_, err = svc.CreateRelease(ctx, owner, "my-charm", []core.Release{{
		Channel: "latest/stable", Revision: 1,
	}})
	require.NoError(t, err)
	result, err := svc.ListReleases(ctx, owner, "my-charm")
	require.NoError(t, err)
	assert.Len(t, result.ChannelMap, 1)

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

func TestDownloadResourceReturnsOCIError(t *testing.T) {
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

	ociBlob := []byte(`{"ImageName":"oci.example.test/charm-my-charm/workload-image","Digest":"sha256:test"}`)
	ociUpload, err := svc.CreateUpload(ctx, "blob.json", ociBlob)
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, "my-charm", "workload-image", PushResourceRequest{
		UploadID: ociUpload.ID, Type: "oci-image",
	})
	require.NoError(t, err)

	pkg, err := svc.GetPackage(ctx, owner, "my-charm", true)
	require.NoError(t, err)

	_, err = svc.OCIImageUploadCredentials(ctx, owner, "my-charm", "workload-image")
	require.NoError(t, err)
	expectedErr := errors.New("boom")

	svc.oci = failingOCIRegistry{
		OCIRegistry:    testutil.OCIRegistry{},
		credentialsErr: expectedErr,
	}

	_, err = svc.DownloadResource(ctx, owner, pkg.ID, "workload-image", 1)

	require.Error(t, err)
	assert.ErrorIs(t, err, expectedErr)

}

func TestDownloadResourceOCIImageRequiresProvisionedPackage(t *testing.T) {
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

	ociBlob := []byte(`{"ImageName":"oci.example.test/charm-my-charm/workload-image","Digest":"sha256:test"}`)
	ociUpload, err := svc.CreateUpload(ctx, "blob.json", ociBlob)
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, "my-charm", "workload-image", PushResourceRequest{
		UploadID: ociUpload.ID, Type: "oci-image",
	})
	require.NoError(t, err)

	pkg, err := svc.GetPackage(ctx, owner, "my-charm", true)
	require.NoError(t, err)
	_, err = svc.DownloadResource(ctx, owner, pkg.ID, "workload-image", 1)
	assertServiceError(t, err, ErrorKindConflict)
	var svcErr *Error
	require.ErrorAs(t, err, &svcErr)
	assert.Equal(t, "oci-not-provisioned", svcErr.Code)

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
	assertServiceError(t, err, ErrorKindInvalidRequest)

}

func TestPushRevisionReturnsUploadReviewRecordingError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseRepo := repo.NewMemory()
	svc := New(
		testConfig(),
		approveUploadFailingRepository{Repository: baseRepo, err: assert.AnError},
		blob.NewMemoryStore(),
		testutil.OCIRegistry{},
	)
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", []byte("not a valid archive"))
	require.NoError(t, err)

	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})

	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
	assert.NotEqual(t, assert.AnError, err)
}

func TestPushRevisionRollsBackOnPackageUpdateFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repository, err := repo.NewSQLite(ctx, t.TempDir()+"/registry.sqlite")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, repository.Close())
	})
	require.NoError(t, repository.Migrate(ctx))
	blobs := blob.NewMemoryStore()
	clock := func() time.Time {
		return time.Date(2024, time.April, 5, 6, 7, 8, 0, time.UTC)
	}
	setupSvc := New(testConfig(), repository, blobs, testutil.OCIRegistry{})
	setupSvc.Clock = clock
	owner, err := setupSvc.ResolveIdentity(ctx, auth.Claims{
		Subject:     "oidc|alice",
		Username:    "alice",
		DisplayName: "alice",
		Email:       "alice@example.com",
	}, nil)
	require.NoError(t, err)
	pkg, err := setupSvc.RegisterPackage(ctx, owner, "rollback-charm", "charm", true)
	require.NoError(t, err)
	upload, err := setupSvc.CreateUpload(ctx, "rollback-charm.charm", buildCharmArchive(t, "rollback-charm"))
	require.NoError(t, err)

	expectedErr := errors.New("update package failed")
	svc := New(
		testConfig(),
		updatePackageFailingRepository{Repository: repository, err: expectedErr},
		blobs,
		testutil.OCIRegistry{},
	)
	svc.Clock = clock

	_, err = svc.PushRevision(ctx, owner, pkg.Name, PushRevisionRequest{UploadID: upload.ID})
	require.Error(t, err)
	assert.ErrorIs(t, err, expectedErr)

	revisions, err := repository.ListRevisions(ctx, pkg.ID, nil)
	require.NoError(t, err)
	assert.Empty(t, revisions)

	storedUpload, err := repository.GetUpload(ctx, upload.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", storedUpload.Status)
	assert.Nil(t, storedUpload.ApprovedAt)
	assert.Nil(t, storedUpload.Revision)
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
	scopedIdentity := newIdentity("acc-1", "alice")
	scopedIdentity.Token = &core.StoreToken{
		Packages:    []core.PackageSelector{{Name: "charm-a", Type: "charm"}},
		Permissions: []string{permPackageManage},
	}
	_, err = svc.GetPackage(ctx, scopedIdentity, "charm-a", true)
	require.NoError(t, err)
	_, err = svc.GetPackage(ctx, scopedIdentity, "charm-b", true)
	assertServiceError(t, err, ErrorKindForbidden)
}

func TestFindPublicPackages(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "public-charm", "charm", false)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "public-charm.charm", buildCharmArchive(t, "public-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "public-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	_, err = svc.CreateRelease(ctx, owner, "public-charm", []core.Release{{
		Channel: "latest/stable", Revision: 1,
	}})
	require.NoError(t, err)
	result, err := svc.SearchPackages(ctx, newIdentity("finder-2", "finder"), "public")

	require.NoError(t, err)
	assert.Len(t, result.Results, 1)
	assert.Equal(t, "public-charm", result.Results[0].Name)
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
	_, err = svc.CreateRelease(ctx, owner, "my-charm", []core.Release{{
		Channel: "latest/stable", Revision: 1,
	}})
	require.NoError(t, err)
	result, err := svc.ResolveRefresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			ID:          &pkg.ID,
			Channel:     stringPtr("latest/stable"),
		}},
	})

	require.NoError(t, err)
	results := result.Results
	require.Len(t, results, 1)
	assert.Equal(t, pkg.ID, results[0].ID)
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
	result, err := svc.ResolveRefresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			Name:        stringPtr("my-charm"),
			Revision:    intPtr(1),
		}},
	})

	require.NoError(t, err)
	results := result.Results
	require.Len(t, results, 1)
}

func TestRefreshMissingIDAndName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	// Per the Charmhub refresh contract, action-level errors are embedded
	// inside the results array — the top-level call succeeds (no error).
	result, err := svc.ResolveRefresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
		}},
	})
	require.NoError(t, err)
	results := result.Results
	require.Len(t, results, 1)
	assert.Equal(t, "error", results[0].Result)
	require.NotNil(t, results[0].Error)
	assert.Equal(t, "invalid-request", results[0].Error.Code)

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
	assertServiceError(t, err, ErrorKindForbidden)

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

func TestPublicPackageAccessibleAnonymously(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "public-charm", "charm", false)
	require.NoError(t, err)

	// Anonymous callers can view public packages.
	pkg, err := svc.GetPackage(ctx, core.Identity{}, "public-charm", true)
	require.NoError(t, err)
	assert.Equal(t, "public-charm", pkg.Name)

	// Authenticated user can view public packages.
	pkg, err = svc.GetPackage(ctx, newIdentity("other-1", "bob"), "public-charm", true)
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
	_, err = svc.CreateRelease(ctx, owner, "my-charm", []core.Release{{
		Channel: "latest/stable", Revision: 1,
	}})
	require.NoError(t, err)
	result, err := svc.ResolveRefresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			Name:        stringPtr("my-charm"),
		}},
	})

	require.NoError(t, err)
	results := result.Results
	require.Len(t, results, 1)
	assert.Equal(t, "latest/stable", results[0].EffectiveChannel)
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
	assertServiceError(t, err, ErrorKindNotFound)

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

	_, err = svc.OCIImageUploadCredentials(ctx, owner, "my-charm", "workload-image")
	require.NoError(t, err)

	pkg, err := svc.GetPackage(ctx, owner, "my-charm", true)
	require.NoError(t, err)
	payload, err := svc.DownloadResource(ctx, owner, pkg.ID, "workload-image", 1)

	require.NoError(t, err)
	assert.Contains(t, string(payload), `"Digest":"sha256:test"`)
	assert.Contains(t, string(payload), `"ImageName":"oci.example.test/charm-my-charm/workload-image@sha256:test"`)
	assert.Contains(t, string(payload), `"RegistryPath":"oci.example.test/charm-my-charm/workload-image@sha256:test"`)
	assert.Contains(t, string(payload), `"Username":"robot$pull-`)
	assert.Contains(t, string(payload), `"username":"robot$pull-`)
}

func TestDownloadResourceOCIImageUsesStoredCredentialsWithoutResync(t *testing.T) {
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

	ociBlob := []byte(`{"ImageName":"oci.example.test/charm-my-charm/workload-image","Digest":"sha256:test"}`)
	ociUpload, err := svc.CreateUpload(ctx, "blob.json", ociBlob)
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, "my-charm", "workload-image", PushResourceRequest{
		UploadID: ociUpload.ID, Type: "oci-image",
	})
	require.NoError(t, err)

	_, err = svc.OCIImageUploadCredentials(ctx, owner, "my-charm", "workload-image")
	require.NoError(t, err)
	svc.oci = failingOCIRegistry{
		OCIRegistry: testutil.OCIRegistry{},
		syncErr:     errors.New("oci unavailable"),
	}
	pkg, err := svc.GetPackage(ctx, owner, "my-charm", true)
	require.NoError(t, err)
	payload, err := svc.DownloadResource(ctx, owner, pkg.ID, "workload-image", 1)

	require.NoError(t, err)
	assert.Contains(t, string(payload), `"Digest":"sha256:test"`)
	assert.Contains(t, string(payload), `"ImageName":"oci.example.test/charm-my-charm/workload-image@sha256:test"`)
	assert.Contains(t, string(payload), `"RegistryPath":"oci.example.test/charm-my-charm/workload-image@sha256:test"`)
	assert.Contains(t, string(payload), `"Username":"robot$pull-`)
	assert.Contains(t, string(payload), `"username":"robot$pull-`)

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
	released, err := svc.CreateRelease(ctx, owner, "my-charm", []core.Release{
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
	_, err = svc.CreateRelease(ctx, owner, "my-charm", []core.Release{{
		Channel: "latest/stable", Revision: 1,
		Resources: []core.ReleaseResourceRef{{Name: "config", Revision: intPtr(1)}},
	}})
	require.NoError(t, err)
	// Refresh with resource revision override
	result, err := svc.ResolveRefresh(ctx, owner, RefreshRequest{
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
	results := result.Results
	assert.Len(t, results, 1)

}

func TestRefreshChannelSwitchIgnoresCurrentRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	for i := 0; i < 22; i++ {
		upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
		require.NoError(t, err)
		_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
		require.NoError(t, err)
	}

	_, err = svc.CreateRelease(ctx, owner, "my-charm", []core.Release{
		{Channel: "latest/stable", Revision: 2},
		{Channel: "latest/edge", Revision: 22},
	})
	require.NoError(t, err)

	result, err := svc.ResolveRefresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			Name:        stringPtr("my-charm"),
			Revision:    intPtr(2),
			Channel:     stringPtr("latest/edge"),
		}},
	})

	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	require.Nil(t, result.Results[0].Error)
	require.NotNil(t, result.Results[0].Charm)
	assert.Equal(t, "latest/edge", result.Results[0].EffectiveChannel)
	assert.Equal(t, 22, result.Results[0].Charm.Revision)
}

func TestRefreshUsesContextTrackingChannel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	pkg, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)

	for i := 0; i < 22; i++ {
		upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
		require.NoError(t, err)
		_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
		require.NoError(t, err)
	}

	_, err = svc.CreateRelease(ctx, owner, "my-charm", []core.Release{
		{
			Channel:  "latest/stable",
			Revision: 2,
			Base:     &core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"},
		},
		{
			Channel:  "latest/edge",
			Revision: 22,
			Base:     &core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"},
		},
	})
	require.NoError(t, err)

	result, err := svc.ResolveRefresh(ctx, owner, RefreshRequest{
		Context: []RefreshContext{{
			InstanceKey:     "app/0",
			ID:              pkg.ID,
			Revision:        2,
			TrackingChannel: "latest/edge",
			Base:            &core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"},
		}},
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			ID:          &pkg.ID,
		}},
	})

	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	require.Nil(t, result.Results[0].Error)
	require.NotNil(t, result.Results[0].Charm)
	assert.Equal(t, "latest/edge", result.Results[0].EffectiveChannel)
	assert.Equal(t, 22, result.Results[0].Charm.Revision)
}

func TestRefreshWithDirectRevisionAndResourceOverrideWithoutReleaseResources(t *testing.T) {
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

	ociBlob := []byte(`{"ImageName":"oci.example.test/charm-my-charm/workload-image","Digest":"sha256:test"}`)
	ociUpload, err := svc.CreateUpload(ctx, "blob.json", ociBlob)
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, "my-charm", "workload-image", PushResourceRequest{
		UploadID: ociUpload.ID,
		Type:     "oci-image",
	})
	require.NoError(t, err)
	result, err := svc.ResolveRefresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			ID:          stringPtr(pkg.ID),
			Name:        stringPtr("my-charm"),
			Revision:    intPtr(1),
			ResourceRevisions: []core.ReleaseResourceRef{
				{Name: "workload-image", Revision: intPtr(1)},
			},
		}},
	})
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	require.NotNil(t, result.Results[0].Charm)
	require.Len(t, result.Results[0].Charm.Resources, 1)
	assert.Equal(t, "workload-image", result.Results[0].Charm.Resources[0].Name)
	assert.Equal(t, 1, result.Results[0].Charm.Resources[0].Revision)
	assert.Equal(t, "oci-image", result.Results[0].Charm.Resources[0].Type)
	assert.NotEmpty(t, result.Results[0].Charm.Resources[0].Download.URL)

}

func TestRefreshWithDirectRevisionAndResourceOverrideUsesAttachedReleaseResource(t *testing.T) {
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

	ociBlob := []byte(`{"ImageName":"oci.example.test/charm-my-charm/workload-image","Digest":"sha256:test"}`)
	ociUpload, err := svc.CreateUpload(ctx, "blob.json", ociBlob)
	require.NoError(t, err)
	_, err = svc.PushResource(ctx, owner, "my-charm", "workload-image", PushResourceRequest{
		UploadID: ociUpload.ID,
		Type:     "oci-image",
	})
	require.NoError(t, err)

	_, err = svc.CreateRelease(ctx, owner, "my-charm", []core.Release{{
		Channel:  "latest/stable",
		Revision: 1,
		Resources: []core.ReleaseResourceRef{
			{Name: "workload-image", Revision: intPtr(1)},
		},
	}})
	require.NoError(t, err)
	result, err := svc.ResolveRefresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "download",
			InstanceKey: "app/0",
			ID:          stringPtr(pkg.ID),
			Revision:    intPtr(1),
			ResourceRevisions: []core.ReleaseResourceRef{
				{Name: "workload-image", Revision: intPtr(1)},
			},
		}},
	})
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	require.NotNil(t, result.Results[0].Charm)
	require.Len(t, result.Results[0].Charm.Resources, 1)
	assert.Equal(t, "workload-image", result.Results[0].Charm.Resources[0].Name)
	assert.Equal(t, 1, result.Results[0].Charm.Resources[0].Revision)
	assert.Equal(t, "oci-image", result.Results[0].Charm.Resources[0].Type)

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
	assertServiceError(t, err, ErrorKindForbidden)

}

func TestDownloadCharmRevisionNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	pkg, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	_, err = svc.DownloadCharm(ctx, owner, pkg.ID, 999)
	assertServiceError(t, err, ErrorKindNotFound)

}

func TestDownloadResourceResourceNotDeclared(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	pkg, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	_, err = svc.DownloadResource(ctx, owner, pkg.ID, "nonexistent-res", 1)
	assertServiceError(t, err, ErrorKindNotFound)

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
	assertServiceError(t, err, ErrorKindNotFound)

}

func TestListResourceRevisionsUndeclaredResource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	_, err = svc.ListResourceRevisions(ctx, owner, "my-charm", "nonexistent")
	assertServiceError(t, err, ErrorKindNotFound)

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
	assertServiceError(t, err, ErrorKindNotFound)

}

func TestOCIImageUploadCredentialsUndeclaredResource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	_, err = svc.OCIImageUploadCredentials(ctx, owner, "my-charm", "nonexistent")
	assertServiceError(t, err, ErrorKindNotFound)

}

func TestPushRevisionNonExistentUpload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: "bogus"})
	assertServiceError(t, err, ErrorKindNotFound)

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
	assertServiceError(t, err, ErrorKindNotFound)

}

func TestInfoNoRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	_, err = svc.GetPackageInfo(ctx, owner, "my-charm")
	assertServiceError(t, err, ErrorKindNotFound)

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
	result, err := svc.ResolveRefresh(ctx, owner, RefreshRequest{
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
	result, err := svc.ResolveRefresh(ctx, owner, RefreshRequest{
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
	assertServiceError(t, err, ErrorKindForbidden)

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
	_, err = svc.CreateRelease(ctx, owner, "my-charm", []core.Release{
		{Channel: "latest/stable", Revision: 1},
		{Channel: "latest/edge", Revision: 1},
	})
	require.NoError(t, err)
	result, err := svc.ListReleases(ctx, owner, "my-charm")
	require.NoError(t, err)
	assert.Len(t, result.ChannelMap, 2)
	assert.Len(t, result.Revisions, 1)
	assert.Equal(t, []any{}, result.Revisions[0].Errors)
	assert.NotEmpty(t, result.Package.Channels)

}

func TestFindNoMatchingPackages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	caller := newIdentity("finder-1", "finder")
	result, err := svc.SearchPackages(ctx, caller, "nonexistent-query-xyz")
	require.NoError(t, err)
	assert.Empty(t, result.Results)

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
	released, err := svc.CreateRelease(ctx, owner, "my-charm", []core.Release{{
		Channel:  "latest/stable",
		Revision: 1,
		Resources: []core.ReleaseResourceRef{
			{Name: "config", Revision: intPtr(1)},
		},
	}})
	require.NoError(t, err)
	assert.Len(t, released, 1)
	info, err := svc.GetPackageInfo(ctx, owner, "my-charm")
	require.NoError(t, err)
	defaultRelease := info.DefaultRelease
	resources := defaultRelease.Resources
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
	released, err := svc.CreateRelease(ctx, owner, "my-charm", []core.Release{{
		Channel:  "latest/stable",
		Revision: 1,
		Resources: []core.ReleaseResourceRef{
			{Name: "config", Revision: nil},
		},
	}})
	require.NoError(t, err)
	assert.Len(t, released, 1)

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
	assert.Contains(t, blob, `"ImageName":"oci.example.test/charm-my-charm/workload-image@sha256:abc123"`)
	assert.Contains(t, blob, `"RegistryPath":"oci.example.test/charm-my-charm/workload-image@sha256:abc123"`)
	assert.Contains(t, blob, `"username":"robot$pull-`)
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
	assert.Contains(t, creds.ImageName, "workload-image")
	assert.NotEmpty(t, creds.Username)
	assert.NotEmpty(t, creds.Password)

}

func TestOCIImageUploadCredentialsProvisioningFailureReturnsServiceError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestServiceWithOCI(failingOCIRegistry{
		syncErr: fmt.Errorf("oci unavailable"),
	})
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "my-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "my-charm.charm", buildCharmArchive(t, "my-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, "my-charm", PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	_, err = svc.OCIImageUploadCredentials(ctx, owner, "my-charm", "workload-image")
	assertServiceError(t, err, ErrorKindConflict)
	var svcErr *Error
	require.ErrorAs(t, err, &svcErr)
	assert.Equal(t, "oci-provisioning-unavailable", svcErr.Code)

}

func TestOCIImageUploadCredentialsUsesStoredCredentialsWithoutResync(t *testing.T) {
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

	_, err = svc.OCIImageUploadCredentials(ctx, owner, "my-charm", "workload-image")
	require.NoError(t, err)

	svc.oci = failingOCIRegistry{
		OCIRegistry: testutil.OCIRegistry{},
		syncErr:     errors.New("oci unavailable"),
	}
	creds, err := svc.OCIImageUploadCredentials(ctx, owner, "my-charm", "workload-image")
	require.NoError(t, err)
	assert.Contains(t, creds.ImageName, "workload-image")
	assert.NotEmpty(t, creds.Username)
	assert.NotEmpty(t, creds.Password)

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
	assertServiceError(t, err, ErrorKindForbidden)

}

func TestGetPackagePublicWithEmptyTokenPermissionsIsForbidden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	_, err := svc.RegisterPackage(ctx, owner, "public-charm", "charm", false)
	require.NoError(t, err)
	// Token with empty permissions slice must NOT bypass requirePermission (C-1 fix)
	viewer := newIdentity("acc-2", "bob")
	viewer.Token = &core.StoreToken{
		Permissions: []string{},
	}
	_, err = svc.GetPackage(ctx, viewer, "public-charm", true)
	assertServiceError(t, err, ErrorKindForbidden)

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
	assertServiceError(t, err, ErrorKindForbidden)

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
	released, err := svc.CreateRelease(ctx, owner, "my-charm", []core.Release{{
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
	result, err := svc.ResolveRefresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			Name:        stringPtr("my-charm"),
		}},
	})
	require.NoError(t, err)
	assertRefreshActionError(t, result, "app/0", "not-found")

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
	assertServiceError(t, err, ErrorKindForbidden)

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
	resources, err := svc.ListResources(ctx, owner, "my-charm")

	require.NoError(t, err)
	require.Len(t, resources, 2) // config + workload-image (auto-generated)
	found := false
	for _, res := range resources {
		if res.Name == "config" {
			assert.Equal(t, 1, res.Revision)
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
	released, err := svc.CreateRelease(ctx, owner, "my-charm", []core.Release{{
		Channel: "stable", Revision: 1,
	}})
	require.NoError(t, err)
	assert.Len(t, released, 1)

	// Info should parse the single-part channel correctly
	info, err := svc.GetPackageInfo(ctx, owner, "my-charm")
	require.NoError(t, err)
	defaultRelease := info.DefaultRelease
	channel := defaultRelease.Channel
	assert.Equal(t, "latest", channel.Track)
	assert.Equal(t, "stable", channel.Risk)

}

func TestFindPackageWithoutRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	// Create public package without releasing — should be filtered from find results
	_, err := svc.RegisterPackage(ctx, owner, "unreleased-charm", "charm", false)
	require.NoError(t, err)
	result, err := svc.SearchPackages(ctx, owner, "unreleased")
	require.NoError(t, err)
	assert.Empty(t, result.Results)

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
	_, err = svc.CreateRelease(ctx, owner, "my-charm", []core.Release{
		{Channel: "latest/stable", Revision: 1, Resources: []core.ReleaseResourceRef{
			{Name: "config", Revision: intPtr(1)},
		}},
		{Channel: "latest/edge", Revision: 1},
	})
	require.NoError(t, err)
	info, err := svc.GetPackageInfo(ctx, owner, "my-charm")
	require.NoError(t, err)
	channelMap := info.ChannelMap
	assert.Len(t, channelMap, 2)
	defaultRelease := info.DefaultRelease
	resources := defaultRelease.Resources
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
	_, err = svc.CreateRelease(ctx, owner, "my-charm", []core.Release{
		{Channel: "latest/stable", Revision: 2},
		{Channel: "latest/edge", Revision: 1},
	})
	require.NoError(t, err)
	result, err := svc.ListReleases(ctx, owner, "my-charm")
	require.NoError(t, err)
	assert.Len(t, result.Revisions, 2)
	assert.Equal(t, 1, result.Revisions[0].Revision)
	assert.Equal(t, 2, result.Revisions[1].Revision)

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

func TestFirstNonEmpty(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", core.FirstNonEmpty())
	assert.Equal(t, "", core.FirstNonEmpty("", "", ""))
	assert.Equal(t, "a", core.FirstNonEmpty("", "a", "b"))
	assert.Equal(t, "first", core.FirstNonEmpty("first"))
}

func TestServiceErrorHelpers(t *testing.T) {
	t.Parallel()

	err := NewError(ErrorKindInvalidRequest, "invalid-request", "invalid input")
	assertServiceError(t, err, ErrorKindInvalidRequest)
	var svcErr *Error
	require.ErrorAs(t, err, &svcErr)
	assert.Equal(t, "invalid-request", svcErr.Code)
	assert.Equal(t, "invalid input", svcErr.Message)

	cause := fmt.Errorf("root cause")
	err = NewErrorWithCause(ErrorKindConflict, "conflict", "conflicting state", cause)
	require.ErrorAs(t, err, &svcErr)
	assert.Equal(t, cause, svcErr.Cause)
	assert.ErrorIs(t, err, cause)
}

func TestTranslateRepoError(t *testing.T) {
	t.Parallel()

	// Nil error returns nil
	assert.NoError(t, TranslateRepoError(nil, "msg"))

	// ErrNotFound becomes 404 not-found
	err := TranslateRepoError(repo.ErrNotFound, "not found message")
	assertServiceError(t, err, ErrorKindNotFound)
	var notFound *Error
	require.ErrorAs(t, err, &notFound)
	assert.Equal(t, "not-found", notFound.Code)

	// ErrConflict (wrapped) becomes 409 already-registered
	wrapped := fmt.Errorf("dup: %w", repo.ErrConflict)
	err = TranslateRepoError(wrapped, "already registered")
	assertServiceError(t, err, ErrorKindConflict)
	var conflict *Error
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, "already-registered", conflict.Code)

	// Other errors pass through unchanged
	other := fmt.Errorf("some other error")
	assert.Equal(t, other, TranslateRepoError(other, "msg"))
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
	_, err = svc.CreateRelease(ctx, owner, "my-charm", []core.Release{{
		Channel:  "latest/stable",
		Revision: 1,
		Resources: []core.ReleaseResourceRef{
			{Name: "config", Revision: nil},
		},
	}})
	require.NoError(t, err)
	// Info calls resolveReleaseResources → nil revision branch
	info, err := svc.GetPackageInfo(ctx, owner, "my-charm")
	require.NoError(t, err)
	assert.NotZero(t, info.DefaultRelease)

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
	_, err = svc.CreateRelease(ctx, owner, "my-charm", []core.Release{{
		Channel: "latest/stable", Revision: 1,
		Resources: []core.ReleaseResourceRef{
			{Name: "config", Revision: intPtr(1)},
		},
	}})
	require.NoError(t, err)
	// Refresh WITHOUT specifying channel — uses default release path with resource resolution
	result, err := svc.ResolveRefresh(ctx, owner, RefreshRequest{
		Actions: []RefreshAction{{
			Action:      "refresh",
			InstanceKey: "app/0",
			Name:        stringPtr("my-charm"),
		}},
	})
	require.NoError(t, err)
	results := result.Results
	require.Len(t, results, 1)
	require.NotNil(t, results[0].Charm)
	charmEntity := results[0].Charm
	resources := charmEntity.Resources
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
	_, err = svc.CreateRelease(ctx, owner, "trackcharm", []core.Release{{
		Channel: "latest/stable", Revision: 1,
	}})
	require.NoError(t, err)
	result, err := svc.SearchPackages(ctx, newIdentity("finder-3", "finder"), "trackcharm")
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	assert.Equal(t, "trackcharm", result.Results[0].Name)

}

func TestRepresentativeServiceMethodsRequireAuthentication(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	pkg, err := svc.RegisterPackage(ctx, owner, "auth-charm", "charm", true)
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "auth-charm.charm", buildCharmArchive(t, "auth-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, pkg.Name, PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	_, token, err := svc.IssueStoreToken(ctx, owner, IssueTokenRequest{})
	require.NoError(t, err)

	tests := []struct {
		name string
		fn   func() error
	}{
		{"ListStoreTokens", func() error { _, err := svc.ListStoreTokens(ctx, core.Identity{}, false); return err }},
		{"RevokeStoreToken", func() error { return svc.RevokeStoreToken(ctx, core.Identity{}, token.SessionID) }},
		{"RegisterPackage", func() error {
			_, err := svc.RegisterPackage(ctx, core.Identity{}, "another-auth-charm", "charm", true)
			return err
		}},
		{"UpdatePackage", func() error {
			_, err := svc.UpdatePackage(ctx, core.Identity{}, pkg.Name, MetadataPatch{Title: stringPtr("updated")})
			return err
		}},
		{"CreateRelease", func() error {
			_, err := svc.CreateRelease(ctx, core.Identity{}, pkg.Name, []core.Release{{Channel: "latest/stable", Revision: 1}})
			return err
		}},
		{"DownloadCharm", func() error { _, err := svc.DownloadCharm(ctx, core.Identity{}, pkg.ID, 1); return err }},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertServiceError(t, tt.fn(), ErrorKindUnauthorized)
		})
	}
}

func TestRepresentativeServiceMethodsReturnNotFoundForMissingPackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")

	tests := []struct {
		name string
		fn   func() error
	}{
		{"GetPackage", func() error { _, err := svc.GetPackage(ctx, owner, "nonexistent", true); return err }},
		{"UpdatePackage", func() error { _, err := svc.UpdatePackage(ctx, owner, "nonexistent", MetadataPatch{}); return err }},
		{"UnregisterPackage", func() error { _, err := svc.UnregisterPackage(ctx, owner, "nonexistent"); return err }},
		{"PushRevision", func() error {
			_, err := svc.PushRevision(ctx, owner, "nonexistent", PushRevisionRequest{UploadID: "missing-upload"})
			return err
		}},
		{"PushResource", func() error {
			_, err := svc.PushResource(ctx, owner, "nonexistent", "config", PushResourceRequest{UploadID: "missing-upload"})
			return err
		}},
		{"CreateRelease", func() error {
			_, err := svc.CreateRelease(ctx, owner, "nonexistent", []core.Release{{Channel: "latest/stable", Revision: 1}})
			return err
		}},
		{"DownloadCharm", func() error { _, err := svc.DownloadCharm(ctx, owner, "missing-id", 1); return err }},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertServiceError(t, tt.fn(), ErrorKindNotFound)
		})
	}
}

func TestCheckReadyAndAuthorizeUpload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	require.NoError(t, svc.CheckReady(ctx))
	assertServiceError(t, svc.AuthorizeUpload(core.Identity{}), ErrorKindUnauthorized)
	require.NoError(t, svc.AuthorizeUpload(newIdentity("acc-1", "alice")))
}

func TestGetPackageInfoForChannelReturnsRequestedRelease(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("acc-1", "alice")
	pkg, err := svc.RegisterPackage(ctx, owner, "channel-info-charm", "charm", true)
	require.NoError(t, err)
	_, err = svc.CreateTracks(ctx, owner, pkg.Name, []core.Track{{Name: "2.0"}})
	require.NoError(t, err)
	upload, err := svc.CreateUpload(ctx, "channel-info-charm.charm", buildCharmArchive(t, "channel-info-charm"))
	require.NoError(t, err)
	_, err = svc.PushRevision(ctx, owner, pkg.Name, PushRevisionRequest{UploadID: upload.ID})
	require.NoError(t, err)
	_, err = svc.CreateRelease(ctx, owner, pkg.Name, []core.Release{{Channel: "2.0/edge", Revision: 1}})
	require.NoError(t, err)

	info, err := svc.GetPackageInfoForChannel(ctx, owner, pkg.Name, "2.0/edge")
	require.NoError(t, err)
	assert.Equal(t, "2.0/edge", info.DefaultRelease.Channel.Name)
	assert.Equal(t, 1, info.DefaultRelease.Revision.Revision)
}

func assertServiceError(t *testing.T, err error, expectedKind ErrorKind) {
	t.Helper()
	svcErr := serviceError(t, err)
	assert.Equal(t, expectedKind, svcErr.Kind)
}

func serviceError(t *testing.T, err error) *Error {
	t.Helper()
	require.Error(t, err)
	var svcErr *Error
	require.ErrorAs(t, err, &svcErr)
	return svcErr
}

// assertRefreshActionError verifies that a per-action error is embedded in the
// Refresh results map for the given instanceKey with the expected error code.
func assertRefreshActionError(t *testing.T, result refreshResponse, instanceKey, expectedCode string) {
	t.Helper()
	var found bool
	for _, item := range result.Results {
		if item.InstanceKey != instanceKey {
			continue
		}
		found = true
		assert.Equal(t, "error", item.Result, "result should be 'error'")
		require.NotNil(t, item.Error)
		assert.Equal(t, expectedCode, item.Error.Code)
	}
	require.True(t, found, "no result found for instance-key %q", instanceKey)
}

func boolPtr(v bool) *bool {
	return &v
}

func newTestService() (*Service, repo.Repository) {
	return newTestServiceWithClock(nil, testutil.OCIRegistry{})
}

func newTestServiceWithOCI(oci OCIRegistry) (*Service, repo.Repository) {
	return newTestServiceWithClock(nil, oci)
}

func newTestServiceWithClock(clock func() time.Time, oci OCIRegistry) (*Service, repo.Repository) {
	repository := repo.NewMemory()
	svc := New(testConfig(), repository, blob.NewMemoryStore(), oci)
	svc.Clock = clock
	return svc, repository
}

type seekablePayloadStore struct {
	payload []byte
}

func (s *seekablePayloadStore) Put(_ context.Context, _ string, payload io.Reader, _ string) error {
	if _, ok := payload.(io.Seeker); !ok {
		return fmt.Errorf("payload is not seekable")
	}
	data, err := io.ReadAll(payload)
	if err != nil {
		return err
	}
	s.payload = data
	return nil
}

func (s *seekablePayloadStore) Get(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *seekablePayloadStore) Open(_ context.Context, _ string) (io.ReadCloser, int64, error) {
	return nil, 0, fmt.Errorf("not implemented")
}

func (s *seekablePayloadStore) Delete(_ context.Context, _ string) error {
	return nil
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
		OCIProjectPrefix:      "charm",
		OCIPullRobotPrefix:    "pull",
		OCIPushRobotPrefix:    "push",
		OCISecretKey:          "test-oci-secret",
	}
}

type failingOCIRegistry struct {
	testutil.OCIRegistry
	syncErr        error
	imageRefErr    error
	credentialsErr error
	mirrorErr      error
	deleteImageErr error
	deletePkgErr   error
}

type approveUploadFailingRepository struct {
	repo.Repository
	err error
}

type updatePackageFailingRepository struct {
	repo.Repository
	err error
}

type updatePackageFailingCompositeRepo struct {
	repo.CompositeRepo
	err error
}

func (r approveUploadFailingRepository) ApproveUpload(
	ctx context.Context,
	uploadID string,
	revision *int,
	reviewErrors []core.APIError,
) error {
	if len(reviewErrors) > 0 {
		return r.err
	}
	return r.Repository.ApproveUpload(ctx, uploadID, revision, reviewErrors)
}

func (r updatePackageFailingRepository) WithinTransaction(
	ctx context.Context,
	fn func(repo.CompositeRepo) error,
) error {
	return r.Repository.WithinTransaction(ctx, func(tx repo.CompositeRepo) error {
		return fn(updatePackageFailingCompositeRepo{CompositeRepo: tx, err: r.err})
	})
}

func (r updatePackageFailingRepository) UpdatePackage(_ context.Context, _ core.Package) error {
	return r.err
}

func (r updatePackageFailingCompositeRepo) UpdatePackage(_ context.Context, _ core.Package) error {
	return r.err
}

func (o failingOCIRegistry) SyncPackage(ctx context.Context, pkg core.Package) (core.Package, error) {
	if o.syncErr != nil {
		return core.Package{}, o.syncErr
	}
	return o.OCIRegistry.SyncPackage(ctx, pkg)
}

func (o failingOCIRegistry) ImageReference(pkg core.Package, resourceName string) (string, error) {
	if o.imageRefErr != nil {
		return "", o.imageRefErr
	}
	return o.OCIRegistry.ImageReference(pkg, resourceName)
}

func (o failingOCIRegistry) Credentials(pkg core.Package, pull bool) (string, string, error) {
	if o.credentialsErr != nil {
		return "", "", o.credentialsErr
	}
	return o.OCIRegistry.Credentials(pkg, pull)
}

func (o failingOCIRegistry) MirrorImage(
	ctx context.Context,
	pkg core.Package,
	resourceName, sourceImage, sourceUsername, sourcePassword string,
) (string, error) {
	if o.mirrorErr != nil {
		return "", o.mirrorErr
	}
	return o.OCIRegistry.MirrorImage(ctx, pkg, resourceName, sourceImage, sourceUsername, sourcePassword)
}

func (o failingOCIRegistry) DeleteImage(ctx context.Context, pkg core.Package, resourceName, digest string) error {
	if o.deleteImageErr != nil {
		return o.deleteImageErr
	}
	return o.OCIRegistry.DeleteImage(ctx, pkg, resourceName, digest)
}

func (o failingOCIRegistry) DeletePackage(ctx context.Context, pkg core.Package) error {
	if o.deletePkgErr != nil {
		return o.deletePkgErr
	}
	return o.OCIRegistry.DeletePackage(ctx, pkg)
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

func TestErrorUnwrap(t *testing.T) {
	t.Parallel()
	cause := fmt.Errorf("db timeout")
	err := &Error{Kind: ErrorKindNotFound, Code: "not-found", Message: "charm missing", Cause: cause}

	// Unwrap returns the cause
	assert.ErrorIs(t, err, cause)

	// nil cause returns nil
	noErr := &Error{Kind: ErrorKindNotFound, Code: "not-found", Message: "x"}
	assert.NoError(t, noErr.Unwrap())
}
