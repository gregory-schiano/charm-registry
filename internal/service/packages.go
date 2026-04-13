package service

import (
	"context"
	"errors"
	"time"

	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
)

// RegisterPackage creates a new package owned by the authenticated account.
//
// The following errors may be returned:
// - Authorization, validation, or repository errors.
func (s *Service) RegisterPackage(
	ctx context.Context,
	identity core.Identity,
	name, packageType string,
	private bool,
) (core.Package, error) {
	if err := s.requirePermission(identity, permAccountRegisterPackage); err != nil {
		return core.Package{}, err
	}
	now := time.Now().UTC()
	pkg := core.Package{
		ID:             compactID(),
		Name:           name,
		Type:           firstNonEmpty(packageType, "charm"),
		Private:        private,
		Status:         "registered",
		OwnerAccountID: identity.Account.ID,
		DefaultTrack:   stringPtr("latest"),
		Publisher: core.Publisher{
			ID:          identity.Account.ID,
			Username:    identity.Account.Username,
			DisplayName: identity.Account.DisplayName,
			Email:       identity.Account.Email,
			Validation:  identity.Account.Validation,
		},
		Store:     s.cfg.PublicAPIURL,
		CreatedAt: now,
		UpdatedAt: now,
		Tracks: []core.Track{{
			Name:      "latest",
			CreatedAt: now,
		}},
	}
	if err := s.withRepositoryTransaction(ctx, func(repository repo.Repository) error {
		if err := repository.CreatePackage(ctx, pkg); err != nil {
			return translateRepoError(err, "package already exists")
		}
		if _, err := repository.CreateTracks(ctx, pkg.ID, pkg.Tracks); err != nil {
			return err
		}
		return repository.UpdatePackage(ctx, pkg)
	}); err != nil {
		return core.Package{}, err
	}
	return pkg, nil
}

// ListRegisteredPackages lists packages visible to the authenticated account.
//
// The following errors may be returned:
// - Authorization or repository errors.
func (s *Service) ListRegisteredPackages(
	ctx context.Context,
	identity core.Identity,
	includeCollaborations bool,
) ([]core.Package, error) {
	if err := s.requirePermission(identity, permAccountViewPackages); err != nil {
		return nil, err
	}
	var (
		packages []core.Package
		err      error
	)
	if identity.Account.IsAdmin {
		packages, err = s.repo.SearchPackages(ctx, "")
	} else {
		packages, err = s.repo.ListPackagesForAccount(ctx, identity.Account.ID, includeCollaborations)
	}
	if err != nil {
		return nil, err
	}
	return s.enrichPackages(ctx, packages)
}

// GetPackage returns package metadata for the named package.
//
// The following errors may be returned:
// - Authorization or repository lookup errors.
func (s *Service) GetPackage(
	ctx context.Context,
	identity core.Identity,
	name string,
	requireViewPermission bool,
) (core.Package, error) {
	pkg, err := s.repo.GetPackageByName(ctx, name)
	if err != nil {
		return core.Package{}, translateRepoError(err, "package not found")
	}
	if err := s.requirePackageView(ctx, identity, pkg, requireViewPermission); err != nil {
		return core.Package{}, err
	}
	return s.enrichPackage(ctx, pkg)
}

// UpdatePackage applies metadata changes to an existing package.
//
// The following errors may be returned:
// - Authorization, validation, or repository errors.
func (s *Service) UpdatePackage(
	ctx context.Context,
	identity core.Identity,
	name string,
	patch MetadataPatch,
) (core.Package, error) {
	pkg, err := s.repo.GetPackageByName(ctx, name)
	if err != nil {
		return core.Package{}, translateRepoError(err, "package not found")
	}
	if err := s.requirePackageManage(ctx, identity, pkg, permPackageManageMetadata); err != nil {
		return core.Package{}, err
	}
	if patch.Contact != nil {
		pkg.Contact = patch.Contact
	}
	if patch.DefaultTrack != nil {
		pkg.DefaultTrack = patch.DefaultTrack
	}
	if patch.Description != nil {
		pkg.Description = patch.Description
	}
	if patch.Summary != nil {
		pkg.Summary = patch.Summary
	}
	if patch.Title != nil {
		pkg.Title = patch.Title
	}
	if patch.Website != nil {
		pkg.Website = patch.Website
	}
	if patch.Private != nil {
		pkg.Private = *patch.Private
	}
	if patch.Links != nil {
		pkg.Links = patch.Links
	}
	pkg.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdatePackage(ctx, pkg); err != nil {
		return core.Package{}, err
	}
	return s.enrichPackage(ctx, pkg)
}

// UnregisterPackage deletes an empty package from the registry.
//
// The following errors may be returned:
// - Authorization, validation, or repository errors.
func (s *Service) UnregisterPackage(ctx context.Context, identity core.Identity, name string) (string, error) {
	pkg, err := s.repo.GetPackageByName(ctx, name)
	if err != nil {
		return "", translateRepoError(err, "package not found")
	}
	if err := s.requirePackageManage(ctx, identity, pkg, permPackageManage); err != nil {
		return "", err
	}
	revisions, err := s.repo.ListRevisions(ctx, pkg.ID, nil)
	if err != nil {
		return "", err
	}
	if len(revisions) > 0 {
		// The caller is authorised — the business rule (not a permission
		// violation) prevents deletion.  HTTP 400 / "invalid-request" matches
		// the Charmhub API contract; 403 is reserved for auth failures.
		return "", newError(ErrorKindInvalidRequest, "invalid-request", "cannot unregister a package with existing revisions")
	}
	if err := s.repo.DeletePackage(ctx, pkg.ID); err != nil {
		return "", err
	}
	return pkg.ID, nil
}

// Find searches packages that the caller is allowed to see.
//
// The following errors may be returned:
// - Repository lookup or package enrichment errors.
func (s *Service) Find(ctx context.Context, identity core.Identity, query string) (map[string]any, error) {
	packages, err := s.repo.SearchPackages(ctx, query)
	if err != nil {
		return nil, err
	}
	packages, err = s.enrichPackages(ctx, packages)
	if err != nil {
		return nil, err
	}
	results := make([]map[string]any, 0, len(packages))
	for _, pkg := range packages {
		if !s.canSeePackage(ctx, identity, pkg) {
			continue
		}
		item, err := s.packageFindResult(ctx, pkg)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				continue
			}
			return nil, err
		}
		results = append(results, item)
	}
	return map[string]any{"results": results}, nil
}

// Info returns Charmhub-style metadata for a package.
//
// The following errors may be returned:
// - Authorization or repository lookup errors.
func (s *Service) Info(ctx context.Context, identity core.Identity, charmName string) (infoResponse, error) {
	return s.info(ctx, identity, charmName, "")
}

func (s *Service) InfoForChannel(
	ctx context.Context,
	identity core.Identity,
	charmName, channel string,
) (infoResponse, error) {
	return s.info(ctx, identity, charmName, channel)
}

func (s *Service) info(ctx context.Context, identity core.Identity, charmName, channel string) (infoResponse, error) {
	pkg, err := s.GetPackage(ctx, identity, charmName, false)
	if err != nil {
		return infoResponse{}, err
	}
	var defaultRelease core.Release
	if channel != "" {
		defaultRelease, err = s.repo.ResolveRelease(ctx, pkg.ID, channel)
	} else {
		defaultRelease, err = s.repo.ResolveDefaultRelease(ctx, pkg.ID)
	}
	if err != nil {
		return infoResponse{}, translateRepoError(err, "no released revisions found")
	}
	defaultRevision, err := s.repo.GetRevisionByNumber(ctx, pkg.ID, defaultRelease.Revision)
	if err != nil {
		return infoResponse{}, err
	}
	resourceDefs, err := s.repo.ListResourceDefinitions(ctx, pkg.ID)
	if err != nil {
		return infoResponse{}, err
	}
	resources, err := s.resolveReleaseResources(ctx, pkg.ID, resourceDefs, defaultRelease)
	if err != nil {
		return infoResponse{}, err
	}
	defaultRevision.Resources = resources
	releases, err := s.repo.ListReleases(ctx, pkg.ID)
	if err != nil {
		return infoResponse{}, err
	}
	revisionNumbers := uniqueRevisionNumbers(releases)
	if len(revisionNumbers) == 0 {
		revisionNumbers = append(revisionNumbers, defaultRelease.Revision)
	}
	revisionCache, err := s.repo.ListRevisionsByNumbers(ctx, pkg.ID, revisionNumbers)
	if err != nil {
		return infoResponse{}, err
	}
	revisionCache[defaultRelease.Revision] = defaultRevision
	channelMap := make([]infoChannelMapItem, 0, len(releases))
	for _, release := range releases {
		rev, ok := revisionCache[release.Revision]
		if !ok {
			continue
		}
		chInfo := splitChannel(release.Channel)
		channelMap = append(channelMap, infoChannelMapItem{
			Channel: infoChannelResponse{
				Base:       release.Base,
				Name:       release.Channel,
				ReleasedAt: release.When,
				Risk:       chInfo.risk,
				Track:      chInfo.track,
			},
			Revision: revisionToInfo(rev, pkg.ID, s.cfg),
		})
	}
	channelInfo := splitChannel(defaultRelease.Channel)
	return infoResponse{
		ID:   pkg.ID,
		Name: pkg.Name,
		Type: pkg.Type,
		DefaultRelease: infoReleaseResponse{
			Channel: infoChannelResponse{
				Base:       defaultRelease.Base,
				Name:       defaultRelease.Channel,
				ReleasedAt: defaultRelease.When,
				Risk:       channelInfo.risk,
				Track:      channelInfo.track,
			},
			Resources: resources,
			Revision:  revisionToInfo(defaultRevision, pkg.ID, s.cfg),
		},
		ChannelMap: channelMap,
		Result:     packageResult(pkg),
	}, nil
}

func (s *Service) packageFindResult(ctx context.Context, pkg core.Package) (map[string]any, error) {
	defaultRelease, err := s.repo.ResolveDefaultRelease(ctx, pkg.ID)
	if err != nil {
		return nil, err
	}
	defaultRevision, err := s.repo.GetRevisionByNumber(ctx, pkg.ID, defaultRelease.Revision)
	if err != nil {
		return nil, err
	}
	channelInfo := splitChannel(defaultRelease.Channel)
	return map[string]any{
		"id":   pkg.ID,
		"name": pkg.Name,
		"type": pkg.Type,
		"default-release": map[string]any{
			"channel": map[string]any{
				"base":        defaultRelease.Base,
				"name":        defaultRelease.Channel,
				"released-at": defaultRelease.When,
				"risk":        channelInfo.risk,
				"track":       channelInfo.track,
			},
			"revision": map[string]any{
				"attributes": defaultRevision.Attributes,
				"bases":      defaultRevision.Bases,
				"created-at": defaultRevision.CreatedAt,
				"download": map[string]any{
					"hash-sha-256": defaultRevision.SHA256,
					"size":         defaultRevision.Size,
					"url":          s.charmDownloadURL(pkg.ID, defaultRevision.Revision),
				},
				"revision": defaultRevision.Revision,
				"version":  defaultRevision.Version,
			},
		},
		"result": packageResult(pkg),
	}, nil
}

func packageResult(pkg core.Package) packageResultResponse {
	website := ""
	if pkg.Website != nil {
		website = *pkg.Website
	}
	return packageResultResponse{
		BugsURL:      firstLink(pkg.Links["issues"]),
		Categories:   []any{},
		DeployableOn: []string{},
		Description:  stringValue(pkg.Description),
		License:      "",
		Links:        pkg.Links,
		Media:        pkg.Media,
		Publisher:    pkg.Publisher,
		StoreURL:     firstNonEmpty(website, pkg.Store),
		StoreURLOld:  "",
		Summary:      stringValue(pkg.Summary),
		Title:        stringValue(pkg.Title),
		Unlisted:     pkg.Private,
		UsedBy:       []any{},
		Website:      website,
	}
}

func (s *Service) enrichPackages(ctx context.Context, packages []core.Package) ([]core.Package, error) {
	if len(packages) == 0 {
		return []core.Package{}, nil
	}
	packageIDs := make([]string, 0, len(packages))
	for _, pkg := range packages {
		packageIDs = append(packageIDs, pkg.ID)
	}
	tracksByPackage, err := s.repo.ListTracksForPackages(ctx, packageIDs)
	if err != nil {
		return nil, err
	}
	out := make([]core.Package, 0, len(packages))
	for _, pkg := range packages {
		pkg.Tracks = tracksByPackage[pkg.ID]
		pkg.Store = s.cfg.PublicAPIURL + "/charms/" + pkg.Name
		out = append(out, pkg)
	}
	return out, nil
}

func (s *Service) enrichPackage(ctx context.Context, pkg core.Package) (core.Package, error) {
	tracks, err := s.repo.ListTracks(ctx, pkg.ID)
	if err != nil {
		return core.Package{}, err
	}
	pkg.Tracks = tracks
	pkg.Store = s.cfg.PublicAPIURL + "/charms/" + pkg.Name
	return pkg, nil
}

func uniqueRevisionNumbers(releases []core.Release) []int {
	seen := make(map[int]struct{}, len(releases))
	out := make([]int, 0, len(releases))
	for _, release := range releases {
		if _, ok := seen[release.Revision]; ok {
			continue
		}
		seen[release.Revision] = struct{}{}
		out = append(out, release.Revision)
	}
	return out
}
