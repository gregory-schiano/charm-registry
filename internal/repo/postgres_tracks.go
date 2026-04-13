package repo

import (
	"context"

	"github.com/gschiano/charm-registry/internal/core"
	sqlcdb "github.com/gschiano/charm-registry/internal/repo/db"
)

// CreateTracks is part of the [Repository] interface.
func (p *Postgres) CreateTracks(ctx context.Context, packageID string, tracks []core.Track) (int, error) {
	var created int
	for _, track := range tracks {
		rowsAffected, err := p.queries().CreateTrack(ctx, sqlcdb.CreateTrackParams{
			PackageID:                  packageID,
			Name:                       track.Name,
			VersionPattern:             track.VersionPattern,
			AutomaticPhasingPercentage: track.AutomaticPhasingPercentage,
			CreatedAt:                  track.CreatedAt,
		})
		if err != nil {
			return created, err
		}
		created += int(rowsAffected)
	}
	return created, nil
}

// ListTracks is part of the [Repository] interface.
func (p *Postgres) ListTracks(ctx context.Context, packageID string) ([]core.Track, error) {
	rows, err := p.queries().ListTracks(ctx, packageID)
	if err != nil {
		return nil, err
	}
	out := make([]core.Track, 0, len(rows))
	for _, row := range rows {
		out = append(out, trackFromSQLC(row))
	}
	return out, nil
}

// ListTracksForPackages is part of the [Repository] interface.
func (p *Postgres) ListTracksForPackages(ctx context.Context, packageIDs []string) (map[string][]core.Track, error) {
	out := make(map[string][]core.Track, len(packageIDs))
	if len(packageIDs) == 0 {
		return out, nil
	}
	rows, err := p.queries().ListTracksForPackages(ctx, packageIDs)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(packageIDs))
	for _, row := range rows {
		out[row.PackageID] = append(out[row.PackageID], trackBatchFromSQLC(row))
	}
	for _, packageID := range packageIDs {
		if _, ok := seen[packageID]; ok {
			continue
		}
		seen[packageID] = struct{}{}
		if _, ok := out[packageID]; !ok {
			out[packageID] = nil
		}
	}
	return out, nil
}
