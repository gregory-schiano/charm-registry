package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gschiano/charm-registry/internal/blob"
	charmhubclient "github.com/gschiano/charm-registry/internal/charmhub"
	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
	"github.com/gschiano/charm-registry/internal/testutil"
)

type fakeCharmhubClient struct {
	channels     map[string]charmhubclient.PackageChannel
	infos        map[string]charmhubclient.PackageChannel
	downloads    map[string][]byte
	downloadErrs map[string]error
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

type trackingOCIRegistry struct {
	testutil.OCIRegistry
	deletedImages   []string
	deletedPackages []string
	mirrorErr       error
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

func TestRegisterPackageConflictsWithCharmhubSyncReservation(t *testing.T) {
	t.Parallel()

	svc := newSyncTestService(t)
	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	user := newIdentity("user-1", "user")

	_, err := svc.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)

	_, err = svc.RegisterPackage(context.Background(), user, "demo", "charm", false)
	assertServiceError(t, err, ErrorKindConflict)
}

func TestAddCharmhubSyncRuleConflictsWithManualPackage(t *testing.T) {
	t.Parallel()

	svc := newSyncTestService(t)
	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	user := newIdentity("user-1", "user")

	_, err := svc.RegisterPackage(context.Background(), user, "demo", "charm", false)
	require.NoError(t, err)

	_, err = svc.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	assertServiceError(t, err, ErrorKindConflict)
}

func TestTriggerCharmhubSyncRequiresExistingRule(t *testing.T) {
	t.Parallel()

	svc := newSyncTestService(t)
	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true

	err := svc.TriggerCharmhubSync(context.Background(), admin, "demo")
	assertServiceError(t, err, ErrorKindNotFound)
}

func TestTriggerCharmhubSyncAcceptsConfiguredPackage(t *testing.T) {
	t.Parallel()

	svc := newSyncTestService(t)
	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true

	_, err := svc.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)

	err = svc.TriggerCharmhubSync(context.Background(), admin, "demo")
	require.NoError(t, err)
}

func TestReconcileCharmhubPackageCreatesMirroredArtifacts(t *testing.T) {
	t.Parallel()

	svc := newSyncTestService(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	svc.charmhub = fakeClient
	svc.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := svc.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)

	require.NoError(t, svc.reconcileCharmhubPackage(context.Background(), "demo"))

	pkg, err := svc.repo.GetPackageByName(context.Background(), "demo")
	require.NoError(t, err)
	require.NotNil(t, pkg.Authority)
	assert.Equal(t, charmhubAuthority, *pkg.Authority)
	assert.Equal(t, charmhubSyncAccountID, pkg.OwnerAccountID)

	tracks, err := svc.repo.ListTracks(context.Background(), pkg.ID)
	require.NoError(t, err)
	require.Len(t, tracks, 1)
	assert.Equal(t, "latest", tracks[0].Name)

	revision, err := svc.repo.GetRevisionByNumber(context.Background(), pkg.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, "7", revision.Version)

	resourceDef, err := svc.repo.GetResourceDefinition(context.Background(), pkg.ID, "app-image")
	require.NoError(t, err)
	resourceRevision, err := svc.repo.GetResourceRevision(context.Background(), resourceDef.ID, 3)
	require.NoError(t, err)
	assert.Equal(t, "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", resourceRevision.OCIImageDigest)

	release, err := svc.repo.ResolveRelease(context.Background(), pkg.ID, "latest/stable")
	require.NoError(t, err)
	assert.Equal(t, 7, release.Revision)
	require.Len(t, release.Resources, 2)

	refresh, err := svc.ResolveRefresh(context.Background(), admin, RefreshRequest{
		Actions: []RefreshAction{{
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

	rules, err := svc.repo.ListCharmhubSyncRules(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, charmhubSyncStatusOK, rules[0].LastSyncStatus)
	assert.Nil(t, rules[0].LastSyncError)
}

func TestCharmhubSyncMirrorsAllBaseArchitectureVariantsByDefault(t *testing.T) {
	t.Parallel()

	svc := newSyncTestService(t)
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
	svc.charmhub = fakeClient
	svc.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := svc.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)

	require.NoError(t, svc.reconcileCharmhubPackage(context.Background(), "demo"))

	pkg, err := svc.repo.GetPackageByName(context.Background(), "demo")
	require.NoError(t, err)
	releases, err := svc.repo.ListReleases(context.Background(), pkg.ID)
	require.NoError(t, err)
	require.Len(t, releases, 2)

	amd64Release, err := svc.repo.ResolveReleaseForBase(
		context.Background(),
		pkg.ID,
		"latest/stable",
		core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"},
	)
	require.NoError(t, err)
	assert.Equal(t, 7, amd64Release.Revision)

	arm64Release, err := svc.repo.ResolveReleaseForBase(
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

	svc := newSyncTestService(t)
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
	svc.charmhub = fakeClient
	svc.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := svc.AddCharmhubSyncRule(
		context.Background(),
		admin,
		"demo",
		"latest",
		[]string{"ubuntu@24.04"},
		[]string{"arm64"},
	)
	require.NoError(t, err)

	require.NoError(t, svc.reconcileCharmhubPackage(context.Background(), "demo"))

	pkg, err := svc.repo.GetPackageByName(context.Background(), "demo")
	require.NoError(t, err)
	releases, err := svc.repo.ListReleases(context.Background(), pkg.ID)
	require.NoError(t, err)
	require.Len(t, releases, 1)
	require.NotNil(t, releases[0].Base)
	assert.Equal(t, "24.04", releases[0].Base.Channel)
	assert.Equal(t, "arm64", releases[0].Base.Architecture)
	assert.Equal(t, 8, releases[0].Revision)
}

func TestCharmhubManagedPackagesBlockPublisherMutations(t *testing.T) {
	t.Parallel()

	svc := newSyncTestService(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	svc.charmhub = fakeClient
	svc.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	publisher := newIdentity("publisher-1", "publisher")

	_, err := svc.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)
	require.NoError(t, svc.reconcileCharmhubPackage(context.Background(), "demo"))

	_, err = svc.UpdatePackage(context.Background(), publisher, "demo", MetadataPatch{Summary: stringPtr("manual")})
	assertServiceError(t, err, ErrorKindConflict)

	_, err = svc.CreateTracks(context.Background(), publisher, "demo", []core.Track{{Name: "2.0"}})
	assertServiceError(t, err, ErrorKindConflict)

	_, err = svc.CreateRelease(context.Background(), publisher, "demo", []core.Release{{Channel: "latest/stable", Revision: 7}})
	assertServiceError(t, err, ErrorKindConflict)

	_, err = svc.PushRevision(context.Background(), publisher, "demo", PushRevisionRequest{UploadID: "upload-1"})
	assertServiceError(t, err, ErrorKindConflict)

	_, err = svc.PushResource(
		context.Background(),
		publisher,
		"demo",
		"app-image",
		PushResourceRequest{UploadID: "upload-1"},
	)
	assertServiceError(t, err, ErrorKindConflict)
}

func TestRemovingLastCharmhubSyncRuleDeletesPackage(t *testing.T) {
	t.Parallel()

	svc := newSyncTestService(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	svc.charmhub = fakeClient
	svc.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := svc.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)
	require.NoError(t, svc.reconcileCharmhubPackage(context.Background(), "demo"))

	require.NoError(t, svc.RemoveCharmhubSyncRule(context.Background(), admin, "demo", "latest"))
	rules, err := svc.repo.ListCharmhubSyncRules(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, charmhubSyncStatusDeleting, rules[0].LastSyncStatus)

	require.NoError(t, svc.reconcileCharmhubPackage(context.Background(), "demo"))

	_, err = svc.repo.GetPackageByName(context.Background(), "demo")
	require.ErrorIs(t, err, repo.ErrNotFound)
	rules, err = svc.repo.ListCharmhubSyncRules(context.Background())
	require.NoError(t, err)
	assert.Empty(t, rules)
	assert.Contains(t, oci.deletedPackages, "demo")
}

func TestRemovingOneTrackPrunesOnlyUnreferencedArtifacts(t *testing.T) {
	t.Parallel()

	svc := newSyncTestService(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	addTrackFixture(t, fakeClient, "demo", "upstream-demo", "2.0", 11, 8, 6)
	svc.charmhub = fakeClient
	svc.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := svc.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)
	_, err = svc.AddCharmhubSyncRule(context.Background(), admin, "demo", "2.0", nil, nil)
	require.NoError(t, err)
	require.NoError(t, svc.reconcileCharmhubPackage(context.Background(), "demo"))

	require.NoError(t, svc.RemoveCharmhubSyncRule(context.Background(), admin, "demo", "latest"))
	rules, err := svc.repo.ListCharmhubSyncRules(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 2)
	assert.Equal(t, charmhubSyncStatusDeleting, syncRuleByTrack(t, rules, "latest").LastSyncStatus)

	require.NoError(t, svc.reconcileCharmhubPackage(context.Background(), "demo"))

	pkg, err := svc.repo.GetPackageByName(context.Background(), "demo")
	require.NoError(t, err)
	rules, err = svc.repo.ListCharmhubSyncRules(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "2.0", rules[0].Track)

	_, err = svc.repo.GetRevisionByNumber(context.Background(), pkg.ID, 7)
	require.ErrorIs(t, err, repo.ErrNotFound)
	_, err = svc.repo.GetRevisionByNumber(context.Background(), pkg.ID, 11)
	require.NoError(t, err)

	_, err = svc.repo.ResolveRelease(context.Background(), pkg.ID, "latest/stable")
	require.ErrorIs(t, err, repo.ErrNotFound)
	release, err := svc.repo.ResolveRelease(context.Background(), pkg.ID, "2.0/stable")
	require.NoError(t, err)
	assert.Equal(t, 11, release.Revision)
	assert.NotEmpty(t, oci.deletedImages)
}

func TestCharmhubSyncFailureMarksRuleErrorAndKeepsExistingRelease(t *testing.T) {
	t.Parallel()

	svc := newSyncTestService(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	svc.charmhub = fakeClient
	svc.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := svc.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)
	require.NoError(t, svc.reconcileCharmhubPackage(context.Background(), "demo"))

	addTrackFixture(t, fakeClient, "demo", "upstream-demo", "latest", 9, 4, 4)
	fakeClient.downloadErrs["https://charmhub.test/demo/latest/stable/ubuntu-24.04-amd64/revision-9.charm"] =
		fmt.Errorf("upstream unavailable")

	err = svc.reconcileCharmhubPackage(context.Background(), "demo")
	require.Error(t, err)

	pkg, getErr := svc.repo.GetPackageByName(context.Background(), "demo")
	require.NoError(t, getErr)
	release, getErr := svc.repo.ResolveRelease(context.Background(), pkg.ID, "latest/stable")
	require.NoError(t, getErr)
	assert.Equal(t, 7, release.Revision)

	rules, getErr := svc.repo.ListCharmhubSyncRules(context.Background())
	require.NoError(t, getErr)
	require.Len(t, rules, 1)
	assert.Equal(t, charmhubSyncStatusError, rules[0].LastSyncStatus)
	require.NotNil(t, rules[0].LastSyncError)
	assert.Contains(t, *rules[0].LastSyncError, "upstream unavailable")
}

func TestCharmhubSyncRemovesChannelWhenUpstreamDisappears(t *testing.T) {
	t.Parallel()

	svc := newSyncTestService(t)
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	addTrackFixture(t, fakeClient, "demo", "upstream-demo", "latest", 7, 3, 2)
	fakeClient.channels["demo|latest/candidate"] = cloneCharmhubChannel(fakeClient.channels["demo|latest/stable"], "latest/candidate", "candidate")
	svc.charmhub = fakeClient
	svc.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := svc.AddCharmhubSyncRule(context.Background(), admin, "demo", "latest", nil, nil)
	require.NoError(t, err)
	require.NoError(t, svc.reconcileCharmhubPackage(context.Background(), "demo"))

	delete(fakeClient.channels, "demo|latest/candidate")
	require.NoError(t, svc.reconcileCharmhubPackage(context.Background(), "demo"))

	pkg, err := svc.repo.GetPackageByName(context.Background(), "demo")
	require.NoError(t, err)
	_, err = svc.repo.ResolveRelease(context.Background(), pkg.ID, "latest/candidate")
	require.ErrorIs(t, err, repo.ErrNotFound)
}

func newSyncTestService(t *testing.T) *Service {
	t.Helper()
	repository := repo.NewMemory()
	return New(testConfig(), repository, blob.NewMemoryStore(), testutil.OCIRegistry{RegistryHost: "oci.test"})
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
