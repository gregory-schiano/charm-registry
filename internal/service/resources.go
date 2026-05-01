package service

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"

	"github.com/google/uuid"

	"github.com/gschiano/charm-registry/internal/core"
)

// ListResources lists declared resources for a package.
//
// The following errors may be returned:
// - Authorization or repository lookup errors.
func (s *Service) ListResources(
	ctx context.Context,
	identity core.Identity,
	charmName string,
) ([]ResourceListItemResponse, error) {
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return nil, translateRepoError(err, messagePackageNotFound)
	}
	if err := s.requirePackageView(ctx, identity, pkg, true); err != nil {
		return nil, err
	}
	defs, err := s.repo.ListResourceDefinitions(ctx, pkg.ID)
	if err != nil {
		return nil, err
	}
	out := make([]ResourceListItemResponse, 0, len(defs))
	for _, def := range defs {
		revs, err := s.repo.ListResourceRevisions(ctx, def.ID)
		if err != nil {
			return nil, err
		}
		currentRevision := 0
		if len(revs) > 0 {
			currentRevision = revs[0].Revision
		}
		out = append(out, ResourceListItemResponse{
			Name:     def.Name,
			Optional: def.Optional,
			Revision: currentRevision,
			Type:     def.Type,
		})
	}
	return out, nil
}

// PushResource publishes a resource revision from a prior upload.
//
// The following errors may be returned:
// - Authorization, validation, blob, or repository errors.
func (s *Service) PushResource(
	ctx context.Context,
	identity core.Identity,
	charmName, resourceName string,
	req PushResourceRequest,
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
	resourceDef, err := s.repo.GetResourceDefinition(ctx, pkg.ID, resourceName)
	if err != nil {
		return "", translateRepoError(err, messageResourceNotDeclared)
	}
	upload, err := s.repo.GetUpload(ctx, req.UploadID)
	if err != nil {
		return "", translateRepoError(err, messageUploadNotFound)
	}
	if req.PackageRevision != nil {
		if _, err := s.repo.GetRevisionByNumber(ctx, pkg.ID, *req.PackageRevision); err != nil {
			return "", translateRepoError(err, messagePackageRevisionNotFound)
		}
	}
	payload, err := s.blobs.Get(ctx, upload.ObjectKey)
	if err != nil {
		return "", err
	}
	existing, err := s.repo.ListResourceRevisions(ctx, resourceDef.ID)
	if err != nil {
		return "", err
	}
	revisionNumber := 1
	if len(existing) > 0 {
		revisionNumber = existing[0].Revision + 1
	}
	now := s.now()
	sha512sum := sha512.Sum512(payload)
	sha3384sum := sha512.Sum384(payload)
	resourceRevision := core.ResourceRevision{
		ID:              uuid.NewString(),
		ResourceID:      resourceDef.ID,
		Name:            resourceDef.Name,
		Type:            core.FirstNonEmpty(req.Type, resourceDef.Type),
		Description:     resourceDef.Description,
		Filename:        core.FirstNonEmpty(resourceDef.Filename, upload.Filename),
		Revision:        revisionNumber,
		CreatedAt:       now,
		Size:            int64(len(payload)),
		SHA256:          upload.SHA256,
		SHA384:          upload.SHA384,
		SHA512:          hex.EncodeToString(sha512sum[:]),
		SHA3384:         hex.EncodeToString(sha3384sum[:]),
		ObjectKey:       upload.ObjectKey,
		Bases:           req.Bases,
		Architectures:   req.Architectures,
		PackageRevision: req.PackageRevision,
	}
	if resourceRevision.Type == "oci-image" {
		var descriptor struct {
			Digest string `json:"Digest"`
		}
		if err := json.Unmarshal(payload, &descriptor); err != nil {
			return "", newError(ErrorKindInvalidRequest, "invalid-request", "invalid OCI image blob payload")
		}
		resourceRevision.OCIImageDigest = descriptor.Digest
		resourceRevision.ObjectKey = ""
		resourceRevision.Size = int64(len(payload))
		slog.DebugContext(ctx, "resource upload treated as OCI image blob",
			"package", pkg.Name,
			"package_id", pkg.ID,
			"resource", resourceName,
			"digest", resourceRevision.OCIImageDigest,
		)
	}
	if err := s.repo.CreateResourceRevision(ctx, resourceRevision); err != nil {
		return "", err
	}
	if err := s.repo.ApproveUpload(ctx, upload.ID, &revisionNumber, nil); err != nil {
		return "", err
	}
	slog.InfoContext(ctx, "resource revision published",
		"package", pkg.Name,
		"package_id", pkg.ID,
		"resource", resourceDef.Name,
		"resource_type", resourceRevision.Type,
		"revision", resourceRevision.Revision,
		"package_revision", intPtrValue(resourceRevision.PackageRevision),
		"upload_id", upload.ID,
		"account_id", identity.Account.ID,
	)
	return fmt.Sprintf("/v1/charm/%s/revisions/review?upload-id=%s", charmName, upload.ID), nil
}

// ListResourceRevisions lists revisions for a declared resource.
//
// The following errors may be returned:
// - Authorization or repository lookup errors.
func (s *Service) ListResourceRevisions(
	ctx context.Context,
	identity core.Identity,
	charmName, resourceName string,
) ([]core.ResourceRevision, error) {
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return nil, translateRepoError(err, messagePackageNotFound)
	}
	if err := s.requirePackageView(ctx, identity, pkg, true); err != nil {
		return nil, err
	}
	resourceDef, err := s.repo.GetResourceDefinition(ctx, pkg.ID, resourceName)
	if err != nil {
		return nil, translateRepoError(err, messageResourceNotFound)
	}
	revisions, err := s.repo.ListResourceRevisions(ctx, resourceDef.ID)
	if err != nil {
		return nil, err
	}
	return s.attachResourceDownloads(resourceDef, revisions, nil)
}

// UpdateResourceRevisions updates metadata for resource revisions.
//
// The following errors may be returned:
// - Authorization, validation, or repository errors.
func (s *Service) UpdateResourceRevisions(
	ctx context.Context,
	identity core.Identity,
	charmName, resourceName string,
	req UpdateResourceRevisionRequest,
) (int, error) {
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return 0, translateRepoError(err, messagePackageNotFound)
	}
	if err := s.ensurePackageNotSynchronized(ctx, pkg.Name); err != nil {
		return 0, err
	}
	if err := s.requirePackageManage(ctx, identity, pkg, permPackageManageRevisions); err != nil {
		return 0, err
	}
	resourceDef, err := s.repo.GetResourceDefinition(ctx, pkg.ID, resourceName)
	if err != nil {
		return 0, translateRepoError(err, messageResourceNotFound)
	}
	updated := 0
	for _, update := range req.ResourceRevisionUpdates {
		item, err := s.repo.GetResourceRevision(ctx, resourceDef.ID, update.Revision)
		if err != nil {
			return updated, err
		}
		item.Bases = update.Bases
		item.Architectures = update.Architectures
		if err := s.repo.UpdateResourceRevision(ctx, item); err != nil {
			return updated, err
		}
		slog.DebugContext(ctx, "resource revision metadata updated",
			"package", pkg.Name,
			"package_id", pkg.ID,
			"resource", resourceDef.Name,
			"revision", item.Revision,
			"base_count", len(item.Bases),
			"architecture_count", len(item.Architectures),
			"account_id", identity.Account.ID,
		)
		updated++
	}
	slog.InfoContext(ctx, "resource revisions updated",
		"package", pkg.Name,
		"package_id", pkg.ID,
		"resource", resourceDef.Name,
		"updated_count", updated,
		"account_id", identity.Account.ID,
	)
	return updated, nil
}

// OCIImageUploadCredentials returns credentials for pushing OCI resources.
//
// The following errors may be returned:
// - Authorization or repository lookup errors.
func (s *Service) OCIImageUploadCredentials(
	ctx context.Context,
	identity core.Identity,
	charmName, resourceName string,
) (ociImageUploadCredentialsResponse, error) {
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return ociImageUploadCredentialsResponse{}, translateRepoError(err, messagePackageNotFound)
	}
	if err := s.ensurePackageNotSynchronized(ctx, pkg.Name); err != nil {
		return ociImageUploadCredentialsResponse{}, err
	}
	if err := s.requirePackageManage(ctx, identity, pkg, permPackageManageRevisions); err != nil {
		return ociImageUploadCredentialsResponse{}, err
	}
	if _, err := s.repo.GetResourceDefinition(ctx, pkg.ID, resourceName); err != nil {
		return ociImageUploadCredentialsResponse{}, translateRepoError(err, messageResourceNotFound)
	}
	pkg, err = s.ensureOCIProvisioned(ctx, pkg)
	if err != nil {
		return ociImageUploadCredentialsResponse{}, err
	}
	imageName, err := s.oci.ImageReference(pkg, resourceName)
	if err != nil {
		return ociImageUploadCredentialsResponse{}, err
	}
	username, password, err := s.oci.Credentials(pkg, false)
	if err != nil {
		return ociImageUploadCredentialsResponse{}, err
	}
	slog.InfoContext(ctx, "OCI image upload credentials issued",
		"package", pkg.Name,
		"package_id", pkg.ID,
		"resource", resourceName,
		"image_name", imageName,
		"account_id", identity.Account.ID,
	)
	return ociImageUploadCredentialsResponse{
		ImageName: imageName,
		Username:  username,
		Password:  password,
	}, nil
}

// OCIImageBlob returns the OCI image descriptor payload for a resource.
//
// The following errors may be returned:
// - JSON marshaling errors.
func (s *Service) OCIImageBlob(
	ctx context.Context,
	identity core.Identity,
	charmName, resourceName, digest string,
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
	if _, err := s.repo.GetResourceDefinition(ctx, pkg.ID, resourceName); err != nil {
		return "", translateRepoError(err, messageResourceNotFound)
	}
	pkg, err = s.ensureOCIProvisioned(ctx, pkg)
	if err != nil {
		return "", err
	}
	content, err := s.renderOCIImageBlob(pkg, resourceName, digest)
	if err == nil {
		slog.DebugContext(ctx, "OCI image blob rendered",
			"package", pkg.Name,
			"package_id", pkg.ID,
			"resource", resourceName,
			"digest", digest,
			"account_id", identity.Account.ID,
		)
	}
	return string(content), err
}

// DownloadResource returns the bytes for a resource revision artifact.
//
// The following errors may be returned:
// - Authorization, repository lookup, or blob errors.
func (s *Service) DownloadResource(
	ctx context.Context,
	identity core.Identity,
	packageID, resourceName string,
	revisionNumber int,
) ([]byte, error) {
	reader, _, err := s.DownloadResourceStream(ctx, identity, packageID, resourceName, revisionNumber)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

// DownloadResourceStream opens a resource revision artifact for streaming.
//
// The following errors may be returned:
// - Authorization, repository lookup, or blob errors.
func (s *Service) DownloadResourceStream(
	ctx context.Context,
	identity core.Identity,
	packageID, resourceName string,
	revisionNumber int,
) (io.ReadCloser, int64, error) {
	pkg, err := s.repo.GetPackageByID(ctx, packageID)
	if err != nil {
		return nil, 0, translateRepoError(err, messagePackageNotFound)
	}
	if err := s.requirePackageView(ctx, identity, pkg, false); err != nil {
		return nil, 0, err
	}
	resourceDef, err := s.repo.GetResourceDefinition(ctx, pkg.ID, resourceName)
	if err != nil {
		return nil, 0, translateRepoError(err, messageResourceNotFound)
	}
	revision, err := s.repo.GetResourceRevision(ctx, resourceDef.ID, revisionNumber)
	if err != nil {
		return nil, 0, translateRepoError(err, messageResourceRevisionNotFound)
	}
	if revision.ObjectKey == "" {
		if err := s.requireOCIPackageReady(pkg, true); err != nil {
			return nil, 0, err
		}
		payload, err := s.renderOCIImageBlob(pkg, resourceName, revision.OCIImageDigest)
		if err != nil {
			return nil, 0, err
		}
		return io.NopCloser(bytes.NewReader(payload)), int64(len(payload)), nil
	}
	return s.blobs.Open(ctx, revision.ObjectKey)
}

func (s *Service) renderOCIImageBlob(pkg core.Package, resourceName, digest string) ([]byte, error) {
	imageName, err := s.oci.ImageReference(pkg, resourceName)
	if err != nil {
		return nil, err
	}
	if digest != "" {
		imageName += "@" + digest
	}
	username, password, err := s.oci.Credentials(pkg, true)
	if err != nil {
		return nil, err
	}
	payload := struct {
		ImageName    string `json:"ImageName"`
		RegistryPath string `json:"RegistryPath"`
		Username     string `json:"Username"`
		Password     string `json:"Password"`
		JujuUsername string `json:"username"`
		JujuPassword string `json:"password"`
		Digest       string `json:"Digest"`
	}{
		ImageName:    imageName,
		RegistryPath: imageName,
		Username:     username,
		Password:     password,
		JujuUsername: username,
		JujuPassword: password,
		Digest:       digest,
	}
	// #nosec G117 -- Charmcraft expects a Docker-style auth blob containing these credentials.
	return json.Marshal(payload)
}

func (s *Service) attachResourceDownloads(
	def core.ResourceDefinition,
	revisions []core.ResourceRevision,
	err error,
) ([]core.ResourceRevision, error) {
	if err != nil {
		return nil, err
	}
	for idx, item := range revisions {
		revisions[idx] = s.attachResourceDownload(def.PackageID, item)
	}
	return revisions, nil
}

func (s *Service) attachResourceDownload(packageID string, item core.ResourceRevision) core.ResourceRevision {
	item.Download = core.Download{
		URL:         s.resourceDownloadURL(packageID, item.Name, item.Revision),
		Size:        item.Size,
		HashSHA256:  item.SHA256,
		HashSHA384:  item.SHA384,
		HashSHA512:  item.SHA512,
		HashSHA3384: item.SHA3384,
	}
	return item
}
