package service

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/gschiano/charm-registry/internal/charm"
	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
)

// CreateUpload stores an uploaded artifact and records its metadata.
//
// The following errors may be returned:
// - Blob storage or repository errors.
func (s *Service) CreateUpload(ctx context.Context, filename string, payload []byte) (core.Upload, error) {
	now := time.Now().UTC()
	uploadID := uuid.NewString()
	sha256sum := sha256.Sum256(payload)
	sha384sum := sha512.Sum384(payload)
	key := filepath.ToSlash(filepath.Join("uploads", uploadID, filename))
	if err := s.blobs.Put(ctx, key, payload, "application/octet-stream"); err != nil {
		return core.Upload{}, err
	}
	upload := core.Upload{
		ID:        uploadID,
		Filename:  filename,
		ObjectKey: key,
		Size:      int64(len(payload)),
		SHA256:    hex.EncodeToString(sha256sum[:]),
		SHA384:    hex.EncodeToString(sha384sum[:]),
		Status:    "pending",
		Kind:      detectUploadKind(filename),
		CreatedAt: now,
	}
	if err := s.repo.CreateUpload(ctx, upload); err != nil {
		return core.Upload{}, err
	}
	return upload, nil
}

// AuthorizeUpload verifies that the caller may create an upload placeholder.
func (s *Service) AuthorizeUpload(identity core.Identity) error {
	return s.requirePermission(identity, permAccountRegisterPackage)
}

// PushRevision publishes a charm revision from a prior upload.
//
// The following errors may be returned:
// - Authorization, validation, blob, or repository errors.
func (s *Service) PushRevision(
	ctx context.Context,
	identity core.Identity,
	charmName string,
	req PushRevisionRequest,
) (string, error) {
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return "", translateRepoError(err, "package not found")
	}
	if err := s.requirePackageManage(ctx, identity, pkg, permPackageManageRevisions); err != nil {
		return "", err
	}
	upload, err := s.repo.GetUpload(ctx, req.UploadID)
	if err != nil {
		return "", translateRepoError(err, "upload not found")
	}
	payload, err := s.blobs.Get(ctx, upload.ObjectKey)
	if err != nil {
		return "", err
	}
	archive, err := charm.ParseArchive(payload)
	if err != nil {
		reviewErr := []core.APIError{{Code: "invalid-archive", Message: err.Error()}}
		_ = s.repo.ApproveUpload(ctx, upload.ID, nil, reviewErr)
		return "", newError(400, "invalid-archive", err.Error())
	}
	latest, err := s.repo.GetLatestRevision(ctx, pkg.ID)
	revisionNumber := 1
	if err == nil {
		revisionNumber = latest.Revision + 1
	} else if !errors.Is(err, repo.ErrNotFound) {
		return "", err
	}
	now := time.Now().UTC()
	rev := core.Revision{
		ID:           uuid.NewString(),
		PackageID:    pkg.ID,
		Revision:     revisionNumber,
		Version:      fmt.Sprintf("%d", revisionNumber),
		Status:       "approved",
		CreatedAt:    now,
		CreatedBy:    identity.Account.ID,
		Size:         upload.Size,
		SHA256:       upload.SHA256,
		SHA384:       upload.SHA384,
		ObjectKey:    upload.ObjectKey,
		MetadataYAML: archive.MetadataYAML,
		ConfigYAML:   archive.ConfigYAML,
		ActionsYAML:  archive.ActionsYAML,
		BundleYAML:   archive.BundleYAML,
		ReadmeMD:     archive.ReadmeMD,
		Bases:        extractBases(archive.Manifest),
		Attributes: map[string]string{
			"framework": "operator",
			"language":  "unknown",
		},
		Relations: map[string]map[string]core.Relation{
			"provides": archive.Manifest.Provides,
			"requires": archive.Manifest.Requires,
			"peers":    archive.Manifest.Peers,
		},
		Subordinate: archive.Manifest.Subordinate,
	}
	if err := s.repo.CreateRevision(ctx, rev); err != nil {
		return "", err
	}
	if err := s.repo.ApproveUpload(ctx, upload.ID, &revisionNumber, nil); err != nil {
		return "", err
	}
	pkg.Status = "published"
	pkg.Title = stringPtr(firstNonEmpty(archive.Manifest.DisplayName, archive.Manifest.Name, pkg.Name))
	pkg.Summary = stringPtr(archive.Manifest.Summary)
	pkg.Description = stringPtr(archive.Manifest.Description)
	websites := charm.ExtractWebsites(archive.Manifest.Website)
	pkg.Links = mergeLinks(pkg.Links, archive.Manifest.Docs, archive.Manifest.Issues, archive.Manifest.Source, websites)
	if len(websites) > 0 {
		pkg.Website = &websites[0]
	}
	pkg.UpdatedAt = now
	if err := s.repo.UpdatePackage(ctx, pkg); err != nil {
		return "", err
	}
	for name, resource := range archive.Manifest.Resources {
		_, err := s.repo.UpsertResourceDefinition(ctx, core.ResourceDefinition{
			ID:          uuid.NewString(),
			PackageID:   pkg.ID,
			Name:        name,
			Type:        resource.Type,
			Description: resource.Description,
			Filename:    resource.Filename,
			Optional:    false,
			CreatedAt:   now,
		})
		if err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("/v1/charm/%s/revisions/review?upload-id=%s", charmName, upload.ID), nil
}

// ReviewUpload returns the review status for an upload.
//
// The following errors may be returned:
// - Authorization or repository lookup errors.
func (s *Service) ReviewUpload(
	ctx context.Context,
	identity core.Identity,
	charmName, uploadID string,
) (map[string]any, error) {
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return nil, translateRepoError(err, "package not found")
	}
	if err := s.requirePackageView(ctx, identity, pkg, true); err != nil {
		return nil, err
	}
	upload, err := s.repo.GetUpload(ctx, uploadID)
	if err != nil {
		return nil, translateRepoError(err, "upload not found")
	}
	return map[string]any{
		"revisions": []map[string]any{{
			"errors":    nullIfEmpty(upload.Errors),
			"revision":  upload.Revision,
			"status":    upload.Status,
			"upload-id": upload.ID,
		}},
	}, nil
}

// ListRevisions lists charm revisions for a package.
//
// The following errors may be returned:
// - Authorization or repository lookup errors.
func (s *Service) ListRevisions(
	ctx context.Context,
	identity core.Identity,
	charmName string,
	revision *int,
) ([]core.Revision, error) {
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return nil, translateRepoError(err, "package not found")
	}
	if err := s.requirePackageView(ctx, identity, pkg, true); err != nil {
		return nil, err
	}
	return s.repo.ListRevisions(ctx, pkg.ID, revision)
}

// DownloadCharm returns the bytes for a charm revision artifact.
//
// The following errors may be returned:
// - Authorization, repository lookup, or blob errors.
func (s *Service) DownloadCharm(
	ctx context.Context,
	identity core.Identity,
	packageID string,
	revisionNumber int,
) ([]byte, error) {
	pkg, err := s.repo.GetPackageByID(ctx, packageID)
	if err != nil {
		return nil, translateRepoError(err, "package not found")
	}
	if err := s.requirePackageView(ctx, identity, pkg, false); err != nil {
		return nil, err
	}
	revision, err := s.repo.GetRevisionByNumber(ctx, pkg.ID, revisionNumber)
	if err != nil {
		return nil, translateRepoError(err, "revision not found")
	}
	return s.blobs.Get(ctx, revision.ObjectKey)
}

func revisionToInfo(revision core.Revision, packageID string, cfg config.Config) map[string]any {
	return map[string]any{
		"actions-yaml": revision.ActionsYAML,
		"attributes":   revision.Attributes,
		"bases":        revision.Bases,
		"bundle-yaml":  revision.BundleYAML,
		"config-yaml":  revision.ConfigYAML,
		"created-at":   revision.CreatedAt,
		"download": map[string]any{
			"hash-sha-256": revision.SHA256,
			"size":         revision.Size,
			"url": cfg.PublicAPIURL + "/api/v1/charms/download/" + packageID + "_" + fmt.Sprintf(
				"%d",
				revision.Revision,
			) + ".charm",
		},
		"metadata-yaml": revision.MetadataYAML,
		"readme-md":     revision.ReadmeMD,
		"relations":     revision.Relations,
		"revision":      revision.Revision,
		"subordinate":   revision.Subordinate,
		"version":       revision.Version,
	}
}
