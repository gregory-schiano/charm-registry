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
	return p.queries().ReplaceRelease(ctx, sqlcdb.ReplaceReleaseParams{
		ID:             release.ID,
		PackageID:      packageID,
		Channel:        release.Channel,
		Revision:       int32(release.Revision),
		Base:           baseJSON,
		Resources:      resourcesJSON,
		WhenCreated:    release.When,
		ExpirationDate: timestamptzPtr(release.ExpirationDate),
		Progressive:    release.Progressive,
	})
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

func scanRelease(row interface{ Scan(dest ...any) error }) (core.Release, error) {
	var release core.Release
	var baseJSON []byte
	var resourcesJSON []byte
	err := row.Scan(
		&release.ID,
		&release.PackageID,
		&release.Channel,
		&release.Revision,
		&baseJSON,
		&resourcesJSON,
		&release.When,
		&release.ExpirationDate,
		&release.Progressive,
	)
	if err != nil {
		return core.Release{}, err
	}
	if string(baseJSON) != "null" && len(baseJSON) != 0 {
		var base core.Base
		if err := unmarshalJSON(baseJSON, &base); err != nil {
			return core.Release{}, err
		}
		release.Base = &base
	}
	if err := unmarshalJSON(resourcesJSON, &release.Resources); err != nil {
		return core.Release{}, err
	}
	return release, nil
}
