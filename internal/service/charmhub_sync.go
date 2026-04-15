package service

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/gschiano/charm-registry/internal/charm"
	charmhubclient "github.com/gschiano/charm-registry/internal/charmhub"
	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
)

const (
	charmhubAuthority          = "charmhub"
	charmhubSyncAccountID      = "internal-charmhub-sync"
	charmhubSyncAccountSubject = "internal|charmhub-sync"
	charmhubSyncAccountName    = "charmhub-sync"
	charmhubSyncStatusPending  = "pending"
	charmhubSyncStatusRunning  = "running"
	charmhubSyncStatusOK       = "ok"
	charmhubSyncStatusError    = "error"
)

var charmhubSyncRisks = []string{"stable", "candidate", "beta", "edge"}

type CharmhubSyncManager struct {
	service *Service
	cancel  context.CancelFunc
	done    chan struct{}
	wake    chan struct{}

	mu      sync.Mutex
	pending map[string]struct{}
}

type upstreamOCIImageBlob struct {
	ImageName string `json:"ImageName"`
	Password  string `json:"Password"`
	Username  string `json:"Username"`
	Digest    string `json:"Digest"`
}

func (s *Service) StartCharmhubSyncManager(ctx context.Context) *CharmhubSyncManager {
	if s.syncManager != nil {
		return s.syncManager
	}
	managerCtx, cancel := context.WithCancel(ctx)
	manager := &CharmhubSyncManager{
		service: s,
		cancel:  cancel,
		done:    make(chan struct{}),
		wake:    make(chan struct{}, 1),
		pending: map[string]struct{}{},
	}
	s.syncManager = manager
	go manager.run(managerCtx)
	return manager
}

func (m *CharmhubSyncManager) Close() error {
	if m == nil {
		return nil
	}
	m.cancel()
	<-m.done
	return nil
}

func (m *CharmhubSyncManager) Enqueue(packageName string) {
	if m == nil || strings.TrimSpace(packageName) == "" {
		return
	}
	m.mu.Lock()
	m.pending[packageName] = struct{}{}
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *CharmhubSyncManager) run(ctx context.Context) {
	defer close(m.done)

	interval := m.service.cfg.CharmhubSyncInterval
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
			m.runPending(ctx)
		case <-ticker.C:
			m.runAll(ctx)
		}
	}
}

func (m *CharmhubSyncManager) runPending(ctx context.Context) {
	packageNames := m.takePending()
	for _, packageName := range packageNames {
		if err := m.service.reconcileCharmhubPackage(ctx, packageName); err != nil {
			slog.ErrorContext(ctx, "charmhub sync failed", "package", packageName, "error", err)
		}
	}
}

func (m *CharmhubSyncManager) runAll(ctx context.Context) {
	rules, err := m.service.repo.ListCharmhubSyncRules(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "cannot list charmhub sync rules", "error", err)
		return
	}
	grouped := map[string]struct{}{}
	for _, rule := range rules {
		grouped[rule.PackageName] = struct{}{}
	}
	packageNames := make([]string, 0, len(grouped))
	for packageName := range grouped {
		packageNames = append(packageNames, packageName)
	}
	slices.Sort(packageNames)
	for _, packageName := range packageNames {
		if err := m.service.reconcileCharmhubPackage(ctx, packageName); err != nil {
			slog.ErrorContext(ctx, "charmhub sync failed", "package", packageName, "error", err)
		}
	}
}

func (m *CharmhubSyncManager) takePending() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	packageNames := make([]string, 0, len(m.pending))
	for packageName := range m.pending {
		packageNames = append(packageNames, packageName)
		delete(m.pending, packageName)
	}
	slices.Sort(packageNames)
	return packageNames
}

func (s *Service) ListCharmhubSyncRules(ctx context.Context, identity core.Identity) ([]core.CharmhubSyncRule, error) {
	if err := s.requireAdmin(identity); err != nil {
		return nil, err
	}
	return s.repo.ListCharmhubSyncRules(ctx)
}

func (s *Service) AddCharmhubSyncRule(
	ctx context.Context,
	identity core.Identity,
	packageName, track string,
) (core.CharmhubSyncRule, error) {
	if err := s.requireAdmin(identity); err != nil {
		return core.CharmhubSyncRule{}, err
	}
	packageName = strings.TrimSpace(packageName)
	track, err := normalizeSyncTrack(track)
	if err != nil {
		return core.CharmhubSyncRule{}, err
	}
	if packageName == "" {
		return core.CharmhubSyncRule{}, newError(ErrorKindInvalidRequest, "invalid-request", "package name is required")
	}

	if pkg, err := s.repo.GetPackageByName(ctx, packageName); err == nil {
		if !isCharmhubManagedPackage(pkg) {
			return core.CharmhubSyncRule{}, newError(
				ErrorKindConflict,
				"package-exists",
				"cannot synchronize a package that already exists outside Charmhub synchronization",
			)
		}
	} else if !errors.Is(err, repo.ErrNotFound) {
		return core.CharmhubSyncRule{}, err
	}

	now := time.Now().UTC()
	rule := core.CharmhubSyncRule{
		PackageName:        packageName,
		Track:              track,
		CreatedByAccountID: identity.Account.ID,
		CreatedAt:          now,
		UpdatedAt:          now,
		LastSyncStatus:     charmhubSyncStatusPending,
	}
	if err := s.repo.CreateCharmhubSyncRule(ctx, rule); err != nil {
		return core.CharmhubSyncRule{}, translateRepoError(err, "sync rule already exists")
	}
	s.enqueueCharmhubSync(packageName)
	return rule, nil
}

func (s *Service) RemoveCharmhubSyncRule(ctx context.Context, identity core.Identity, packageName, track string) error {
	if err := s.requireAdmin(identity); err != nil {
		return err
	}
	track, err := normalizeSyncTrack(track)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteCharmhubSyncRule(ctx, strings.TrimSpace(packageName), track); err != nil {
		return translateRepoError(err, "sync rule not found")
	}
	s.enqueueCharmhubSync(packageName)
	return nil
}

func (s *Service) TriggerCharmhubSync(ctx context.Context, identity core.Identity, packageName string) error {
	if err := s.requireAdmin(identity); err != nil {
		return err
	}
	packageName = strings.TrimSpace(packageName)
	if packageName == "" {
		return newError(ErrorKindInvalidRequest, "invalid-request", "package name is required")
	}
	rules, err := s.repo.ListCharmhubSyncRulesByPackageName(ctx, packageName)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return newError(ErrorKindNotFound, "not-found", "package is not configured for Charmhub synchronization")
	}
	s.enqueueCharmhubSync(packageName)
	return nil
}

func (s *Service) enqueueCharmhubSync(packageName string) {
	if s.syncManager != nil {
		s.syncManager.Enqueue(packageName)
	}
}

func (s *Service) ensurePackageNotSynchronized(ctx context.Context, packageName string) error {
	rules, err := s.repo.ListCharmhubSyncRulesByPackageName(ctx, packageName)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return nil
	}
	return newError(
		ErrorKindConflict,
		"package-synchronized",
		"package is managed by Charmhub synchronization",
	)
}

func normalizeSyncTrack(track string) (string, error) {
	track = strings.TrimSpace(track)
	if track == "" {
		return "", newError(ErrorKindInvalidRequest, "invalid-request", "track is required")
	}
	if strings.Contains(track, "/") {
		return "", newError(ErrorKindInvalidRequest, "invalid-request", "track must not include a risk")
	}
	return track, nil
}

func isCharmhubManagedPackage(pkg core.Package) bool {
	return pkg.Authority != nil && *pkg.Authority == charmhubAuthority
}

func (s *Service) charmhubSyncAccount(ctx context.Context) (core.Account, error) {
	now := time.Now().UTC()
	return s.repo.EnsureAccount(ctx, core.Account{
		ID:          charmhubSyncAccountID,
		Subject:     charmhubSyncAccountSubject,
		Username:    charmhubSyncAccountName,
		DisplayName: "Charmhub",
		Email:       "charmhub-sync@example.invalid",
		Validation:  "verified",
		CreatedAt:   now,
	})
}

func (s *Service) reconcileCharmhubPackage(ctx context.Context, packageName string) error {
	rules, err := s.repo.ListCharmhubSyncRulesByPackageName(ctx, packageName)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return s.cleanupSyncedPackage(ctx, packageName)
	}
	slices.SortFunc(rules, func(left, right core.CharmhubSyncRule) int {
		return strings.Compare(left.Track, right.Track)
	})

	pkg, err := s.repo.GetPackageByName(ctx, packageName)
	if errors.Is(err, repo.ErrNotFound) {
		pkg = core.Package{}
	} else if err != nil {
		return err
	} else if !isCharmhubManagedPackage(pkg) {
		return newError(
			ErrorKindConflict,
			"package-exists",
			"cannot synchronize a package that already exists outside Charmhub synchronization",
		)
	}

	var errs []error
	for _, rule := range rules {
		rule = s.ruleWithStatus(rule, charmhubSyncStatusRunning, nil)
		if updateErr := s.repo.UpdateCharmhubSyncRule(ctx, rule); updateErr != nil {
			errs = append(errs, updateErr)
			continue
		}

		updatedPkg, syncErr := s.syncCharmhubTrack(ctx, pkg, rules, rule)
		if syncErr != nil {
			errs = append(errs, syncErr)
			rule = s.ruleWithStatus(rule, charmhubSyncStatusError, syncErr)
			if updateErr := s.repo.UpdateCharmhubSyncRule(ctx, rule); updateErr != nil {
				errs = append(errs, updateErr)
			}
			continue
		}
		pkg = updatedPkg
		rule = s.ruleWithStatus(rule, charmhubSyncStatusOK, nil)
		if updateErr := s.repo.UpdateCharmhubSyncRule(ctx, rule); updateErr != nil {
			errs = append(errs, updateErr)
		}
	}

	if pkg.ID != "" {
		if pruneErr := s.pruneSyncedPackage(ctx, pkg, rules); pruneErr != nil {
			errs = append(errs, pruneErr)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) ruleWithStatus(rule core.CharmhubSyncRule, status string, syncErr error) core.CharmhubSyncRule {
	now := time.Now().UTC()
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

//nolint:gocognit,cyclop // Track synchronization is a single workflow that is clearer kept together.
func (s *Service) syncCharmhubTrack(
	ctx context.Context,
	pkg core.Package,
	allRules []core.CharmhubSyncRule,
	rule core.CharmhubSyncRule,
) (core.Package, error) {
	type channelState struct {
		channel string
		info    charmhubclient.PackageChannel
	}

	var present []channelState
	for _, risk := range charmhubSyncRisks {
		channel := rule.Track + "/" + risk
		info, err := s.charmhub.GetChannel(ctx, rule.PackageName, channel)
		if err != nil {
			return pkg, err
		}
		if info.DefaultRelease.Present() {
			present = append(present, channelState{channel: channel, info: info})
		}
	}

	if len(present) == 0 {
		if pkg.ID == "" {
			return pkg, nil
		}
		for _, risk := range charmhubSyncRisks {
			_ = s.repo.DeleteRelease(ctx, pkg.ID, rule.Track+"/"+risk)
		}
		_ = s.repo.DeleteTrack(ctx, pkg.ID, rule.Track)
		return pkg, nil
	}

	syncAccount, err := s.charmhubSyncAccount(ctx)
	if err != nil {
		return pkg, err
	}

	first := present[0].info
	if pkg.ID == "" {
		pkg = core.Package{
			ID:             first.ID,
			Name:           rule.PackageName,
			Type:           "charm",
			Private:        false,
			Status:         "published",
			OwnerAccountID: syncAccount.ID,
			Authority:      stringPtr(charmhubAuthority),
			DefaultTrack:   stringPtr(defaultTrackForRules(allRules)),
			Publisher: core.Publisher{
				ID:          syncAccount.ID,
				Username:    syncAccount.Username,
				DisplayName: syncAccount.DisplayName,
				Email:       syncAccount.Email,
				Validation:  syncAccount.Validation,
			},
			Store:     s.cfg.PublicAPIURL,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.repo.CreatePackage(ctx, pkg); err != nil {
			return core.Package{}, err
		}
	}

	if err := s.ensureCharmhubTrack(ctx, pkg, rule.Track); err != nil {
		return core.Package{}, err
	}

	pkg.DefaultTrack = stringPtr(defaultTrackForRules(allRules))
	for _, item := range present {
		var resourceRefs []core.ReleaseResourceRef
		pkg, resourceRefs, err = s.ensureCharmhubArtifacts(ctx, pkg, syncAccount.ID, item.info)
		if err != nil {
			return core.Package{}, err
		}
		release := core.Release{
			ID:        uuid.NewString(),
			Channel:   item.channel,
			Revision:  item.info.DefaultRelease.Revision.Revision,
			Base:      item.info.DefaultRelease.Channel.Base,
			Resources: resourceRefs,
			When:      item.info.DefaultRelease.Channel.ReleasedAt,
		}
		if release.When.IsZero() {
			release.When = time.Now().UTC()
		}
		if err := s.repo.ReplaceRelease(ctx, pkg.ID, release); err != nil {
			return core.Package{}, err
		}
	}

	for _, risk := range charmhubSyncRisks {
		channel := rule.Track + "/" + risk
		found := false
		for _, item := range present {
			if item.channel == channel {
				found = true
				break
			}
		}
		if !found {
			_ = s.repo.DeleteRelease(ctx, pkg.ID, channel)
		}
	}

	pkg.UpdatedAt = time.Now().UTC()
	if tracks, err := s.repo.ListTracks(ctx, pkg.ID); err == nil {
		pkg.Tracks = tracks
	}
	if err := s.repo.UpdatePackage(ctx, pkg); err != nil {
		return core.Package{}, err
	}
	return pkg, nil
}

func (s *Service) ensureCharmhubTrack(ctx context.Context, pkg core.Package, track string) error {
	tracks, err := s.repo.ListTracks(ctx, pkg.ID)
	if err != nil {
		return err
	}
	for _, item := range tracks {
		if item.Name == track {
			return nil
		}
	}
	_, err = s.repo.CreateTracks(ctx, pkg.ID, []core.Track{{
		Name:      track,
		CreatedAt: time.Now().UTC(),
	}})
	return err
}

//nolint:gocognit,cyclop,nestif // Artifact mirroring mirrors the external Charmhub workflow step-by-step.
func (s *Service) ensureCharmhubArtifacts(
	ctx context.Context,
	pkg core.Package,
	createdBy string,
	info charmhubclient.PackageChannel,
) (core.Package, []core.ReleaseResourceRef, error) {
	revisionNumber := info.DefaultRelease.Revision.Revision
	if _, err := s.repo.GetRevisionByNumber(ctx, pkg.ID, revisionNumber); errors.Is(err, repo.ErrNotFound) {
		revisionPayload, downloadErr := s.charmhub.Download(ctx, info.DefaultRelease.Revision.Download.URL)
		if downloadErr != nil {
			return core.Package{}, nil, downloadErr
		}
		archive, parseErr := charm.ParseArchiveWithMaxFileSize(revisionPayload, s.cfg.MaxArchiveFileBytes)
		if parseErr != nil {
			return core.Package{}, nil, parseErr
		}

		revisionKey := filepath.ToSlash(filepath.Join("charms", pkg.ID, fmt.Sprintf("%d.charm", revisionNumber)))
		if putErr := s.blobs.Put(ctx, revisionKey, revisionPayload, "application/octet-stream"); putErr != nil {
			return core.Package{}, nil, putErr
		}

		pkg = applyCharmhubPackageMetadata(pkg, info.Result, archive.Manifest)
		pkg.Status = "published"
		pkg.UpdatedAt = time.Now().UTC()
		if tracks, err := s.repo.ListTracks(ctx, pkg.ID); err == nil {
			pkg.Tracks = tracks
		}
		if err := s.repo.UpdatePackage(ctx, pkg); err != nil {
			return core.Package{}, nil, err
		}

		sum256 := sha256.Sum256(revisionPayload)
		sum384 := sha512.Sum384(revisionPayload)
		revision := core.Revision{
			ID:           uuid.NewString(),
			PackageID:    pkg.ID,
			Revision:     revisionNumber,
			Version:      core.FirstNonEmpty(info.DefaultRelease.Revision.Version, fmt.Sprintf("%d", revisionNumber)),
			Status:       "approved",
			CreatedAt:    info.DefaultRelease.Revision.CreatedAt,
			CreatedBy:    createdBy,
			Size:         int64(len(revisionPayload)),
			SHA256:       hex.EncodeToString(sum256[:]),
			SHA384:       hex.EncodeToString(sum384[:]),
			ObjectKey:    revisionKey,
			MetadataYAML: archive.MetadataYAML,
			ConfigYAML:   archive.ConfigYAML,
			ActionsYAML:  archive.ActionsYAML,
			BundleYAML:   archive.BundleYAML,
			ReadmeMD:     archive.ReadmeMD,
			Bases:        extractBases(archive.Manifest),
			Attributes:   mapOrDefault(info.DefaultRelease.Revision.Attributes, map[string]string{"framework": "operator", "language": "unknown"}),
			Relations: map[string]map[string]core.Relation{
				"provides": toCoreRelations(archive.Manifest.Provides),
				"requires": toCoreRelations(archive.Manifest.Requires),
				"peers":    toCoreRelations(archive.Manifest.Peers),
			},
			Subordinate: archive.Manifest.Subordinate,
		}
		if revision.CreatedAt.IsZero() {
			revision.CreatedAt = time.Now().UTC()
		}
		if err := s.repo.CreateRevision(ctx, revision); err != nil {
			return core.Package{}, nil, err
		}
		for resourceName, resource := range archive.Manifest.Resources {
			if _, err := s.repo.UpsertResourceDefinition(ctx, core.ResourceDefinition{
				ID:          uuid.NewString(),
				PackageID:   pkg.ID,
				Name:        resourceName,
				Type:        resource.Type,
				Description: resource.Description,
				Filename:    resource.Filename,
				Optional:    false,
				CreatedAt:   time.Now().UTC(),
			}); err != nil {
				return core.Package{}, nil, err
			}
		}
	} else if err != nil {
		return core.Package{}, nil, err
	}

	resourceRefs := make([]core.ReleaseResourceRef, 0, len(info.DefaultRelease.Resources))
	for _, resource := range info.DefaultRelease.Resources {
		resourceDef, err := s.repo.GetResourceDefinition(ctx, pkg.ID, resource.Name)
		if err != nil {
			return core.Package{}, nil, translateRepoError(err, "resource not declared")
		}
		if _, err := s.repo.GetResourceRevision(ctx, resourceDef.ID, resource.Revision); errors.Is(err, repo.ErrNotFound) {
			resourcePayload, downloadErr := s.charmhub.Download(ctx, resource.Download.URL)
			if downloadErr != nil {
				return core.Package{}, nil, downloadErr
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
				item.CreatedAt = time.Now().UTC()
			}

			if item.Type == "oci-image" {
				pkg, err = s.ensureOCIProvisioned(ctx, pkg)
				if err != nil {
					return core.Package{}, nil, err
				}
				var blob upstreamOCIImageBlob
				if err := json.Unmarshal(resourcePayload, &blob); err != nil {
					return core.Package{}, nil, err
				}
				mirroredDigest, err := s.oci.MirrorImage(
					ctx,
					pkg,
					resource.Name,
					core.FirstNonEmpty(blob.ImageName, resource.Download.URL),
					blob.Username,
					blob.Password,
				)
				if err != nil {
					return core.Package{}, nil, err
				}
				item.OCIImageDigest = core.FirstNonEmpty(mirroredDigest, blob.Digest)
				item.ObjectKey = ""
				if item.Size == 0 {
					item.Size = int64(len(resourcePayload))
				}
			} else {
				resourceKey := filepath.ToSlash(
					filepath.Join("resources", pkg.ID, resource.Name, fmt.Sprintf("%d", resource.Revision)),
				)
				if err := s.blobs.Put(ctx, resourceKey, resourcePayload, "application/octet-stream"); err != nil {
					return core.Package{}, nil, err
				}
				item.ObjectKey = resourceKey
				if item.Size == 0 {
					item.Size = int64(len(resourcePayload))
				}
				if item.SHA256 == "" || item.SHA384 == "" || item.SHA512 == "" || item.SHA3384 == "" {
					sum256 := sha256.Sum256(resourcePayload)
					sum384 := sha512.Sum384(resourcePayload)
					sum512 := sha512.Sum512(resourcePayload)
					item.SHA256 = hex.EncodeToString(sum256[:])
					item.SHA384 = hex.EncodeToString(sum384[:])
					item.SHA512 = hex.EncodeToString(sum512[:])
					item.SHA3384 = item.SHA384
				}
			}

			if err := s.repo.CreateResourceRevision(ctx, item); err != nil {
				return core.Package{}, nil, err
			}
		} else if err != nil {
			return core.Package{}, nil, err
		}

		resourceRefs = append(resourceRefs, core.ReleaseResourceRef{
			Name:     resource.Name,
			Revision: intPointer(resource.Revision),
		})
	}

	return pkg, resourceRefs, nil
}

//nolint:gocognit,cyclop,nestif // Pruning spans releases, revisions, blobs, and OCI digests in one pass.
func (s *Service) pruneSyncedPackage(ctx context.Context, pkg core.Package, rules []core.CharmhubSyncRule) error {
	validTracks := map[string]struct{}{}
	for _, rule := range rules {
		validTracks[rule.Track] = struct{}{}
	}

	releases, err := s.repo.ListReleases(ctx, pkg.ID)
	if err != nil {
		return err
	}
	filteredReleases := make([]core.Release, 0, len(releases))
	for _, release := range releases {
		if _, ok := validTracks[splitChannel(release.Channel).track]; !ok {
			if err := s.repo.DeleteRelease(ctx, pkg.ID, release.Channel); err != nil && !errors.Is(err, repo.ErrNotFound) {
				return err
			}
			continue
		}
		filteredReleases = append(filteredReleases, release)
	}
	referencedRevisions := map[int]struct{}{}
	referencedResources := map[string]map[int]struct{}{}
	for _, release := range filteredReleases {
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

	revisions, err := s.repo.ListRevisions(ctx, pkg.ID, nil)
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
		if err := s.repo.DeleteRevision(ctx, pkg.ID, revision.Revision); err != nil {
			return err
		}
	}

	resourceDefs, err := s.repo.ListResourceDefinitions(ctx, pkg.ID)
	if err != nil {
		return err
	}
	seenDigests := map[string]struct{}{}
	for _, def := range resourceDefs {
		resourceRevisions, err := s.repo.ListResourceRevisions(ctx, def.ID)
		if err != nil {
			return err
		}
		keep := referencedResources[def.Name]
		for _, resourceRevision := range resourceRevisions {
			if keep != nil {
				if _, ok := keep[resourceRevision.Revision]; ok {
					continue
				}
			}
			if resourceRevision.ObjectKey != "" {
				if err := s.blobs.Delete(ctx, resourceRevision.ObjectKey); err != nil {
					return err
				}
			} else if resourceRevision.OCIImageDigest != "" {
				if _, ok := seenDigests[resourceRevision.OCIImageDigest]; !ok {
					if err := s.oci.DeleteImage(ctx, pkg, def.Name, resourceRevision.OCIImageDigest); err != nil {
						return err
					}
					seenDigests[resourceRevision.OCIImageDigest] = struct{}{}
				}
			}
			if err := s.repo.DeleteResourceRevision(ctx, def.ID, resourceRevision.Revision); err != nil {
				return err
			}
		}

		remaining, err := s.repo.ListResourceRevisions(ctx, def.ID)
		if err != nil {
			return err
		}
		if len(remaining) == 0 {
			if err := s.repo.DeleteResourceDefinition(ctx, def.ID); err != nil {
				return err
			}
		}
	}

	tracks, err := s.repo.ListTracks(ctx, pkg.ID)
	if err != nil {
		return err
	}
	for _, track := range tracks {
		if _, ok := validTracks[track.Name]; ok {
			continue
		}
		_ = s.repo.DeleteTrack(ctx, pkg.ID, track.Name)
	}
	return nil
}

//nolint:gocognit,cyclop // Package cleanup is intentionally linear across all stored artifact types.
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

	if s.oci != nil {
		if err := s.oci.DeletePackage(ctx, pkg); err != nil {
			return err
		}
	}

	resourceDefs, err := s.repo.ListResourceDefinitions(ctx, pkg.ID)
	if err != nil {
		return err
	}
	for _, def := range resourceDefs {
		resourceRevisions, err := s.repo.ListResourceRevisions(ctx, def.ID)
		if err != nil {
			return err
		}
		for _, revision := range resourceRevisions {
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

	revisions, err := s.repo.ListRevisions(ctx, pkg.ID, nil)
	if err != nil {
		return err
	}
	for _, revision := range revisions {
		if revision.ObjectKey != "" {
			if err := s.blobs.Delete(ctx, revision.ObjectKey); err != nil {
				return err
			}
		}
		if err := s.repo.DeleteRevision(ctx, pkg.ID, revision.Revision); err != nil {
			return err
		}
	}

	releases, err := s.repo.ListReleases(ctx, pkg.ID)
	if err != nil {
		return err
	}
	for _, release := range releases {
		if err := s.repo.DeleteRelease(ctx, pkg.ID, release.Channel); err != nil && !errors.Is(err, repo.ErrNotFound) {
			return err
		}
	}

	tracks, err := s.repo.ListTracks(ctx, pkg.ID)
	if err != nil {
		return err
	}
	for _, track := range tracks {
		if err := s.repo.DeleteTrack(ctx, pkg.ID, track.Name); err != nil && !errors.Is(err, repo.ErrNotFound) {
			return err
		}
	}

	return s.repo.DeletePackage(ctx, pkg.ID)
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

func mapOrDefault[K comparable, V any](value, fallback map[K]V) map[K]V {
	if len(value) == 0 {
		return fallback
	}
	return value
}

func toCoreRelations(value map[string]core.Relation) map[string]core.Relation {
	if value == nil {
		return map[string]core.Relation{}
	}
	return value
}

func intPointer(value int) *int {
	return &value
}
