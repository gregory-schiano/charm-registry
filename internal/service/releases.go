package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
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
	if err := s.requireAuth(identity); err != nil {
		return nil, err
	}
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return nil, translateRepoError(err, messagePackageNotFound)
	}
	if err := s.ensurePackageNotSynchronized(ctx, pkg.Name); err != nil {
		return nil, err
	}
	if err := s.requirePackageManage(ctx, identity, pkg, permPackageManageReleases); err != nil {
		return nil, err
	}
	now := s.now()
	released := make([]core.Release, 0, len(requests))
	for _, request := range requests {
		created, err := s.createRelease(ctx, identity, pkg.ID, request, now)
		if err != nil {
			return nil, err
		}
		released = append(released, created)
	}
	pkg.Status = "published"
	pkg.UpdatedAt = now
	if err := s.repo.UpdatePackage(ctx, pkg); err != nil {
		return nil, err
	}
	return released, nil
}

func (s *Service) createRelease(
	ctx context.Context,
	identity core.Identity,
	packageID string,
	request core.Release,
	now time.Time,
) (core.Release, error) {
	if request.Channel == "" {
		return core.Release{}, newError(ErrorKindInvalidRequest, "invalid-request", "channel is required")
	}
	if _, err := s.repo.GetRevisionByNumber(ctx, packageID, request.Revision); err != nil {
		return core.Release{}, translateRepoError(err, messageRevisionNotFound)
	}
	if err := s.validateReleaseResources(ctx, packageID, request.Revision, request.Resources); err != nil {
		return core.Release{}, err
	}
	if request.When.IsZero() {
		request.When = now
	}
	if request.ID == "" {
		request.ID = uuid.NewString()
	}
	validated, err := core.NewRelease(request)
	if err != nil {
		return core.Release{}, newError(ErrorKindInvalidRequest, "invalid-request", err.Error())
	}
	request = validated
	if err := s.enforceChannelRestriction(identity, request.Channel); err != nil {
		return core.Release{}, err
	}
	if err := s.repo.ReplaceRelease(ctx, packageID, request); err != nil {
		return core.Release{}, err
	}
	slog.InfoContext(ctx, "release published",
		"package_id", packageID,
		"channel", request.Channel,
		"revision", request.Revision,
		"resource_count", len(request.Resources),
		"account_id", identity.Account.ID,
	)
	return request, nil
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
			return translateRepoError(err, messageResourceNotFound)
		}
		resourceRevision, err := s.repo.GetResourceRevision(ctx, def.ID, *ref.Revision)
		if err != nil {
			return translateRepoError(err, messageResourceRevisionNotFound)
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
		return listReleasesResponse{}, translateRepoError(err, messagePackageNotFound)
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
	if err := s.requireAuth(identity); err != nil {
		return 0, err
	}
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return 0, translateRepoError(err, messagePackageNotFound)
	}
	if err := s.ensurePackageNotSynchronized(ctx, pkg.Name); err != nil {
		return 0, err
	}
	if err := s.requirePackageManage(ctx, identity, pkg, permPackageManageMetadata); err != nil {
		return 0, err
	}
	now := s.now()
	for index := range tracks {
		if tracks[index].CreatedAt.IsZero() {
			tracks[index].CreatedAt = now
		}
	}
	created, err := s.repo.CreateTracks(ctx, pkg.ID, tracks)
	if err != nil {
		return 0, err
	}
	slog.InfoContext(ctx, "tracks created",
		"package", pkg.Name,
		"package_id", pkg.ID,
		"requested_count", len(tracks),
		"created_count", created,
		"account_id", identity.Account.ID,
	)
	return created, nil
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
		if item.Error != nil {
			slog.InfoContext(ctx, "refresh action failed",
				"action", action.Action,
				"instance_key", action.InstanceKey,
				"name", stringValue(action.Name),
				"id", stringValue(action.ID),
				"revision", intValue(action.Revision),
				"channel", stringValue(action.Channel),
				"base", action.Base,
				"error_code", item.Error.Code,
				"error_message", item.Error.Message,
			)
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
	charm := s.refreshEntityResponseFrom(pkg, revision, resources)
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
		return pkg, translateRepoError(err, messagePackageNotFound)
	}
	if action.ID != nil && *action.ID != "" {
		pkg, err := s.repo.GetPackageByID(ctx, *action.ID)
		return pkg, translateRepoError(err, messagePackageNotFound)
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
	requestedChannel := channelOrDefault(action.Channel)
	channel := normalizeChannel(requestedChannel)
	redirect := channel
	base := effectiveRefreshBase(action.Base)

	if action.Revision != nil && *action.Revision > 0 {
		revision, err := s.repo.GetRevisionByNumber(ctx, pkg.ID, *action.Revision)
		if err != nil {
			return core.Release{}, core.Revision{}, "", "", translateRepoError(err, messageRevisionNotFound)
		}
		release := core.Release{
			Channel:        channel,
			Revision:       revision.Revision,
			When:           revision.CreatedAt,
			ExpirationDate: nil,
		}
		slog.DebugContext(ctx, "refresh resolved explicit revision",
			"package", pkg.Name,
			"package_id", pkg.ID,
			"revision", revision.Revision,
			"requested_channel", requestedChannel,
			"normalized_channel", channel,
		)
		return release, revision, channel, redirect, nil
	}

	if channel != "" {
		release, resolvedChannel, err := s.resolveReleaseForActionChannel(ctx, pkg, channel, requestedChannel, base)
		if err != nil {
			slog.InfoContext(ctx, "refresh release resolution failed",
				"package", pkg.Name,
				"requested_channel", requestedChannel,
				"normalized_channel", channel,
				"default_track", stringValue(pkg.DefaultTrack),
				"base", action.Base,
				"effective_base", base,
				"error", err,
			)
			return core.Release{}, core.Revision{}, "", "", translateRepoError(err, messageReleaseNotFound)
		}
		revision, err := s.repo.GetRevisionByNumber(ctx, pkg.ID, release.Revision)
		if err != nil {
			return core.Release{}, core.Revision{}, "", "", err
		}
		slog.DebugContext(ctx, "refresh resolved channel",
			"package", pkg.Name,
			"package_id", pkg.ID,
			"requested_channel", requestedChannel,
			"resolved_channel", resolvedChannel,
			"revision", release.Revision,
			"base", release.Base,
		)
		return release, revision, resolvedChannel, redirect, nil
	}

	release, err := s.repo.ResolveDefaultRelease(ctx, pkg.ID)
	if err != nil {
		return core.Release{}, core.Revision{}, "", "", translateRepoError(err, messageReleaseNotFound)
	}
	revision, err := s.repo.GetRevisionByNumber(ctx, pkg.ID, release.Revision)
	if err != nil {
		return core.Release{}, core.Revision{}, "", "", err
	}
	slog.DebugContext(ctx, "refresh resolved default release",
		"package", pkg.Name,
		"package_id", pkg.ID,
		"channel", release.Channel,
		"revision", release.Revision,
		"base", release.Base,
	)
	return release, revision, release.Channel, release.Channel, nil
}

func (s *Service) resolveReleaseForActionChannel(
	ctx context.Context,
	pkg core.Package,
	channel, requestedChannel string,
	base *core.Base,
) (core.Release, string, error) {
	release, err := s.resolveReleaseForChannel(ctx, pkg.ID, channel, base)
	if err == nil {
		return release, channel, nil
	}
	if !isRiskOnlyChannel(requestedChannel) || pkg.DefaultTrack == nil || *pkg.DefaultTrack == "" {
		return core.Release{}, "", err
	}
	defaultTrackChannel := *pkg.DefaultTrack + "/" + requestedChannel
	if defaultTrackChannel == channel {
		return core.Release{}, "", err
	}
	release, fallbackErr := s.resolveReleaseForChannel(ctx, pkg.ID, defaultTrackChannel, base)
	if fallbackErr != nil {
		return core.Release{}, "", err
	}
	return release, defaultTrackChannel, nil
}

func (s *Service) resolveReleaseForChannel(
	ctx context.Context,
	packageID, channel string,
	base *core.Base,
) (core.Release, error) {
	if base != nil {
		return s.resolveReleaseForBaseConstraint(ctx, packageID, channel, *base)
	}
	return s.repo.ResolveRelease(ctx, packageID, channel)
}

func (s *Service) resolveReleaseForBaseConstraint(
	ctx context.Context,
	packageID, channel string,
	base core.Base,
) (core.Release, error) {
	if hasConcreteBase(base) {
		release, err := s.repo.ResolveReleaseForBase(ctx, packageID, channel, base)
		if err == nil {
			return release, nil
		}
	}

	releases, err := s.repo.ListReleases(ctx, packageID)
	if err != nil {
		return core.Release{}, err
	}
	bestVariant, variantOK, bestGeneric, genericOK, err := bestReleaseForBaseConstraint(ctx, releases, channel, base)
	if err != nil {
		return core.Release{}, err
	}
	if variantOK {
		return bestVariant, nil
	}
	if genericOK {
		return bestGeneric, nil
	}
	return core.Release{}, repo.ErrNotFound
}

func bestReleaseForBaseConstraint(
	ctx context.Context,
	releases []core.Release,
	channel string,
	base core.Base,
) (core.Release, bool, core.Release, bool, error) {
	var (
		bestVariant core.Release
		variantOK   bool
		bestGeneric core.Release
		genericOK   bool
	)
	for _, release := range releases {
		if err := checkContext(ctx); err != nil {
			return core.Release{}, false, core.Release{}, false, err
		}
		if release.Channel != channel {
			continue
		}
		if release.Base == nil {
			if !genericOK || release.When.After(bestGeneric.When) {
				bestGeneric = release
				genericOK = true
			}
			continue
		}
		if releaseMatchesBaseConstraint(*release.Base, base) &&
			(!variantOK || release.When.After(bestVariant.When)) {
			bestVariant = release
			variantOK = true
		}
	}
	return bestVariant, variantOK, bestGeneric, genericOK, nil
}

func effectiveRefreshBase(base *core.Base) *core.Base {
	if base == nil {
		return nil
	}
	name := strings.TrimSpace(base.Name)
	channel := strings.TrimSpace(base.Channel)
	architecture := strings.TrimSpace(base.Architecture)
	if name == "" || channel == "" || strings.EqualFold(name, "NA") || strings.EqualFold(channel, "NA") {
		if architecture == "" {
			return nil
		}
		return &core.Base{Architecture: architecture}
	}
	return &core.Base{
		Name:         name,
		Channel:      channel,
		Architecture: architecture,
	}
}

func hasConcreteBase(base core.Base) bool {
	return strings.TrimSpace(base.Name) != "" &&
		strings.TrimSpace(base.Channel) != "" &&
		!strings.EqualFold(base.Name, "NA") &&
		!strings.EqualFold(base.Channel, "NA")
}

func releaseMatchesBaseConstraint(releaseBase, requested core.Base) bool {
	if hasConcreteBase(requested) &&
		(!strings.EqualFold(releaseBase.Name, requested.Name) ||
			!strings.EqualFold(releaseBase.Channel, requested.Channel)) {
		return false
	}
	architecture := strings.TrimSpace(requested.Architecture)
	if architecture == "" {
		return true
	}
	if strings.EqualFold(releaseBase.Architecture, architecture) {
		return true
	}
	for _, item := range releaseBase.Architectures {
		if strings.EqualFold(item, architecture) {
			return true
		}
	}
	return false
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

func (s *Service) refreshEntityResponseFrom(
	pkg core.Package,
	revision core.Revision,
	resources []core.ResourceRevision,
) refreshEntityResponse {
	return refreshEntityResponse{
		CreatedAt: revision.CreatedAt,
		Download: core.Download{
			HashSHA256: revision.SHA256,
			Size:       revision.Size,
			URL:        s.charmDownloadURL(pkg.ID, revision.Revision),
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

func isRiskOnlyChannel(channel string) bool {
	return channel != "" && !strings.Contains(channel, "/")
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
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
