package service

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/gschiano/charm-registry/internal/config"
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
) ([]map[string]any, error) {
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return nil, translateRepoError(err, "package not found")
	}
	if err := s.requirePackageView(ctx, identity, pkg, true); err != nil {
		return nil, err
	}
	defs, err := s.repo.ListResourceDefinitions(ctx, pkg.ID)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, def := range defs {
		revs, err := s.repo.ListResourceRevisions(ctx, def.ID)
		if err != nil {
			return nil, err
		}
		currentRevision := 0
		if len(revs) > 0 {
			currentRevision = revs[0].Revision
		}
		out = append(out, map[string]any{
			"name":     def.Name,
			"optional": def.Optional,
			"revision": currentRevision,
			"type":     def.Type,
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
		return "", translateRepoError(err, "package not found")
	}
	if err := s.requirePackageManage(ctx, identity, pkg, permPackageManageRevisions); err != nil {
		return "", err
	}
	resourceDef, err := s.repo.GetResourceDefinition(ctx, pkg.ID, resourceName)
	if err != nil {
		return "", translateRepoError(err, "resource not declared")
	}
	upload, err := s.repo.GetUpload(ctx, req.UploadID)
	if err != nil {
		return "", translateRepoError(err, "upload not found")
	}
	if req.PackageRevision != nil {
		if _, err := s.repo.GetRevisionByNumber(ctx, pkg.ID, *req.PackageRevision); err != nil {
			return "", translateRepoError(err, "package revision not found")
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
	now := time.Now().UTC()
	sha512sum := sha512.Sum512(payload)
	sha3384sum := sha512.Sum384(payload)
	resourceRevision := core.ResourceRevision{
		ID:              uuid.NewString(),
		ResourceID:      resourceDef.ID,
		Name:            resourceDef.Name,
		Type:            firstNonEmpty(req.Type, resourceDef.Type),
		Description:     resourceDef.Description,
		Filename:        firstNonEmpty(resourceDef.Filename, upload.Filename),
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
	}
	if err := s.repo.CreateResourceRevision(ctx, resourceRevision); err != nil {
		return "", err
	}
	if err := s.repo.ApproveUpload(ctx, upload.ID, &revisionNumber, nil); err != nil {
		return "", err
	}
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
		return nil, translateRepoError(err, "package not found")
	}
	if err := s.requirePackageView(ctx, identity, pkg, true); err != nil {
		return nil, err
	}
	resourceDef, err := s.repo.GetResourceDefinition(ctx, pkg.ID, resourceName)
	if err != nil {
		return nil, translateRepoError(err, "resource not found")
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
		return 0, translateRepoError(err, "package not found")
	}
	if err := s.requirePackageManage(ctx, identity, pkg, permPackageManageRevisions); err != nil {
		return 0, err
	}
	resourceDef, err := s.repo.GetResourceDefinition(ctx, pkg.ID, resourceName)
	if err != nil {
		return 0, translateRepoError(err, "resource not found")
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
		updated++
	}
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
) (map[string]any, error) {
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return nil, translateRepoError(err, "package not found")
	}
	if err := s.requirePackageManage(ctx, identity, pkg, permPackageManageRevisions); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetResourceDefinition(ctx, pkg.ID, resourceName); err != nil {
		return nil, translateRepoError(err, "resource not found")
	}
	pkg, err = s.ensureOCIProvisioned(ctx, pkg)
	if err != nil {
		return nil, err
	}
	imageName, err := s.oci.ImageReference(pkg, resourceName)
	if err != nil {
		return nil, err
	}
	username, password, err := s.oci.Credentials(pkg, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"image-name": imageName,
		"username":   username,
		"password":   password,
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
		return "", translateRepoError(err, "package not found")
	}
	if err := s.requirePackageManage(ctx, identity, pkg, permPackageManageRevisions); err != nil {
		return "", err
	}
	if _, err := s.repo.GetResourceDefinition(ctx, pkg.ID, resourceName); err != nil {
		return "", translateRepoError(err, "resource not found")
	}
	pkg, err = s.ensureOCIProvisioned(ctx, pkg)
	if err != nil {
		return "", err
	}
	content, err := s.renderOCIImageBlob(pkg, resourceName, digest)
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
	pkg, err := s.repo.GetPackageByID(ctx, packageID)
	if err != nil {
		return nil, translateRepoError(err, "package not found")
	}
	if err := s.requirePackageView(ctx, identity, pkg, false); err != nil {
		return nil, err
	}
	resourceDef, err := s.repo.GetResourceDefinition(ctx, pkg.ID, resourceName)
	if err != nil {
		return nil, translateRepoError(err, "resource not found")
	}
	revision, err := s.repo.GetResourceRevision(ctx, resourceDef.ID, revisionNumber)
	if err != nil {
		return nil, translateRepoError(err, "resource revision not found")
	}
	if revision.ObjectKey == "" {
		if err := s.requireOCIPackageReady(pkg, true); err != nil {
			return nil, err
		}
		return s.renderOCIImageBlob(pkg, resourceName, revision.OCIImageDigest)
	}
	return s.blobs.Get(ctx, revision.ObjectKey)
}

func (s *Service) renderOCIImageBlob(pkg core.Package, resourceName, digest string) ([]byte, error) {
	imageName, err := s.oci.ImageReference(pkg, resourceName)
	if err != nil {
		return nil, err
	}
	username, password, err := s.oci.Credentials(pkg, true)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"ImageName": imageName,
		"Username":  username,
		"Password":  password,
		"Digest":    digest,
	}
	return json.Marshal(payload)
}

func releaseResourcesToDownloads(
	packageID string,
	resources []core.ResourceRevision,
	cfg config.Config,
) []map[string]any {
	out := make([]map[string]any, 0, len(resources))
	for _, resource := range resources {
		out = append(out, map[string]any{
			"name":             resource.Name,
			"revision":         resource.Revision,
			"type":             resource.Type,
			"filename":         resource.Filename,
			"description":      resource.Description,
			"package-revision": resource.PackageRevision,
			"download": map[string]any{
				"url": cfg.PublicAPIURL + "/api/v1/resources/download/charm_" + packageID + "." + resource.Name + "_" + fmt.Sprintf(
					"%d",
					resource.Revision,
				),
				"size":          resource.Size,
				"hash-sha-256":  resource.SHA256,
				"hash-sha-384":  resource.SHA384,
				"hash-sha-512":  resource.SHA512,
				"hash-sha3-384": resource.SHA3384,
			},
			"created-at": resource.CreatedAt,
		})
	}
	return out
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
