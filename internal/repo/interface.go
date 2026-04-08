package repo

import (
	"context"

	"github.com/gschiano/charm-registry/internal/core"
)

type Repository interface {
	coreTokenFinder

	Ping(ctx context.Context) error
	WithinTransaction(ctx context.Context, fn func(Repository) error) error

	EnsureAccount(ctx context.Context, account core.Account) (core.Account, error)
	GetAccountByID(ctx context.Context, accountID string) (core.Account, error)

	CreateStoreToken(ctx context.Context, token core.StoreToken) error
	ListStoreTokens(ctx context.Context, accountID string, includeInactive bool) ([]core.StoreToken, error)
	RevokeStoreToken(ctx context.Context, accountID, sessionID, revokedBy string) error

	CreatePackage(ctx context.Context, pkg core.Package) error
	UpdatePackage(ctx context.Context, pkg core.Package) error
	DeletePackage(ctx context.Context, packageID string) error
	GetPackageByName(ctx context.Context, name string) (core.Package, error)
	GetPackageByID(ctx context.Context, packageID string) (core.Package, error)
	ListPackagesForAccount(ctx context.Context, accountID string, includeCollaborations bool) ([]core.Package, error)
	SearchPackages(ctx context.Context, query string) ([]core.Package, error)
	CanViewPackage(ctx context.Context, packageID, accountID string) (bool, error)
	CanManagePackage(ctx context.Context, packageID, accountID string) (bool, error)
	CreateTracks(ctx context.Context, packageID string, tracks []core.Track) (int, error)
	ListTracks(ctx context.Context, packageID string) ([]core.Track, error)
	ListTracksForPackages(ctx context.Context, packageIDs []string) (map[string][]core.Track, error)

	CreateUpload(ctx context.Context, upload core.Upload) error
	GetUpload(ctx context.Context, uploadID string) (core.Upload, error)
	ApproveUpload(ctx context.Context, uploadID string, revision *int, errors []core.APIError) error

	CreateRevision(ctx context.Context, revision core.Revision) error
	ListRevisions(ctx context.Context, packageID string, revision *int) ([]core.Revision, error)
	GetRevisionByNumber(ctx context.Context, packageID string, revision int) (core.Revision, error)
	GetLatestRevision(ctx context.Context, packageID string) (core.Revision, error)

	UpsertResourceDefinition(ctx context.Context, resource core.ResourceDefinition) (core.ResourceDefinition, error)
	GetResourceDefinition(ctx context.Context, packageID, resourceName string) (core.ResourceDefinition, error)
	ListResourceDefinitions(ctx context.Context, packageID string) ([]core.ResourceDefinition, error)
	CreateResourceRevision(ctx context.Context, revision core.ResourceRevision) error
	UpdateResourceRevision(ctx context.Context, revision core.ResourceRevision) error
	ListResourceRevisions(ctx context.Context, resourceID string) ([]core.ResourceRevision, error)
	GetResourceRevision(ctx context.Context, resourceID string, revision int) (core.ResourceRevision, error)

	ReplaceRelease(ctx context.Context, packageID string, release core.Release) error
	ListReleases(ctx context.Context, packageID string) ([]core.Release, error)
	ResolveRelease(ctx context.Context, packageID string, channel string) (core.Release, error)
	ResolveDefaultRelease(ctx context.Context, packageID string) (core.Release, error)
}

type coreTokenFinder interface {
	FindStoreTokenByHash(ctx context.Context, hash string) (core.StoreToken, core.Account, error)
}
