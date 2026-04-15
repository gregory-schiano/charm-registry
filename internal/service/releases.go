package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
)

// CreateRelease assigns revisions to channels for a package.
//
// The following errors may be returned:
// - Authorization, validation, or repository errors.
func (s *Service) CreateRelease(
	ctx context.Context,
	identity core.Identity,
	charmName string,
	requests []core.Release,
) ([]core.Release, error) {
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return nil, translateRepoError(err, "package not found")
	}
	if err := s.ensurePackageNotSynchronized(ctx, pkg.Name); err != nil {
		return nil, err
	}
	if err := s.requirePackageManage(ctx, identity, pkg, permPackageManageReleases); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var released []core.Release
	for _, request := range requests {
		if request.Channel == "" {
			return nil, newError(ErrorKindInvalidRequest, "invalid-request", "channel is required")
		}
		if _, err := s.repo.GetRevisionByNumber(ctx, pkg.ID, request.Revision); err != nil {
			return nil, translateRepoError(err, "revision not found")
		}
		if err := s.validateReleaseResources(ctx, pkg.ID, request.Revision, request.Resources); err != nil {
			return nil, err
		}
		if request.When.IsZero() {
			request.When = now
		}
		if request.ID == "" {
			request.ID = uuid.NewString()
		}
		if err := s.enforceChannelRestriction(identity, request.Channel); err != nil {
			return nil, err
		}
		if err := s.repo.ReplaceRelease(ctx, pkg.ID, request); err != nil {
			return nil, err
		}
		released = append(released, request)
	}
	pkg.Status = "published"
	pkg.UpdatedAt = now
	if err := s.repo.UpdatePackage(ctx, pkg); err != nil {
		return nil, err
	}
	return released, nil
}

func (s *Service) validateReleaseResources(
	ctx context.Context,
	packageID string,
	packageRevision int,
	resources []core.ReleaseResourceRef,
) error {
	for _, ref := range resources {
		if ref.Revision == nil {
			continue
		}
		def, err := s.repo.GetResourceDefinition(ctx, packageID, ref.Name)
		if err != nil {
			return translateRepoError(err, "resource not found")
		}
		resourceRevision, err := s.repo.GetResourceRevision(ctx, def.ID, *ref.Revision)
		if err != nil {
			return translateRepoError(err, "resource revision not found")
		}
		if resourceRevision.PackageRevision != nil && *resourceRevision.PackageRevision != packageRevision {
			return newError(
				ErrorKindInvalidRequest,
				"invalid-request",
				fmt.Sprintf("resource %q revision %d is not compatible with package revision %d",
					ref.Name,
					*ref.Revision,
					packageRevision,
				),
			)
		}
	}
	return nil
}

// ListReleases returns the release map for a package.
//
// The following errors may be returned:
// - Authorization or repository lookup errors.
func (s *Service) ListReleases(ctx context.Context, identity core.Identity, charmName string) (listReleasesResponse, error) {
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return listReleasesResponse{}, translateRepoError(err, "package not found")
	}
	if err := s.requirePackageView(ctx, identity, pkg, true); err != nil {
		return listReleasesResponse{}, err
	}
	pkg, err = s.enrichPackage(ctx, pkg)
	if err != nil {
		return listReleasesResponse{}, err
	}
	releases, err := s.repo.ListReleases(ctx, pkg.ID)
	if err != nil {
		return listReleasesResponse{}, err
	}
	channelMap := make([]listReleaseChannelMapItem, 0, len(releases))
	revisionsMap, err := s.repo.ListRevisionsByNumbers(ctx, pkg.ID, uniqueRevisionNumbers(releases))
	if err != nil {
		return listReleasesResponse{}, err
	}
	for _, release := range releases {
		channelMap = append(channelMap, listReleaseChannelMapItem{
			Base:           release.Base,
			Channel:        release.Channel,
			ExpirationDate: release.ExpirationDate,
			Resources:      release.Resources,
			Revision:       release.Revision,
			When:           release.When,
		})
	}
	revisions := make([]listReleasesRevisionRow, 0, len(revisionsMap))
	for _, revision := range revisionsMap {
		revisions = append(revisions, listReleasesRevisionRow{
			Bases:     revision.Bases,
			CreatedAt: revision.CreatedAt,
			CreatedBy: revision.CreatedBy,
			Errors:    []any{},
			Revision:  revision.Revision,
			SHA384:    revision.SHA384,
			Size:      revision.Size,
			Status:    revision.Status,
			Version:   revision.Version,
		})
	}
	sort.Slice(
		revisions,
		func(i, j int) bool { return revisions[i].Revision < revisions[j].Revision },
	)
	return listReleasesResponse{
		ChannelMap:      channelMap,
		CraftChannelMap: []any{},
		Package: listReleasesPackageResponse{
			Channels: packageChannels(pkg.Tracks),
		},
		Revisions: revisions,
	}, nil
}

// CreateTracks creates tracks for a package.
//
// The following errors may be returned:
// - Authorization, validation, or repository errors.
func (s *Service) CreateTracks(
	ctx context.Context,
	identity core.Identity,
	charmName string,
	tracks []core.Track,
) (int, error) {
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return 0, translateRepoError(err, "package not found")
	}
	if err := s.ensurePackageNotSynchronized(ctx, pkg.Name); err != nil {
		return 0, err
	}
	if err := s.requirePackageManage(ctx, identity, pkg, permPackageManageMetadata); err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	for index := range tracks {
		if tracks[index].CreatedAt.IsZero() {
			tracks[index].CreatedAt = now
		}
	}
	return s.repo.CreateTracks(ctx, pkg.ID, tracks)
}

// ResolveRefresh resolves refresh actions for one or more packages.
//
// Per the Charmhub API contract, errors that apply to a single action (e.g.
// package not found, permission denied) are embedded as per-action error
// entries inside "results" rather than turning the whole request into an HTTP
// error response.  Only unexpected infrastructure errors (DB, blob storage)
// are returned as a top-level error.
func (s *Service) ResolveRefresh(ctx context.Context, identity core.Identity, request RefreshRequest) (refreshResponse, error) {
	results := make([]refreshActionResponse, 0, len(request.Actions))
	for _, action := range request.Actions {
		item, err := s.resolveRefreshAction(ctx, identity, action)
		if err != nil {
			// Unexpected infrastructure error — propagate so the API layer
			// returns a top-level 500.
			return refreshResponse{}, err
		}
		results = append(results, item)
	}
	return refreshResponse{
		ErrorList: []any{},
		Results:   results,
	}, nil
}

// resolveRefreshAction handles a single refresh action.  Service-level errors
// (not-found, forbidden, invalid-request) are returned as an error entry
// inside the result map so the caller can batch multiple actions without the
// whole request failing.  Infrastructure errors are returned as a Go error.
func (s *Service) resolveRefreshAction(
	ctx context.Context,
	identity core.Identity,
	action RefreshAction,
) (refreshActionResponse, error) {
	errorResult := func(svcErr *Error) refreshActionResponse {
		return refreshActionResponse{
			InstanceKey: action.InstanceKey,
			Result:      "error",
			Error:       &core.APIError{Code: svcErr.Code, Message: svcErr.Message},
		}
	}

	pkg, err := s.resolvePackageForRefresh(ctx, action)
	if err != nil {
		var svcErr *Error
		if errors.As(err, &svcErr) {
			return errorResult(svcErr), nil
		}
		return refreshActionResponse{}, err
	}

	if err := s.requirePackageView(ctx, identity, pkg, false); err != nil {
		var svcErr *Error
		if errors.As(err, &svcErr) {
			return errorResult(svcErr), nil
		}
		return refreshActionResponse{}, err
	}

	release, revision, resources, effectiveChannel, redirectChannel, err := s.resolveRefreshSelection(ctx, pkg, action)
	if err != nil {
		var svcErr *Error
		if errors.As(err, &svcErr) {
			return errorResult(svcErr), nil
		}
		return refreshActionResponse{}, err
	}

	revision.Resources = resources
	charm := refreshEntityResponseFrom(pkg, revision, resources, s.cfg)
	item := refreshActionResponse{
		Charm:            &charm,
		EffectiveChannel: effectiveChannel,
		ID:               pkg.ID,
		InstanceKey:      action.InstanceKey,
		Name:             pkg.Name,
		ReleasedAt:       &release.When,
		Result:           action.Action,
	}
	if redirectChannel != "" {
		item.RedirectChannel = redirectChannel
	}
	return item, nil
}

func (s *Service) resolvePackageForRefresh(ctx context.Context, action RefreshAction) (core.Package, error) {
	if action.Name != nil && *action.Name != "" {
		pkg, err := s.repo.GetPackageByName(ctx, *action.Name)
		return pkg, translateRepoError(err, "package not found")
	}
	if action.ID != nil && *action.ID != "" {
		pkg, err := s.repo.GetPackageByID(ctx, *action.ID)
		return pkg, translateRepoError(err, "package not found")
	}
	return core.Package{}, newError(ErrorKindInvalidRequest, "invalid-request", "refresh action must include id or name")
}

func (s *Service) resolveRefreshSelection(
	ctx context.Context,
	pkg core.Package,
	action RefreshAction,
) (core.Release, core.Revision, []core.ResourceRevision, string, string, error) {
	release, revision, channel, redirect, err := s.resolveReleaseAndRevision(ctx, pkg, action)
	if err != nil {
		return core.Release{}, core.Revision{}, nil, "", "", err
	}
	resourceDefs, err := s.repo.ListResourceDefinitions(ctx, pkg.ID)
	if err != nil {
		return core.Release{}, core.Revision{}, nil, "", "", err
	}
	resources, err := s.resolveReleaseResources(ctx, pkg.ID, resourceDefs, release)
	if err != nil {
		return core.Release{}, core.Revision{}, nil, "", "", err
	}
	resources, err = s.applyResourceOverrides(ctx, pkg.ID, resources, action.ResourceRevisions)
	if err != nil {
		return core.Release{}, core.Revision{}, nil, "", "", err
	}
	return release, revision, resources, channel, redirect, nil
}

func (s *Service) resolveReleaseAndRevision(
	ctx context.Context,
	pkg core.Package,
	action RefreshAction,
) (core.Release, core.Revision, string, string, error) {
	channel := normalizeChannel(channelOrDefault(action.Channel))
	redirect := channel

	if action.Revision != nil && *action.Revision > 0 {
		revision, err := s.repo.GetRevisionByNumber(ctx, pkg.ID, *action.Revision)
		if err != nil {
			return core.Release{}, core.Revision{}, "", "", translateRepoError(err, "revision not found")
		}
		release := core.Release{
			Channel:        channel,
			Revision:       revision.Revision,
			When:           revision.CreatedAt,
			ExpirationDate: nil,
		}
		return release, revision, channel, redirect, nil
	}

	if channel != "" {
		release, err := s.repo.ResolveRelease(ctx, pkg.ID, channel)
		if err != nil {
			return core.Release{}, core.Revision{}, "", "", translateRepoError(err, "release not found")
		}
		revision, err := s.repo.GetRevisionByNumber(ctx, pkg.ID, release.Revision)
		if err != nil {
			return core.Release{}, core.Revision{}, "", "", err
		}
		return release, revision, channel, redirect, nil
	}

	release, err := s.repo.ResolveDefaultRelease(ctx, pkg.ID)
	if err != nil {
		return core.Release{}, core.Revision{}, "", "", translateRepoError(err, "release not found")
	}
	revision, err := s.repo.GetRevisionByNumber(ctx, pkg.ID, release.Revision)
	if err != nil {
		return core.Release{}, core.Revision{}, "", "", err
	}
	return release, revision, release.Channel, release.Channel, nil
}

func (s *Service) applyResourceOverrides(
	ctx context.Context,
	packageID string,
	resources []core.ResourceRevision,
	overrides []core.ReleaseResourceRef,
) ([]core.ResourceRevision, error) {
	if len(overrides) == 0 {
		return resources, nil
	}
	lookup := make(map[string]int, len(overrides))
	for _, ref := range overrides {
		if ref.Revision != nil {
			lookup[ref.Name] = *ref.Revision
		}
	}
	resolvedDefs := make(map[string]core.ResourceDefinition, len(lookup))
	for idx, item := range resources {
		revisionNumber, ok := lookup[item.Name]
		if !ok {
			continue
		}
		def, err := s.repo.GetResourceDefinition(ctx, packageID, item.Name)
		if err != nil {
			return nil, err
		}
		resolvedDefs[item.Name] = def
		overrideItem, err := s.repo.GetResourceRevision(ctx, def.ID, revisionNumber)
		if err != nil {
			return nil, err
		}
		resources[idx] = s.attachResourceDownload(packageID, overrideItem)
		delete(lookup, item.Name)
	}
	for resourceName, revisionNumber := range lookup {
		def, ok := resolvedDefs[resourceName]
		if !ok {
			var err error
			def, err = s.repo.GetResourceDefinition(ctx, packageID, resourceName)
			if err != nil {
				return nil, err
			}
		}
		overrideItem, err := s.repo.GetResourceRevision(ctx, def.ID, revisionNumber)
		if err != nil {
			return nil, err
		}
		resources = append(resources, s.attachResourceDownload(packageID, overrideItem))
	}
	return resources, nil
}

func (s *Service) resolveReleaseResources(
	ctx context.Context,
	packageID string,
	resourceDefs []core.ResourceDefinition,
	release core.Release,
) ([]core.ResourceRevision, error) {
	var out []core.ResourceRevision
	for _, ref := range release.Resources {
		def, err := s.repo.GetResourceDefinition(ctx, packageID, ref.Name)
		if err != nil {
			return nil, err
		}
		if ref.Revision == nil {
			continue
		}
		revision, err := s.repo.GetResourceRevision(ctx, def.ID, *ref.Revision)
		if err != nil {
			return nil, err
		}
		out = append(out, s.attachResourceDownload(packageID, revision))
	}
	return out, nil
}

func refreshEntityResponseFrom(
	pkg core.Package,
	revision core.Revision,
	resources []core.ResourceRevision,
	cfg config.Config,
) refreshEntityResponse {
	return refreshEntityResponse{
		CreatedAt: revision.CreatedAt,
		Download: core.Download{
			HashSHA256: revision.SHA256,
			Size:       revision.Size,
			URL: cfg.PublicAPIURL + "/api/v1/charms/download/" + pkg.ID + "_" + fmt.Sprintf(
				"%d",
				revision.Revision,
			) + ".charm",
		},
		ID:           pkg.ID,
		License:      "",
		Name:         pkg.Name,
		Publisher:    pkg.Publisher,
		Resources:    resources,
		Revision:     revision.Revision,
		Summary:      stringValue(pkg.Summary),
		Type:         pkg.Type,
		Version:      revision.Version,
		Bases:        revision.Bases,
		ConfigYAML:   revision.ConfigYAML,
		MetadataYAML: revision.MetadataYAML,
	}
}

type channelParts struct {
	track string
	risk  string
}

func splitChannel(channel string) channelParts {
	parts := strings.Split(channel, "/")
	if len(parts) == 1 {
		return channelParts{track: "latest", risk: parts[0]}
	}
	return channelParts{track: parts[0], risk: parts[1]}
}

// normalizeChannel expands a bare risk name (e.g. "stable") to its fully
// qualified form ("latest/stable"). Fully-qualified channels ("2.0/stable")
// are returned unchanged. An empty string is returned as-is.
func normalizeChannel(channel string) string {
	if channel == "" || strings.Contains(channel, "/") {
		return channel
	}
	return "latest/" + channel
}

func packageChannels(tracks []core.Track) []releaseChannelDescriptorResponse {
	if len(tracks) == 0 {
		tracks = []core.Track{{Name: "latest"}}
	}
	channels := make([]releaseChannelDescriptorResponse, 0, len(tracks)*4)
	for _, track := range tracks {
		channels = append(channels,
			channelDescriptor(track.Name, "stable", nil),
			channelDescriptor(track.Name, "candidate", stringPtr(track.Name+"/stable")),
			channelDescriptor(track.Name, "beta", stringPtr(track.Name+"/candidate")),
			channelDescriptor(track.Name, "edge", stringPtr(track.Name+"/beta")),
		)
	}
	return channels
}

func channelDescriptor(track, risk string, fallback *string) releaseChannelDescriptorResponse {
	return releaseChannelDescriptorResponse{
		Name:     track + "/" + risk,
		Track:    track,
		Risk:     risk,
		Branch:   nil,
		Fallback: fallback,
	}
}
