package repo

import (
	"context"

	"github.com/gschiano/charm-registry/internal/core"
	sqlcdb "github.com/gschiano/charm-registry/internal/repo/db"
)

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

func (p *Postgres) DeleteReleaseForBase(ctx context.Context, packageID, channel string, base *core.Base) error {
	baseJSON, err := rawJSON(base)
	if err != nil {
		return err
	}
	rowsAffected, err := p.queries().DeleteReleaseForBase(ctx, sqlcdb.DeleteReleaseForBaseParams{
		PackageID: packageID,
		Channel:   channel,
		Column3:   baseJSON,
	})
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) DeleteStaleTrackReleases(ctx context.Context, packageID, track string, keep []ReleaseVariant) (int64, error) {
	keepVariants := make([]map[string]string, 0, len(keep))
	for _, variant := range keep {
		baseJSON, err := rawJSON(variant.Base)
		if err != nil {
			return 0, err
		}
		keepVariants = append(keepVariants, map[string]string{
			"channel":  variant.Channel,
			"base_key": string(baseJSON),
		})
	}
	keepJSON, err := rawJSON(keepVariants)
	if err != nil {
		return 0, err
	}
	return p.queries().DeleteStaleTrackReleases(ctx, sqlcdb.DeleteStaleTrackReleasesParams{
		PackageID:    packageID,
		TrackPrefix:  track + "/%",
		KeepVariants: keepJSON,
	})
}

func (p *Postgres) ListReleases(ctx context.Context, packageID string) ([]core.Release, error) {
	rows, err := p.queries().ListReleases(ctx, packageID)
	if err != nil {
		return nil, err
	}
	out := make([]core.Release, 0, len(rows))
	for _, row := range rows {
		release, err := releaseFromListRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, release)
	}
	return out, nil
}

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
	return releaseFromResolveRow(row)
}

func (p *Postgres) ResolveReleaseForBase(
	ctx context.Context,
	packageID string,
	channel string,
	base core.Base,
) (core.Release, error) {
	baseJSON, err := rawJSON(base)
	if err != nil {
		return core.Release{}, err
	}
	row, err := p.queries().ResolveReleaseForBase(ctx, sqlcdb.ResolveReleaseForBaseParams{
		PackageID: packageID,
		Channel:   channel,
		Column3:   baseJSON,
	})
	if pgxNotFound(err) {
		return core.Release{}, ErrNotFound
	}
	if err != nil {
		return core.Release{}, err
	}
	return releaseFromResolveForBaseRow(row)
}

func (p *Postgres) ResolveDefaultRelease(ctx context.Context, packageID string) (core.Release, error) {
	release, err := p.queries().ResolveDefaultRelease(ctx, packageID)
	if pgxNotFound(err) {
		latest, latestErr := p.queries().ResolveLatestRelease(ctx, packageID)
		if pgxNotFound(latestErr) {
			return core.Release{}, ErrNotFound
		}
		if latestErr != nil {
			return core.Release{}, latestErr
		}
		return releaseFromLatestRow(latest)
	}
	if err != nil {
		return core.Release{}, err
	}
	return releaseFromDefaultRow(release)
}
