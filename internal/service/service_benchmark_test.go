package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gschiano/charm-registry/internal/blob"
	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
	"github.com/gschiano/charm-registry/internal/testutil"
)

func BenchmarkListRegisteredPackages(b *testing.B) {
	ctx := context.Background()
	repository := repo.NewMemory()
	svc := New(testConfig(), repository, blob.NewMemoryStore(), testutil.OCIRegistry{})
	owner := newIdentity("bench-owner", "bench-owner")
	now := time.Now().UTC()

	for i := range 500 {
		pkg := core.Package{
			ID:             fmt.Sprintf("pkg-%d", i),
			Name:           fmt.Sprintf("bench-%03d", i),
			Type:           "charm",
			Status:         "registered",
			OwnerAccountID: owner.Account.ID,
			CreatedAt:      now,
			UpdatedAt:      now,
			Tracks:         []core.Track{{Name: "latest", CreatedAt: now}},
		}
		if err := repository.CreatePackage(ctx, pkg); err != nil {
			b.Fatal(err)
		}
		if _, err := repository.CreateTracks(ctx, pkg.ID, pkg.Tracks); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		packages, err := svc.ListRegisteredPackages(ctx, owner, false)
		if err != nil {
			b.Fatal(err)
		}
		if len(packages) != 500 {
			b.Fatalf("got %d packages", len(packages))
		}
	}
}
