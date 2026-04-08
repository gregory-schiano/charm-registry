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

// Release assigns revisions to channels for a package.
//
// The following errors may be returned:
// - Authorization, validation, or repository errors.
func (s *Service) Release(
	ctx context.Context,
	identity core.Identity,
	charmName string,
	requests []core.Release,
) ([]core.Release, error) {
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return nil, translateRepoError(err, "package not found")
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
func (s *Service) ListReleases(ctx context.Context, identity core.Identity, charmName string) (map[string]any, error) {
	pkg, err := s.repo.GetPackageByName(ctx, charmName)
	if err != nil {
		return nil, translateRepoError(err, "package not found")
	}
	if err := s.requirePackageView(ctx, identity, pkg, true); err != nil {
		return nil, err
	}
	pkg, err = s.enrichPackage(ctx, pkg)
	if err != nil {
		return nil, err
	}
	releases, err := s.repo.ListReleases(ctx, pkg.ID)
	if err != nil {
		return nil, err
	}
	channelMap := make([]map[string]any, 0, len(releases))
	revisionsMap, err := s.repo.ListRevisionsByNumbers(ctx, pkg.ID, uniqueRevisionNumbers(releases))
	if err != nil {
		return nil, err
	}
	for _, release := range releases {
		channelMap = append(channelMap, map[string]any{
			"base":            release.Base,
			"channel":         release.Channel,
			"expiration-date": release.ExpirationDate,
			"resources":       release.Resources,
			"revision":        release.Revision,
			"when":            release.When,
		})
	}
	var revisions []map[string]any
	for _, revision := range revisionsMap {
		revisions = append(revisions, map[string]any{
			"bases":      revision.Bases,
			"created-at": revision.CreatedAt,
			"created-by": revision.CreatedBy,
			"errors":     []any{},
			"revision":   revision.Revision,
			"sha3-384":   revision.SHA384,
			"size":       revision.Size,
			"status":     revision.Status,
			"version":    revision.Version,
		})
	}
	sort.Slice(
		revisions,
		func(i, j int) bool { return revisions[i]["revision"].(int) < revisions[j]["revision"].(int) },
	)
	return map[string]any{
		"channel-map":       channelMap,
		"craft-channel-map": []any{},
		"package": map[string]any{
			"channels": packageChannels(pkg.Tracks),
		},
		"revisions": revisions,
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

// Refresh resolves refresh actions for one or more packages.
//
// Per the Charmhub API contract, errors that apply to a single action (e.g.
// package not found, permission denied) are embedded as per-action error
// entries inside "results" rather than turning the whole request into an HTTP
// error response.  Only unexpected infrastructure errors (DB, blob storage)
// are returned as a top-level error.
func (s *Service) Refresh(ctx context.Context, identity core.Identity, request RefreshRequest) (map[string]any, error) {
	results := make([]map[string]any, 0, len(request.Actions))
	for _, action := range request.Actions {
		item, err := s.resolveRefreshAction(ctx, identity, action)
		if err != nil {
			// Unexpected infrastructure error — propagate so the API layer
			// returns a top-level 500.
			return nil, err
		}
		results = append(results, item)
	}
	return map[string]any{
		"error-list": []any{},
		"results":    results,
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
) (map[string]any, error) {
	errorResult := func(svcErr *Error) map[string]any {
		return map[string]any{
			"instance-key": action.InstanceKey,
			"result":       "error",
			"error":        core.APIError{Code: svcErr.Code, Message: svcErr.Message},
		}
	}

	pkg, err := s.resolvePackageForRefresh(ctx, action)
	if err != nil {
		var svcErr *Error
		if errors.As(err, &svcErr) {
			return errorResult(svcErr), nil
		}
		return nil, err
	}

	if err := s.requirePackageView(ctx, identity, pkg, false); err != nil {
		var svcErr *Error
		if errors.As(err, &svcErr) {
			return errorResult(svcErr), nil
		}
		return nil, err
	}

	release, revision, resources, effectiveChannel, redirectChannel, err := s.resolveRefreshSelection(ctx, pkg, action)
	if err != nil {
		var svcErr *Error
		if errors.As(err, &svcErr) {
			return errorResult(svcErr), nil
		}
		return nil, err
	}

	revision.Resources = resources
	item := map[string]any{
		"charm":             refreshEntity(pkg, revision, resources, s.cfg),
		"effective-channel": effectiveChannel,
		"id":                pkg.ID,
		"instance-key":      action.InstanceKey,
		"name":              pkg.Name,
		"released-at":       release.When,
		"result":            action.Action,
	}
	if redirectChannel != "" {
		item["redirect-channel"] = redirectChannel
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
	for idx, item := range resources {
		revisionNumber, ok := lookup[item.Name]
		if !ok {
			continue
		}
		def, err := s.repo.GetResourceDefinition(ctx, packageID, item.Name)
		if err != nil {
			return nil, err
		}
		overrideItem, err := s.repo.GetResourceRevision(ctx, def.ID, revisionNumber)
		if err != nil {
			return nil, err
		}
		resources[idx] = s.attachResourceDownload(packageID, overrideItem)
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

func refreshEntity(
	pkg core.Package,
	revision core.Revision,
	resources []core.ResourceRevision,
	cfg config.Config,
) map[string]any {
	return map[string]any{
		"created-at": revision.CreatedAt,
		"download": map[string]any{
			"hash-sha-256": revision.SHA256,
			"size":         revision.Size,
			"url": cfg.PublicAPIURL + "/api/v1/charms/download/" + pkg.ID + "_" + fmt.Sprintf(
				"%d",
				revision.Revision,
			) + ".charm",
		},
		"id":            pkg.ID,
		"license":       "",
		"name":          pkg.Name,
		"publisher":     pkg.Publisher,
		"resources":     releaseResourcesToDownloads(pkg.ID, resources, cfg),
		"revision":      revision.Revision,
		"summary":       stringValue(pkg.Summary),
		"type":          pkg.Type,
		"version":       revision.Version,
		"bases":         revision.Bases,
		"config-yaml":   revision.ConfigYAML,
		"metadata-yaml": revision.MetadataYAML,
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

func packageChannels(tracks []core.Track) []map[string]any {
	if len(tracks) == 0 {
		tracks = []core.Track{{Name: "latest"}}
	}
	var channels []map[string]any
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

func channelDescriptor(track, risk string, fallback *string) map[string]any {
	return map[string]any{
		"name":     track + "/" + risk,
		"track":    track,
		"risk":     risk,
		"branch":   nil,
		"fallback": fallback,
	}
}
