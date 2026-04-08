package repo

import (
	"context"
	"slices"

	"github.com/gschiano/charm-registry/internal/core"
)

// CreateTracks is part of the [Repository] interface.
func (p *Postgres) CreateTracks(ctx context.Context, packageID string, tracks []core.Track) (int, error) {
	var created int
	for _, track := range tracks {
		tag, err := p.db.Exec(ctx, `
			INSERT INTO tracks (package_id, name, version_pattern, automatic_phasing_percentage, created_at)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT DO NOTHING
		`, packageID, track.Name, track.VersionPattern, track.AutomaticPhasingPercentage, track.CreatedAt)
		if err != nil {
			return created, err
		}
		created += int(tag.RowsAffected())
	}
	return created, nil
}

// ListTracks is part of the [Repository] interface.
func (p *Postgres) ListTracks(ctx context.Context, packageID string) ([]core.Track, error) {
	rows, err := p.db.Query(ctx, `
		SELECT name, version_pattern, automatic_phasing_percentage, created_at
		FROM tracks WHERE package_id = $1 ORDER BY created_at ASC
	`, packageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Track
	for rows.Next() {
		var track core.Track
		if err := rows.Scan(
			&track.Name,
			&track.VersionPattern,
			&track.AutomaticPhasingPercentage,
			&track.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, track)
	}
	return out, rows.Err()
}

// ListTracksForPackages is part of the [Repository] interface.
func (p *Postgres) ListTracksForPackages(ctx context.Context, packageIDs []string) (map[string][]core.Track, error) {
	out := make(map[string][]core.Track, len(packageIDs))
	if len(packageIDs) == 0 {
		return out, nil
	}
	rows, err := p.db.Query(ctx, `
		SELECT package_id, name, version_pattern, automatic_phasing_percentage, created_at
		FROM tracks WHERE package_id = ANY($1) ORDER BY created_at ASC
	`, packageIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			packageID string
			track     core.Track
		)
		if err := rows.Scan(
			&packageID,
			&track.Name,
			&track.VersionPattern,
			&track.AutomaticPhasingPercentage,
			&track.CreatedAt,
		); err != nil {
			return nil, err
		}
		out[packageID] = append(out[packageID], track)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, packageID := range slices.Compact(packageIDs) {
		if _, ok := out[packageID]; !ok {
			out[packageID] = nil
		}
	}
	return out, nil
}
