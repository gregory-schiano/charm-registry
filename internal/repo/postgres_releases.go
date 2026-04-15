package repo

import (
	"context"

	"github.com/gschiano/charm-registry/internal/core"
	sqlcdb "github.com/gschiano/charm-registry/internal/repo/db"
)

// ReplaceRelease is part of the [Repository] interface.
func (p *Postgres) ReplaceRelease(ctx context.Context, packageID string, release core.Release) error {
	baseJSON, err := rawJSON(release.Base)
	if err != nil {
		return err
	}
	resourcesJSON, err := rawJSON(release.Resources)
	if err != nil {
		return err
	}
	revision, err := toInt32(release.Revision)
	if err != nil {
		return err
	}
	return p.queries().ReplaceRelease(ctx, sqlcdb.ReplaceReleaseParams{
		ID:             release.ID,
		PackageID:      packageID,
		Channel:        release.Channel,
		Revision:       revision,
		Base:           baseJSON,
		Resources:      resourcesJSON,
		WhenCreated:    release.When,
		ExpirationDate: timestamptzPtr(release.ExpirationDate),
		Progressive:    release.Progressive,
	})
}

// DeleteRelease is part of the [Repository] interface.
func (p *Postgres) DeleteRelease(ctx context.Context, packageID, channel string) error {
	rowsAffected, err := p.queries().DeleteRelease(ctx, sqlcdb.DeleteReleaseParams{
		PackageID: packageID,
		Channel:   channel,
	})
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListReleases is part of the [Repository] interface.
func (p *Postgres) ListReleases(ctx context.Context, packageID string) ([]core.Release, error) {
	rows, err := p.queries().ListReleases(ctx, packageID)
	if err != nil {
		return nil, err
	}
	out := make([]core.Release, 0, len(rows))
	for _, row := range rows {
		release, err := releaseFromSQLC(row)
		if err != nil {
			return nil, err
		}
		out = append(out, release)
	}
	return out, nil
}

// ResolveRelease is part of the [Repository] interface.
func (p *Postgres) ResolveRelease(ctx context.Context, packageID string, channel string) (core.Release, error) {
	row, err := p.queries().ResolveRelease(ctx, sqlcdb.ResolveReleaseParams{
		PackageID: packageID,
		Channel:   channel,
	})
	if pgxNotFound(err) {
		return core.Release{}, ErrNotFound
	}
	if err != nil {
		return core.Release{}, err
	}
	return releaseFromSQLC(row)
}

// ResolveDefaultRelease is part of the [Repository] interface.
func (p *Postgres) ResolveDefaultRelease(ctx context.Context, packageID string) (core.Release, error) {
	release, err := p.queries().ResolveDefaultRelease(ctx, packageID)
	if pgxNotFound(err) {
		release, err = p.queries().ResolveLatestRelease(ctx, packageID)
		if pgxNotFound(err) {
			return core.Release{}, ErrNotFound
		}
	}
	if err != nil {
		return core.Release{}, err
	}
	return releaseFromSQLC(release)
}
