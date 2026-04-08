package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/gschiano/charm-registry/internal/core"
)

// UpsertResourceDefinition is part of the [Repository] interface.
func (p *Postgres) UpsertResourceDefinition(
	ctx context.Context,
	resource core.ResourceDefinition,
) (core.ResourceDefinition, error) {
	row := p.db.QueryRow(
		ctx,
		`
		INSERT INTO resource_definitions (id, package_id, name, type, description, filename, optional, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (package_id, name) DO UPDATE SET
			type = EXCLUDED.type,
			description = EXCLUDED.description,
			filename = EXCLUDED.filename,
			optional = EXCLUDED.optional
		RETURNING id, package_id, name, type, description, filename, optional, created_at
	`,
		resource.ID,
		resource.PackageID,
		resource.Name,
		resource.Type,
		resource.Description,
		resource.Filename,
		resource.Optional,
		resource.CreatedAt,
	)
	var stored core.ResourceDefinition
	err := row.Scan(
		&stored.ID,
		&stored.PackageID,
		&stored.Name,
		&stored.Type,
		&stored.Description,
		&stored.Filename,
		&stored.Optional,
		&stored.CreatedAt,
	)
	return stored, err
}

// GetResourceDefinition is part of the [Repository] interface.
func (p *Postgres) GetResourceDefinition(
	ctx context.Context,
	packageID, resourceName string,
) (core.ResourceDefinition, error) {
	row := p.db.QueryRow(ctx, `
		SELECT id, package_id, name, type, description, filename, optional, created_at
		FROM resource_definitions WHERE package_id = $1 AND name = $2
	`, packageID, resourceName)
	var resource core.ResourceDefinition
	err := row.Scan(
		&resource.ID,
		&resource.PackageID,
		&resource.Name,
		&resource.Type,
		&resource.Description,
		&resource.Filename,
		&resource.Optional,
		&resource.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.ResourceDefinition{}, ErrNotFound
	}
	return resource, err
}

// ListResourceDefinitions is part of the [Repository] interface.
func (p *Postgres) ListResourceDefinitions(ctx context.Context, packageID string) ([]core.ResourceDefinition, error) {
	rows, err := p.db.Query(ctx, `
		SELECT id, package_id, name, type, description, filename, optional, created_at
		FROM resource_definitions WHERE package_id = $1 ORDER BY name ASC
	`, packageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.ResourceDefinition
	for rows.Next() {
		var resource core.ResourceDefinition
		if err := rows.Scan(
			&resource.ID,
			&resource.PackageID,
			&resource.Name,
			&resource.Type,
			&resource.Description,
			&resource.Filename,
			&resource.Optional,
			&resource.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, resource)
	}
	return out, rows.Err()
}

// CreateResourceRevision is part of the [Repository] interface.
func (p *Postgres) CreateResourceRevision(ctx context.Context, revision core.ResourceRevision) error {
	basesJSON, err := marshalJSON(revision.Bases)
	if err != nil {
		return err
	}
	architecturesJSON, err := marshalJSON(revision.Architectures)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(
		ctx,
		`
		INSERT INTO resource_revisions (
			id, resource_id, revision, package_revision, name, type, description, filename, created_at, size,
			sha256, sha384, sha512, sha3_384, object_key, bases, architectures, oci_image_digest, oci_image_blob
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
	`,
		revision.ID,
		revision.ResourceID,
		revision.Revision,
		revision.PackageRevision,
		revision.Name,
		revision.Type,
		revision.Description,
		revision.Filename,
		revision.CreatedAt,
		revision.Size,
		revision.SHA256,
		revision.SHA384,
		revision.SHA512,
		revision.SHA3384,
		revision.ObjectKey,
		basesJSON,
		architecturesJSON,
		revision.OCIImageDigest,
		revision.OCIImageBlob,
	)
	return err
}

// UpdateResourceRevision is part of the [Repository] interface.
func (p *Postgres) UpdateResourceRevision(ctx context.Context, revision core.ResourceRevision) error {
	basesJSON, err := marshalJSON(revision.Bases)
	if err != nil {
		return err
	}
	architecturesJSON, err := marshalJSON(revision.Architectures)
	if err != nil {
		return err
	}
	tag, err := p.db.Exec(
		ctx,
		`
		UPDATE resource_revisions
		SET bases = $4, architectures = $5, oci_image_digest = $6,
		    oci_image_blob = $7
		WHERE resource_id = $1 AND revision = $2 AND id = $3
	`,
		revision.ResourceID,
		revision.Revision,
		revision.ID,
		basesJSON,
		architecturesJSON,
		revision.OCIImageDigest,
		revision.OCIImageBlob,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListResourceRevisions is part of the [Repository] interface.
func (p *Postgres) ListResourceRevisions(ctx context.Context, resourceID string) ([]core.ResourceRevision, error) {
	rows, err := p.db.Query(ctx, `
		SELECT id, resource_id, revision, package_revision, name, type, description, filename, created_at, size,
		       sha256, sha384, sha512, sha3_384, object_key, bases, architectures, oci_image_digest, oci_image_blob
		FROM resource_revisions WHERE resource_id = $1 ORDER BY revision DESC
	`, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanResourceRevisions(rows)
}

// GetResourceRevision is part of the [Repository] interface.
func (p *Postgres) GetResourceRevision(
	ctx context.Context,
	resourceID string,
	revision int,
) (core.ResourceRevision, error) {
	rows, err := p.db.Query(ctx, `
		SELECT id, resource_id, revision, package_revision, name, type, description, filename, created_at, size,
		       sha256, sha384, sha512, sha3_384, object_key, bases, architectures, oci_image_digest, oci_image_blob
		FROM resource_revisions WHERE resource_id = $1 AND revision = $2
	`, resourceID, revision)
	if err != nil {
		return core.ResourceRevision{}, err
	}
	defer rows.Close()
	items, err := scanResourceRevisions(rows)
	if err != nil {
		return core.ResourceRevision{}, err
	}
	if len(items) == 0 {
		return core.ResourceRevision{}, ErrNotFound
	}
	return items[0], nil
}

func scanResourceRevisions(rows pgx.Rows) ([]core.ResourceRevision, error) {
	var out []core.ResourceRevision
	for rows.Next() {
		var item core.ResourceRevision
		var basesJSON []byte
		var archJSON []byte
		if err := rows.Scan(
			&item.ID, &item.ResourceID, &item.Revision, &item.PackageRevision, &item.Name, &item.Type,
			&item.Description, &item.Filename,
			&item.CreatedAt, &item.Size, &item.SHA256, &item.SHA384, &item.SHA512, &item.SHA3384, &item.ObjectKey,
			&basesJSON, &archJSON, &item.OCIImageDigest, &item.OCIImageBlob,
		); err != nil {
			return nil, err
		}
		if err := unmarshalJSON(basesJSON, &item.Bases); err != nil {
			return nil, fmt.Errorf("unmarshal resource revision bases: %w", err)
		}
		if err := unmarshalJSON(archJSON, &item.Architectures); err != nil {
			return nil, fmt.Errorf("unmarshal resource revision architectures: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
