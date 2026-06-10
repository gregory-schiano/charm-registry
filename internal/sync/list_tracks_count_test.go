package registrysync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gschiano/charm-registry/internal/blob"
	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
	registryservice "github.com/gschiano/charm-registry/internal/service"
	"github.com/gschiano/charm-registry/internal/testutil"
)

type listTracksCountingRepo struct {
	repo.Backend
	listTracksCalls int
}

func (r *listTracksCountingRepo) ListTracks(ctx context.Context, packageID string) ([]core.Track, error) {
	r.listTracksCalls++
	return r.Backend.ListTracks(ctx, packageID)
}

func TestReconcilePackageDoesNotRepeatListTracksForPackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseRepo := repo.NewMemory()
	countingRepo := &listTracksCountingRepo{Backend: baseRepo}
	storage := blob.NewMemoryStore()
	ociRegistry := testutil.OCIRegistry{RegistryHost: "oci.test"}
	env := &syncTestHarness{
		registry: registryservice.New(testConfig(), countingRepo, storage, ociRegistry),
		sync:     New(testConfig(), countingRepo, storage, ociRegistry),
		repo:     countingRepo,
	}
	fakeClient, oci := newSyncFixture(t, "demo", "upstream-demo")
	addTrackFixture(t, fakeClient, "demo", "upstream-demo", "2.0", 8, 4, 3)
	env.sync.charmhub = fakeClient
	env.sync.oci = oci

	admin := newIdentity("admin-1", "admin")
	admin.Account.IsAdmin = true
	_, err := env.sync.AddCharmhubSyncRule(ctx, admin, "demo", "latest", nil, nil)
	require.NoError(t, err)
	_, err = env.sync.AddCharmhubSyncRule(ctx, admin, "demo", "2.0", nil, nil)
	require.NoError(t, err)

	require.NoError(t, env.sync.reconcilePackage(ctx, "demo"))

	assert.LessOrEqual(t, countingRepo.listTracksCalls, 1)
}
