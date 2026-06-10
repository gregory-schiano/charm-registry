package registrysync

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	stdsync "sync"

	"github.com/google/uuid"

	"github.com/gschiano/charm-registry/internal/charm"
	charmhubclient "github.com/gschiano/charm-registry/internal/charmhub"
	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
	"github.com/gschiano/charm-registry/internal/service"
)

// charmhubResourceSyncConcurrency bounds how many resource artifacts are
// downloaded and uploaded in parallel during a package sync.
const charmhubResourceSyncConcurrency = 4

func (s *Service) reconcilePackage(ctx context.Context, packageName string) error {
	slog.InfoContext(ctx, "charmhub sync reconciliation started", "package", packageName)
	rules, err := s.syncRules.ListCharmhubSyncRulesByPackageName(ctx, packageName)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		slog.DebugContext(ctx, "charmhub sync has no rules; cleaning package", "package", packageName)
		return s.cleanupSyncedPackage(ctx, packageName)
	}
	slices.SortFunc(rules, func(left, right core.CharmhubSyncRule) int {
		return strings.Compare(left.Track, right.Track)
	})
	activeRules, deletingRules := partitionCharmhubSyncRules(rules)

	pkg, err := s.repo.GetPackageByName(ctx, packageName)
	if errors.Is(err, repo.ErrNotFound) {
		pkg = core.Package{}
	} else if err != nil {
		return err
	} else if !isCharmhubManagedPackage(pkg) {
		return service.NewError(
			service.ErrorKindConflict,
			"package-exists",
			"cannot synchronize a package that already exists outside Charmhub synchronization",
		)
	}

	if len(activeRules) == 0 {
		slog.InfoContext(ctx, "charmhub sync deleting package after final rule removal",
			"package", packageName,
			"deleting_rule_count", len(deletingRules),
		)
		return s.reconcileDeletingPackage(ctx, packageName, deletingRules)
	}

	trackCache := newPackageTrackCache()
	var errs []error
	for _, rule := range activeRules {
		slog.InfoContext(ctx, "charmhub sync track started",
			"package", packageName,
			"track", rule.Track,
			"base_count", len(rule.Bases),
			"architecture_count", len(rule.Architectures),
		)
		rule = s.ruleWithStatus(rule, charmhubSyncStatusRunning, nil)
		if updateErr := s.syncRules.UpdateCharmhubSyncRule(ctx, rule); updateErr != nil {
			errs = append(errs, updateErr)
			continue
		}

		updatedPkg, syncErr := s.syncCharmhubTrack(ctx, pkg, activeRules, rule, trackCache)
		if syncErr != nil {
			slog.InfoContext(ctx, "charmhub sync track failed",
				"package", packageName,
				"track", rule.Track,
				"error", syncErr,
			)
			errs = append(errs, syncErr)
			rule = s.ruleWithStatus(rule, charmhubSyncStatusError, syncErr)
			if updateErr := s.syncRules.UpdateCharmhubSyncRule(ctx, rule); updateErr != nil {
				errs = append(errs, updateErr)
			}
			continue
		}
		pkg = updatedPkg
		slog.InfoContext(ctx, "charmhub sync track completed",
			"package", packageName,
			"track", rule.Track,
			"package_id", pkg.ID,
		)
		rule = s.ruleWithStatus(rule, charmhubSyncStatusOK, nil)
		if updateErr := s.syncRules.UpdateCharmhubSyncRule(ctx, rule); updateErr != nil {
			errs = append(errs, updateErr)
		}
	}

	if pruneErr := s.pruneDeletedRules(ctx, pkg, activeRules, deletingRules, trackCache); pruneErr != nil {
		errs = append(errs, pruneErr)
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	slog.InfoContext(ctx, "charmhub sync reconciliation completed",
		"package", packageName,
		"active_rule_count", len(activeRules),
		"deleting_rule_count", len(deletingRules),
	)
	return nil
}

func (s *Service) reconcileDeletingPackage(
	ctx context.Context,
	packageName string,
	deletingRules []core.CharmhubSyncRule,
) error {
	if err := s.markCharmhubSyncRules(ctx, deletingRules, charmhubSyncStatusDeleting, nil); err != nil {
		return err
	}
	if err := s.cleanupSyncedPackage(ctx, packageName); err != nil {
		markErr := s.markCharmhubSyncRules(ctx, deletingRules, charmhubSyncStatusDeleteError, err)
		return errors.Join(err, markErr)
	}
	return s.deleteCharmhubSyncRules(ctx, deletingRules)
}

func (s *Service) pruneDeletedRules(
	ctx context.Context,
	pkg core.Package,
	activeRules []core.CharmhubSyncRule,
	deletingRules []core.CharmhubSyncRule,
	trackCache *packageTrackCache,
) error {
	if pkg.ID == "" || len(deletingRules) == 0 {
		return nil
	}
	var errs []error
	if err := s.markCharmhubSyncRules(ctx, deletingRules, charmhubSyncStatusDeleting, nil); err != nil {
		errs = append(errs, err)
	}
	if pruneErr := s.pruneSyncedPackage(ctx, pkg, activeRules, trackCache); pruneErr != nil {
		errs = append(errs, pruneErr)
		if markErr := s.markCharmhubSyncRules(ctx, deletingRules, charmhubSyncStatusDeleteError, pruneErr); markErr != nil {
			errs = append(errs, markErr)
		}
		return errors.Join(errs...)
	}
	if deleteErr := s.deleteCharmhubSyncRules(ctx, deletingRules); deleteErr != nil {
		errs = append(errs, deleteErr)
	}
	return errors.Join(errs...)
}

type packageTrackCache struct {
	byPackage map[string][]core.Track
}

func newPackageTrackCache() *packageTrackCache {
	return &packageTrackCache{byPackage: map[string][]core.Track{}}
}

func (c *packageTrackCache) tracks(ctx context.Context, repository repo.PackageRepo, packageID string) ([]core.Track, error) {
	if tracks, ok := c.byPackage[packageID]; ok {
		return slices.Clone(tracks), nil
	}
	tracks, err := repository.ListTracks(ctx, packageID)
	if err != nil {
		return nil, err
	}
	c.remember(packageID, tracks)
	return slices.Clone(tracks), nil
}

func (c *packageTrackCache) remember(packageID string, tracks []core.Track) {
	c.byPackage[packageID] = slices.Clone(tracks)
}

func (c *packageTrackCache) add(packageID string, track core.Track) {
	tracks := c.byPackage[packageID]
	for _, item := range tracks {
		if item.Name == track.Name {
			return
		}
	}
	c.byPackage[packageID] = append(slices.Clone(tracks), track)
}

func (c *packageTrackCache) remove(packageID, trackName string) {
	tracks, ok := c.byPackage[packageID]
	if !ok {
		return
	}
	c.byPackage[packageID] = slices.DeleteFunc(slices.Clone(tracks), func(track core.Track) bool {
		return track.Name == trackName
	})
}

func partitionCharmhubSyncRules(rules []core.CharmhubSyncRule) ([]core.CharmhubSyncRule, []core.CharmhubSyncRule) {
	active := make([]core.CharmhubSyncRule, 0, len(rules))
	deleting := make([]core.CharmhubSyncRule, 0, len(rules))
	for _, rule := range rules {
		if isDeletingCharmhubSyncRule(rule) {
			deleting = append(deleting, rule)
			continue
		}
		active = append(active, rule)
	}
	return active, deleting
}

func isDeletingCharmhubSyncRule(rule core.CharmhubSyncRule) bool {
	return rule.LastSyncStatus == charmhubSyncStatusDeleting || rule.LastSyncStatus == charmhubSyncStatusDeleteError
}

func (s *Service) markCharmhubSyncRules(
	ctx context.Context,
	rules []core.CharmhubSyncRule,
	status string,
	syncErr error,
) error {
	var errs []error
	for _, rule := range rules {
		rule = s.ruleWithStatus(rule, status, syncErr)
		if err := s.syncRules.UpdateCharmhubSyncRule(ctx, rule); err != nil && !errors.Is(err, repo.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) deleteCharmhubSyncRules(ctx context.Context, rules []core.CharmhubSyncRule) error {
	var errs []error
	for _, rule := range rules {
		if err := s.syncRules.DeleteCharmhubSyncRule(ctx, rule.PackageName, rule.Track); err != nil &&
			!errors.Is(err, repo.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) ruleWithStatus(rule core.CharmhubSyncRule, status string, syncErr error) core.CharmhubSyncRule {
	now := s.now()
	rule.UpdatedAt = now
	rule.LastSyncStatus = status
	if status == charmhubSyncStatusRunning {
		rule.LastSyncStartedAt = timePtr(now)
		rule.LastSyncError = nil
		return rule
	}
	if rule.LastSyncStartedAt == nil {
		rule.LastSyncStartedAt = timePtr(now)
	}
	rule.LastSyncFinishedAt = timePtr(now)
	if syncErr != nil {
		message := syncErr.Error()
		rule.LastSyncError = &message
	} else {
		rule.LastSyncError = nil
	}
	return rule
}

func (s *Service) syncCharmhubTrack(
	ctx context.Context,
	pkg core.Package,
	allRules []core.CharmhubSyncRule,
	rule core.CharmhubSyncRule,
	trackCache *packageTrackCache,
) (core.Package, error) {
	present, err := s.loadTrackChannelStates(ctx, rule)
	if err != nil {
		return pkg, err
	}
	slog.DebugContext(ctx, "charmhub sync selected channel variants",
		"package", rule.PackageName,
		"track", rule.Track,
		"variant_count", len(present),
	)
	if len(present) == 0 {
		return s.pruneEmptyTrack(ctx, pkg, rule.Track)
	}

	syncIdentity, err := s.charmhubSyncIdentity(ctx)
	if err != nil {
		return core.Package{}, err
	}
	pkg, err = s.ensureSyncedPackage(ctx, pkg, rule, allRules, syncIdentity, present[0].info, trackCache)
	if err != nil {
		return core.Package{}, err
	}

	pkg.DefaultTrack = stringPtr(defaultTrackForRules(allRules))
	pkg, err = s.syncTrackReleases(ctx, pkg, syncIdentity.Account.ID, present, trackCache)
	if err != nil {
		return core.Package{}, err
	}
	if err := s.removeStaleTrackReleases(ctx, pkg.ID, rule.Track, present); err != nil {
		return core.Package{}, err
	}
	return s.persistSyncedPackage(ctx, pkg, trackCache)
}

type channelState struct {
	channel string
	info    charmhubclient.PackageChannel
}

func (s *Service) loadTrackChannelStates(ctx context.Context, rule core.CharmhubSyncRule) ([]channelState, error) {
	upstreamInfo, err := s.charmhub.GetInfo(ctx, rule.PackageName)
	if err != nil {
		return nil, err
	}

	states := make([]channelState, 0)
	for _, item := range selectCharmhubSyncVariants(upstreamInfo.ChannelMap, rule) {
		state, err := s.refreshChannelState(ctx, rule, upstreamInfo, item)
		if err != nil {
			return nil, err
		}
		if state != nil {
			states = append(states, *state)
		}
	}
	return states, nil
}

func (s *Service) refreshChannelState(
	ctx context.Context,
	rule core.CharmhubSyncRule,
	upstreamInfo charmhubclient.PackageChannel,
	item charmhubclient.ChannelMap,
) (*channelState, error) {
	if item.Channel.Base == nil {
		return nil, nil
	}

	channel := charmhubSyncChannelName(item.Channel)
	info, err := s.charmhub.RefreshChannel(ctx, rule.PackageName, channel, *item.Channel.Base)
	if err != nil {
		return nil, err
	}
	info.ID = core.FirstNonEmpty(info.ID, upstreamInfo.ID)
	info.Name = core.FirstNonEmpty(info.Name, upstreamInfo.Name, rule.PackageName)
	info.Result = upstreamInfo.Result
	if !info.DefaultRelease.Present() {
		return nil, nil
	}
	return &channelState{channel: channel, info: info}, nil
}

func (s *Service) pruneEmptyTrack(ctx context.Context, pkg core.Package, track string) (core.Package, error) {
	if pkg.ID == "" {
		return pkg, nil
	}
	for _, risk := range charmhubSyncRisks {
		_ = s.repo.DeleteRelease(ctx, pkg.ID, track+"/"+risk)
	}
	_ = s.repo.DeleteTrack(ctx, pkg.ID, track)
	return pkg, nil
}

func (s *Service) ensureSyncedPackage(
	ctx context.Context,
	pkg core.Package,
	rule core.CharmhubSyncRule,
	allRules []core.CharmhubSyncRule,
	syncIdentity core.Identity,
	firstInfo charmhubclient.PackageChannel,
	trackCache *packageTrackCache,
) (core.Package, error) {
	if pkg.ID != "" {
		return pkg, s.ensureCharmhubTrack(ctx, pkg, rule.Track, trackCache)
	}

	created, err := core.NewPackage(core.Package{
		ID:             firstInfo.ID,
		Name:           rule.PackageName,
		Type:           "charm",
		Private:        false,
		Status:         "published",
		OwnerAccountID: syncIdentity.Account.ID,
		Authority:      stringPtr(charmhubAuthority),
		DefaultTrack:   stringPtr(defaultTrackForRules(allRules)),
		Publisher: core.Publisher{
			ID:          syncIdentity.Account.ID,
			Username:    syncIdentity.Account.Username,
			DisplayName: syncIdentity.Account.DisplayName,
			Email:       syncIdentity.Account.Email,
			Validation:  syncIdentity.Account.Validation,
		},
		Store:     s.cfg.PublicAPIURL,
		CreatedAt: s.now(),
		UpdatedAt: s.now(),
	})
	if err != nil {
		return core.Package{}, err
	}
	if err := s.repo.CreatePackage(ctx, created); err != nil {
		return core.Package{}, err
	}
	slog.InfoContext(ctx, "charmhub synced package created",
		"package", created.Name,
		"package_id", created.ID,
		"default_track", stringValue(created.DefaultTrack),
	)
	trackCache.remember(created.ID, nil)
	return created, s.ensureCharmhubTrack(ctx, created, rule.Track, trackCache)
}

func (s *Service) syncTrackReleases(
	ctx context.Context,
	pkg core.Package,
	createdBy string,
	present []channelState,
	trackCache *packageTrackCache,
) (core.Package, error) {
	var err error
	for _, state := range present {
		var resourceRefs []core.ReleaseResourceRef
		pkg, resourceRefs, err = s.ensureCharmhubArtifacts(ctx, pkg, createdBy, state.info, trackCache)
		if err != nil {
			return core.Package{}, err
		}
		releaseWhen := state.info.DefaultRelease.Channel.ReleasedAt
		if releaseWhen.IsZero() {
			releaseWhen = s.now()
		}
		release, err := core.NewRelease(core.Release{
			ID:        uuid.NewString(),
			Channel:   state.channel,
			Revision:  state.info.DefaultRelease.Revision.Revision,
			Base:      state.info.DefaultRelease.Channel.Base,
			Resources: resourceRefs,
			When:      releaseWhen,
		})
		if err != nil {
			return core.Package{}, err
		}
		if err := s.repo.ReplaceRelease(ctx, pkg.ID, release); err != nil {
			return core.Package{}, err
		}
		slog.InfoContext(ctx, "charmhub release synced",
			"package", pkg.Name,
			"package_id", pkg.ID,
			"channel", release.Channel,
			"revision", release.Revision,
			"resource_count", len(release.Resources),
			"base", release.Base,
		)
	}
	return pkg, nil
}

func (s *Service) removeStaleTrackReleases(
	ctx context.Context,
	packageID, track string,
	present []channelState,
) error {
	presentVariants := make([]repo.ReleaseVariant, 0, len(present))
	for _, state := range present {
		presentVariants = append(presentVariants, repo.ReleaseVariant{
			Channel: state.channel,
			Base:    state.info.DefaultRelease.Channel.Base,
		})
	}

	deleted, err := s.repo.DeleteStaleTrackReleases(ctx, packageID, track, presentVariants)
	if err != nil {
		return err
	}
	if deleted > 0 {
		slog.InfoContext(ctx, "stale charmhub releases pruned",
			"package_id", packageID,
			"track", track,
			"release_count", deleted,
		)
	}
	return nil
}

func (s *Service) persistSyncedPackage(ctx context.Context, pkg core.Package, trackCache *packageTrackCache) (core.Package, error) {
	pkg.UpdatedAt = s.now()
	if tracks, err := trackCache.tracks(ctx, s.repo, pkg.ID); err == nil {
		pkg.Tracks = tracks
	}
	if err := s.repo.UpdatePackage(ctx, pkg); err != nil {
		return core.Package{}, err
	}
	slog.DebugContext(ctx, "charmhub synced package persisted",
		"package", pkg.Name,
		"package_id", pkg.ID,
		"default_track", stringValue(pkg.DefaultTrack),
		"track_count", len(pkg.Tracks),
	)
	return pkg, nil
}

func (s *Service) ensureCharmhubTrack(ctx context.Context, pkg core.Package, track string, trackCache *packageTrackCache) error {
	tracks, err := trackCache.tracks(ctx, s.repo, pkg.ID)
	if err != nil {
		return err
	}
	for _, item := range tracks {
		if item.Name == track {
			return nil
		}
	}
	created, err := core.NewTrack(core.Track{
		Name:      track,
		CreatedAt: s.now(),
	})
	if err != nil {
		return fmt.Errorf("charmhub sync track %s: %w", track, err)
	}
	_, err = s.repo.CreateTracks(ctx, pkg.ID, []core.Track{created})
	if err == nil {
		trackCache.add(pkg.ID, created)
		slog.InfoContext(ctx, "charmhub track created",
			"package", pkg.Name,
			"package_id", pkg.ID,
			"track", track,
		)
	}
	return err
}

func selectCharmhubSyncVariants(items []charmhubclient.ChannelMap, rule core.CharmhubSyncRule) []charmhubclient.ChannelMap {
	var selected []charmhubclient.ChannelMap
	for _, item := range items {
		base := item.Channel.Base
		if base == nil {
			continue
		}
		if item.Channel.Track != rule.Track {
			continue
		}
		if !slices.Contains(charmhubSyncRisks, item.Channel.Risk) {
			continue
		}
		if !syncRuleMatchesBase(rule, *base) || !syncRuleMatchesArchitecture(rule, base.Architecture) {
			continue
		}
		selected = append(selected, item)
	}
	slices.SortFunc(selected, func(left, right charmhubclient.ChannelMap) int {
		return strings.Compare(
			releaseVariantID(left.Channel.Name, left.Channel.Base),
			releaseVariantID(right.Channel.Name, right.Channel.Base),
		)
	})
	return selected
}

func syncRuleMatchesBase(rule core.CharmhubSyncRule, base core.Base) bool {
	if len(rule.Bases) == 0 {
		return true
	}
	selector := base.Name + "@" + base.Channel
	return slices.Contains(rule.Bases, selector)
}

func syncRuleMatchesArchitecture(rule core.CharmhubSyncRule, architecture string) bool {
	return len(rule.Architectures) == 0 || slices.Contains(rule.Architectures, architecture)
}

func charmhubSyncChannelName(channel charmhubclient.ReleaseChannel) string {
	if strings.Contains(channel.Name, "/") {
		return channel.Name
	}
	if channel.Track != "" && channel.Risk != "" {
		return channel.Track + "/" + channel.Risk
	}
	return channel.Name
}

func releaseVariantID(channel string, base *core.Base) string {
	if base == nil {
		return channel + "\x00"
	}
	return channel + "\x00" + base.Name + "\x00" + base.Channel + "\x00" + base.Architecture
}

func (s *Service) ensureCharmhubArtifacts(
	ctx context.Context,
	pkg core.Package,
	createdBy string,
	info charmhubclient.PackageChannel,
	trackCache *packageTrackCache,
) (core.Package, []core.ReleaseResourceRef, error) {
	revisionNumber := info.DefaultRelease.Revision.Revision
	updatedPkg, err := s.ensureCharmhubRevisionArtifacts(ctx, pkg, createdBy, info, revisionNumber, trackCache)
	if err != nil {
		return core.Package{}, nil, err
	}
	pkg = updatedPkg

	resourceRefs, updatedPkg, err := s.ensureCharmhubResourceArtifacts(ctx, pkg, info, revisionNumber)
	if err != nil {
		return core.Package{}, nil, err
	}

	return updatedPkg, resourceRefs, nil
}

func (s *Service) ensureCharmhubRevisionArtifacts(
	ctx context.Context,
	pkg core.Package,
	createdBy string,
	info charmhubclient.PackageChannel,
	revisionNumber int,
	trackCache *packageTrackCache,
) (core.Package, error) {
	_, err := s.repo.GetRevisionByNumber(ctx, pkg.ID, revisionNumber)
	switch {
	case err == nil:
		slog.DebugContext(ctx, "charmhub revision already present",
			"package", pkg.Name,
			"package_id", pkg.ID,
			"revision", revisionNumber,
		)
		return pkg, nil
	case !errors.Is(err, repo.ErrNotFound):
		return core.Package{}, err
	}

	revisionArtifact, err := s.downloadAndParseRevision(ctx, info.DefaultRelease.Revision.Download.URL)
	if err != nil {
		return core.Package{}, err
	}
	defer revisionArtifact.Close()

	revisionKey := filepath.ToSlash(filepath.Join("charms", pkg.ID, fmt.Sprintf("%d.charm", revisionNumber)))
	if err := s.putRevisionBlob(ctx, revisionKey, revisionArtifact); err != nil {
		return core.Package{}, err
	}

	updatedPkg, err := s.updatePackageFromUpstream(ctx, pkg, info, revisionArtifact.Archive.Manifest, trackCache)
	if err != nil {
		return core.Package{}, err
	}
	if err := s.createRevisionRecord(ctx, updatedPkg, createdBy, info, revisionNumber, revisionKey, revisionArtifact); err != nil {
		return core.Package{}, err
	}
	if err := s.upsertManifestResourceDefinitions(ctx, updatedPkg.ID, revisionArtifact.Archive.Manifest); err != nil {
		return core.Package{}, err
	}
	slog.InfoContext(ctx, "charmhub revision imported",
		"package", updatedPkg.Name,
		"package_id", updatedPkg.ID,
		"revision", revisionNumber,
		"size", revisionArtifact.Size,
		"resource_definition_count", len(revisionArtifact.Archive.Manifest.Resources),
	)
	return updatedPkg, nil
}

// preparedResourceRevision carries the outcome of the parallel preparation
// phase for one resource. The artifact is retained only for oci-image
// resources, whose descriptor payload is consumed during the sequential
// persistence phase; for other types the blob is uploaded during preparation
// and the temp file is already removed.
type preparedResourceRevision struct {
	resource charmhubclient.ReleaseResource
	revision core.ResourceRevision
	artifact *downloadedArtifact
}

func (s *Service) ensureCharmhubResourceArtifacts(
	ctx context.Context,
	pkg core.Package,
	info charmhubclient.PackageChannel,
	revisionNumber int,
) ([]core.ReleaseResourceRef, core.Package, error) {
	refs := make([]core.ReleaseResourceRef, len(info.DefaultRelease.Resources))
	results := make([]preparedResourceRevision, len(info.DefaultRelease.Resources))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tokens := make(chan struct{}, charmhubResourceSyncConcurrency)
	errCh := make(chan error, 1)
	var wg stdsync.WaitGroup
	for i, resource := range info.DefaultRelease.Resources {
		refs[i] = core.ReleaseResourceRef{Name: resource.Name, Revision: intPointer(resource.Revision)}
		wg.Add(1)
		go func(index int, item charmhubclient.ReleaseResource) {
			defer wg.Done()
			select {
			case tokens <- struct{}{}:
				defer func() { <-tokens }()
			case <-ctx.Done():
				sendResourceSyncError(errCh, ctx.Err(), cancel)
				return
			}
			result, err := s.prepareCharmhubResourceRevision(ctx, pkg, item, revisionNumber)
			if err != nil {
				sendResourceSyncError(errCh, err, cancel)
				return
			}
			results[index] = result
		}(i, resource)
	}
	wg.Wait()
	defer func() {
		for _, prepared := range results {
			_ = prepared.artifact.Close()
		}
	}()
	select {
	case err := <-errCh:
		return nil, core.Package{}, err
	default:
	}

	updatedPkg := pkg
	for _, prepared := range results {
		item := prepared.revision
		if item.ID == "" {
			continue
		}
		// OCI image resources are the only synced resource type with an out-of-band artifact lifecycle:
		// Charmhub publishes a descriptor blob, but the registry stores the mirrored image digest.
		// Keep this explicit branch until another resource type needs distinct import behavior.
		if item.Type == "oci-image" {
			provisioned, updatedItem, err := s.populateOCIResourceRevision(ctx, updatedPkg, prepared.resource, prepared.artifact, item)
			if err != nil {
				return nil, core.Package{}, err
			}
			updatedPkg = provisioned
			item = updatedItem
		}
		if err := s.repo.CreateResourceRevision(ctx, item); err != nil {
			return nil, core.Package{}, err
		}
		slog.InfoContext(ctx, "charmhub resource revision imported",
			"package", updatedPkg.Name,
			"package_id", updatedPkg.ID,
			"resource", item.Name,
			"resource_type", item.Type,
			"revision", item.Revision,
			"package_revision", revisionNumber,
			"size", item.Size,
			"oci_digest", item.OCIImageDigest,
		)
	}
	return refs, updatedPkg, nil
}

// prepareCharmhubResourceRevision performs the network- and disk-bound part of
// resource import (download, hash, blob upload) and is safe to call
// concurrently; persistence happens sequentially in the caller. A zero-ID
// result means the revision already exists and nothing needs persisting.
func (s *Service) prepareCharmhubResourceRevision(
	ctx context.Context,
	pkg core.Package,
	resource charmhubclient.ReleaseResource,
	revisionNumber int,
) (preparedResourceRevision, error) {
	resourceDef, err := s.repo.GetResourceDefinition(ctx, pkg.ID, resource.Name)
	if err != nil {
		return preparedResourceRevision{}, service.TranslateRepoError(err, messageResourceNotDeclared)
	}

	_, err = s.repo.GetResourceRevision(ctx, resourceDef.ID, resource.Revision)
	switch {
	case err == nil:
		slog.DebugContext(ctx, "charmhub resource revision already present",
			"package", pkg.Name,
			"package_id", pkg.ID,
			"resource", resource.Name,
			"revision", resource.Revision,
		)
		return preparedResourceRevision{}, nil
	case !errors.Is(err, repo.ErrNotFound):
		return preparedResourceRevision{}, err
	}

	resourceArtifact, item, err := s.prepareResourceRevision(ctx, pkg, resourceDef, resource, revisionNumber)
	if err != nil {
		return preparedResourceRevision{}, err
	}

	if item.Type == "oci-image" {
		if item.Size == 0 {
			item.Size = resourceArtifact.Size
		}
		return preparedResourceRevision{resource: resource, revision: item, artifact: resourceArtifact}, nil
	}

	defer resourceArtifact.Close()
	resourceKey := filepath.ToSlash(filepath.Join("resources", pkg.ID, resource.Name, fmt.Sprintf("%d", resource.Revision)))
	if err := s.putArtifactBlob(ctx, resourceKey, resourceArtifact); err != nil {
		return preparedResourceRevision{}, err
	}
	item.ObjectKey = resourceKey
	s.populateResourceHashes(&item, resourceArtifact)
	if item.Size == 0 {
		item.Size = resourceArtifact.Size
	}
	return preparedResourceRevision{resource: resource, revision: item}, nil
}

func sendResourceSyncError(errCh chan<- error, err error, cancel context.CancelFunc) {
	if err == nil {
		return
	}
	select {
	case errCh <- err:
		cancel()
	default:
	}
}

type downloadedArtifact struct {
	Path    string
	Size    int64
	SHA256  [sha256.Size]byte
	SHA384  [sha512.Size384]byte
	SHA512  [sha512.Size]byte
	Archive core.CharmArchive
}

func (a *downloadedArtifact) Close() error {
	if a == nil || a.Path == "" {
		return nil
	}
	return os.Remove(a.Path)
}

func (a *downloadedArtifact) Open() (*os.File, error) {
	// #nosec G304 -- path is a service-created temp file.
	return os.Open(a.Path)
}

func (a *downloadedArtifact) CopyTo(ctx context.Context, dst io.Writer) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	file, err := a.Open()
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(dst, file)
	return err
}

func (a *downloadedArtifact) ReadAll(ctx context.Context) ([]byte, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	file, err := a.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func (s *Service) downloadAndParseRevision(ctx context.Context, downloadURL string) (*downloadedArtifact, error) {
	artifact, err := s.downloadArtifactToTemp(ctx, downloadURL)
	if err != nil {
		return nil, err
	}
	archive, err := charm.ParseArchiveFile(artifact.Path, artifact.Size, s.cfg.MaxArchiveFileBytes)
	if err != nil {
		_ = artifact.Close()
		return nil, err
	}
	artifact.Archive = archive
	return artifact, nil
}

func (s *Service) downloadArtifactToTemp(ctx context.Context, downloadURL string) (*downloadedArtifact, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "charmhub-artifact-*")
	if err != nil {
		return nil, err
	}
	artifact := &downloadedArtifact{Path: tmp.Name()}
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = artifact.Close()
		}
	}()

	hash256 := sha256.New()
	hash384 := sha512.New384()
	hash512 := sha512.New()
	writer := io.MultiWriter(tmp, hash256, hash384, hash512)
	artifact.Size, err = s.charmhub.DownloadTo(ctx, downloadURL, writer)
	if err != nil {
		return nil, err
	}
	if err = tmp.Close(); err != nil {
		return nil, err
	}
	copy(artifact.SHA256[:], hash256.Sum(nil))
	copy(artifact.SHA384[:], hash384.Sum(nil))
	copy(artifact.SHA512[:], hash512.Sum(nil))
	return artifact, nil
}

func (s *Service) putRevisionBlob(ctx context.Context, key string, artifact *downloadedArtifact) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	return s.putArtifactBlob(ctx, key, artifact)
}

func (s *Service) putArtifactBlob(ctx context.Context, key string, artifact *downloadedArtifact) error {
	file, err := artifact.Open()
	if err != nil {
		return err
	}
	defer file.Close()
	return s.blobs.Put(ctx, key, file, "application/octet-stream")
}

func (s *Service) updatePackageFromUpstream(
	ctx context.Context,
	pkg core.Package,
	info charmhubclient.PackageChannel,
	manifest core.CharmManifest,
	trackCache *packageTrackCache,
) (core.Package, error) {
	pkg = applyCharmhubPackageMetadata(pkg, info.Result, manifest)
	pkg.Status = "published"
	pkg.UpdatedAt = s.now()
	if tracks, err := trackCache.tracks(ctx, s.repo, pkg.ID); err == nil {
		pkg.Tracks = tracks
	}
	if err := s.repo.UpdatePackage(ctx, pkg); err != nil {
		return core.Package{}, err
	}
	slog.DebugContext(ctx, "charmhub package metadata updated from upstream",
		"package", pkg.Name,
		"package_id", pkg.ID,
		"title", stringValue(pkg.Title),
		"track_count", len(pkg.Tracks),
	)
	return pkg, nil
}

func (s *Service) createRevisionRecord(
	ctx context.Context,
	pkg core.Package,
	createdBy string, info charmhubclient.PackageChannel,
	revisionNumber int,
	revisionKey string,
	artifact *downloadedArtifact,
) error {
	revisionCreatedAt := info.DefaultRelease.Revision.CreatedAt
	if revisionCreatedAt.IsZero() {
		revisionCreatedAt = s.now()
	}
	revision, err := core.NewRevision(core.Revision{
		ID:           uuid.NewString(),
		PackageID:    pkg.ID,
		Revision:     revisionNumber,
		Version:      core.FirstNonEmpty(info.DefaultRelease.Revision.Version, fmt.Sprintf("%d", revisionNumber)),
		Status:       "approved",
		CreatedAt:    revisionCreatedAt,
		CreatedBy:    createdBy,
		Size:         artifact.Size,
		SHA256:       hex.EncodeToString(artifact.SHA256[:]),
		SHA384:       hex.EncodeToString(artifact.SHA384[:]),
		ObjectKey:    revisionKey,
		MetadataYAML: artifact.Archive.MetadataYAML,
		ConfigYAML:   artifact.Archive.ConfigYAML,
		ActionsYAML:  artifact.Archive.ActionsYAML,
		BundleYAML:   artifact.Archive.BundleYAML,
		ReadmeMD:     artifact.Archive.ReadmeMD,
		Bases:        extractBases(artifact.Archive.Manifest),
		Attributes:   mapOrDefault(info.DefaultRelease.Revision.Attributes, map[string]string{"framework": "operator", "language": "unknown"}),
		Relations: map[string]map[string]core.Relation{
			"provides": toCoreRelations(artifact.Archive.Manifest.Provides),
			"requires": toCoreRelations(artifact.Archive.Manifest.Requires),
			"peers":    toCoreRelations(artifact.Archive.Manifest.Peers),
		},
		Subordinate: artifact.Archive.Manifest.Subordinate,
	})
	if err != nil {
		return err
	}
	if err := s.repo.CreateRevision(ctx, revision); err != nil {
		return err
	}
	slog.DebugContext(ctx, "charmhub revision record created",
		"package", pkg.Name,
		"package_id", pkg.ID,
		"revision", revision.Revision,
		"version", revision.Version,
		"base_count", len(revision.Bases),
	)
	return nil
}

func (s *Service) upsertManifestResourceDefinitions(ctx context.Context, packageID string, manifest core.CharmManifest) error {
	for resourceName, resource := range manifest.Resources {
		definition, err := core.NewResourceDefinition(core.ResourceDefinition{
			ID:          uuid.NewString(),
			PackageID:   packageID,
			Name:        resourceName,
			Type:        resource.Type,
			Description: resource.Description,
			Filename:    resource.Filename,
			Optional:    false,
			CreatedAt:   s.now(),
		})
		if err != nil {
			return fmt.Errorf("resource definition %s: %w", resourceName, err)
		}
		_, err = s.repo.UpsertResourceDefinition(ctx, definition)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) prepareResourceRevision(
	ctx context.Context,
	pkg core.Package,
	resourceDef core.ResourceDefinition,
	resource charmhubclient.ReleaseResource,
	revisionNumber int,
) (*downloadedArtifact, core.ResourceRevision, error) {
	artifact, err := s.downloadArtifactToTemp(ctx, resource.Download.URL)
	if err != nil {
		return nil, core.ResourceRevision{}, err
	}
	item := core.ResourceRevision{
		ID:              uuid.NewString(),
		ResourceID:      resourceDef.ID,
		Name:            resourceDef.Name,
		Type:            core.FirstNonEmpty(resource.Type, resourceDef.Type),
		Description:     core.FirstNonEmpty(resource.Description, resourceDef.Description),
		Filename:        core.FirstNonEmpty(resource.Filename, resourceDef.Filename),
		Revision:        resource.Revision,
		CreatedAt:       resource.CreatedAt,
		Size:            resource.Download.Size,
		SHA256:          resource.Download.HashSHA256,
		SHA384:          resource.Download.HashSHA384,
		SHA512:          resource.Download.HashSHA512,
		SHA3384:         resource.Download.HashSHA3384,
		PackageRevision: intPointer(revisionNumber),
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = s.now()
	}
	return artifact, item, nil
}

func (s *Service) populateOCIResourceRevision(
	ctx context.Context,
	pkg core.Package,
	resource charmhubclient.ReleaseResource,
	artifact *downloadedArtifact,
	item core.ResourceRevision,
) (core.Package, core.ResourceRevision, error) {
	updatedPkg, err := s.ensureOCIProvisioned(ctx, pkg)
	if err != nil {
		return core.Package{}, core.ResourceRevision{}, err
	}
	payload, err := artifact.ReadAll(ctx)
	if err != nil {
		return core.Package{}, core.ResourceRevision{}, err
	}
	var blob upstreamOCIImageBlob
	if err := json.Unmarshal(payload, &blob); err != nil {
		return core.Package{}, core.ResourceRevision{}, err
	}
	if err := checkContext(ctx); err != nil {
		return core.Package{}, core.ResourceRevision{}, err
	}
	mirroredDigest, err := s.oci.MirrorImage(
		ctx,
		updatedPkg,
		resource.Name,
		core.FirstNonEmpty(blob.ImageName, resource.Download.URL),
		blob.Username,
		blob.Password,
	)
	if err != nil {
		return core.Package{}, core.ResourceRevision{}, err
	}
	slog.InfoContext(ctx, "charmhub OCI resource mirrored",
		"package", updatedPkg.Name,
		"package_id", updatedPkg.ID,
		"resource", resource.Name,
		"digest", core.FirstNonEmpty(mirroredDigest, blob.Digest),
	)
	item.OCIImageDigest = core.FirstNonEmpty(mirroredDigest, blob.Digest)
	item.ObjectKey = ""
	if item.Size == 0 {
		item.Size = int64(len(payload))
	}
	return updatedPkg, item, nil
}

func (s *Service) populateResourceHashes(item *core.ResourceRevision, artifact *downloadedArtifact) {
	if item.SHA256 != "" && item.SHA384 != "" && item.SHA512 != "" && item.SHA3384 != "" {
		return
	}
	item.SHA256 = hex.EncodeToString(artifact.SHA256[:])
	item.SHA384 = hex.EncodeToString(artifact.SHA384[:])
	item.SHA512 = hex.EncodeToString(artifact.SHA512[:])
	item.SHA3384 = item.SHA384
}

func (s *Service) pruneSyncedPackage(ctx context.Context, pkg core.Package, rules []core.CharmhubSyncRule, trackCache *packageTrackCache) error {
	validTracks := validTracksFromRules(rules)
	revisionRefs, resourceRefs, err := s.pruneOutOfScopeReleases(ctx, pkg.ID, validTracks)
	if err != nil {
		return err
	}
	if err := s.pruneDanglingRevisions(ctx, pkg.ID, revisionRefs); err != nil {
		return err
	}
	if err := s.pruneDanglingResources(ctx, pkg, resourceRefs); err != nil {
		return err
	}
	return s.pruneDanglingTracks(ctx, pkg.ID, validTracks, trackCache)
}

func (s *Service) cleanupSyncedPackage(ctx context.Context, packageName string) error {
	pkg, err := s.repo.GetPackageByName(ctx, packageName)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !isCharmhubManagedPackage(pkg) {
		return nil
	}
	if err := s.deleteOCIPackage(ctx, pkg); err != nil {
		return err
	}
	if err := s.deleteAllResourceArtifacts(ctx, pkg.ID); err != nil {
		return err
	}
	if err := s.deleteAllRevisions(ctx, pkg.ID); err != nil {
		return err
	}
	if err := s.deleteAllReleases(ctx, pkg.ID); err != nil {
		return err
	}
	if err := s.deleteAllTracks(ctx, pkg.ID); err != nil {
		return err
	}
	if err := s.repo.DeletePackage(ctx, pkg.ID); err != nil {
		return err
	}
	slog.InfoContext(ctx, "charmhub synced package deleted",
		"package", pkg.Name,
		"package_id", pkg.ID,
	)
	return nil
}

func validTracksFromRules(rules []core.CharmhubSyncRule) map[string]struct{} {
	validTracks := map[string]struct{}{}
	for _, rule := range rules {
		validTracks[rule.Track] = struct{}{}
	}
	return validTracks
}

func (s *Service) pruneOutOfScopeReleases(
	ctx context.Context,
	packageID string,
	validTracks map[string]struct{},
) (map[int]struct{}, map[string]map[int]struct{}, error) {
	releases, err := s.repo.ListReleases(ctx, packageID)
	if err != nil {
		return nil, nil, err
	}

	referencedRevisions := map[int]struct{}{}
	referencedResources := map[string]map[int]struct{}{}
	for _, release := range releases {
		if _, ok := validTracks[splitChannel(release.Channel).track]; !ok {
			if err := s.repo.DeleteRelease(ctx, packageID, release.Channel); err != nil && !errors.Is(err, repo.ErrNotFound) {
				return nil, nil, err
			}
			slog.InfoContext(ctx, "out-of-scope charmhub release deleted",
				"package_id", packageID,
				"channel", release.Channel,
				"revision", release.Revision,
			)
			continue
		}
		referencedRevisions[release.Revision] = struct{}{}
		for _, resource := range release.Resources {
			if resource.Revision == nil {
				continue
			}
			if _, ok := referencedResources[resource.Name]; !ok {
				referencedResources[resource.Name] = map[int]struct{}{}
			}
			referencedResources[resource.Name][*resource.Revision] = struct{}{}
		}
	}
	return referencedRevisions, referencedResources, nil
}

func (s *Service) pruneDanglingRevisions(ctx context.Context, packageID string, referencedRevisions map[int]struct{}) error {
	revisions, err := s.repo.ListRevisions(ctx, packageID, nil)
	if err != nil {
		return err
	}
	for _, revision := range revisions {
		if _, ok := referencedRevisions[revision.Revision]; ok {
			continue
		}
		if revision.ObjectKey != "" {
			if err := s.blobs.Delete(ctx, revision.ObjectKey); err != nil {
				return err
			}
		}
		if err := s.repo.DeleteRevision(ctx, packageID, revision.Revision); err != nil {
			return err
		}
		slog.InfoContext(ctx, "dangling charmhub revision deleted",
			"package_id", packageID,
			"revision", revision.Revision,
		)
	}
	return nil
}

func (s *Service) pruneDanglingResources(
	ctx context.Context,
	pkg core.Package,
	referencedResources map[string]map[int]struct{},
) error {
	resourceDefs, err := s.repo.ListResourceDefinitions(ctx, pkg.ID)
	if err != nil {
		return err
	}

	seenDigests := map[string]struct{}{}
	for _, def := range resourceDefs {
		if err := s.pruneResourceDefinitionRevisions(ctx, pkg, def, referencedResources[def.Name], seenDigests); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) pruneResourceDefinitionRevisions(
	ctx context.Context,
	pkg core.Package,
	def core.ResourceDefinition,
	keep map[int]struct{},
	seenDigests map[string]struct{},
) error {
	revisions, err := s.repo.ListResourceRevisions(ctx, def.ID)
	if err != nil {
		return err
	}

	for _, revision := range revisions {
		if keep != nil {
			if _, ok := keep[revision.Revision]; ok {
				continue
			}
		}
		if err := s.deleteResourceArtifact(ctx, pkg, def.Name, revision, seenDigests); err != nil {
			return err
		}
		if err := s.repo.DeleteResourceRevision(ctx, def.ID, revision.Revision); err != nil {
			return err
		}
		slog.InfoContext(ctx, "dangling charmhub resource revision deleted",
			"package", pkg.Name,
			"package_id", pkg.ID,
			"resource", def.Name,
			"revision", revision.Revision,
		)
	}

	remaining, err := s.repo.ListResourceRevisions(ctx, def.ID)
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		return s.repo.DeleteResourceDefinition(ctx, def.ID)
	}
	return nil
}

func (s *Service) deleteResourceArtifact(
	ctx context.Context,
	pkg core.Package,
	resourceName string,
	revision core.ResourceRevision,
	seenDigests map[string]struct{},
) error {
	if revision.ObjectKey != "" {
		return s.blobs.Delete(ctx, revision.ObjectKey)
	}
	if revision.OCIImageDigest == "" {
		return nil
	}
	if _, ok := seenDigests[revision.OCIImageDigest]; ok {
		return nil
	}
	if err := s.oci.DeleteImage(ctx, pkg, resourceName, revision.OCIImageDigest); err != nil {
		return err
	}
	seenDigests[revision.OCIImageDigest] = struct{}{}
	slog.InfoContext(ctx, "dangling charmhub OCI image deleted",
		"package", pkg.Name,
		"package_id", pkg.ID,
		"resource", resourceName,
		"digest", revision.OCIImageDigest,
	)
	return nil
}

func (s *Service) pruneDanglingTracks(ctx context.Context, packageID string, validTracks map[string]struct{}, trackCache *packageTrackCache) error {
	tracks, err := trackCache.tracks(ctx, s.repo, packageID)
	if err != nil {
		return err
	}
	for _, track := range tracks {
		if _, ok := validTracks[track.Name]; ok {
			continue
		}
		if err := s.repo.DeleteTrack(ctx, packageID, track.Name); err != nil && !errors.Is(err, repo.ErrNotFound) {
			return err
		}
		trackCache.remove(packageID, track.Name)
		slog.InfoContext(ctx, "dangling charmhub track deleted",
			"package_id", packageID,
			"track", track.Name,
		)
	}
	return nil
}

func (s *Service) deleteOCIPackage(ctx context.Context, pkg core.Package) error {
	if s.oci == nil {
		return nil
	}
	return s.oci.DeletePackage(ctx, pkg)
}

func (s *Service) deleteAllResourceArtifacts(ctx context.Context, packageID string) error {
	resourceDefs, err := s.repo.ListResourceDefinitions(ctx, packageID)
	if err != nil {
		return err
	}
	for _, def := range resourceDefs {
		revisions, err := s.repo.ListResourceRevisions(ctx, def.ID)
		if err != nil {
			return err
		}
		for _, revision := range revisions {
			if revision.ObjectKey != "" {
				if err := s.blobs.Delete(ctx, revision.ObjectKey); err != nil {
					return err
				}
			}
			if err := s.repo.DeleteResourceRevision(ctx, def.ID, revision.Revision); err != nil {
				return err
			}
		}
		if err := s.repo.DeleteResourceDefinition(ctx, def.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) deleteAllRevisions(ctx context.Context, packageID string) error {
	revisions, err := s.repo.ListRevisions(ctx, packageID, nil)
	if err != nil {
		return err
	}
	for _, revision := range revisions {
		if revision.ObjectKey != "" {
			if err := s.blobs.Delete(ctx, revision.ObjectKey); err != nil {
				return err
			}
		}
		if err := s.repo.DeleteRevision(ctx, packageID, revision.Revision); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) deleteAllReleases(ctx context.Context, packageID string) error {
	releases, err := s.repo.ListReleases(ctx, packageID)
	if err != nil {
		return err
	}
	for _, release := range releases {
		if err := s.repo.DeleteRelease(ctx, packageID, release.Channel); err != nil && !errors.Is(err, repo.ErrNotFound) {
			return err
		}
	}
	return nil
}

func (s *Service) deleteAllTracks(ctx context.Context, packageID string) error {
	tracks, err := s.repo.ListTracks(ctx, packageID)
	if err != nil {
		return err
	}
	for _, track := range tracks {
		if err := s.repo.DeleteTrack(ctx, packageID, track.Name); err != nil && !errors.Is(err, repo.ErrNotFound) {
			return err
		}
	}
	return nil
}

func defaultTrackForRules(rules []core.CharmhubSyncRule) string {
	for _, rule := range rules {
		if rule.Track == "latest" {
			return "latest"
		}
	}
	if len(rules) == 0 {
		return "latest"
	}
	tracks := make([]string, 0, len(rules))
	for _, rule := range rules {
		tracks = append(tracks, rule.Track)
	}
	slices.Sort(tracks)
	return tracks[0]
}

func applyCharmhubPackageMetadata(
	pkg core.Package,
	result charmhubclient.PackageResult,
	manifest core.CharmManifest,
) core.Package {
	pkg.Title = stringPtr(core.FirstNonEmpty(manifest.DisplayName, result.Title, manifest.Name, pkg.Name))
	pkg.Summary = stringPtr(core.FirstNonEmpty(manifest.Summary, result.Summary))
	pkg.Description = stringPtr(core.FirstNonEmpty(manifest.Description, result.Description))
	if len(result.Links) > 0 {
		pkg.Links = cloneLinks(result.Links)
	} else {
		pkg.Links = mergeLinks(pkg.Links, manifest.Docs, manifest.Issues, manifest.Source, charm.ExtractWebsites(manifest.Website))
	}
	pkg.Website = stringPtr(core.FirstNonEmpty(result.Website, firstLink(pkg.Links["website"])))
	pkg.Media = make([]core.Media, 0, len(result.Media))
	for _, item := range result.Media {
		pkg.Media = append(pkg.Media, core.Media{Type: item.Type, URL: item.URL})
	}
	return pkg
}

func cloneLinks(value map[string][]string) map[string][]string {
	out := make(map[string][]string, len(value))
	for key, items := range value {
		out[key] = append([]string(nil), items...)
	}
	return out
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
