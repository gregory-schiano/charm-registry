package repo

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/gschiano/charm-registry/internal/core"
)

//go:embed sqlite/migrations/*.sql
var sqliteMigrationsFS embed.FS

type SQLite struct {
	db sqliteDB
}

type sqliteDB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func NewSQLite(ctx context.Context, databasePath string) (*SQLite, error) {
	if databasePath == "" {
		return nil, fmt.Errorf("sqlite database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o750); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", databasePath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return &SQLite{db: db}, nil
}

func (s *SQLite) Close() error {
	if db, ok := s.db.(*sql.DB); ok {
		return db.Close()
	}
	return nil
}

func (s *SQLite) Ping(ctx context.Context) error {
	if db, ok := s.db.(*sql.DB); ok {
		return db.PingContext(ctx)
	}
	_, err := s.db.ExecContext(ctx, "SELECT 1")
	return err
}

func (s *SQLite) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TIMESTAMP NOT NULL
)`); err != nil {
		return err
	}
	entries, err := sqliteMigrationsFS.ReadDir("sqlite/migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		var applied string
		err := s.db.QueryRowContext(ctx, "SELECT version FROM schema_migrations WHERE version = ?", entry.Name()).Scan(&applied)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		payload, err := sqliteMigrationsFS.ReadFile("sqlite/migrations/" + entry.Name())
		if err != nil {
			return err
		}
		if err := s.WithinTransaction(ctx, func(repository CompositeRepo) error {
			sqliteRepo := repository.(*SQLite)
			if _, err := sqliteRepo.db.ExecContext(ctx, string(payload)); err != nil {
				return fmt.Errorf("cannot apply migration %s: %w", entry.Name(), err)
			}
			_, err := sqliteRepo.db.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", entry.Name(), time.Now().UTC())
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLite) WithinTransaction(ctx context.Context, fn func(CompositeRepo) error) error {
	db, ok := s.db.(*sql.DB)
	if !ok {
		return fn(s)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	txRepo := &SQLite{db: tx}
	if err := fn(txRepo); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *SQLite) EnsureAccount(ctx context.Context, account core.Account) (core.Account, error) {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO accounts (id, subject, username, display_name, email, validation, is_admin, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(subject) DO UPDATE SET
    username = excluded.username,
    display_name = excluded.display_name,
    email = excluded.email,
    validation = excluded.validation,
    is_admin = excluded.is_admin`,
		account.ID, account.Subject, account.Username, account.DisplayName, account.Email, account.Validation, account.IsAdmin, account.CreatedAt)
	if err != nil {
		return core.Account{}, err
	}
	return s.accountBySubject(ctx, account.Subject)
}

func (s *SQLite) GetAccountByID(ctx context.Context, accountID string) (core.Account, error) {
	return scanAccount(s.db.QueryRowContext(ctx, `
SELECT id, subject, username, display_name, email, validation, is_admin, created_at
FROM accounts WHERE id = ?`, accountID))
}

func (s *SQLite) accountBySubject(ctx context.Context, subject string) (core.Account, error) {
	return scanAccount(s.db.QueryRowContext(ctx, `
SELECT id, subject, username, display_name, email, validation, is_admin, created_at
FROM accounts WHERE subject = ?`, subject))
}

func (s *SQLite) CreateStoreToken(ctx context.Context, token core.StoreToken) error {
	packagesJSON, err := rawJSON(token.Packages)
	if err != nil {
		return err
	}
	channelsJSON, err := rawJSON(token.Channels)
	if err != nil {
		return err
	}
	permissionsJSON, err := rawJSON(token.Permissions)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO store_tokens (
    session_id, token_hash, account_id, description, packages, channels, permissions,
    valid_since, valid_until, revoked_at, revoked_by
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		token.SessionID, token.TokenHash, token.AccountID, token.Description, string(packagesJSON), string(channelsJSON),
		string(permissionsJSON), token.ValidSince, token.ValidUntil, token.RevokedAt, token.RevokedBy)
	return err
}

func (s *SQLite) ListStoreTokens(ctx context.Context, accountID string, includeInactive bool) ([]core.StoreToken, error) {
	query := `
SELECT session_id, token_hash, account_id, description, packages, channels, permissions,
       valid_since, valid_until, revoked_at, revoked_by
FROM store_tokens
WHERE account_id = ?`
	args := []any{accountID}
	if !includeInactive {
		query += " AND revoked_at IS NULL AND valid_until > ?"
		args = append(args, time.Now().UTC())
	}
	query += " ORDER BY valid_since ASC"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []core.StoreToken
	for rows.Next() {
		token, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func (s *SQLite) RevokeStoreToken(ctx context.Context, accountID, sessionID, revokedBy string) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE store_tokens SET revoked_at = ?, revoked_by = ?
WHERE account_id = ? AND session_id = ?`, time.Now().UTC(), revokedBy, accountID, sessionID)
	return rowsErr(res, err)
}

func (s *SQLite) FindStoreTokenByHash(ctx context.Context, hash string) (core.StoreToken, core.Account, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT t.session_id, t.token_hash, t.account_id, t.description, t.packages, t.channels, t.permissions,
       t.valid_since, t.valid_until, t.revoked_at, t.revoked_by,
       a.id, a.subject, a.username, a.display_name, a.email, a.validation, a.is_admin, a.created_at
FROM store_tokens t
JOIN accounts a ON a.id = t.account_id
WHERE t.token_hash = ?`, hash)
	token, account, err := scanTokenAndAccount(row)
	return token, account, err
}

func (s *SQLite) CreatePackage(ctx context.Context, pkg core.Package) error {
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
	_, err = s.db.ExecContext(ctx, `
INSERT INTO packages (
    id, name, type, private, status, owner_account_id,
    oci_project, oci_push_robot_id, oci_push_robot_name, oci_push_robot_secret,
    oci_pull_robot_id, oci_pull_robot_name, oci_pull_robot_secret, oci_synced_at,
    authority, contact, default_track, description, summary, title, website,
    links, media, track_guardrails, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pkg.ID, pkg.Name, pkg.Type, pkg.Private, pkg.Status, pkg.OwnerAccountID,
		pkg.OCIProject, nullInt64(pkg.OCIPushRobot), robotUsername(pkg.OCIPushRobot), robotSecret(pkg.OCIPushRobot),
		nullInt64(pkg.OCIPullRobot), robotUsername(pkg.OCIPullRobot), robotSecret(pkg.OCIPullRobot), pkg.OCISyncedAt,
		pkg.Authority, pkg.Contact, pkg.DefaultTrack, pkg.Description, pkg.Summary, pkg.Title, pkg.Website,
		string(linksJSON), string(mediaJSON), string(guardrailsJSON), pkg.CreatedAt, pkg.UpdatedAt)
	if isSQLiteConstraint(err) {
		return fmt.Errorf("cannot create package: %w", ErrConflict)
	}
	return err
}

func (s *SQLite) UpdatePackage(ctx context.Context, pkg core.Package) error {
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
	res, err := s.db.ExecContext(ctx, `
UPDATE packages SET private = ?, status = ?, oci_project = ?, oci_push_robot_id = ?,
    oci_push_robot_name = ?, oci_push_robot_secret = ?, oci_pull_robot_id = ?,
    oci_pull_robot_name = ?, oci_pull_robot_secret = ?, oci_synced_at = ?,
    authority = ?, contact = ?, default_track = ?, description = ?, summary = ?,
    title = ?, website = ?, links = ?, media = ?, track_guardrails = ?, updated_at = ?
WHERE id = ?`,
		pkg.Private, pkg.Status, pkg.OCIProject, nullInt64(pkg.OCIPushRobot),
		robotUsername(pkg.OCIPushRobot), robotSecret(pkg.OCIPushRobot), nullInt64(pkg.OCIPullRobot),
		robotUsername(pkg.OCIPullRobot), robotSecret(pkg.OCIPullRobot), pkg.OCISyncedAt,
		pkg.Authority, pkg.Contact, pkg.DefaultTrack, pkg.Description, pkg.Summary, pkg.Title, pkg.Website,
		string(linksJSON), string(mediaJSON), string(guardrailsJSON), pkg.UpdatedAt, pkg.ID)
	return rowsErr(res, err)
}

func (s *SQLite) DeletePackage(ctx context.Context, packageID string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM packages WHERE id = ?", packageID)
	return rowsErr(res, err)
}

func (s *SQLite) GetPackageByName(ctx context.Context, name string) (core.Package, error) {
	return s.scanPackage(s.db.QueryRowContext(ctx, packageSelectSQL()+" WHERE p.name = ?", name))
}

func (s *SQLite) GetPackageByID(ctx context.Context, packageID string) (core.Package, error) {
	return s.scanPackage(s.db.QueryRowContext(ctx, packageSelectSQL()+" WHERE p.id = ?", packageID))
}

func (s *SQLite) ListPackagesForAccount(ctx context.Context, accountID string, includeCollaborations bool) ([]core.Package, error) {
	query := packageSelectSQL() + " WHERE p.owner_account_id = ?"
	if includeCollaborations {
		query += `
   OR EXISTS (
        SELECT 1 FROM package_acl acl
        LEFT JOIN account_group_members gm
          ON acl.principal_type = 'group' AND acl.principal_id = gm.group_id
        WHERE acl.package_id = p.id
          AND ((acl.principal_type = 'account' AND acl.principal_id = ?) OR gm.account_id = ?)
   )`
		return s.listPackages(ctx, query, accountID, accountID, accountID)
	}
	return s.listPackages(ctx, query, accountID)
}

func (s *SQLite) SearchPackages(ctx context.Context, query string) ([]core.Package, error) {
	pattern := "%"
	if trimmed := strings.TrimSpace(query); trimmed != "" {
		pattern = "%" + escapeLikePattern(trimmed) + "%"
	}
	return s.listPackages(ctx, packageSelectSQL()+` WHERE p.name LIKE ? ESCAPE '\' COLLATE NOCASE ORDER BY p.name ASC`, pattern)
}

func (s *SQLite) CanViewPackage(ctx context.Context, packageID, accountID string) (bool, error) {
	return s.canAccess(ctx, packageID, accountID, []string{"viewer", "editor", "admin"})
}

func (s *SQLite) CanManagePackage(ctx context.Context, packageID, accountID string) (bool, error) {
	return s.canAccess(ctx, packageID, accountID, []string{"editor", "admin"})
}

func (s *SQLite) CreateTracks(ctx context.Context, packageID string, tracks []core.Track) (int, error) {
	var created int
	for _, track := range tracks {
		res, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO tracks (package_id, name, version_pattern, automatic_phasing_percentage, created_at)
VALUES (?, ?, ?, ?, ?)`, packageID, track.Name, track.VersionPattern, track.AutomaticPhasingPercentage, track.CreatedAt)
		if err != nil {
			return created, err
		}
		count, _ := res.RowsAffected()
		created += int(count)
	}
	return created, nil
}

func (s *SQLite) DeleteTrack(ctx context.Context, packageID, trackName string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM tracks WHERE package_id = ? AND name = ?", packageID, trackName)
	return rowsErr(res, err)
}

func (s *SQLite) ListTracks(ctx context.Context, packageID string) ([]core.Track, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT name, version_pattern, automatic_phasing_percentage, created_at
FROM tracks WHERE package_id = ? ORDER BY created_at ASC`, packageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTracks(rows)
}

func (s *SQLite) ListTracksForPackages(ctx context.Context, packageIDs []string) (map[string][]core.Track, error) {
	out := make(map[string][]core.Track, len(packageIDs))
	for _, packageID := range packageIDs {
		out[packageID] = nil
	}
	if len(packageIDs) == 0 {
		return out, nil
	}
	query, args := inQuery(`
SELECT package_id, name, version_pattern, automatic_phasing_percentage, created_at
FROM tracks WHERE package_id IN (%s) ORDER BY created_at ASC`, packageIDs)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var packageID string
		track, err := scanTrackWithPackage(rows, &packageID)
		if err != nil {
			return nil, err
		}
		out[packageID] = append(out[packageID], track)
	}
	return out, rows.Err()
}

func (s *SQLite) CreateUpload(ctx context.Context, upload core.Upload) error {
	errorsJSON, err := rawJSON(upload.Errors)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO uploads (id, filename, object_key, size, sha256, sha384, status, kind, created_at, approved_at, revision, errors)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		upload.ID, upload.Filename, upload.ObjectKey, upload.Size, upload.SHA256, upload.SHA384, upload.Status,
		upload.Kind, upload.CreatedAt, upload.ApprovedAt, upload.Revision, string(errorsJSON))
	return err
}

func (s *SQLite) GetUpload(ctx context.Context, uploadID string) (core.Upload, error) {
	return scanUpload(s.db.QueryRowContext(ctx, `
SELECT id, filename, object_key, size, sha256, sha384, status, kind, created_at, approved_at, revision, errors
FROM uploads WHERE id = ?`, uploadID))
}

func (s *SQLite) ApproveUpload(ctx context.Context, uploadID string, revision *int, apiErrors []core.APIError) error {
	status := "approved"
	if len(apiErrors) > 0 {
		status = "rejected"
	}
	errorsJSON, err := rawJSON(apiErrors)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE uploads SET approved_at = ?, revision = ?, errors = ?, status = ? WHERE id = ?`,
		time.Now().UTC(), revision, string(errorsJSON), status, uploadID)
	return rowsErr(res, err)
}

func (s *SQLite) CreateRevision(ctx context.Context, revision core.Revision) error {
	basesJSON, err := rawJSON(revision.Bases)
	if err != nil {
		return err
	}
	attributesJSON, err := rawJSON(revision.Attributes)
	if err != nil {
		return err
	}
	relationsJSON, err := rawJSON(revision.Relations)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO revisions (
    id, package_id, revision, version, status, created_at, created_by, size,
    sha256, sha384, object_key, metadata_yaml, config_yaml, actions_yaml,
    bundle_yaml, readme_md, bases, attributes, relations, subordinate
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		revision.ID, revision.PackageID, revision.Revision, revision.Version, revision.Status, revision.CreatedAt,
		revision.CreatedBy, revision.Size, revision.SHA256, revision.SHA384, revision.ObjectKey, revision.MetadataYAML,
		revision.ConfigYAML, revision.ActionsYAML, revision.BundleYAML, revision.ReadmeMD, string(basesJSON),
		string(attributesJSON), string(relationsJSON), revision.Subordinate)
	return err
}

func (s *SQLite) DeleteRevision(ctx context.Context, packageID string, revision int) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM revisions WHERE package_id = ? AND revision = ?", packageID, revision)
	return rowsErr(res, err)
}

func (s *SQLite) ListRevisions(ctx context.Context, packageID string, revision *int) ([]core.Revision, error) {
	if revision != nil {
		item, err := s.GetRevisionByNumber(ctx, packageID, *revision)
		if errors.Is(err, ErrNotFound) {
			return []core.Revision{}, nil
		}
		if err != nil {
			return nil, err
		}
		return []core.Revision{item}, nil
	}
	rows, err := s.db.QueryContext(ctx, revisionSelectSQL()+" WHERE package_id = ? ORDER BY revision DESC", packageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRevisions(rows)
}

func (s *SQLite) ListRevisionsByNumbers(ctx context.Context, packageID string, revisions []int) (map[int]core.Revision, error) {
	if len(revisions) == 0 {
		return map[int]core.Revision{}, nil
	}
	placeholders := make([]string, len(revisions))
	args := []any{packageID}
	for i, revision := range revisions {
		placeholders[i] = "?"
		args = append(args, revision)
	}
	rows, err := s.db.QueryContext(ctx, revisionSelectSQL()+" WHERE package_id = ? AND revision IN ("+strings.Join(placeholders, ", ")+") ORDER BY revision DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := scanRevisions(rows)
	if err != nil {
		return nil, err
	}
	out := make(map[int]core.Revision, len(items))
	for _, item := range items {
		out[item.Revision] = item
	}
	return out, nil
}

func (s *SQLite) GetRevisionByNumber(ctx context.Context, packageID string, revision int) (core.Revision, error) {
	return scanRevision(s.db.QueryRowContext(ctx, revisionSelectSQL()+" WHERE package_id = ? AND revision = ?", packageID, revision))
}

func (s *SQLite) GetLatestRevision(ctx context.Context, packageID string) (core.Revision, error) {
	return scanRevision(s.db.QueryRowContext(ctx, revisionSelectSQL()+" WHERE package_id = ? ORDER BY revision DESC LIMIT 1", packageID))
}

func (s *SQLite) UpsertResourceDefinition(ctx context.Context, resource core.ResourceDefinition) (core.ResourceDefinition, error) {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO resource_definitions (id, package_id, name, type, description, filename, optional, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(package_id, name) DO UPDATE SET
    type = excluded.type,
    description = excluded.description,
    filename = excluded.filename,
    optional = excluded.optional`,
		resource.ID, resource.PackageID, resource.Name, resource.Type, resource.Description, resource.Filename, resource.Optional, resource.CreatedAt)
	if err != nil {
		return core.ResourceDefinition{}, err
	}
	return s.GetResourceDefinition(ctx, resource.PackageID, resource.Name)
}

func (s *SQLite) GetResourceDefinition(ctx context.Context, packageID, resourceName string) (core.ResourceDefinition, error) {
	return scanResourceDefinition(s.db.QueryRowContext(ctx, `
SELECT id, package_id, name, type, description, filename, optional, created_at
FROM resource_definitions WHERE package_id = ? AND name = ?`, packageID, resourceName))
}

func (s *SQLite) ListResourceDefinitions(ctx context.Context, packageID string) ([]core.ResourceDefinition, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, package_id, name, type, description, filename, optional, created_at
FROM resource_definitions WHERE package_id = ? ORDER BY name ASC`, packageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.ResourceDefinition
	for rows.Next() {
		item, err := scanResourceDefinition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLite) DeleteResourceDefinition(ctx context.Context, resourceID string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM resource_definitions WHERE id = ?", resourceID)
	return rowsErr(res, err)
}

func (s *SQLite) CreateResourceRevision(ctx context.Context, revision core.ResourceRevision) error {
	basesJSON, err := rawJSON(revision.Bases)
	if err != nil {
		return err
	}
	architecturesJSON, err := rawJSON(revision.Architectures)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO resource_revisions (
    id, resource_id, revision, package_revision, name, type, description,
    filename, created_at, size, sha256, sha384, sha512, sha3_384,
    object_key, bases, architectures, oci_image_digest, oci_image_blob
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		revision.ID, revision.ResourceID, revision.Revision, revision.PackageRevision, revision.Name, revision.Type,
		revision.Description, revision.Filename, revision.CreatedAt, revision.Size, revision.SHA256, revision.SHA384,
		revision.SHA512, revision.SHA3384, revision.ObjectKey, string(basesJSON), string(architecturesJSON),
		revision.OCIImageDigest, revision.OCIImageBlob)
	return err
}

func (s *SQLite) DeleteResourceRevision(ctx context.Context, resourceID string, revision int) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM resource_revisions WHERE resource_id = ? AND revision = ?", resourceID, revision)
	return rowsErr(res, err)
}

func (s *SQLite) UpdateResourceRevision(ctx context.Context, revision core.ResourceRevision) error {
	basesJSON, err := rawJSON(revision.Bases)
	if err != nil {
		return err
	}
	architecturesJSON, err := rawJSON(revision.Architectures)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE resource_revisions
SET bases = ?, architectures = ?, oci_image_digest = ?, oci_image_blob = ?
WHERE resource_id = ? AND revision = ? AND id = ?`,
		string(basesJSON), string(architecturesJSON), revision.OCIImageDigest, revision.OCIImageBlob,
		revision.ResourceID, revision.Revision, revision.ID)
	return rowsErr(res, err)
}

func (s *SQLite) ListResourceRevisions(ctx context.Context, resourceID string) ([]core.ResourceRevision, error) {
	rows, err := s.db.QueryContext(ctx, resourceRevisionSelectSQL()+" WHERE resource_id = ? ORDER BY revision DESC", resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanResourceRevisions(rows)
}

func (s *SQLite) GetResourceRevision(ctx context.Context, resourceID string, revision int) (core.ResourceRevision, error) {
	return scanResourceRevision(s.db.QueryRowContext(ctx, resourceRevisionSelectSQL()+" WHERE resource_id = ? AND revision = ?", resourceID, revision))
}

func (s *SQLite) ReplaceRelease(ctx context.Context, packageID string, release core.Release) error {
	baseJSON, err := rawJSON(release.Base)
	if err != nil {
		return err
	}
	resourcesJSON, err := rawJSON(release.Resources)
	if err != nil {
		return err
	}
	baseKey := string(baseJSON)
	_, err = s.db.ExecContext(ctx, `
INSERT INTO releases (
    id, package_id, channel, revision, base, base_key, resources, when_created, expiration_date, progressive
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(package_id, channel, base_key) DO UPDATE SET
    revision = excluded.revision,
    base = excluded.base,
    resources = excluded.resources,
    when_created = excluded.when_created,
    expiration_date = excluded.expiration_date,
    progressive = excluded.progressive`,
		release.ID, packageID, release.Channel, release.Revision, string(baseJSON), baseKey, string(resourcesJSON),
		release.When, release.ExpirationDate, release.Progressive)
	return err
}

func (s *SQLite) DeleteRelease(ctx context.Context, packageID, channel string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM releases WHERE package_id = ? AND channel = ?", packageID, channel)
	return rowsErr(res, err)
}

func (s *SQLite) DeleteReleaseForBase(ctx context.Context, packageID, channel string, base *core.Base) error {
	baseJSON, err := rawJSON(base)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM releases WHERE package_id = ? AND channel = ? AND base_key = ?", packageID, channel, string(baseJSON))
	return rowsErr(res, err)
}

func (s *SQLite) ListReleases(ctx context.Context, packageID string) ([]core.Release, error) {
	rows, err := s.db.QueryContext(ctx, releaseSelectSQL()+" WHERE package_id = ? ORDER BY channel ASC, base_key ASC", packageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReleases(rows)
}

func (s *SQLite) ResolveRelease(ctx context.Context, packageID string, channel string) (core.Release, error) {
	return scanRelease(s.db.QueryRowContext(ctx, releaseSelectSQL()+" WHERE package_id = ? AND channel = ? ORDER BY when_created DESC LIMIT 1", packageID, channel))
}

func (s *SQLite) ResolveReleaseForBase(ctx context.Context, packageID string, channel string, base core.Base) (core.Release, error) {
	baseJSON, err := rawJSON(base)
	if err != nil {
		return core.Release{}, err
	}
	return scanRelease(s.db.QueryRowContext(ctx, releaseSelectSQL()+" WHERE package_id = ? AND channel = ? AND base_key = ?", packageID, channel, string(baseJSON)))
}

func (s *SQLite) ResolveDefaultRelease(ctx context.Context, packageID string) (core.Release, error) {
	release, err := scanRelease(s.db.QueryRowContext(ctx, `
SELECT r.id, r.package_id, r.channel, r.revision, r.base, r.resources, r.when_created, r.expiration_date, r.progressive
FROM packages p
JOIN releases r ON r.package_id = p.id
WHERE p.id = ?
  AND ((p.default_track IS NOT NULL AND r.channel = p.default_track || '/stable')
   OR (p.default_track IS NULL AND r.channel = 'latest/stable'))
ORDER BY r.when_created DESC
LIMIT 1`, packageID))
	if !errors.Is(err, ErrNotFound) {
		return release, err
	}
	return scanRelease(s.db.QueryRowContext(ctx, releaseSelectSQL()+" WHERE package_id = ? ORDER BY when_created DESC LIMIT 1", packageID))
}

func (s *SQLite) CreateCharmhubSyncRule(ctx context.Context, rule core.CharmhubSyncRule) error {
	basesJSON, err := rawJSON(rule.Bases)
	if err != nil {
		return err
	}
	architecturesJSON, err := rawJSON(rule.Architectures)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO charmhub_sync_rules (
    package_name, track, bases, architectures, created_by_account_id, created_at, updated_at,
    last_sync_status, last_sync_started_at, last_sync_finished_at, last_sync_error
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rule.PackageName, rule.Track, string(basesJSON), string(architecturesJSON), rule.CreatedByAccountID,
		rule.CreatedAt, rule.UpdatedAt, rule.LastSyncStatus, rule.LastSyncStartedAt, rule.LastSyncFinishedAt, rule.LastSyncError)
	if isSQLiteConstraint(err) {
		return ErrConflict
	}
	return err
}

func (s *SQLite) DeleteCharmhubSyncRule(ctx context.Context, packageName, track string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM charmhub_sync_rules WHERE package_name = ? AND track = ?", packageName, track)
	return rowsErr(res, err)
}

func (s *SQLite) ListCharmhubSyncRules(ctx context.Context) ([]core.CharmhubSyncRule, error) {
	rows, err := s.db.QueryContext(ctx, charmhubRuleSelectSQL()+" ORDER BY package_name ASC, track ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCharmhubRules(rows)
}

func (s *SQLite) ListCharmhubSyncRulesByPackageName(ctx context.Context, packageName string) ([]core.CharmhubSyncRule, error) {
	rows, err := s.db.QueryContext(ctx, charmhubRuleSelectSQL()+" WHERE package_name = ? ORDER BY track ASC", packageName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCharmhubRules(rows)
}

func (s *SQLite) UpdateCharmhubSyncRule(ctx context.Context, rule core.CharmhubSyncRule) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE charmhub_sync_rules
SET updated_at = ?, last_sync_status = ?, last_sync_started_at = ?, last_sync_finished_at = ?, last_sync_error = ?
WHERE package_name = ? AND track = ?`,
		rule.UpdatedAt, rule.LastSyncStatus, rule.LastSyncStartedAt, rule.LastSyncFinishedAt, rule.LastSyncError,
		rule.PackageName, rule.Track)
	return rowsErr(res, err)
}

func packageSelectSQL() string {
	return `
SELECT p.id, p.name, p.type, p.private, p.status, p.owner_account_id,
       p.oci_project, p.oci_push_robot_id, p.oci_push_robot_name, p.oci_push_robot_secret,
       p.oci_pull_robot_id, p.oci_pull_robot_name, p.oci_pull_robot_secret, p.oci_synced_at,
       p.authority, p.contact, p.default_track, p.description, p.summary, p.title, p.website,
       p.links, p.media, p.track_guardrails, p.created_at, p.updated_at,
       a.id, a.username, a.display_name, a.email, a.validation
FROM packages p
JOIN accounts a ON a.id = p.owner_account_id`
}

func revisionSelectSQL() string {
	return `
SELECT id, package_id, revision, version, status, created_at, created_by, size,
       sha256, sha384, object_key, metadata_yaml, config_yaml, actions_yaml,
       bundle_yaml, readme_md, bases, attributes, relations, subordinate
FROM revisions`
}

func resourceRevisionSelectSQL() string {
	return `
SELECT id, resource_id, revision, package_revision, name, type, description,
       filename, created_at, size, sha256, sha384, sha512, sha3_384,
       object_key, bases, architectures, oci_image_digest, oci_image_blob
FROM resource_revisions`
}

func releaseSelectSQL() string {
	return `
SELECT id, package_id, channel, revision, base, resources, when_created, expiration_date, progressive
FROM releases`
}

func charmhubRuleSelectSQL() string {
	return `
SELECT package_name, track, bases, architectures, created_by_account_id, created_at, updated_at,
       last_sync_status, last_sync_started_at, last_sync_finished_at, last_sync_error
FROM charmhub_sync_rules`
}

func (s *SQLite) listPackages(ctx context.Context, query string, args ...any) ([]core.Package, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var packages []core.Package
	for rows.Next() {
		pkg, err := s.scanPackage(rows)
		if err != nil {
			return nil, err
		}
		packages = append(packages, pkg)
	}
	return packages, rows.Err()
}

func (s *SQLite) scanPackage(scanner interface{ Scan(dest ...any) error }) (core.Package, error) {
	var (
		pkg                                        core.Package
		pushID, pullID                             sql.NullInt64
		pushName, pushSecret, pullName, pullSecret string
		ociSyncedAt                                sql.NullTime
		authority, contact, defaultTrack           sql.NullString
		description, summary, title, website       sql.NullString
		linksJSON, mediaJSON, trackGuardrailsJSON  string
	)
	err := scanner.Scan(
		&pkg.ID, &pkg.Name, &pkg.Type, &pkg.Private, &pkg.Status, &pkg.OwnerAccountID,
		&pkg.OCIProject, &pushID, &pushName, &pushSecret, &pullID, &pullName, &pullSecret, &ociSyncedAt,
		&authority, &contact, &defaultTrack, &description, &summary, &title, &website,
		&linksJSON, &mediaJSON, &trackGuardrailsJSON, &pkg.CreatedAt, &pkg.UpdatedAt,
		&pkg.Publisher.ID, &pkg.Publisher.Username, &pkg.Publisher.DisplayName, &pkg.Publisher.Email, &pkg.Publisher.Validation,
	)
	if sqlNotFound(err) {
		return core.Package{}, ErrNotFound
	}
	if err != nil {
		return core.Package{}, err
	}
	pkg.OCIPushRobot, err = robotFromSQLC(nullInt64Ptr(pushID), pushName, pushSecret)
	if err != nil {
		return core.Package{}, fmt.Errorf("load OCI push robot: %w", err)
	}
	pkg.OCIPullRobot, err = robotFromSQLC(nullInt64Ptr(pullID), pullName, pullSecret)
	if err != nil {
		return core.Package{}, fmt.Errorf("load OCI pull robot: %w", err)
	}
	pkg.OCISyncedAt = nullTimePtr(ociSyncedAt)
	pkg.Authority = nullStringPtr(authority)
	pkg.Contact = nullStringPtr(contact)
	pkg.DefaultTrack = nullStringPtr(defaultTrack)
	pkg.Description = nullStringPtr(description)
	pkg.Summary = nullStringPtr(summary)
	pkg.Title = nullStringPtr(title)
	pkg.Website = nullStringPtr(website)
	if err := unmarshalJSON([]byte(linksJSON), &pkg.Links); err != nil {
		return core.Package{}, err
	}
	if err := unmarshalJSON([]byte(mediaJSON), &pkg.Media); err != nil {
		return core.Package{}, err
	}
	if err := unmarshalJSON([]byte(trackGuardrailsJSON), &pkg.TrackGuardrails); err != nil {
		return core.Package{}, err
	}
	return pkg, nil
}

func (s *SQLite) canAccess(ctx context.Context, packageID, accountID string, roles []string) (bool, error) {
	var private bool
	var ownerAccountID string
	if err := s.db.QueryRowContext(ctx, "SELECT private, owner_account_id FROM packages WHERE id = ?", packageID).Scan(&private, &ownerAccountID); err != nil {
		if sqlNotFound(err) {
			return false, ErrNotFound
		}
		return false, err
	}
	if !private || ownerAccountID == accountID {
		return true, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT acl.role
FROM package_acl acl
LEFT JOIN account_group_members gm
  ON acl.principal_type = 'group' AND acl.principal_id = gm.group_id
WHERE acl.package_id = ?
  AND ((acl.principal_type = 'account' AND acl.principal_id = ?) OR gm.account_id = ?)`, packageID, accountID, accountID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return false, err
		}
		if rolesContain(roles, role) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func scanUpload(scanner interface{ Scan(dest ...any) error }) (core.Upload, error) {
	var (
		upload     core.Upload
		approvedAt sql.NullTime
		revision   sql.NullInt64
		errorsJSON string
	)
	err := scanner.Scan(&upload.ID, &upload.Filename, &upload.ObjectKey, &upload.Size, &upload.SHA256, &upload.SHA384, &upload.Status, &upload.Kind, &upload.CreatedAt, &approvedAt, &revision, &errorsJSON)
	if sqlNotFound(err) {
		return core.Upload{}, ErrNotFound
	}
	if err != nil {
		return core.Upload{}, err
	}
	upload.ApprovedAt = nullTimePtr(approvedAt)
	if revision.Valid {
		value := int(revision.Int64)
		upload.Revision = &value
	}
	if err := unmarshalJSON([]byte(errorsJSON), &upload.Errors); err != nil {
		return core.Upload{}, err
	}
	return upload, nil
}

func scanRevision(scanner interface{ Scan(dest ...any) error }) (core.Revision, error) {
	var (
		revision                                 core.Revision
		basesJSON, attributesJSON, relationsJSON string
	)
	err := scanner.Scan(&revision.ID, &revision.PackageID, &revision.Revision, &revision.Version, &revision.Status, &revision.CreatedAt, &revision.CreatedBy, &revision.Size, &revision.SHA256, &revision.SHA384, &revision.ObjectKey, &revision.MetadataYAML, &revision.ConfigYAML, &revision.ActionsYAML, &revision.BundleYAML, &revision.ReadmeMD, &basesJSON, &attributesJSON, &relationsJSON, &revision.Subordinate)
	if sqlNotFound(err) {
		return core.Revision{}, ErrNotFound
	}
	if err != nil {
		return core.Revision{}, err
	}
	if err := unmarshalJSON([]byte(basesJSON), &revision.Bases); err != nil {
		return core.Revision{}, err
	}
	if err := unmarshalJSON([]byte(attributesJSON), &revision.Attributes); err != nil {
		return core.Revision{}, err
	}
	if err := unmarshalJSON([]byte(relationsJSON), &revision.Relations); err != nil {
		return core.Revision{}, err
	}
	return revision, nil
}

func scanRevisions(rows *sql.Rows) ([]core.Revision, error) {
	var out []core.Revision
	for rows.Next() {
		item, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanResourceDefinition(scanner interface{ Scan(dest ...any) error }) (core.ResourceDefinition, error) {
	var item core.ResourceDefinition
	err := scanner.Scan(&item.ID, &item.PackageID, &item.Name, &item.Type, &item.Description, &item.Filename, &item.Optional, &item.CreatedAt)
	if sqlNotFound(err) {
		return core.ResourceDefinition{}, ErrNotFound
	}
	return item, err
}

func scanResourceRevision(scanner interface{ Scan(dest ...any) error }) (core.ResourceRevision, error) {
	var (
		revision                     core.ResourceRevision
		packageRevision              sql.NullInt64
		basesJSON, architecturesJSON string
	)
	err := scanner.Scan(&revision.ID, &revision.ResourceID, &revision.Revision, &packageRevision, &revision.Name, &revision.Type, &revision.Description, &revision.Filename, &revision.CreatedAt, &revision.Size, &revision.SHA256, &revision.SHA384, &revision.SHA512, &revision.SHA3384, &revision.ObjectKey, &basesJSON, &architecturesJSON, &revision.OCIImageDigest, &revision.OCIImageBlob)
	if sqlNotFound(err) {
		return core.ResourceRevision{}, ErrNotFound
	}
	if err != nil {
		return core.ResourceRevision{}, err
	}
	if packageRevision.Valid {
		value := int(packageRevision.Int64)
		revision.PackageRevision = &value
	}
	if err := unmarshalJSON([]byte(basesJSON), &revision.Bases); err != nil {
		return core.ResourceRevision{}, err
	}
	if err := unmarshalJSON([]byte(architecturesJSON), &revision.Architectures); err != nil {
		return core.ResourceRevision{}, err
	}
	return revision, nil
}

func scanResourceRevisions(rows *sql.Rows) ([]core.ResourceRevision, error) {
	var out []core.ResourceRevision
	for rows.Next() {
		item, err := scanResourceRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanRelease(scanner interface{ Scan(dest ...any) error }) (core.Release, error) {
	var (
		release                 core.Release
		baseJSON, resourcesJSON string
		expirationDate          sql.NullTime
		progressive             sql.NullFloat64
	)
	err := scanner.Scan(&release.ID, &release.PackageID, &release.Channel, &release.Revision, &baseJSON, &resourcesJSON, &release.When, &expirationDate, &progressive)
	if sqlNotFound(err) {
		return core.Release{}, ErrNotFound
	}
	if err != nil {
		return core.Release{}, err
	}
	if baseJSON != "" && baseJSON != "null" {
		var base core.Base
		if err := unmarshalJSON([]byte(baseJSON), &base); err != nil {
			return core.Release{}, err
		}
		release.Base = &base
	}
	if err := unmarshalJSON([]byte(resourcesJSON), &release.Resources); err != nil {
		return core.Release{}, err
	}
	release.ExpirationDate = nullTimePtr(expirationDate)
	release.Progressive = nullFloat64Ptr(progressive)
	return release, nil
}

func scanReleases(rows *sql.Rows) ([]core.Release, error) {
	var out []core.Release
	for rows.Next() {
		item, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanCharmhubRule(scanner interface{ Scan(dest ...any) error }) (core.CharmhubSyncRule, error) {
	var (
		rule                core.CharmhubSyncRule
		basesJSON, archJSON string
		started, finished   sql.NullTime
		lastError           sql.NullString
	)
	err := scanner.Scan(&rule.PackageName, &rule.Track, &basesJSON, &archJSON, &rule.CreatedByAccountID, &rule.CreatedAt, &rule.UpdatedAt, &rule.LastSyncStatus, &started, &finished, &lastError)
	if sqlNotFound(err) {
		return core.CharmhubSyncRule{}, ErrNotFound
	}
	if err != nil {
		return core.CharmhubSyncRule{}, err
	}
	if err := unmarshalJSON([]byte(basesJSON), &rule.Bases); err != nil {
		return core.CharmhubSyncRule{}, err
	}
	if err := unmarshalJSON([]byte(archJSON), &rule.Architectures); err != nil {
		return core.CharmhubSyncRule{}, err
	}
	rule.LastSyncStartedAt = nullTimePtr(started)
	rule.LastSyncFinishedAt = nullTimePtr(finished)
	rule.LastSyncError = nullStringPtr(lastError)
	return rule, nil
}

func scanCharmhubRules(rows *sql.Rows) ([]core.CharmhubSyncRule, error) {
	var out []core.CharmhubSyncRule
	for rows.Next() {
		item, err := scanCharmhubRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func rowsErr(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func sqlNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

func isSQLiteConstraint(err error) bool {
	return err != nil && strings.Contains(err.Error(), "constraint failed")
}

func inQuery(format string, values []string) (string, []any) {
	placeholders := make([]string, len(values))
	args := make([]any, len(values))
	for i, value := range values {
		placeholders[i] = "?"
		args[i] = value
	}
	return fmt.Sprintf(format, strings.Join(placeholders, ", ")), args
}

func scanAccount(scanner interface{ Scan(dest ...any) error }) (core.Account, error) {
	var account core.Account
	err := scanner.Scan(&account.ID, &account.Subject, &account.Username, &account.DisplayName, &account.Email, &account.Validation, &account.IsAdmin, &account.CreatedAt)
	if sqlNotFound(err) {
		return core.Account{}, ErrNotFound
	}
	return account, err
}

func scanToken(scanner interface{ Scan(dest ...any) error }) (core.StoreToken, error) {
	var (
		token                           core.StoreToken
		description, revokedBy          sql.NullString
		packages, channels, permissions string
		revokedAt                       sql.NullTime
	)
	if err := scanner.Scan(&token.SessionID, &token.TokenHash, &token.AccountID, &description, &packages, &channels, &permissions, &token.ValidSince, &token.ValidUntil, &revokedAt, &revokedBy); err != nil {
		if sqlNotFound(err) {
			return core.StoreToken{}, ErrNotFound
		}
		return core.StoreToken{}, err
	}
	token.Description = nullStringPtr(description)
	token.RevokedBy = nullStringPtr(revokedBy)
	if revokedAt.Valid {
		token.RevokedAt = &revokedAt.Time
	}
	if err := unmarshalJSON([]byte(packages), &token.Packages); err != nil {
		return core.StoreToken{}, err
	}
	if err := unmarshalJSON([]byte(channels), &token.Channels); err != nil {
		return core.StoreToken{}, err
	}
	if err := unmarshalJSON([]byte(permissions), &token.Permissions); err != nil {
		return core.StoreToken{}, err
	}
	return token, nil
}

func scanTokenAndAccount(scanner interface{ Scan(dest ...any) error }) (core.StoreToken, core.Account, error) {
	var (
		token                           core.StoreToken
		account                         core.Account
		description, revokedBy          sql.NullString
		packages, channels, permissions string
		revokedAt                       sql.NullTime
	)
	err := scanner.Scan(&token.SessionID, &token.TokenHash, &token.AccountID, &description, &packages, &channels, &permissions, &token.ValidSince, &token.ValidUntil, &revokedAt, &revokedBy, &account.ID, &account.Subject, &account.Username, &account.DisplayName, &account.Email, &account.Validation, &account.IsAdmin, &account.CreatedAt)
	if sqlNotFound(err) {
		return core.StoreToken{}, core.Account{}, ErrNotFound
	}
	if err != nil {
		return core.StoreToken{}, core.Account{}, err
	}
	token.Description = nullStringPtr(description)
	token.RevokedBy = nullStringPtr(revokedBy)
	if revokedAt.Valid {
		token.RevokedAt = &revokedAt.Time
	}
	if err := unmarshalJSON([]byte(packages), &token.Packages); err != nil {
		return core.StoreToken{}, core.Account{}, err
	}
	if err := unmarshalJSON([]byte(channels), &token.Channels); err != nil {
		return core.StoreToken{}, core.Account{}, err
	}
	if err := unmarshalJSON([]byte(permissions), &token.Permissions); err != nil {
		return core.StoreToken{}, core.Account{}, err
	}
	return token, account, nil
}

func scanTracks(rows *sql.Rows) ([]core.Track, error) {
	var tracks []core.Track
	for rows.Next() {
		var track core.Track
		if err := rows.Scan(&track.Name, &track.VersionPattern, &track.AutomaticPhasingPercentage, &track.CreatedAt); err != nil {
			return nil, err
		}
		tracks = append(tracks, track)
	}
	return tracks, rows.Err()
}

func scanTrackWithPackage(scanner interface{ Scan(dest ...any) error }, packageID *string) (core.Track, error) {
	var track core.Track
	err := scanner.Scan(packageID, &track.Name, &track.VersionPattern, &track.AutomaticPhasingPercentage, &track.CreatedAt)
	return track, err
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func nullFloat64Ptr(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func rolesContain(roles []string, role string) bool {
	return slices.Contains(roles, role)
}
