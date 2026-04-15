package repo

import (
	"context"
	"errors"

	"github.com/gschiano/charm-registry/internal/core"
	sqlcdb "github.com/gschiano/charm-registry/internal/repo/db"
)

// CreateUpload is part of the [Repository] interface.
func (p *Postgres) CreateUpload(ctx context.Context, upload core.Upload) error {
	errorsJSON, err := rawJSON(upload.Errors)
	if err != nil {
		return err
	}
	revision, err := int32Ptr(upload.Revision)
	if err != nil {
		return err
	}
	return p.queries().CreateUpload(ctx, sqlcdb.CreateUploadParams{
		ID:         upload.ID,
		Filename:   upload.Filename,
		ObjectKey:  upload.ObjectKey,
		Size:       upload.Size,
		Sha256:     upload.SHA256,
		Sha384:     upload.SHA384,
		Status:     upload.Status,
		Kind:       upload.Kind,
		CreatedAt:  upload.CreatedAt,
		ApprovedAt: timestamptzPtr(upload.ApprovedAt),
		Revision:   revision,
		Errors:     errorsJSON,
	})
}

// GetUpload is part of the [Repository] interface.
func (p *Postgres) GetUpload(ctx context.Context, uploadID string) (core.Upload, error) {
	upload, err := p.queries().GetUpload(ctx, uploadID)
	if pgxNotFound(err) {
		return core.Upload{}, ErrNotFound
	}
	if err != nil {
		return core.Upload{}, err
	}
	return uploadFromSQLC(upload)
}

// ApproveUpload is part of the [Repository] interface.
func (p *Postgres) ApproveUpload(ctx context.Context, uploadID string, revision *int, apiErrors []core.APIError) error {
	status := "approved"
	if len(apiErrors) > 0 {
		status = "rejected"
	}
	errorsJSON, err := rawJSON(apiErrors)
	if err != nil {
		return err
	}
	approvedRevision, err := int32Ptr(revision)
	if err != nil {
		return err
	}
	rowsAffected, err := p.queries().ApproveUpload(ctx, sqlcdb.ApproveUploadParams{
		ID:       uploadID,
		Revision: approvedRevision,
		Errors:   errorsJSON,
		Status:   status,
	})
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateRevision is part of the [Repository] interface.
func (p *Postgres) CreateRevision(ctx context.Context, revision core.Revision) error {
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
	revisionNumber, err := toInt32(revision.Revision)
	if err != nil {
		return err
	}
	return p.queries().CreateRevision(ctx, sqlcdb.CreateRevisionParams{
		ID:           revision.ID,
		PackageID:    revision.PackageID,
		Revision:     revisionNumber,
		Version:      revision.Version,
		Status:       revision.Status,
		CreatedAt:    revision.CreatedAt,
		CreatedBy:    revision.CreatedBy,
		Size:         revision.Size,
		Sha256:       revision.SHA256,
		Sha384:       revision.SHA384,
		ObjectKey:    revision.ObjectKey,
		MetadataYaml: revision.MetadataYAML,
		ConfigYaml:   revision.ConfigYAML,
		ActionsYaml:  revision.ActionsYAML,
		BundleYaml:   revision.BundleYAML,
		ReadmeMd:     revision.ReadmeMD,
		Bases:        basesJSON,
		Attributes:   attributesJSON,
		Relations:    relationsJSON,
		Subordinate:  revision.Subordinate,
	})
}

// DeleteRevision is part of the [Repository] interface.
func (p *Postgres) DeleteRevision(ctx context.Context, packageID string, revision int) error {
	revisionNumber, err := toInt32(revision)
	if err != nil {
		return err
	}
	rowsAffected, err := p.queries().DeleteRevision(ctx, sqlcdb.DeleteRevisionParams{
		PackageID: packageID,
		Revision:  revisionNumber,
	})
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListRevisions is part of the [Repository] interface.
func (p *Postgres) ListRevisions(ctx context.Context, packageID string, revision *int) ([]core.Revision, error) {
	if revision != nil {
		item, err := p.GetRevisionByNumber(ctx, packageID, *revision)
		if errors.Is(err, ErrNotFound) {
			return []core.Revision{}, nil
		}
		if err != nil {
			return nil, err
		}
		return []core.Revision{item}, nil
	}
	rows, err := p.queries().ListRevisions(ctx, packageID)
	if err != nil {
		return nil, err
	}
	out := make([]core.Revision, 0, len(rows))
	for _, row := range rows {
		item, err := revisionFromSQLC(row)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// ListRevisionsByNumbers is part of the [Repository] interface.
func (p *Postgres) ListRevisionsByNumbers(
	ctx context.Context,
	packageID string,
	revisions []int,
) (map[int]core.Revision, error) {
	if len(revisions) == 0 {
		return map[int]core.Revision{}, nil
	}
	numbers, err := int32Slice(revisions)
	if err != nil {
		return nil, err
	}
	rows, err := p.queries().ListRevisionsByNumbers(ctx, sqlcdb.ListRevisionsByNumbersParams{
		PackageID: packageID,
		Column2:   numbers,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[int]core.Revision, len(rows))
	for _, row := range rows {
		item, err := revisionFromSQLC(row)
		if err != nil {
			return nil, err
		}
		out[item.Revision] = item
	}
	return out, nil
}

// GetRevisionByNumber is part of the [Repository] interface.
func (p *Postgres) GetRevisionByNumber(ctx context.Context, packageID string, revision int) (core.Revision, error) {
	revisionNumber, err := toInt32(revision)
	if err != nil {
		return core.Revision{}, err
	}
	item, err := p.queries().GetRevisionByNumber(ctx, sqlcdb.GetRevisionByNumberParams{
		PackageID: packageID,
		Revision:  revisionNumber,
	})
	if pgxNotFound(err) {
		return core.Revision{}, ErrNotFound
	}
	if err != nil {
		return core.Revision{}, err
	}
	return revisionFromSQLC(item)
}

// GetLatestRevision is part of the [Repository] interface.
func (p *Postgres) GetLatestRevision(ctx context.Context, packageID string) (core.Revision, error) {
	item, err := p.queries().GetLatestRevision(ctx, packageID)
	if pgxNotFound(err) {
		return core.Revision{}, ErrNotFound
	}
	if err != nil {
		return core.Revision{}, err
	}
	return revisionFromSQLC(item)
}
