package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/gschiano/charm-registry/internal/core"
)

// CreateUpload is part of the [Repository] interface.
func (p *Postgres) CreateUpload(ctx context.Context, upload core.Upload) error {
	errorsJSON, err := marshalJSON(upload.Errors)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(
		ctx,
		`
		INSERT INTO uploads (
			id, filename, object_key, size, sha256, sha384, status, kind,
			created_at, approved_at, revision, errors
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`,
		upload.ID,
		upload.Filename,
		upload.ObjectKey,
		upload.Size,
		upload.SHA256,
		upload.SHA384,
		upload.Status,
		upload.Kind,
		upload.CreatedAt,
		upload.ApprovedAt,
		upload.Revision,
		errorsJSON,
	)
	return err
}

// GetUpload is part of the [Repository] interface.
func (p *Postgres) GetUpload(ctx context.Context, uploadID string) (core.Upload, error) {
	row := p.db.QueryRow(ctx, `
		SELECT id, filename, object_key, size, sha256, sha384, status, kind, created_at, approved_at, revision, errors
		FROM uploads WHERE id = $1
	`, uploadID)
	var upload core.Upload
	var errorsJSON []byte
	err := row.Scan(
		&upload.ID,
		&upload.Filename,
		&upload.ObjectKey,
		&upload.Size,
		&upload.SHA256,
		&upload.SHA384,
		&upload.Status,
		&upload.Kind,
		&upload.CreatedAt,
		&upload.ApprovedAt,
		&upload.Revision,
		&errorsJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Upload{}, ErrNotFound
	}
	if err != nil {
		return core.Upload{}, err
	}
	if err := unmarshalJSON(errorsJSON, &upload.Errors); err != nil {
		return core.Upload{}, fmt.Errorf("unmarshal upload errors: %w", err)
	}
	return upload, nil
}

// ApproveUpload is part of the [Repository] interface.
func (p *Postgres) ApproveUpload(ctx context.Context, uploadID string, revision *int, apiErrors []core.APIError) error {
	status := "approved"
	if len(apiErrors) > 0 {
		status = "rejected"
	}
	errorsJSON, err := marshalJSON(apiErrors)
	if err != nil {
		return err
	}
	tag, err := p.db.Exec(ctx, `
		UPDATE uploads SET approved_at = NOW(), revision = $2, errors = $3, status = $4
		WHERE id = $1
	`, uploadID, revision, errorsJSON, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateRevision is part of the [Repository] interface.
func (p *Postgres) CreateRevision(ctx context.Context, revision core.Revision) error {
	basesJSON, err := marshalJSON(revision.Bases)
	if err != nil {
		return err
	}
	attributesJSON, err := marshalJSON(revision.Attributes)
	if err != nil {
		return err
	}
	relationsJSON, err := marshalJSON(revision.Relations)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(
		ctx,
		`
		INSERT INTO revisions (
			id, package_id, revision, version, status, created_at, created_by, size,
			sha256, sha384, object_key, metadata_yaml, config_yaml, actions_yaml,
			bundle_yaml, readme_md, bases, attributes, relations, subordinate
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
	`,
		revision.ID,
		revision.PackageID,
		revision.Revision,
		revision.Version,
		revision.Status,
		revision.CreatedAt,
		revision.CreatedBy,
		revision.Size,
		revision.SHA256,
		revision.SHA384,
		revision.ObjectKey,
		revision.MetadataYAML,
		revision.ConfigYAML,
		revision.ActionsYAML,
		revision.BundleYAML,
		revision.ReadmeMD,
		basesJSON,
		attributesJSON,
		relationsJSON,
		revision.Subordinate,
	)
	return err
}

// ListRevisions is part of the [Repository] interface.
func (p *Postgres) ListRevisions(ctx context.Context, packageID string, revision *int) ([]core.Revision, error) {
	query := `
		SELECT id, package_id, revision, version, status, created_at, created_by, size,
		       sha256, sha384, object_key, metadata_yaml, config_yaml, actions_yaml,
		       bundle_yaml, readme_md, bases, attributes, relations, subordinate
		FROM revisions WHERE package_id = $1
	`
	args := []any{packageID}
	if revision != nil {
		query += ` AND revision = $2`
		args = append(args, *revision)
	}
	query += ` ORDER BY revision DESC`
	rows, err := p.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRevisions(rows)
}

// GetRevisionByNumber is part of the [Repository] interface.
func (p *Postgres) GetRevisionByNumber(ctx context.Context, packageID string, revision int) (core.Revision, error) {
	rows, err := p.db.Query(ctx, `
		SELECT id, package_id, revision, version, status, created_at, created_by, size,
		       sha256, sha384, object_key, metadata_yaml, config_yaml, actions_yaml,
		       bundle_yaml, readme_md, bases, attributes, relations, subordinate
		FROM revisions WHERE package_id = $1 AND revision = $2
	`, packageID, revision)
	if err != nil {
		return core.Revision{}, err
	}
	defer rows.Close()
	items, err := scanRevisions(rows)
	if err != nil {
		return core.Revision{}, err
	}
	if len(items) == 0 {
		return core.Revision{}, ErrNotFound
	}
	return items[0], nil
}

// GetLatestRevision is part of the [Repository] interface.
func (p *Postgres) GetLatestRevision(ctx context.Context, packageID string) (core.Revision, error) {
	rows, err := p.ListRevisions(ctx, packageID, nil)
	if err != nil {
		return core.Revision{}, err
	}
	if len(rows) == 0 {
		return core.Revision{}, ErrNotFound
	}
	return rows[0], nil
}

func scanRevisions(rows pgx.Rows) ([]core.Revision, error) {
	var out []core.Revision
	for rows.Next() {
		var item core.Revision
		var basesJSON []byte
		var attributesJSON []byte
		var relationsJSON []byte
		if err := rows.Scan(
			&item.ID,
			&item.PackageID,
			&item.Revision,
			&item.Version,
			&item.Status,
			&item.CreatedAt,
			&item.CreatedBy,
			&item.Size,
			&item.SHA256,
			&item.SHA384,
			&item.ObjectKey,
			&item.MetadataYAML,
			&item.ConfigYAML,
			&item.ActionsYAML,
			&item.BundleYAML,
			&item.ReadmeMD,
			&basesJSON,
			&attributesJSON,
			&relationsJSON,
			&item.Subordinate,
		); err != nil {
			return nil, err
		}
		if err := unmarshalJSON(basesJSON, &item.Bases); err != nil {
			return nil, fmt.Errorf("unmarshal revision bases: %w", err)
		}
		if err := unmarshalJSON(attributesJSON, &item.Attributes); err != nil {
			return nil, fmt.Errorf("unmarshal revision attributes: %w", err)
		}
		if err := unmarshalJSON(relationsJSON, &item.Relations); err != nil {
			return nil, fmt.Errorf("unmarshal revision relations: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
