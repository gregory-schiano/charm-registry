package service

import (
	"context"
	"fmt"

	"github.com/gschiano/charm-registry/internal/blob"
	charmhubclient "github.com/gschiano/charm-registry/internal/charmhub"
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
	cfg         config.Config
	repo        repo.Repository
	blobs       blob.Store
	oci         OCIRegistry
	charmhub    charmhubClient
	syncManager *CharmhubSyncManager
}

type charmhubClient interface {
	GetChannel(ctx context.Context, name, channel string) (charmhubclient.PackageChannel, error)
	Download(ctx context.Context, artifactURL string) ([]byte, error)
}

// New returns a [Service] backed by the provided repository and blob store.
func New(cfg config.Config, repository repo.Repository, blobs blob.Store, oci OCIRegistry) *Service {
	return &Service{
		cfg:      cfg,
		repo:     repository,
		blobs:    blobs,
		oci:      oci,
		charmhub: charmhubclient.New(cfg.CharmhubURL),
	}
}

func (s *Service) withRepositoryTransaction(ctx context.Context, fn func(repo.Repository) error) error {
	if err := s.repo.WithinTransaction(ctx, fn); err != nil {
		return fmt.Errorf("cannot complete repository transaction: %w", err)
	}
	return nil
}

// CheckReady reports whether the service dependencies are ready to serve requests.
func (s *Service) CheckReady(ctx context.Context) error {
	return s.repo.Ping(ctx)
}

// GetRootDocument returns the top-level service metadata document.
func (s *Service) GetRootDocument() rootDocumentResponse {
	return rootDocumentResponse{
		ServiceName: "private-charm-registry",
		Version:     "v1",
		APIURL:      s.cfg.PublicAPIURL,
		StorageURL:  s.cfg.PublicStorageURL,
		RegistryURL: s.cfg.PublicRegistryURL,
	}
}
