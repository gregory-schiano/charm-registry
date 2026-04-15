package repo

import (
	"context"

	"github.com/gschiano/charm-registry/internal/core"
	sqlcdb "github.com/gschiano/charm-registry/internal/repo/db"
)

// UpsertResourceDefinition is part of the [Repository] interface.
func (p *Postgres) UpsertResourceDefinition(
	ctx context.Context,
	resource core.ResourceDefinition,
) (core.ResourceDefinition, error) {
	item, err := p.queries().UpsertResourceDefinition(ctx, sqlcdb.UpsertResourceDefinitionParams{
		ID:          resource.ID,
		PackageID:   resource.PackageID,
		Name:        resource.Name,
		Type:        resource.Type,
		Description: resource.Description,
		Filename:    resource.Filename,
		Optional:    resource.Optional,
		CreatedAt:   resource.CreatedAt,
	})
	if err != nil {
		return core.ResourceDefinition{}, err
	}
	return resourceDefinitionFromSQLC(item), nil
}

// GetResourceDefinition is part of the [Repository] interface.
func (p *Postgres) GetResourceDefinition(
	ctx context.Context,
	packageID, resourceName string,
) (core.ResourceDefinition, error) {
	item, err := p.queries().GetResourceDefinition(ctx, sqlcdb.GetResourceDefinitionParams{
		PackageID: packageID,
		Name:      resourceName,
	})
	if pgxNotFound(err) {
		return core.ResourceDefinition{}, ErrNotFound
	}
	if err != nil {
		return core.ResourceDefinition{}, err
	}
	return resourceDefinitionFromSQLC(item), nil
}

// ListResourceDefinitions is part of the [Repository] interface.
func (p *Postgres) ListResourceDefinitions(ctx context.Context, packageID string) ([]core.ResourceDefinition, error) {
	rows, err := p.queries().ListResourceDefinitions(ctx, packageID)
	if err != nil {
		return nil, err
	}
	out := make([]core.ResourceDefinition, 0, len(rows))
	for _, row := range rows {
		out = append(out, resourceDefinitionFromSQLC(row))
	}
	return out, nil
}

// DeleteResourceDefinition is part of the [Repository] interface.
func (p *Postgres) DeleteResourceDefinition(ctx context.Context, resourceID string) error {
	rowsAffected, err := p.queries().DeleteResourceDefinition(ctx, resourceID)
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateResourceRevision is part of the [Repository] interface.
func (p *Postgres) CreateResourceRevision(ctx context.Context, revision core.ResourceRevision) error {
	basesJSON, err := rawJSON(revision.Bases)
	if err != nil {
		return err
	}
	architecturesJSON, err := rawJSON(revision.Architectures)
	if err != nil {
		return err
	}
	revisionNumber, err := toInt32(revision.Revision)
	if err != nil {
		return err
	}
	packageRevision, err := int32Ptr(revision.PackageRevision)
	if err != nil {
		return err
	}
	return p.queries().CreateResourceRevision(ctx, sqlcdb.CreateResourceRevisionParams{
		ID:              revision.ID,
		ResourceID:      revision.ResourceID,
		Revision:        revisionNumber,
		PackageRevision: packageRevision,
		Name:            revision.Name,
		Type:            revision.Type,
		Description:     revision.Description,
		Filename:        revision.Filename,
		CreatedAt:       revision.CreatedAt,
		Size:            revision.Size,
		Sha256:          revision.SHA256,
		Sha384:          revision.SHA384,
		Sha512:          revision.SHA512,
		Sha3384:         revision.SHA3384,
		ObjectKey:       revision.ObjectKey,
		Bases:           basesJSON,
		Architectures:   architecturesJSON,
		OciImageDigest:  revision.OCIImageDigest,
		OciImageBlob:    revision.OCIImageBlob,
	})
}

// DeleteResourceRevision is part of the [Repository] interface.
func (p *Postgres) DeleteResourceRevision(ctx context.Context, resourceID string, revision int) error {
	revisionNumber, err := toInt32(revision)
	if err != nil {
		return err
	}
	rowsAffected, err := p.queries().DeleteResourceRevision(ctx, sqlcdb.DeleteResourceRevisionParams{
		ResourceID: resourceID,
		Revision:   revisionNumber,
	})
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateResourceRevision is part of the [Repository] interface.
func (p *Postgres) UpdateResourceRevision(ctx context.Context, revision core.ResourceRevision) error {
	basesJSON, err := rawJSON(revision.Bases)
	if err != nil {
		return err
	}
	architecturesJSON, err := rawJSON(revision.Architectures)
	if err != nil {
		return err
	}
	revisionNumber, err := toInt32(revision.Revision)
	if err != nil {
		return err
	}
	tag, err := p.queries().UpdateResourceRevision(ctx, sqlcdb.UpdateResourceRevisionParams{
		ResourceID:     revision.ResourceID,
		Revision:       revisionNumber,
		ID:             revision.ID,
		Bases:          basesJSON,
		Architectures:  architecturesJSON,
		OciImageDigest: revision.OCIImageDigest,
		OciImageBlob:   revision.OCIImageBlob,
	})
	if err != nil {
		return err
	}
	if tag == 0 {
		return ErrNotFound
	}
	return nil
}

// ListResourceRevisions is part of the [Repository] interface.
func (p *Postgres) ListResourceRevisions(ctx context.Context, resourceID string) ([]core.ResourceRevision, error) {
	rows, err := p.queries().ListResourceRevisions(ctx, resourceID)
	if err != nil {
		return nil, err
	}
	out := make([]core.ResourceRevision, 0, len(rows))
	for _, row := range rows {
		item, err := resourceRevisionFromSQLC(row)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// GetResourceRevision is part of the [Repository] interface.
func (p *Postgres) GetResourceRevision(
	ctx context.Context,
	resourceID string,
	revision int,
) (core.ResourceRevision, error) {
	revisionNumber, err := toInt32(revision)
	if err != nil {
		return core.ResourceRevision{}, err
	}
	item, err := p.queries().GetResourceRevision(ctx, sqlcdb.GetResourceRevisionParams{
		ResourceID: resourceID,
		Revision:   revisionNumber,
	})
	if pgxNotFound(err) {
		return core.ResourceRevision{}, ErrNotFound
	}
	if err != nil {
		return core.ResourceRevision{}, err
	}
	return resourceRevisionFromSQLC(item)
}
