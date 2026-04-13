package repo

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gschiano/charm-registry/internal/core"
)

type Memory struct {
	mu                sync.RWMutex
	accounts          map[string]core.Account
	accountsByID      map[string]core.Account
	tokens            map[string]core.StoreToken
	packages          map[string]core.Package
	packagesByID      map[string]core.Package
	uploads           map[string]core.Upload
	revisions         map[string][]core.Revision
	resourceDefs      map[string]map[string]core.ResourceDefinition
	resourceRevisions map[string][]core.ResourceRevision
	releases          map[string]map[string]core.Release
}

// NewMemory returns an in-memory [Repository] implementation.
//
// This is a test-oriented approximation of the repository contract, not the
// canonical source of access-control behavior. PostgreSQL remains the
// production reference implementation.
func NewMemory() *Memory {
	return &Memory{
		accounts:          map[string]core.Account{},
		accountsByID:      map[string]core.Account{},
		tokens:            map[string]core.StoreToken{},
		packages:          map[string]core.Package{},
		packagesByID:      map[string]core.Package{},
		uploads:           map[string]core.Upload{},
		revisions:         map[string][]core.Revision{},
		resourceDefs:      map[string]map[string]core.ResourceDefinition{},
		resourceRevisions: map[string][]core.ResourceRevision{},
		releases:          map[string]map[string]core.Release{},
	}
}

// Ping is part of the [Repository] interface.
func (m *Memory) Ping(_ context.Context) error {
	return nil
}

// WithinTransaction is part of the [Repository] interface.
func (m *Memory) WithinTransaction(ctx context.Context, fn func(Repository) error) error {
	return fn(m)
}

// EnsureAccount is part of the [Repository] interface.
func (m *Memory) EnsureAccount(_ context.Context, account core.Account) (core.Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.accounts[account.Subject]; ok {
		existing.Email = account.Email
		existing.DisplayName = account.DisplayName
		existing.Username = account.Username
		existing.Validation = account.Validation
		existing.IsAdmin = account.IsAdmin
		m.accounts[account.Subject] = existing
		m.accountsByID[existing.ID] = existing
		return existing, nil
	}
	account.CreatedAt = time.Now().UTC()
	m.accounts[account.Subject] = account
	m.accountsByID[account.ID] = account
	return account, nil
}

// GetAccountByID is part of the [Repository] interface.
func (m *Memory) GetAccountByID(_ context.Context, accountID string) (core.Account, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	account, ok := m.accountsByID[accountID]
	if !ok {
		return core.Account{}, ErrNotFound
	}
	return account, nil
}

// CreateStoreToken is part of the [Repository] interface.
func (m *Memory) CreateStoreToken(_ context.Context, token core.StoreToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[token.TokenHash] = token
	return nil
}

// ListStoreTokens is part of the [Repository] interface.
func (m *Memory) ListStoreTokens(_ context.Context, accountID string, includeInactive bool) ([]core.StoreToken, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var tokens []core.StoreToken
	for _, token := range m.tokens {
		if token.AccountID != accountID {
			continue
		}
		if !includeInactive && (token.RevokedAt != nil || token.ValidUntil.Before(time.Now().UTC())) {
			continue
		}
		tokens = append(tokens, token)
	}
	slices.SortFunc(tokens, func(a, b core.StoreToken) int {
		return a.ValidSince.Compare(b.ValidSince)
	})
	return tokens, nil
}

// RevokeStoreToken is part of the [Repository] interface.
func (m *Memory) RevokeStoreToken(_ context.Context, accountID, sessionID, revokedBy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for hash, token := range m.tokens {
		if token.AccountID == accountID && token.SessionID == sessionID {
			now := time.Now().UTC()
			token.RevokedAt = &now
			token.RevokedBy = &revokedBy
			m.tokens[hash] = token
			return nil
		}
	}
	return ErrNotFound
}

// FindStoreTokenByHash is part of the [Repository] interface.
func (m *Memory) FindStoreTokenByHash(_ context.Context, hash string) (core.StoreToken, core.Account, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	token, ok := m.tokens[hash]
	if !ok {
		return core.StoreToken{}, core.Account{}, ErrNotFound
	}
	account, ok := m.accountsByID[token.AccountID]
	if !ok {
		return core.StoreToken{}, core.Account{}, ErrNotFound
	}
	return token, account, nil
}

// CreatePackage is part of the [Repository] interface.
func (m *Memory) CreatePackage(_ context.Context, pkg core.Package) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.packages[pkg.Name]; exists {
		return fmt.Errorf("cannot create package: %w", ErrConflict)
	}
	m.packages[pkg.Name] = pkg
	m.packagesByID[pkg.ID] = pkg
	return nil
}

// UpdatePackage is part of the [Repository] interface.
func (m *Memory) UpdatePackage(_ context.Context, pkg core.Package) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.packages[pkg.Name]; !exists {
		return ErrNotFound
	}
	m.packages[pkg.Name] = pkg
	m.packagesByID[pkg.ID] = pkg
	return nil
}

// DeletePackage is part of the [Repository] interface.
func (m *Memory) DeletePackage(_ context.Context, packageID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pkg, ok := m.packagesByID[packageID]
	if !ok {
		return ErrNotFound
	}
	delete(m.packagesByID, packageID)
	delete(m.packages, pkg.Name)
	return nil
}

// GetPackageByName is part of the [Repository] interface.
func (m *Memory) GetPackageByName(_ context.Context, name string) (core.Package, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pkg, ok := m.packages[name]
	if !ok {
		return core.Package{}, ErrNotFound
	}
	return pkg, nil
}

// GetPackageByID is part of the [Repository] interface.
func (m *Memory) GetPackageByID(_ context.Context, packageID string) (core.Package, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pkg, ok := m.packagesByID[packageID]
	if !ok {
		return core.Package{}, ErrNotFound
	}
	return pkg, nil
}

// ListPackagesForAccount is part of the [Repository] interface.
func (m *Memory) ListPackagesForAccount(_ context.Context, accountID string, _ bool) ([]core.Package, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []core.Package
	for _, pkg := range m.packages {
		if pkg.OwnerAccountID == accountID {
			out = append(out, pkg)
		}
	}
	return out, nil
}

// SearchPackages is part of the [Repository] interface.
func (m *Memory) SearchPackages(_ context.Context, query string) ([]core.Package, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	needle := strings.ToLower(strings.TrimSpace(query))
	var out []core.Package
	for _, pkg := range m.packages {
		if needle == "" || strings.Contains(strings.ToLower(pkg.Name), needle) {
			out = append(out, pkg)
		}
	}
	return out, nil
}

// CanViewPackage is part of the [Repository] interface.
func (m *Memory) CanViewPackage(_ context.Context, packageID, accountID string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pkg, ok := m.packagesByID[packageID]
	if !ok {
		return false, ErrNotFound
	}
	return !pkg.Private || pkg.OwnerAccountID == accountID, nil
}

// CanManagePackage is part of the [Repository] interface.
func (m *Memory) CanManagePackage(_ context.Context, packageID, accountID string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pkg, ok := m.packagesByID[packageID]
	if !ok {
		return false, ErrNotFound
	}
	return pkg.OwnerAccountID == accountID, nil
}

// CreateTracks is part of the [Repository] interface.
func (m *Memory) CreateTracks(_ context.Context, packageID string, tracks []core.Track) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pkg, ok := m.packagesByID[packageID]
	if !ok {
		return 0, ErrNotFound
	}
	created := 0
	for _, track := range tracks {
		exists := false
		for _, existing := range pkg.Tracks {
			if existing.Name == track.Name {
				exists = true
				break
			}
		}
		if exists {
			continue
		}
		pkg.Tracks = append(pkg.Tracks, track)
		created++
	}
	m.packagesByID[packageID] = pkg
	m.packages[pkg.Name] = pkg
	return created, nil
}

// ListTracks is part of the [Repository] interface.
func (m *Memory) ListTracks(_ context.Context, packageID string) ([]core.Track, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pkg, ok := m.packagesByID[packageID]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]core.Track(nil), pkg.Tracks...), nil
}

// ListTracksForPackages is part of the [Repository] interface.
func (m *Memory) ListTracksForPackages(_ context.Context, packageIDs []string) (map[string][]core.Track, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string][]core.Track, len(packageIDs))
	for _, packageID := range packageIDs {
		pkg, ok := m.packagesByID[packageID]
		if !ok {
			out[packageID] = nil
			continue
		}
		out[packageID] = append([]core.Track(nil), pkg.Tracks...)
	}
	return out, nil
}

// CreateUpload is part of the [Repository] interface.
func (m *Memory) CreateUpload(_ context.Context, upload core.Upload) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.uploads[upload.ID] = upload
	return nil
}

// GetUpload is part of the [Repository] interface.
func (m *Memory) GetUpload(_ context.Context, uploadID string) (core.Upload, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	upload, ok := m.uploads[uploadID]
	if !ok {
		return core.Upload{}, ErrNotFound
	}
	return upload, nil
}

// ApproveUpload is part of the [Repository] interface.
func (m *Memory) ApproveUpload(_ context.Context, uploadID string, revision *int, errors []core.APIError) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	upload, ok := m.uploads[uploadID]
	if !ok {
		return ErrNotFound
	}
	now := time.Now().UTC()
	upload.ApprovedAt = &now
	upload.Errors = errors
	if len(errors) == 0 {
		upload.Status = "approved"
	} else {
		upload.Status = "rejected"
	}
	upload.Revision = revision
	m.uploads[uploadID] = upload
	return nil
}

// CreateRevision is part of the [Repository] interface.
func (m *Memory) CreateRevision(_ context.Context, revision core.Revision) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revisions[revision.PackageID] = append(m.revisions[revision.PackageID], revision)
	return nil
}

// ListRevisions is part of the [Repository] interface.
func (m *Memory) ListRevisions(_ context.Context, packageID string, revision *int) ([]core.Revision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	all := m.revisions[packageID]
	if revision == nil {
		return append([]core.Revision(nil), all...), nil
	}
	for _, item := range all {
		if item.Revision == *revision {
			return []core.Revision{item}, nil
		}
	}
	return nil, ErrNotFound
}

// ListRevisionsByNumbers is part of the [Repository] interface.
func (m *Memory) ListRevisionsByNumbers(
	_ context.Context,
	packageID string,
	revisions []int,
) (map[int]core.Revision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[int]core.Revision, len(revisions))
	if len(revisions) == 0 {
		return out, nil
	}
	wanted := make(map[int]struct{}, len(revisions))
	for _, revision := range revisions {
		wanted[revision] = struct{}{}
	}
	for _, item := range m.revisions[packageID] {
		if _, ok := wanted[item.Revision]; ok {
			out[item.Revision] = item
		}
	}
	return out, nil
}

// GetRevisionByNumber is part of the [Repository] interface.
func (m *Memory) GetRevisionByNumber(_ context.Context, packageID string, revision int) (core.Revision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, item := range m.revisions[packageID] {
		if item.Revision == revision {
			return item, nil
		}
	}
	return core.Revision{}, ErrNotFound
}

// GetLatestRevision is part of the [Repository] interface.
func (m *Memory) GetLatestRevision(_ context.Context, packageID string) (core.Revision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := m.revisions[packageID]
	if len(items) == 0 {
		return core.Revision{}, ErrNotFound
	}
	return items[len(items)-1], nil
}

// UpsertResourceDefinition is part of the [Repository] interface.
func (m *Memory) UpsertResourceDefinition(
	_ context.Context,
	resource core.ResourceDefinition,
) (core.ResourceDefinition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.resourceDefs[resource.PackageID]; !ok {
		m.resourceDefs[resource.PackageID] = map[string]core.ResourceDefinition{}
	}
	m.resourceDefs[resource.PackageID][resource.Name] = resource
	return resource, nil
}

// GetResourceDefinition is part of the [Repository] interface.
func (m *Memory) GetResourceDefinition(
	_ context.Context,
	packageID, resourceName string,
) (core.ResourceDefinition, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	resources := m.resourceDefs[packageID]
	resource, ok := resources[resourceName]
	if !ok {
		return core.ResourceDefinition{}, ErrNotFound
	}
	return resource, nil
}

// ListResourceDefinitions is part of the [Repository] interface.
func (m *Memory) ListResourceDefinitions(_ context.Context, packageID string) ([]core.ResourceDefinition, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []core.ResourceDefinition
	for _, resource := range m.resourceDefs[packageID] {
		out = append(out, resource)
	}
	return out, nil
}

// CreateResourceRevision is part of the [Repository] interface.
func (m *Memory) CreateResourceRevision(_ context.Context, revision core.ResourceRevision) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resourceRevisions[revision.ResourceID] = append(m.resourceRevisions[revision.ResourceID], revision)
	return nil
}

// UpdateResourceRevision is part of the [Repository] interface.
func (m *Memory) UpdateResourceRevision(_ context.Context, revision core.ResourceRevision) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries := m.resourceRevisions[revision.ResourceID]
	for idx, item := range entries {
		if item.Revision == revision.Revision {
			entries[idx] = revision
			m.resourceRevisions[revision.ResourceID] = entries
			return nil
		}
	}
	return ErrNotFound
}

// ListResourceRevisions is part of the [Repository] interface.
func (m *Memory) ListResourceRevisions(_ context.Context, resourceID string) ([]core.ResourceRevision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]core.ResourceRevision(nil), m.resourceRevisions[resourceID]...), nil
}

// GetResourceRevision is part of the [Repository] interface.
func (m *Memory) GetResourceRevision(
	_ context.Context,
	resourceID string,
	revision int,
) (core.ResourceRevision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, item := range m.resourceRevisions[resourceID] {
		if item.Revision == revision {
			return item, nil
		}
	}
	return core.ResourceRevision{}, ErrNotFound
}

// ReplaceRelease is part of the [Repository] interface.
func (m *Memory) ReplaceRelease(_ context.Context, packageID string, release core.Release) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.releases[packageID]; !ok {
		m.releases[packageID] = map[string]core.Release{}
	}
	m.releases[packageID][release.Channel] = release
	return nil
}

// ListReleases is part of the [Repository] interface.
func (m *Memory) ListReleases(_ context.Context, packageID string) ([]core.Release, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []core.Release
	for _, release := range m.releases[packageID] {
		out = append(out, release)
	}
	return out, nil
}

// ResolveRelease is part of the [Repository] interface.
func (m *Memory) ResolveRelease(_ context.Context, packageID string, channel string) (core.Release, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	release, ok := m.releases[packageID][channel]
	if !ok {
		return core.Release{}, ErrNotFound
	}
	return release, nil
}

// ResolveDefaultRelease is part of the [Repository] interface.
func (m *Memory) ResolveDefaultRelease(_ context.Context, packageID string) (core.Release, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	releases := m.releases[packageID]
	if release, ok := releases["latest/stable"]; ok {
		return release, nil
	}
	for _, release := range releases {
		return release, nil
	}
	return core.Release{}, ErrNotFound
}
