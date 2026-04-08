package service

import (
	"context"

	"github.com/gschiano/charm-registry/internal/blob"
	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
)

const (
	permAccountRegisterPackage = "account-register-package"
	permAccountViewPackages    = "account-view-packages"
	permPackageManage          = "package-manage"
	permPackageManageMetadata  = "package-manage-metadata"
	permPackageManageReleases  = "package-manage-releases"
	permPackageManageRevisions = "package-manage-revisions"
	permPackageView            = "package-view"
	permPackageViewMetadata    = "package-view-metadata"
	permPackageViewReleases    = "package-view-releases"
	permPackageViewRevisions   = "package-view-revisions"
)

var defaultPermissions = []string{
	permAccountRegisterPackage,
	permAccountViewPackages,
	permPackageManage,
	permPackageManageMetadata,
	permPackageManageReleases,
	permPackageManageRevisions,
	permPackageView,
	permPackageViewMetadata,
	permPackageViewReleases,
	permPackageViewRevisions,
}

// IssueTokenRequest describes the body for the issue-token endpoint.
type IssueTokenRequest struct {
	Description *string                `json:"description"`
	TTL         *int                   `json:"ttl"`
	Permissions []string               `json:"permissions"`
	Packages    []core.PackageSelector `json:"packages"`
	Channels    []string               `json:"channels"`
}

// MetadataPatch carries optional fields for updating package metadata.
type MetadataPatch struct {
	Contact      *string             `json:"contact"`
	DefaultTrack *string             `json:"default-track"`
	Description  *string             `json:"description"`
	Links        map[string][]string `json:"links"`
	Private      *bool               `json:"private"`
	Summary      *string             `json:"summary"`
	Title        *string             `json:"title"`
	Website      *string             `json:"website"`
}

// PushRevisionRequest describes the body for publishing a revision.
type PushRevisionRequest struct {
	UploadID string `json:"upload-id"`
}

// PushResourceRequest describes the body for publishing a resource revision.
type PushResourceRequest struct {
	UploadID        string      `json:"upload-id"`
	Type            string      `json:"type"`
	Bases           []core.Base `json:"bases"`
	Architectures   []string    `json:"architectures"`
	PackageRevision *int        `json:"package-revision"`
}

// UpdateResourceRevisionRequest describes the body for updating resource revisions.
type UpdateResourceRevisionRequest struct {
	ResourceRevisionUpdates []struct {
		Revision      int         `json:"revision"`
		Bases         []core.Base `json:"bases"`
		Architectures []string    `json:"architectures"`
	} `json:"resource-revision-updates"`
}

// RefreshRequest is the body for the Charmhub-compatible refresh endpoint.
type RefreshRequest struct {
	Context []RefreshContext             `json:"context"`
	Actions []RefreshAction              `json:"actions"`
	Fields  []string                     `json:"fields"`
	Metrics map[string]map[string]string `json:"metrics,omitempty"`
}

// RefreshContext describes the installed state of one charm instance.
type RefreshContext struct {
	InstanceKey     string     `json:"instance-key"`
	ID              string     `json:"id"`
	Revision        int        `json:"revision"`
	Base            *core.Base `json:"base"`
	TrackingChannel string     `json:"tracking-channel"`
}

// RefreshAction describes a single refresh operation request.
type RefreshAction struct {
	Action            string                    `json:"action"`
	InstanceKey       string                    `json:"instance-key"`
	ID                *string                   `json:"id"`
	Name              *string                   `json:"name"`
	Revision          *int                      `json:"revision"`
	Channel           *string                   `json:"channel"`
	Base              *core.Base                `json:"base"`
	ResourceRevisions []core.ReleaseResourceRef `json:"resource-revisions"`
}

// Service is the application service layer.
type Service struct {
	cfg   config.Config
	repo  repo.Repository
	blobs blob.Store
	oci   OCIRegistry
}

type OCIRegistry interface {
	SyncPackage(ctx context.Context, pkg core.Package) (core.Package, error)
	ImageReference(pkg core.Package, resourceName string) (string, error)
	Credentials(pkg core.Package, pull bool) (username, password string, err error)
}

// New returns a [Service] backed by the provided repository and blob store.
func New(cfg config.Config, repository repo.Repository, blobs blob.Store, oci OCIRegistry) *Service {
	return &Service{cfg: cfg, repo: repository, blobs: blobs, oci: oci}
}

func (s *Service) withRepositoryTransaction(ctx context.Context, fn func(repo.Repository) error) error {
	return s.repo.WithinTransaction(ctx, fn)
}

// Ready reports whether the service dependencies are ready to serve requests.
func (s *Service) Ready(ctx context.Context) error {
	return s.repo.Ping(ctx)
}

// RootDocument returns the top-level service metadata document.
func (s *Service) RootDocument() map[string]any {
	return map[string]any{
		"service-name": "private-charm-registry",
		"version":      "v1",
		"api-url":      s.cfg.PublicAPIURL,
		"storage-url":  s.cfg.PublicStorageURL,
		"registry-url": s.cfg.PublicRegistryURL,
	}
}
