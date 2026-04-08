package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/gschiano/charm-registry/internal/core"
)

// CreatePackage is part of the [Repository] interface.
func (p *Postgres) CreatePackage(ctx context.Context, pkg core.Package) error {
	linksJSON, err := marshalJSON(pkg.Links)
	if err != nil {
		return err
	}
	mediaJSON, err := marshalJSON(pkg.Media)
	if err != nil {
		return err
	}
	guardrailsJSON, err := marshalJSON(pkg.TrackGuardrails)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(
		ctx,
		`
			INSERT INTO packages (
				id, name, type, private, status, owner_account_id,
				harbor_project, harbor_push_robot_id, harbor_push_robot_name, harbor_push_robot_secret,
				harbor_pull_robot_id, harbor_pull_robot_name, harbor_pull_robot_secret, harbor_synced_at,
				authority, contact, default_track, description, summary, title, website,
				links, media, track_guardrails, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)
		`,
		pkg.ID,
		pkg.Name,
		pkg.Type,
		pkg.Private,
		pkg.Status,
		pkg.OwnerAccountID,
		pkg.HarborProject,
		nullInt64(pkg.HarborPushRobot),
		robotUsername(pkg.HarborPushRobot),
		robotSecret(pkg.HarborPushRobot),
		nullInt64(pkg.HarborPullRobot),
		robotUsername(pkg.HarborPullRobot),
		robotSecret(pkg.HarborPullRobot),
		pkg.HarborSyncedAt,
		pkg.Authority,
		pkg.Contact,
		pkg.DefaultTrack,
		pkg.Description,
		pkg.Summary,
		pkg.Title,
		pkg.Website,
		linksJSON,
		mediaJSON,
		guardrailsJSON,
		pkg.CreatedAt,
		pkg.UpdatedAt,
	)
	if err != nil && strings.Contains(err.Error(), "duplicate key") {
		return fmt.Errorf("package already exists: %w", ErrConflict)
	}
	return err
}

// UpdatePackage is part of the [Repository] interface.
func (p *Postgres) UpdatePackage(ctx context.Context, pkg core.Package) error {
	linksJSON, err := marshalJSON(pkg.Links)
	if err != nil {
		return err
	}
	mediaJSON, err := marshalJSON(pkg.Media)
	if err != nil {
		return err
	}
	guardrailsJSON, err := marshalJSON(pkg.TrackGuardrails)
	if err != nil {
		return err
	}
	tag, err := p.db.Exec(
		ctx,
		`
			UPDATE packages SET
				private = $2,
				status = $3,
				harbor_project = $4,
				harbor_push_robot_id = $5,
				harbor_push_robot_name = $6,
				harbor_push_robot_secret = $7,
				harbor_pull_robot_id = $8,
				harbor_pull_robot_name = $9,
				harbor_pull_robot_secret = $10,
				harbor_synced_at = $11,
				authority = $12,
				contact = $13,
				default_track = $14,
				description = $15,
				summary = $16,
				title = $17,
				website = $18,
				links = $19,
				media = $20,
				track_guardrails = $21,
				updated_at = $22
			WHERE id = $1
		`,
		pkg.ID,
		pkg.Private,
		pkg.Status,
		pkg.HarborProject,
		nullInt64(pkg.HarborPushRobot),
		robotUsername(pkg.HarborPushRobot),
		robotSecret(pkg.HarborPushRobot),
		nullInt64(pkg.HarborPullRobot),
		robotUsername(pkg.HarborPullRobot),
		robotSecret(pkg.HarborPullRobot),
		pkg.HarborSyncedAt,
		pkg.Authority,
		pkg.Contact,
		pkg.DefaultTrack,
		pkg.Description,
		pkg.Summary,
		pkg.Title,
		pkg.Website,
		linksJSON,
		mediaJSON,
		guardrailsJSON,
		pkg.UpdatedAt,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeletePackage is part of the [Repository] interface.
func (p *Postgres) DeletePackage(ctx context.Context, packageID string) error {
	tag, err := p.db.Exec(ctx, `DELETE FROM packages WHERE id = $1`, packageID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetPackageByName is part of the [Repository] interface.
func (p *Postgres) GetPackageByName(ctx context.Context, name string) (core.Package, error) {
	row := p.db.QueryRow(ctx, packageQuery(`WHERE p.name = $1`), name)
	pkg, err := scanPackage(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Package{}, ErrNotFound
	}
	return pkg, err
}

// GetPackageByID is part of the [Repository] interface.
func (p *Postgres) GetPackageByID(ctx context.Context, packageID string) (core.Package, error) {
	row := p.db.QueryRow(ctx, packageQuery(`WHERE p.id = $1`), packageID)
	pkg, err := scanPackage(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Package{}, ErrNotFound
	}
	return pkg, err
}

// ListPackagesForAccount is part of the [Repository] interface.
func (p *Postgres) ListPackagesForAccount(
	ctx context.Context,
	accountID string,
	includeCollaborations bool,
) ([]core.Package, error) {
	query := packageQuery(`WHERE p.owner_account_id = $1`)
	if includeCollaborations {
		query = packageQuery(`
			WHERE p.owner_account_id = $1
			   OR EXISTS (
				    SELECT 1 FROM package_acl acl
				    LEFT JOIN account_group_members gm
				      ON acl.principal_type = 'group' AND acl.principal_id = gm.group_id
				    WHERE acl.package_id = p.id
				      AND ((acl.principal_type = 'account' AND acl.principal_id = $1) OR gm.account_id = $1)
			   )
		`)
	}
	rows, err := p.db.Query(ctx, query, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(rows)
}

// SearchPackages is part of the [Repository] interface.
func (p *Postgres) SearchPackages(ctx context.Context, query string) ([]core.Package, error) {
	pattern := "%"
	if trimmed := strings.TrimSpace(query); trimmed != "" {
		pattern = "%" + trimmed + "%"
	}
	rows, err := p.db.Query(ctx, packageQuery(`WHERE p.name ILIKE $1 ORDER BY p.name ASC`), pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(rows)
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
	var private bool
	var ownerID string
	err := p.db.QueryRow(ctx, `SELECT private, owner_account_id FROM packages WHERE id = $1`, packageID).
		Scan(&private, &ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if !manage && !private {
		return true, nil
	}
	if accountID == "" {
		return false, nil
	}
	if ownerID == accountID {
		return true, nil
	}
	// Pass the allowed roles as a parameterized array ($3) so no SQL fragment
	// is ever interpolated. PostgreSQL's = ANY($3) is the safe equivalent of
	// the old IN (...) pattern built with fmt.Sprintf.
	roles := []string{"viewer", "editor", "owner"}
	if manage {
		roles = []string{"editor", "owner"}
	}
	var allowed bool
	err = p.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM package_acl acl
			LEFT JOIN account_group_members gm
			  ON acl.principal_type = 'group' AND acl.principal_id = gm.group_id
			WHERE acl.package_id = $1
			  AND acl.role = ANY($3)
			  AND ((acl.principal_type = 'account' AND acl.principal_id = $2) OR gm.account_id = $2)
		)
	`, packageID, accountID, roles).Scan(&allowed)
	return allowed, err
}

func packageQuery(where string) string {
	return `
			SELECT
				p.id, p.name, p.type, p.private, p.status, p.owner_account_id,
				p.harbor_project, p.harbor_push_robot_id, p.harbor_push_robot_name, p.harbor_push_robot_secret,
				p.harbor_pull_robot_id, p.harbor_pull_robot_name, p.harbor_pull_robot_secret, p.harbor_synced_at,
				p.authority, p.contact, p.default_track, p.description, p.summary, p.title,
				p.website, p.links, p.media, p.track_guardrails, p.created_at, p.updated_at,
				a.id, a.username, a.display_name, a.email, a.validation
			FROM packages p
			JOIN accounts a ON a.id = p.owner_account_id
	` + where
}

func scanPackages(rows pgx.Rows) ([]core.Package, error) {
	var out []core.Package
	for rows.Next() {
		pkg, err := scanPackage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pkg)
	}
	return out, rows.Err()
}

func scanPackage(row interface{ Scan(dest ...any) error }) (core.Package, error) {
	var pkg core.Package
	var linksJSON []byte
	var mediaJSON []byte
	var guardrailsJSON []byte
	var harborPushRobotID *int64
	var harborPushRobotName string
	var harborPushRobotSecret string
	var harborPullRobotID *int64
	var harborPullRobotName string
	var harborPullRobotSecret string
	err := row.Scan(
		&pkg.ID,
		&pkg.Name,
		&pkg.Type,
		&pkg.Private,
		&pkg.Status,
		&pkg.OwnerAccountID,
		&pkg.HarborProject,
		&harborPushRobotID,
		&harborPushRobotName,
		&harborPushRobotSecret,
		&harborPullRobotID,
		&harborPullRobotName,
		&harborPullRobotSecret,
		&pkg.HarborSyncedAt,
		&pkg.Authority,
		&pkg.Contact,
		&pkg.DefaultTrack,
		&pkg.Description,
		&pkg.Summary,
		&pkg.Title,
		&pkg.Website,
		&linksJSON,
		&mediaJSON,
		&guardrailsJSON,
		&pkg.CreatedAt,
		&pkg.UpdatedAt,
		&pkg.Publisher.ID,
		&pkg.Publisher.Username,
		&pkg.Publisher.DisplayName,
		&pkg.Publisher.Email,
		&pkg.Publisher.Validation,
	)
	if err != nil {
		return core.Package{}, err
	}
	pkg.HarborPushRobot = newRobotCredential(harborPushRobotID, harborPushRobotName, harborPushRobotSecret)
	pkg.HarborPullRobot = newRobotCredential(harborPullRobotID, harborPullRobotName, harborPullRobotSecret)
	if err := unmarshalJSON(linksJSON, &pkg.Links); err != nil {
		return core.Package{}, fmt.Errorf("unmarshal package links: %w", err)
	}
	if err := unmarshalJSON(mediaJSON, &pkg.Media); err != nil {
		return core.Package{}, fmt.Errorf("unmarshal package media: %w", err)
	}
	if err := unmarshalJSON(guardrailsJSON, &pkg.TrackGuardrails); err != nil {
		return core.Package{}, fmt.Errorf("unmarshal package track guardrails: %w", err)
	}
	pkg.Store = ""
	return pkg, nil
}

func newRobotCredential(id *int64, username, encryptedSecret string) *core.RobotCredential {
	if id == nil && username == "" && encryptedSecret == "" {
		return nil
	}
	credential := &core.RobotCredential{
		Username:        username,
		EncryptedSecret: encryptedSecret,
	}
	if id != nil {
		credential.ID = *id
	}
	return credential
}

func nullInt64(credential *core.RobotCredential) *int64 {
	if credential == nil || credential.ID == 0 {
		return nil
	}
	value := credential.ID
	return &value
}

func robotUsername(credential *core.RobotCredential) string {
	if credential == nil {
		return ""
	}
	return credential.Username
}

func robotSecret(credential *core.RobotCredential) string {
	if credential == nil {
		return ""
	}
	return credential.EncryptedSecret
}
