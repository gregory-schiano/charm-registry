package repo

import (
	"context"
	"fmt"
	"strings"

	"github.com/gschiano/charm-registry/internal/core"
	sqlcdb "github.com/gschiano/charm-registry/internal/repo/db"
)

// CreatePackage is part of the [Repository] interface.
func (p *Postgres) CreatePackage(ctx context.Context, pkg core.Package) error {
	linksJSON, err := rawJSON(pkg.Links)
	if err != nil {
		return err
	}
	mediaJSON, err := rawJSON(pkg.Media)
	if err != nil {
		return err
	}
	guardrailsJSON, err := rawJSON(pkg.TrackGuardrails)
	if err != nil {
		return err
	}
	err = p.queries().CreatePackage(ctx, sqlcdb.CreatePackageParams{
		ID:                    pkg.ID,
		Name:                  pkg.Name,
		Type:                  pkg.Type,
		Private:               pkg.Private,
		Status:                pkg.Status,
		OwnerAccountID:        pkg.OwnerAccountID,
		HarborProject:         pkg.HarborProject,
		HarborPushRobotID:     nullInt64(pkg.HarborPushRobot),
		HarborPushRobotName:   robotUsername(pkg.HarborPushRobot),
		HarborPushRobotSecret: robotSecret(pkg.HarborPushRobot),
		HarborPullRobotID:     nullInt64(pkg.HarborPullRobot),
		HarborPullRobotName:   robotUsername(pkg.HarborPullRobot),
		HarborPullRobotSecret: robotSecret(pkg.HarborPullRobot),
		HarborSyncedAt:        timestamptzPtr(pkg.HarborSyncedAt),
		Authority:             pkg.Authority,
		Contact:               pkg.Contact,
		DefaultTrack:          pkg.DefaultTrack,
		Description:           pkg.Description,
		Summary:               pkg.Summary,
		Title:                 pkg.Title,
		Website:               pkg.Website,
		Links:                 linksJSON,
		Media:                 mediaJSON,
		TrackGuardrails:       guardrailsJSON,
		CreatedAt:             pkg.CreatedAt,
		UpdatedAt:             pkg.UpdatedAt,
	})
	if err != nil && strings.Contains(err.Error(), "duplicate key") {
		return fmt.Errorf("package already exists: %w", ErrConflict)
	}
	return err
}

// UpdatePackage is part of the [Repository] interface.
func (p *Postgres) UpdatePackage(ctx context.Context, pkg core.Package) error {
	linksJSON, err := rawJSON(pkg.Links)
	if err != nil {
		return err
	}
	mediaJSON, err := rawJSON(pkg.Media)
	if err != nil {
		return err
	}
	guardrailsJSON, err := rawJSON(pkg.TrackGuardrails)
	if err != nil {
		return err
	}
	rowsAffected, err := p.queries().UpdatePackage(ctx, sqlcdb.UpdatePackageParams{
		ID:                    pkg.ID,
		Private:               pkg.Private,
		Status:                pkg.Status,
		HarborProject:         pkg.HarborProject,
		HarborPushRobotID:     nullInt64(pkg.HarborPushRobot),
		HarborPushRobotName:   robotUsername(pkg.HarborPushRobot),
		HarborPushRobotSecret: robotSecret(pkg.HarborPushRobot),
		HarborPullRobotID:     nullInt64(pkg.HarborPullRobot),
		HarborPullRobotName:   robotUsername(pkg.HarborPullRobot),
		HarborPullRobotSecret: robotSecret(pkg.HarborPullRobot),
		HarborSyncedAt:        timestamptzPtr(pkg.HarborSyncedAt),
		Authority:             pkg.Authority,
		Contact:               pkg.Contact,
		DefaultTrack:          pkg.DefaultTrack,
		Description:           pkg.Description,
		Summary:               pkg.Summary,
		Title:                 pkg.Title,
		Website:               pkg.Website,
		Links:                 linksJSON,
		Media:                 mediaJSON,
		TrackGuardrails:       guardrailsJSON,
		UpdatedAt:             pkg.UpdatedAt,
	})
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeletePackage is part of the [Repository] interface.
func (p *Postgres) DeletePackage(ctx context.Context, packageID string) error {
	rowsAffected, err := p.queries().DeletePackage(ctx, packageID)
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// GetPackageByName is part of the [Repository] interface.
func (p *Postgres) GetPackageByName(ctx context.Context, name string) (core.Package, error) {
	row, err := p.queries().GetPackageByName(ctx, name)
	if pgxNotFound(err) {
		return core.Package{}, ErrNotFound
	}
	if err != nil {
		return core.Package{}, err
	}
	return packageFromGetPackageByNameRow(row)
}

// GetPackageByID is part of the [Repository] interface.
func (p *Postgres) GetPackageByID(ctx context.Context, packageID string) (core.Package, error) {
	row, err := p.queries().GetPackageByID(ctx, packageID)
	if pgxNotFound(err) {
		return core.Package{}, ErrNotFound
	}
	if err != nil {
		return core.Package{}, err
	}
	return packageFromGetPackageByIDRow(row)
}

// ListPackagesForAccount is part of the [Repository] interface.
func (p *Postgres) ListPackagesForAccount(
	ctx context.Context,
	accountID string,
	includeCollaborations bool,
) ([]core.Package, error) {
	if includeCollaborations {
		rows, err := p.queries().ListPackagesForAccountWithCollaborations(ctx, accountID)
		if err != nil {
			return nil, err
		}
		out := make([]core.Package, 0, len(rows))
		for _, row := range rows {
			pkg, err := packageFromListPackagesForAccountWithCollaborationsRow(row)
			if err != nil {
				return nil, err
			}
			out = append(out, pkg)
		}
		return out, nil
	}

	rows, err := p.queries().ListPackagesForAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	out := make([]core.Package, 0, len(rows))
	for _, row := range rows {
		pkg, err := packageFromListPackagesForAccountRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, pkg)
	}
	return out, nil
}

// SearchPackages is part of the [Repository] interface.
func (p *Postgres) SearchPackages(ctx context.Context, query string) ([]core.Package, error) {
	pattern := "%"
	if trimmed := strings.TrimSpace(query); trimmed != "" {
		pattern = "%" + escapeLikePattern(trimmed) + "%"
	}
	rows, err := p.queries().SearchPackages(ctx, pattern)
	if err != nil {
		return nil, err
	}
	out := make([]core.Package, 0, len(rows))
	for _, row := range rows {
		pkg, err := packageFromSearchPackagesRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, pkg)
	}
	return out, nil
}

func escapeLikePattern(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

// CanViewPackage is part of the [Repository] interface.
func (p *Postgres) CanViewPackage(ctx context.Context, packageID, accountID string) (bool, error) {
	return p.canAccess(ctx, packageID, accountID, false)
}

// CanManagePackage is part of the [Repository] interface.
func (p *Postgres) CanManagePackage(ctx context.Context, packageID, accountID string) (bool, error) {
	return p.canAccess(ctx, packageID, accountID, true)
}

func (p *Postgres) canAccess(ctx context.Context, packageID, accountID string, manage bool) (bool, error) {
	owner, err := p.queries().GetPackageOwner(ctx, packageID)
	if pgxNotFound(err) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if !manage && !owner.Private {
		return true, nil
	}
	if accountID == "" {
		return false, nil
	}
	if owner.OwnerAccountID == accountID {
		return true, nil
	}
	roles := []string{"viewer", "editor", "owner"}
	if manage {
		roles = []string{"editor", "owner"}
		return p.queries().CanManagePackage(ctx, sqlcdb.CanManagePackageParams{
			PackageID:   packageID,
			PrincipalID: accountID,
			Column3:     roles,
		})
	}
	return p.queries().CanViewPackage(ctx, sqlcdb.CanViewPackageParams{
		PackageID:   packageID,
		PrincipalID: accountID,
		Column3:     roles,
	})
}
