package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/gschiano/charm-registry/internal/core"
)

// ReplaceRelease is part of the [Repository] interface.
func (p *Postgres) ReplaceRelease(ctx context.Context, packageID string, release core.Release) error {
	_, err := p.pool.Exec(
		ctx,
		`
		INSERT INTO releases (id, package_id, channel, revision, base, resources, when_created, expiration_date, progressive)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (package_id, channel) DO UPDATE SET
			revision = EXCLUDED.revision,
			base = EXCLUDED.base,
			resources = EXCLUDED.resources,
			when_created = EXCLUDED.when_created,
			expiration_date = EXCLUDED.expiration_date,
			progressive = EXCLUDED.progressive
	`,
		release.ID,
		packageID,
		release.Channel,
		release.Revision,
		mustJSON(release.Base),
		mustJSON(release.Resources),
		release.When,
		release.ExpirationDate,
		release.Progressive,
	)
	return err
}

// ListReleases is part of the [Repository] interface.
func (p *Postgres) ListReleases(ctx context.Context, packageID string) ([]core.Release, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, package_id, channel, revision, base, resources, when_created, expiration_date, progressive
		FROM releases WHERE package_id = $1 ORDER BY channel ASC
	`, packageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Release
	for rows.Next() {
		release, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, release)
	}
	return out, rows.Err()
}

// ResolveRelease is part of the [Repository] interface.
func (p *Postgres) ResolveRelease(ctx context.Context, packageID string, channel string) (core.Release, error) {
	row := p.pool.QueryRow(ctx, `
		SELECT id, package_id, channel, revision, base, resources, when_created, expiration_date, progressive
		FROM releases WHERE package_id = $1 AND channel = $2
	`, packageID, channel)
	release, err := scanRelease(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Release{}, ErrNotFound
	}
	return release, err
}

// ResolveDefaultRelease is part of the [Repository] interface.
func (p *Postgres) ResolveDefaultRelease(ctx context.Context, packageID string) (core.Release, error) {
	row := p.pool.QueryRow(ctx, `
		SELECT
			r.id, r.package_id, r.channel, r.revision, r.base, r.resources,
			r.when_created, r.expiration_date, r.progressive
		FROM packages p
		JOIN releases r ON r.package_id = p.id
		WHERE p.id = $1 AND (
			(p.default_track IS NOT NULL AND r.channel = p.default_track || '/stable') OR
			(p.default_track IS NULL AND r.channel = 'latest/stable')
		)
		ORDER BY r.when_created DESC LIMIT 1
	`, packageID)
	release, err := scanRelease(row)
	if errors.Is(err, pgx.ErrNoRows) {
		rows, err := p.pool.Query(ctx, `
			SELECT id, package_id, channel, revision, base, resources, when_created, expiration_date, progressive
			FROM releases WHERE package_id = $1 ORDER BY when_created DESC LIMIT 1
		`, packageID)
		if err != nil {
			return core.Release{}, err
		}
		defer rows.Close()
		if !rows.Next() {
			return core.Release{}, ErrNotFound
		}
		return scanRelease(rows)
	}
	return release, err
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
		unmarshalJSON(baseJSON, &base)
		release.Base = &base
	}
	unmarshalJSON(resourcesJSON, &release.Resources)
	return release, nil
}
