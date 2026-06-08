package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/google/uuid"

	"github.com/gschiano/charm-registry/internal/charm"
	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
)

// CreateUpload stores an uploaded artifact and records its metadata.
//
// The following errors may be returned:
// - Blob storage or repository errors.
func (s *Service) CreateUpload(ctx context.Context, filename string, payload []byte) (core.Upload, error) {
	return s.CreateUploadStream(ctx, filename, bytes.NewReader(payload))
}

// CreateUploadStream stores an uploaded artifact directly from a reader and records its metadata.
//
// The following errors may be returned:
// - Blob storage or repository errors.
func (s *Service) CreateUploadStream(ctx context.Context, filename string, payload io.Reader) (core.Upload, error) {
	now := s.now()
	uploadID := uuid.NewString()
	sha256sum := sha256.New()
	sha384sum := sha512.New384()
	tmp, err := os.CreateTemp("", "charm-registry-upload-*")
	if err != nil {
		return core.Upload{}, err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	key := filepath.ToSlash(filepath.Join("uploads", uploadID, filename))
	size, err := io.Copy(io.MultiWriter(tmp, sha256sum, sha384sum), payload)
	if err != nil {
		return core.Upload{}, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return core.Upload{}, err
	}
	if err := s.blobs.Put(ctx, key, tmp, "application/octet-stream"); err != nil {
		return core.Upload{}, err
	}
	upload := core.Upload{
		ID:        uploadID,
		Filename:  filename,
		ObjectKey: key,
		Size:      size,
		SHA256:    hex.EncodeToString(sha256sum.Sum(nil)),
		SHA384:    hex.EncodeToString(sha384sum.Sum(nil)),
		Status:    "pending",
		Kind:      detectUploadKind(filename),
		CreatedAt: now,
	}
	if err := s.repo.CreateUpload(ctx, upload); err != nil {
		return core.Upload{}, err
	}
	slog.DebugContext(ctx, "upload stored",
		"upload_id", upload.ID,
		"filename", upload.Filename,
		"kind", upload.Kind,
		"size", upload.Size,
	)
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
//
//nolint:gocognit,cyclop // Publishing a revision intentionally follows the end-to-end workflow in one place.
func (s *Service) PushRevision(
	ctx context.Context,
	identity core.Identity,
	charmName string,
	req PushRevisionRequest,
) (string, error) {
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return "", translateRepoError(err, messagePackageNotFound)
	}
	if err := s.ensurePackageNotSynchronized(ctx, pkg.Name); err != nil {
		return "", err
	}
	if err := s.requirePackageManage(ctx, identity, pkg, permPackageManageRevisions); err != nil {
		return "", err
	}
	upload, err := s.repo.GetUpload(ctx, req.UploadID)
	if err != nil {
		return "", translateRepoError(err, messageUploadNotFound)
	}
	payload, err := s.blobs.Get(ctx, upload.ObjectKey)
	if err != nil {
		return "", err
	}
	archive, err := charm.ParseArchiveWithMaxFileSize(payload, s.cfg.MaxArchiveFileBytes)
	if err != nil {
		slog.InfoContext(ctx, "revision upload rejected",
			"package", pkg.Name,
			"package_id", pkg.ID,
			"upload_id", upload.ID,
			"error", err,
		)
		reviewErr := []core.APIError{{Code: "invalid-archive", Message: err.Error()}}
		if approveErr := s.repo.ApproveUpload(ctx, upload.ID, nil, reviewErr); approveErr != nil {
			return "", fmt.Errorf("cannot record upload review failure: %w", approveErr)
		}
		return "", newError(ErrorKindInvalidRequest, "invalid-archive", err.Error())
	}
	latest, err := s.repo.GetLatestRevision(ctx, pkg.ID)
	revisionNumber := 1
	if err == nil {
		revisionNumber = latest.Revision + 1
	} else if !errors.Is(err, repo.ErrNotFound) {
		return "", err
	}
	now := s.now()
	rev, err := core.NewRevision(core.Revision{
		ID:           uuid.NewString(),
		PackageID:    pkg.ID,
		Revision:     revisionNumber,
		Version:      strconv.Itoa(revisionNumber),
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
	})
	if err != nil {
		return "", err
	}
	pkg.Status = "published"
	pkg.Title = stringPtr(core.FirstNonEmpty(archive.Manifest.DisplayName, archive.Manifest.Name, pkg.Name))
	pkg.Summary = stringPtr(archive.Manifest.Summary)
	pkg.Description = stringPtr(archive.Manifest.Description)
	websites := charm.ExtractWebsites(archive.Manifest.Website)
	pkg.Links = mergeLinks(pkg.Links, archive.Manifest.Docs, archive.Manifest.Issues, archive.Manifest.Source, websites)
	if len(websites) > 0 {
		pkg.Website = &websites[0]
	}
	pkg.UpdatedAt = now
	if err := s.withRepositoryTransaction(ctx, func(repository repo.PackageRepo) error {
		if err := repository.CreateRevision(ctx, rev); err != nil {
			return err
		}
		if err := repository.ApproveUpload(ctx, upload.ID, &revisionNumber, nil); err != nil {
			return err
		}
		if err := repository.UpdatePackage(ctx, pkg); err != nil {
			return err
		}
		for name, resource := range archive.Manifest.Resources {
			if _, err := repository.UpsertResourceDefinition(ctx, core.ResourceDefinition{
				ID:          uuid.NewString(),
				PackageID:   pkg.ID,
				Name:        name,
				Type:        resource.Type,
				Description: resource.Description,
				Filename:    resource.Filename,
				Optional:    false,
				CreatedAt:   now,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return "", err
	}
	slog.InfoContext(ctx, "revision published",
		"package", pkg.Name,
		"package_id", pkg.ID,
		"revision", rev.Revision,
		"upload_id", upload.ID,
		"size", rev.Size,
		"account_id", identity.Account.ID,
	)
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
) (reviewUploadResponse, error) {
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return reviewUploadResponse{}, translateRepoError(err, messagePackageNotFound)
	}
	if err := s.requirePackageView(ctx, identity, pkg, true); err != nil {
		return reviewUploadResponse{}, err
	}
	upload, err := s.repo.GetUpload(ctx, uploadID)
	if err != nil {
		return reviewUploadResponse{}, translateRepoError(err, messageUploadNotFound)
	}
	return reviewUploadResponse{
		Revisions: []uploadReviewResponse{{
			Errors:   upload.Errors,
			Revision: upload.Revision,
			Status:   upload.Status,
			UploadID: upload.ID,
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
	if err := s.requireAuth(identity); err != nil {
		return nil, err
	}
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return nil, translateRepoError(err, messagePackageNotFound)
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
	reader, _, err := s.DownloadCharmStream(ctx, identity, packageID, revisionNumber)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

// DownloadCharmStream opens a charm revision artifact for streaming.
//
// The following errors may be returned:
// - Authorization, repository lookup, or blob errors.
func (s *Service) DownloadCharmStream(
	ctx context.Context,
	identity core.Identity,
	packageID string,
	revisionNumber int,
) (io.ReadCloser, int64, error) {
	if err := s.requireAuth(identity); err != nil {
		return nil, 0, err
	}
	pkg, err := s.repo.GetPackageByID(ctx, packageID)
	if err != nil {
		return nil, 0, translateRepoError(err, messagePackageNotFound)
	}
	if err := s.requirePackageView(ctx, identity, pkg, false); err != nil {
		return nil, 0, err
	}
	revision, err := s.repo.GetRevisionByNumber(ctx, pkg.ID, revisionNumber)
	if err != nil {
		return nil, 0, translateRepoError(err, messageRevisionNotFound)
	}
	return s.blobs.Open(ctx, revision.ObjectKey)
}

func (s *Service) revisionToInfo(revision core.Revision, packageID string) infoRevisionResponse {
	return infoRevisionResponse{
		ActionsYAML: revision.ActionsYAML,
		Attributes:  revision.Attributes,
		Bases:       revision.Bases,
		BundleYAML:  revision.BundleYAML,
		ConfigYAML:  revision.ConfigYAML,
		CreatedAt:   revision.CreatedAt,
		Download: core.Download{
			HashSHA256: revision.SHA256,
			Size:       revision.Size,
			URL:        s.charmDownloadURL(packageID, revision.Revision),
		},
		MetadataYAML: revision.MetadataYAML,
		ReadmeMD:     revision.ReadmeMD,
		Relations:    revision.Relations,
		Revision:     revision.Revision,
		Subordinate:  revision.Subordinate,
		Version:      revision.Version,
	}
}
