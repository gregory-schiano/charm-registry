package registrysync

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gschiano/charm-registry/internal/blob"
	charmhubclient "github.com/gschiano/charm-registry/internal/charmhub"
	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
	registryservice "github.com/gschiano/charm-registry/internal/service"
	"github.com/gschiano/charm-registry/internal/testutil"
)

type fakeCharmhubClient struct {
	channels     map[string]charmhubclient.PackageChannel
	infos        map[string]charmhubclient.PackageChannel
	downloads    map[string][]byte
	downloadErrs map[string]error
}

type trackingOCIRegistry struct {
	testutil.OCIRegistry
	deletedImages   []string
	deletedPackages []string
	mirrorErr       error
}

type syncTestHarness struct {
	registry *registryservice.Service
	sync     *Service
	repo     repo.Backend
}

func (f *fakeCharmhubClient) GetChannel(_ context.Context, name, channel string) (charmhubclient.PackageChannel, error) {
	if item, ok := f.channels[name+"|"+channel]; ok {
		return item, nil
	}
	return charmhubclient.PackageChannel{
		ID:   "upstream-" + name,
		Name: name,
		Result: charmhubclient.PackageResult{
			Title:   "Synced " + name,
			Summary: "Synced summary",
		},
	}, nil
}

func (f *fakeCharmhubClient) GetInfo(_ context.Context, name string) (charmhubclient.PackageChannel, error) {
	if item, ok := f.infos[name]; ok {
		return item, nil
	}
	return charmhubclient.PackageChannel{ID: "upstream-" + name, Name: name}, nil
}

func (f *fakeCharmhubClient) RefreshChannel(
	_ context.Context,
	name, channel string,
	base core.Base,
) (charmhubclient.PackageChannel, error) {
	if item, ok := f.channels[name+"|"+channel+"|"+base.Name+"@"+base.Channel+"|"+base.Architecture]; ok {
		return item, nil
	}
	return f.GetChannel(context.Background(), name, channel)
}

func (f *fakeCharmhubClient) Download(_ context.Context, artifactURL string) ([]byte, error) {
	if err, ok := f.downloadErrs[artifactURL]; ok {
		return nil, err
	}
	payload, ok := f.downloads[artifactURL]
	if !ok {
		return nil, fmt.Errorf("unknown download URL %s", artifactURL)
	}
	return append([]byte(nil), payload...), nil
}

func (o *trackingOCIRegistry) MirrorImage(
	ctx context.Context,
	pkg core.Package,
	resourceName, sourceImage, sourceUsername, sourcePassword string,
) (string, error) {
	if o.mirrorErr != nil {
		return "", o.mirrorErr
	}
	return o.OCIRegistry.MirrorImage(ctx, pkg, resourceName, sourceImage, sourceUsername, sourcePassword)
}

func (o *trackingOCIRegistry) DeleteImage(_ context.Context, pkg core.Package, resourceName, digest string) error {
	o.deletedImages = append(o.deletedImages, pkg.Name+":"+resourceName+"@"+digest)
	return nil
}

func (o *trackingOCIRegistry) DeletePackage(_ context.Context, pkg core.Package) error {
	o.deletedPackages = append(o.deletedPackages, pkg.Name)
	return nil
}

func TestStartManagerIsIdempotent(t *testing.T) {
	t.Parallel()

	env := newSyncTestHarness(t)
	manager := env.sync.StartManager(context.Background())
	require.NotNil(t, manager)
	assert.Same(t, manager, env.sync.StartManager(context.Background()))
	require.NoError(t, manager.Close())
}

func TestManagerEnqueueNormalizesPackageName(t *testing.T) {
	t.Parallel()

	manager := &Manager{
		wake:    make(chan struct{}, 1),
		pending: map[string]struct{}{},
	}
	manager.Enqueue(" sync-charm ")
	manager.Enqueue(" ")

	manager.mu.Lock()
	_, ok := manager.pending["sync-charm"]
	pendingCount := len(manager.pending)
	manager.mu.Unlock()

	assert.True(t, ok)
	assert.Equal(t, 1, pendingCount)
}

func TestRegisterPackageConflictsWithCharmhubSyncReservation(t *testing.T) {
	t.Parallel()

	env := newSyncTestHarness(t)
	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	user := newIdentity("user-1", "user")

	_, err := env.sync.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)

	_, err = env.registry.RegisterPackage(context.Background(), user, "demo", "charm", false)
	assertServiceError(t, err, registryservice.ErrorKindConflict)
}

func TestAddCharmhubSyncRuleConflictsWithManualPackage(t *testing.T) {
	t.Parallel()

	env := newSyncTestHarness(t)
	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	user := newIdentity("user-1", "user")

	_, err := env.registry.RegisterPackage(context.Background(), user, "demo", "charm", false)
	require.NoError(t, err)

	_, err = env.sync.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	assertServiceError(t, err, registryservice.ErrorKindConflict)
}

func TestTriggerCharmhubSyncRequiresExistingRule(t *testing.T) {
	t.Parallel()

	env := newSyncTestHarness(t)
	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true

	err := env.sync.TriggerCharmhubSync(context.Background(), admin, "demo")
	assertServiceError(t, err, registryservice.ErrorKindNotFound)
}

func TestTriggerCharmhubSyncAcceptsConfiguredPackage(t *testing.T) {
	t.Parallel()

	env := newSyncTestHarness(t)
	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true

	_, err := env.sync.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)

	err = env.sync.TriggerCharmhubSync(context.Background(), admin, "demo")
	require.NoError(t, err)
}

func TestReconcileCharmhubPackageCreatesMirroredArtifacts(t *testing.T) {
	t.Parallel()

	env := newSyncTestHarness(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	env.sync.charmhub = fakeClient
	env.sync.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := env.sync.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)

	require.NoError(t, env.sync.reconcilePackage(context.Background(), "demo"))

	pkg, err := env.repo.GetPackageByName(context.Background(), "demo")
	require.NoError(t, err)
	require.NotNil(t, pkg.Authority)
	assert.Equal(t, charmhubAuthority, *pkg.Authority)
	assert.Equal(t, charmhubSyncAccountID, pkg.OwnerAccountID)

	tracks, err := env.repo.ListTracks(context.Background(), pkg.ID)
	require.NoError(t, err)
	require.Len(t, tracks, 1)
	assert.Equal(t, "latest", tracks[0].Name)

	revision, err := env.repo.GetRevisionByNumber(context.Background(), pkg.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, "7", revision.Version)

	resourceDef, err := env.repo.GetResourceDefinition(context.Background(), pkg.ID, "app-image")
	require.NoError(t, err)
	resourceRevision, err := env.repo.GetResourceRevision(context.Background(), resourceDef.ID, 3)
	require.NoError(t, err)
	assert.Equal(t, "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", resourceRevision.OCIImageDigest)

	release, err := env.repo.ResolveRelease(context.Background(), pkg.ID, "latest/stable")
	require.NoError(t, err)
	assert.Equal(t, 7, release.Revision)
	require.Len(t, release.Resources, 2)

	refresh, err := env.registry.ResolveRefresh(context.Background(), admin, registryservice.RefreshRequest{
		Actions: []registryservice.RefreshAction{{
			Action:      "install",
			InstanceKey: "demo/0",
			Name:        stringPtr("demo"),
			Channel:     stringPtr("latest/stable"),
			Base:        &core.Base{Name: "NA", Channel: "NA", Architecture: "amd64"},
		}},
	})
	require.NoError(t, err)
	require.Len(t, refresh.Results, 1)
	require.Nil(t, refresh.Results[0].Error)
	require.NotNil(t, refresh.Results[0].Charm)
	assert.Equal(t, 7, refresh.Results[0].Charm.Revision)

	rules, err := env.repo.ListCharmhubSyncRules(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, charmhubSyncStatusOK, rules[0].LastSyncStatus)
	assert.Nil(t, rules[0].LastSyncError)
}

func TestReconcileCharmhubPackagePersistsSyncAccountOnSQLite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	env := newSQLiteSyncTestHarness(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	env.sync.charmhub = fakeClient
	env.sync.oci = oci

	admin := ensurePersistedIdentity(t, env.repo, "admin-1", "admin", true)
	_, err := env.sync.AddCharmhubSyncRule(ctx, admin, "demo", "latest", nil, nil)
	require.NoError(t, err)

	require.NoError(t, env.sync.reconcilePackage(ctx, "demo"))

	pkg, err := env.repo.GetPackageByName(ctx, "demo")
	require.NoError(t, err)
	assert.Equal(t, charmhubSyncAccountID, pkg.OwnerAccountID)

	account, err := env.repo.GetAccountByID(ctx, charmhubSyncAccountID)
	require.NoError(t, err)
	assert.Equal(t, charmhubSyncAccountSubject, account.Subject)
	assert.Equal(t, charmhubSyncAccountName, account.Username)
	assert.True(t, account.IsAdmin)
	assert.Equal(t, "verified", account.Validation)
}

func TestReconcileCharmhubPackageFallsBackWhenUpstreamTimestampsMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	env := newSyncTestHarness(t)
	fixedNow := time.Date(2026, time.May, 1, 17, 45, 0, 0, time.FixedZone("UTC+2", 2*60*60))
	expected := fixedNow.UTC()
	env.sync.Clock = func() time.Time { return fixedNow }
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	env.sync.charmhub = fakeClient
	env.sync.oci = oci

	stable := fakeClient.channels["demo|latest/stable"]
	stable.DefaultRelease.Channel.ReleasedAt = time.Time{}
	stable.DefaultRelease.Revision.CreatedAt = time.Time{}
	fakeClient.channels["demo|latest/stable"] = stable

	variant := fakeClient.channels["demo|latest/stable|ubuntu@24.04|amd64"]
	variant.DefaultRelease.Channel.ReleasedAt = time.Time{}
	variant.DefaultRelease.Revision.CreatedAt = time.Time{}
	fakeClient.channels["demo|latest/stable|ubuntu@24.04|amd64"] = variant

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := env.sync.AddCharmhubSyncRule(ctx, admin, "demo", "latest", nil, nil)
	require.NoError(t, err)

	require.NoError(t, env.sync.reconcilePackage(ctx, "demo"))

	pkg, err := env.repo.GetPackageByName(ctx, "demo")
	require.NoError(t, err)
	revision, err := env.repo.GetRevisionByNumber(ctx, pkg.ID, 7)
	require.NoError(t, err)
	assert.True(t, revision.CreatedAt.Equal(expected))

	release, err := env.repo.ResolveRelease(ctx, pkg.ID, "latest/stable")
	require.NoError(t, err)
	assert.True(t, release.When.Equal(expected))
}

func TestCharmhubSyncMirrorsAllBaseArchitectureVariantsByDefault(t *testing.T) {
	t.Parallel()

	env := newSyncTestHarness(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	addTrackVariantFixture(
		t,
		fakeClient,
		"demo",
		"upstream-demo",
		"latest",
		core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "arm64"},
		8,
		4,
		5,
	)
	env.sync.charmhub = fakeClient
	env.sync.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := env.sync.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)

	require.NoError(t, env.sync.reconcilePackage(context.Background(), "demo"))

	pkg, err := env.repo.GetPackageByName(context.Background(), "demo")
	require.NoError(t, err)
	releases, err := env.repo.ListReleases(context.Background(), pkg.ID)
	require.NoError(t, err)
	require.Len(t, releases, 2)

	amd64Release, err := env.repo.ResolveReleaseForBase(
		context.Background(),
		pkg.ID,
		"latest/stable",
		core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"},
	)
	require.NoError(t, err)
	assert.Equal(t, 7, amd64Release.Revision)

	arm64Release, err := env.repo.ResolveReleaseForBase(
		context.Background(),
		pkg.ID,
		"latest/stable",
		core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "arm64"},
	)
	require.NoError(t, err)
	assert.Equal(t, 8, arm64Release.Revision)
}

func TestCharmhubSyncFiltersVariantsByBaseAndArchitecture(t *testing.T) {
	t.Parallel()

	env := newSyncTestHarness(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	addTrackVariantFixture(
		t,
		fakeClient,
		"demo",
		"upstream-demo",
		"latest",
		core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "arm64"},
		8,
		4,
		5,
	)
	addTrackVariantFixture(
		t,
		fakeClient,
		"demo",
		"upstream-demo",
		"latest",
		core.Base{Name: "ubuntu", Channel: "22.04", Architecture: "amd64"},
		9,
		6,
		7,
	)
	env.sync.charmhub = fakeClient
	env.sync.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := env.sync.AddCharmhubSyncRule(
		context.Background(),
		admin,
		"demo",
		"latest",
		[]string{"ubuntu@24.04"},
		[]string{"arm64"},
	)
	require.NoError(t, err)

	require.NoError(t, env.sync.reconcilePackage(context.Background(), "demo"))

	pkg, err := env.repo.GetPackageByName(context.Background(), "demo")
	require.NoError(t, err)
	releases, err := env.repo.ListReleases(context.Background(), pkg.ID)
	require.NoError(t, err)
	require.Len(t, releases, 1)
	require.NotNil(t, releases[0].Base)
	assert.Equal(t, "24.04", releases[0].Base.Channel)
	assert.Equal(t, "arm64", releases[0].Base.Architecture)
	assert.Equal(t, 8, releases[0].Revision)
}

func TestCharmhubManagedPackagesBlockPublisherMutations(t *testing.T) {
	t.Parallel()

	env := newSyncTestHarness(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	env.sync.charmhub = fakeClient
	env.sync.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	publisher := newIdentity("publisher-1", "publisher")

	_, err := env.sync.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)
	require.NoError(t, env.sync.reconcilePackage(context.Background(), "demo"))

	_, err = env.registry.UpdatePackage(context.Background(), publisher, "demo", registryservice.MetadataPatch{Summary: stringPtr("manual")})
	assertServiceError(t, err, registryservice.ErrorKindConflict)

	_, err = env.registry.CreateTracks(context.Background(), publisher, "demo", []core.Track{{Name: "2.0"}})
	assertServiceError(t, err, registryservice.ErrorKindConflict)

	_, err = env.registry.CreateRelease(context.Background(), publisher, "demo", []core.Release{{Channel: "latest/stable", Revision: 7}})
	assertServiceError(t, err, registryservice.ErrorKindConflict)

	_, err = env.registry.PushRevision(context.Background(), publisher, "demo", registryservice.PushRevisionRequest{UploadID: "upload-1"})
	assertServiceError(t, err, registryservice.ErrorKindConflict)

	_, err = env.registry.PushResource(
		context.Background(),
		publisher,
		"demo",
		"app-image",
		registryservice.PushResourceRequest{UploadID: "upload-1"},
	)
	assertServiceError(t, err, registryservice.ErrorKindConflict)
}

func TestCharmhubSyncRejectsInvalidTrackBeforePersistence(t *testing.T) {
	t.Parallel()

	env := newSyncTestHarness(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	env.sync.charmhub = fakeClient
	env.sync.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := env.sync.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)

	require.NoError(t, env.sync.reconcilePackage(context.Background(), "demo"))
	pkg, lookupErr := env.repo.GetPackageByName(context.Background(), "demo")
	require.NoError(t, lookupErr)
	tracks, listErr := env.repo.ListTracks(context.Background(), pkg.ID)
	require.NoError(t, listErr)
	require.Len(t, tracks, 1)

	err = env.repo.CreateCharmhubSyncRule(context.Background(), core.CharmhubSyncRule{
		PackageName:        "demo",
		Track:              " ",
		CreatedByAccountID: admin.Account.ID,
		CreatedAt:          time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC),
		UpdatedAt:          time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC),
		LastSyncStatus:     charmhubSyncStatusPending,
	})
	require.NoError(t, err)
	blankTrack := fakeClient.channels["demo|latest/stable"]
	blankTrack.DefaultRelease.Channel.Name = "stable"
	blankTrack.DefaultRelease.Channel.Track = " "
	blankTrack.DefaultRelease.Revision.Revision = 8
	blankTrack.DefaultRelease.Revision.Version = "8"
	blankTrack.DefaultRelease.Revision.Download.URL = "https://charmhub.test/demo/stable/revision-8.charm"
	fakeClient.downloads[blankTrack.DefaultRelease.Revision.Download.URL] = buildSyncCharmArchive(t, "demo")
	fakeClient.channels["demo| /stable"] = blankTrack
	fakeClient.channels["demo| /stable|ubuntu@24.04|amd64"] = blankTrack
	fakeClient.infos["demo"] = charmhubclient.PackageChannel{
		ID:     "upstream-demo",
		Name:   "demo",
		Result: blankTrack.Result,
		ChannelMap: []charmhubclient.ChannelMap{{
			Channel:  blankTrack.DefaultRelease.Channel,
			Revision: blankTrack.DefaultRelease.Revision,
		}},
	}

	err = env.sync.reconcilePackage(context.Background(), "demo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "charmhub sync track")
	assert.Contains(t, err.Error(), "track name is required")

	tracks, listErr = env.repo.ListTracks(context.Background(), pkg.ID)
	require.NoError(t, listErr)
	for _, track := range tracks {
		assert.NotEmpty(t, strings.TrimSpace(track.Name))
	}
}

func TestCharmhubSyncRejectsInvalidResourceDefinitionBeforePersistence(t *testing.T) {
	t.Parallel()

	env := newSyncTestHarness(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	fakeClient.downloads = map[string][]byte{}
	stable := fakeClient.channels["demo|latest/stable"]
	stable.DefaultRelease.Revision.Download.URL = "https://charmhub.test/demo/latest/stable/revision-invalid-resource.charm"
	stable.DefaultRelease.Resources = nil
	fakeClient.channels["demo|latest/stable"] = stable
	fakeClient.channels["demo|latest/stable|ubuntu@24.04|amd64"] = stable
	fakeClient.infos["demo"] = charmhubclient.PackageChannel{
		ID:     "upstream-demo",
		Name:   "demo",
		Result: stable.Result,
		ChannelMap: []charmhubclient.ChannelMap{{
			Channel:  stable.DefaultRelease.Channel,
			Revision: stable.DefaultRelease.Revision,
		}},
	}
	fakeClient.downloads[stable.DefaultRelease.Revision.Download.URL] = buildInvalidResourceArchive(t, "demo")
	env.sync.charmhub = fakeClient
	env.sync.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := env.sync.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)

	err = env.sync.reconcilePackage(context.Background(), "demo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resource definition config")
	assert.Contains(t, err.Error(), "resource definition type is required")

	pkg, lookupErr := env.repo.GetPackageByName(context.Background(), "demo")
	require.NoError(t, lookupErr)
	defs, listErr := env.repo.ListResourceDefinitions(context.Background(), pkg.ID)
	require.NoError(t, listErr)
	assert.Empty(t, defs)
}

func TestCharmhubSyncMirrorFailureMarksRuleErrorAndRetries(t *testing.T) {
	t.Parallel()

	env := newSyncTestHarness(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	oci.mirrorErr = assert.AnError
	env.sync.charmhub = fakeClient
	env.sync.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := env.sync.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)

	err = env.sync.reconcilePackage(context.Background(), "demo")
	require.ErrorIs(t, err, assert.AnError)

	rules, err := env.repo.ListCharmhubSyncRules(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, charmhubSyncStatusError, rules[0].LastSyncStatus)
	require.NotNil(t, rules[0].LastSyncError)
	assert.Contains(t, *rules[0].LastSyncError, assert.AnError.Error())

	oci.mirrorErr = nil
	require.NoError(t, env.sync.reconcilePackage(context.Background(), "demo"))

	rules, err = env.repo.ListCharmhubSyncRules(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, charmhubSyncStatusOK, rules[0].LastSyncStatus)
	assert.Nil(t, rules[0].LastSyncError)

	pkg, err := env.repo.GetPackageByName(context.Background(), "demo")
	require.NoError(t, err)
	release, err := env.repo.ResolveRelease(context.Background(), pkg.ID, "latest/stable")
	require.NoError(t, err)
	assert.Equal(t, 7, release.Revision)
}

func TestRemovingLastCharmhubSyncRuleDeletesPackage(t *testing.T) {
	t.Parallel()

	env := newSyncTestHarness(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	env.sync.charmhub = fakeClient
	env.sync.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := env.sync.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)
	require.NoError(t, env.sync.reconcilePackage(context.Background(), "demo"))

	require.NoError(t, env.sync.RemoveCharmhubSyncRule(context.Background(), admin, "demo", "latest"))
	rules, err := env.repo.ListCharmhubSyncRules(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, charmhubSyncStatusDeleting, rules[0].LastSyncStatus)

	require.NoError(t, env.sync.reconcilePackage(context.Background(), "demo"))

	_, err = env.repo.GetPackageByName(context.Background(), "demo")
	require.ErrorIs(t, err, repo.ErrNotFound)
	rules, err = env.repo.ListCharmhubSyncRules(context.Background())
	require.NoError(t, err)
	assert.Empty(t, rules)
	assert.Contains(t, oci.deletedPackages, "demo")
}

func TestRemovingOneTrackPrunesOnlyUnreferencedArtifacts(t *testing.T) {
	t.Parallel()

	env := newSyncTestHarness(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	addTrackFixture(t, fakeClient, "demo", "upstream-demo", "2.0", 11, 8, 6)
	env.sync.charmhub = fakeClient
	env.sync.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := env.sync.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)
	_, err = env.sync.AddCharmhubSyncRule(context.Background(), admin, "demo", "2.0", nil, nil)
	require.NoError(t, err)
	require.NoError(t, env.sync.reconcilePackage(context.Background(), "demo"))

	require.NoError(t, env.sync.RemoveCharmhubSyncRule(context.Background(), admin, "demo", "latest"))
	rules, err := env.repo.ListCharmhubSyncRules(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 2)
	assert.Equal(t, charmhubSyncStatusDeleting, syncRuleByTrack(t, rules, "latest").LastSyncStatus)

	require.NoError(t, env.sync.reconcilePackage(context.Background(), "demo"))

	pkg, err := env.repo.GetPackageByName(context.Background(), "demo")
	require.NoError(t, err)
	rules, err = env.repo.ListCharmhubSyncRules(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "2.0", rules[0].Track)

	_, err = env.repo.GetRevisionByNumber(context.Background(), pkg.ID, 7)
	require.ErrorIs(t, err, repo.ErrNotFound)
	_, err = env.repo.GetRevisionByNumber(context.Background(), pkg.ID, 11)
	require.NoError(t, err)

	_, err = env.repo.ResolveRelease(context.Background(), pkg.ID, "latest/stable")
	require.ErrorIs(t, err, repo.ErrNotFound)
	release, err := env.repo.ResolveRelease(context.Background(), pkg.ID, "2.0/stable")
	require.NoError(t, err)
	assert.Equal(t, 11, release.Revision)
	assert.NotEmpty(t, oci.deletedImages)
}

func TestCharmhubSyncFailureMarksRuleErrorAndKeepsExistingRelease(t *testing.T) {
	t.Parallel()

	env := newSyncTestHarness(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	env.sync.charmhub = fakeClient
	env.sync.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := env.sync.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)
	require.NoError(t, env.sync.reconcilePackage(context.Background(), "demo"))

	addTrackFixture(t, fakeClient, "demo", "upstream-demo", "latest", 9, 4, 4)
	fakeClient.downloadErrs["https://charmhub.test/demo/latest/stable/ubuntu-24.04-amd64/revision-9.charm"] =
		fmt.Errorf("upstream unavailable")

	err = env.sync.reconcilePackage(context.Background(), "demo")
	require.Error(t, err)

	pkg, getErr := env.repo.GetPackageByName(context.Background(), "demo")
	require.NoError(t, getErr)
	release, getErr := env.repo.ResolveRelease(context.Background(), pkg.ID, "latest/stable")
	require.NoError(t, getErr)
	assert.Equal(t, 7, release.Revision)

	rules, getErr := env.repo.ListCharmhubSyncRules(context.Background())
	require.NoError(t, getErr)
	require.Len(t, rules, 1)
	assert.Equal(t, charmhubSyncStatusError, rules[0].LastSyncStatus)
	require.NotNil(t, rules[0].LastSyncError)
	assert.Contains(t, *rules[0].LastSyncError, "upstream unavailable")
}

func TestCharmhubSyncRemovesChannelWhenUpstreamDisappears(t *testing.T) {
	t.Parallel()

	env := newSyncTestHarness(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	addTrackFixture(t, fakeClient, "demo", "upstream-demo", "latest", 7, 3, 2)
	fakeClient.channels["demo|latest/candidate"] = cloneCharmhubChannel(fakeClient.channels["demo|latest/stable"], "latest/candidate", "candidate")
	env.sync.charmhub = fakeClient
	env.sync.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := env.sync.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)
	require.NoError(t, env.sync.reconcilePackage(context.Background(), "demo"))

	delete(fakeClient.channels, "demo|latest/candidate")
	require.NoError(t, env.sync.reconcilePackage(context.Background(), "demo"))

	pkg, err := env.repo.GetPackageByName(context.Background(), "demo")
	require.NoError(t, err)
	_, err = env.repo.ResolveRelease(context.Background(), pkg.ID, "latest/candidate")
	require.ErrorIs(t, err, repo.ErrNotFound)
}

func newSyncTestHarness(t *testing.T) *syncTestHarness {
	t.Helper()
	repository := repo.NewMemory()
	storage := blob.NewMemoryStore()
	ociRegistry := testutil.OCIRegistry{RegistryHost: "oci.test"}
	return &syncTestHarness{
		registry: registryservice.New(testConfig(), repository, storage, ociRegistry),
		sync:     New(testConfig(), repository, storage, ociRegistry),
		repo:     repository,
	}
}

func newSQLiteSyncTestHarness(t *testing.T) *syncTestHarness {
	t.Helper()
	ctx := context.Background()
	repository, err := repo.NewSQLite(ctx, t.TempDir()+"/registry.sqlite")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, repository.Close())
	})
	require.NoError(t, repository.Migrate(ctx))

	storage := blob.NewMemoryStore()
	ociRegistry := testutil.OCIRegistry{RegistryHost: "oci.test"}
	return &syncTestHarness{
		registry: registryservice.New(testConfig(), repository, storage, ociRegistry),
		sync:     New(testConfig(), repository, storage, ociRegistry),
		repo:     repository,
	}
}

func newSyncFixture(
	t *testing.T,
	packageName, packageID string,
) (*fakeCharmhubClient, *trackingOCIRegistry) {
	t.Helper()

	client := &fakeCharmhubClient{
		channels:     map[string]charmhubclient.PackageChannel{},
		infos:        map[string]charmhubclient.PackageChannel{},
		downloads:    map[string][]byte{},
		downloadErrs: map[string]error{},
	}
	addTrackFixture(t, client, packageName, packageID, "latest", 7, 3, 2)
	return client, &trackingOCIRegistry{OCIRegistry: testutil.OCIRegistry{RegistryHost: "oci.test"}}
}

func addTrackFixture(
	t *testing.T,
	client *fakeCharmhubClient,
	packageName, packageID, track string,
	revisionNumber, ociResourceRevision, fileResourceRevision int,
) {
	t.Helper()
	addTrackVariantFixture(
		t,
		client,
		packageName,
		packageID,
		track,
		core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"},
		revisionNumber,
		ociResourceRevision,
		fileResourceRevision,
	)
}

func addTrackVariantFixture(
	t *testing.T,
	client *fakeCharmhubClient,
	packageName, packageID, track string,
	base core.Base,
	revisionNumber, ociResourceRevision, fileResourceRevision int,
) {
	t.Helper()
	archivePayload := buildSyncCharmArchive(t, packageName)
	baseKey := base.Name + "-" + base.Channel + "-" + base.Architecture
	revisionURL := fmt.Sprintf(
		"https://charmhub.test/%s/%s/stable/%s/revision-%d.charm",
		packageName,
		track,
		baseKey,
		revisionNumber,
	)
	client.downloads[revisionURL] = archivePayload

	fileResourcePayload := []byte("config-data-" + track)
	fileResourceURL := fmt.Sprintf(
		"https://charmhub.test/%s/%s/stable/%s/resource-config-%d",
		packageName,
		track,
		baseKey,
		fileResourceRevision,
	)
	client.downloads[fileResourceURL] = fileResourcePayload

	ociPayload := []byte(`{"ImageName":"registry.example.test/upstream/app@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","Username":"upstream","Password":"secret"}`)
	ociResourceURL := fmt.Sprintf(
		"https://charmhub.test/%s/%s/stable/%s/resource-app-image-%d",
		packageName,
		track,
		baseKey,
		ociResourceRevision,
	)
	client.downloads[ociResourceURL] = ociPayload

	stable := charmhubclient.PackageChannel{
		ID:   packageID,
		Name: packageName,
		Result: charmhubclient.PackageResult{
			Title:       "Synced " + packageName,
			Summary:     "Synced summary",
			Description: "Synced description",
			Links: map[string][]string{
				"website": {"https://example.com/" + packageName},
			},
		},
		DefaultRelease: charmhubclient.DefaultRelease{
			Channel: charmhubclient.ReleaseChannel{
				Name:       track + "/stable",
				Track:      track,
				Risk:       "stable",
				ReleasedAt: time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC),
				Base:       &base,
			},
			Resources: []charmhubclient.ReleaseResource{
				makeFakeResource("config", "file", fileResourceRevision, fileResourceURL, fileResourcePayload),
				makeFakeResource("app-image", "oci-image", ociResourceRevision, ociResourceURL, ociPayload),
			},
			Revision: charmhubclient.ReleaseRevision{
				Revision:  revisionNumber,
				Version:   fmt.Sprintf("%d", revisionNumber),
				CreatedAt: time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC),
				Download: core.Download{
					URL:  revisionURL,
					Size: int64(len(archivePayload)),
				},
				Attributes: map[string]string{
					"framework": "operator",
					"language":  "go",
				},
			},
		},
	}
	client.channels[packageName+"|"+track+"/stable"] = stable
	client.channels[packageName+"|"+track+"/stable|"+base.Name+"@"+base.Channel+"|"+base.Architecture] = stable
	info := client.infos[packageName]
	info.ID = packageID
	info.Name = packageName
	info.Result = stable.Result
	info.ChannelMap = append(info.ChannelMap, charmhubclient.ChannelMap{
		Channel:  stable.DefaultRelease.Channel,
		Revision: stable.DefaultRelease.Revision,
	})
	client.infos[packageName] = info
}

func makeFakeResource(name, resourceType string, revision int, downloadURL string, payload []byte) charmhubclient.ReleaseResource {
	sum256 := sha256.Sum256(payload)
	sum384 := sha512.Sum384(payload)
	sum512 := sha512.Sum512(payload)
	return charmhubclient.ReleaseResource{
		Name:        name,
		Type:        resourceType,
		Revision:    revision,
		CreatedAt:   time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC),
		Description: name + " resource",
		Download: core.Download{
			URL:         downloadURL,
			Size:        int64(len(payload)),
			HashSHA256:  hex.EncodeToString(sum256[:]),
			HashSHA384:  hex.EncodeToString(sum384[:]),
			HashSHA512:  hex.EncodeToString(sum512[:]),
			HashSHA3384: hex.EncodeToString(sum384[:]),
		},
		Filename: name + ".bin",
	}
}

func cloneCharmhubChannel(
	item charmhubclient.PackageChannel,
	channelName, risk string,
) charmhubclient.PackageChannel {
	item.DefaultRelease.Channel.Name = channelName
	item.DefaultRelease.Channel.Risk = risk
	return item
}

func syncRuleByTrack(t *testing.T, rules []core.CharmhubSyncRule, track string) core.CharmhubSyncRule {
	t.Helper()

	for _, rule := range rules {
		if rule.Track == track {
			return rule
		}
	}
	require.Failf(t, "missing sync rule", "track %q", track)
	return core.CharmhubSyncRule{}
}

func buildSyncCharmArchive(t *testing.T, name string) []byte {
	t.Helper()

	var payload bytes.Buffer
	writer := zip.NewWriter(&payload)

	files := map[string]string{
		"metadata.yaml": "name: " + name + "\n" +
			"display-name: Synced Demo\n" +
			"summary: Synced summary\n" +
			"description: Synced description\n" +
			"website:\n" +
			"  - https://example.com/" + name + "\n" +
			"resources:\n" +
			"  config:\n" +
			"    type: file\n" +
			"    filename: config.txt\n" +
			"    description: Config file\n" +
			"  app-image:\n" +
			"    type: oci-image\n" +
			"    description: Application image\n" +
			"containers:\n" +
			"  app:\n" +
			"    resource: app-image\n",
		"config.txt": "value=true\n",
		"README.md":  "# Synced Demo\n",
	}

	for fileName, content := range files {
		entry, err := writer.Create(fileName)
		require.NoError(t, err)
		_, err = entry.Write([]byte(content))
		require.NoError(t, err)
	}

	require.NoError(t, writer.Close())
	return payload.Bytes()
}

func buildInvalidResourceArchive(t *testing.T, name string) []byte {
	t.Helper()

	var payload bytes.Buffer
	writer := zip.NewWriter(&payload)
	files := map[string]string{
		"metadata.yaml": "name: " + name + "\n" +
			"display-name: Synced Demo\n" +
			"summary: Synced summary\n" +
			"description: Synced description\n" +
			"resources:\n" +
			"  config:\n" +
			"    description: Missing type should be rejected\n",
		"config.txt": "value=true\n",
	}

	for fileName, content := range files {
		entry, err := writer.Create(fileName)
		require.NoError(t, err)
		_, err = entry.Write([]byte(content))
		require.NoError(t, err)
	}

	require.NoError(t, writer.Close())
	return payload.Bytes()
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

func ensurePersistedIdentity(t *testing.T, repository repo.AccountRepo, id, username string, isAdmin bool) core.Identity {
	t.Helper()
	account, err := repository.EnsureAccount(context.Background(), core.Account{
		ID:          id,
		Subject:     username,
		Username:    username,
		DisplayName: username,
		Email:       username + "@example.com",
		Validation:  "verified",
		IsAdmin:     isAdmin,
		CreatedAt:   time.Unix(0, 0).UTC(),
	})
	require.NoError(t, err)
	return core.Identity{Account: account, Authenticated: true}
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

func assertServiceError(t *testing.T, err error, expectedKind registryservice.ErrorKind) {
	t.Helper()
	require.Error(t, err)
	var svcErr *registryservice.Error
	require.ErrorAs(t, err, &svcErr)
	assert.Equal(t, expectedKind, svcErr.Kind)
}
