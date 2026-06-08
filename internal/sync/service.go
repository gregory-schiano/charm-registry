package registrysync

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	stdsync "sync"
	"time"

	"github.com/gschiano/charm-registry/internal/blob"
	charmhubclient "github.com/gschiano/charm-registry/internal/charmhub"
	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
	"github.com/gschiano/charm-registry/internal/service"
)

const (
	charmhubAuthority             = "charmhub"
	charmhubSyncAccountID         = "internal-charmhub-sync"
	charmhubSyncAccountSubject    = "internal|charmhub-sync"
	charmhubSyncAccountName       = "charmhub-sync"
	charmhubSyncStatusPending     = "pending"
	charmhubSyncStatusRunning     = "running"
	charmhubSyncStatusOK          = "ok"
	charmhubSyncStatusError       = "error"
	charmhubSyncStatusDeleting    = "deleting"
	charmhubSyncStatusDeleteError = "delete-error"

	messageResourceNotDeclared   = "resource not declared"
	messageSyncRuleAlreadyExists = "sync rule already exists"
	messageSyncRuleNotFound      = "sync rule not found"
)

var charmhubSyncRisks = []string{"stable", "candidate", "beta", "edge"}

type Service struct {
	cfg       config.Config
	repo      repo.PackageRepo
	syncRules repo.CharmhubSyncRepo
	accounts  repo.AccountRepo
	blobs     blob.Store
	oci       service.OCIRegistry
	Clock     func() time.Time
	charmhub  charmhubClient
	manager   *Manager
}

type Manager struct {
	service *Service
	cancel  context.CancelFunc
	done    chan struct{}
	wake    chan struct{}

	mu      stdsync.Mutex
	pending map[string]struct{}
}

type upstreamOCIImageBlob struct {
	ImageName string `json:"ImageName"`
	Password  string `json:"Password"`
	Username  string `json:"Username"`
	Digest    string `json:"Digest"`
}

type charmhubClient interface {
	GetChannel(ctx context.Context, name, channel string) (charmhubclient.PackageChannel, error)
	GetInfo(ctx context.Context, name string) (charmhubclient.PackageChannel, error)
	RefreshChannel(ctx context.Context, name, channel string, base core.Base) (charmhubclient.PackageChannel, error)
	Download(ctx context.Context, artifactURL string) ([]byte, error)
}

func New(cfg config.Config, repository repo.Backend, blobs blob.Store, oci service.OCIRegistry) *Service {
	return &Service{
		cfg:       cfg,
		repo:      repository,
		syncRules: repository,
		accounts:  repository,
		blobs:     blobs,
		oci:       oci,
		Clock:     time.Now,
		charmhub: charmhubclient.NewWithLimits(
			cfg.CharmhubURL,
			cfg.CharmhubMaxResponseBytes,
			cfg.CharmhubMaxArtifactBytes,
		),
	}
}

func (s *Service) now() time.Time {
	if s.Clock == nil {
		return time.Now().UTC()
	}
	return s.Clock().UTC()
}

func (s *Service) StartManager(ctx context.Context) *Manager {
	if s.manager != nil {
		return s.manager
	}
	managerCtx, cancel := context.WithCancel(ctx)
	manager := &Manager{
		service: s,
		cancel:  cancel,
		done:    make(chan struct{}),
		wake:    make(chan struct{}, 1),
		pending: map[string]struct{}{},
	}
	s.manager = manager
	go manager.run(managerCtx)
	return manager
}

func (m *Manager) Close() error {
	m.cancel()
	<-m.done
	return nil
}

func (m *Manager) Enqueue(packageName string) {
	packageName = strings.TrimSpace(packageName)
	if packageName == "" {
		return
	}
	m.mu.Lock()
	m.pending[packageName] = struct{}{}
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
	slog.DebugContext(context.Background(), "charmhub sync enqueued", "package", packageName)
}

func (m *Manager) run(ctx context.Context) {
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

func (m *Manager) runPending(ctx context.Context) {
	packageNames := m.takePending()
	if len(packageNames) > 0 {
		slog.DebugContext(ctx, "processing pending charmhub sync queue", "package_count", len(packageNames))
	}
	for _, packageName := range packageNames {
		if err := m.service.reconcilePackage(ctx, packageName); err != nil {
			slog.ErrorContext(ctx, "charmhub sync failed", "package", packageName, "error", err)
		}
	}
}

func (m *Manager) runAll(ctx context.Context) {
	rules, err := m.service.syncRules.ListCharmhubSyncRules(ctx)
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
	slog.DebugContext(ctx, "processing scheduled charmhub sync", "package_count", len(packageNames))
	for _, packageName := range packageNames {
		if err := m.service.reconcilePackage(ctx, packageName); err != nil {
			slog.ErrorContext(ctx, "charmhub sync failed", "package", packageName, "error", err)
		}
	}
}

func (m *Manager) takePending() []string {
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
	if err := requireAdmin(identity); err != nil {
		return nil, err
	}
	return s.syncRules.ListCharmhubSyncRules(ctx)
}

func (s *Service) AddCharmhubSyncRule(
	ctx context.Context,
	identity core.Identity,
	packageName, track string,
	bases, architectures []string,
) (core.CharmhubSyncRule, error) {
	if err := requireAdmin(identity); err != nil {
		return core.CharmhubSyncRule{}, err
	}
	packageName = strings.TrimSpace(packageName)
	track, err := normalizeSyncTrack(track)
	if err != nil {
		return core.CharmhubSyncRule{}, err
	}
	bases, err = normalizeSyncBases(bases)
	if err != nil {
		return core.CharmhubSyncRule{}, err
	}
	architectures = normalizeSyncArchitectures(architectures)
	if packageName == "" {
		return core.CharmhubSyncRule{}, newError(service.ErrorKindInvalidRequest, "invalid-request", "package name is required")
	}

	if pkg, err := s.repo.GetPackageByName(ctx, packageName); err == nil {
		if !isCharmhubManagedPackage(pkg) {
			return core.CharmhubSyncRule{}, newError(
				service.ErrorKindConflict,
				"package-exists",
				"cannot synchronize a package that already exists outside Charmhub synchronization",
			)
		}
	} else if !errors.Is(err, repo.ErrNotFound) {
		return core.CharmhubSyncRule{}, err
	}

	now := s.now()
	rule := core.CharmhubSyncRule{
		PackageName:        packageName,
		Track:              track,
		Bases:              bases,
		Architectures:      architectures,
		CreatedByAccountID: identity.Account.ID,
		CreatedAt:          now,
		UpdatedAt:          now,
		LastSyncStatus:     charmhubSyncStatusPending,
	}
	if err := s.syncRules.CreateCharmhubSyncRule(ctx, rule); err != nil {
		return core.CharmhubSyncRule{}, translateRepoError(err, messageSyncRuleAlreadyExists)
	}
	slog.InfoContext(ctx, "charmhub sync rule added",
		"package", rule.PackageName,
		"track", rule.Track,
		"base_count", len(rule.Bases),
		"architecture_count", len(rule.Architectures),
		"account_id", identity.Account.ID,
	)
	s.enqueue(packageName)
	return rule, nil
}

func (s *Service) RemoveCharmhubSyncRule(ctx context.Context, identity core.Identity, packageName, track string) error {
	if err := requireAdmin(identity); err != nil {
		return err
	}
	packageName = strings.TrimSpace(packageName)
	track, err := normalizeSyncTrack(track)
	if err != nil {
		return err
	}
	rules, err := s.syncRules.ListCharmhubSyncRulesByPackageName(ctx, packageName)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		if rule.Track != track {
			continue
		}
		rule.LastSyncStatus = charmhubSyncStatusDeleting
		rule.LastSyncStartedAt = nil
		rule.LastSyncFinishedAt = nil
		rule.LastSyncError = nil
		rule.UpdatedAt = s.now()
		if err := s.syncRules.UpdateCharmhubSyncRule(ctx, rule); err != nil {
			return translateRepoError(err, messageSyncRuleNotFound)
		}
		slog.InfoContext(ctx, "charmhub sync rule marked for deletion",
			"package", rule.PackageName,
			"track", rule.Track,
			"account_id", identity.Account.ID,
		)
		s.enqueue(packageName)
		return nil
	}
	return newError(service.ErrorKindNotFound, "not-found", messageSyncRuleNotFound)
}

func (s *Service) TriggerCharmhubSync(ctx context.Context, identity core.Identity, packageName string) error {
	if err := requireAdmin(identity); err != nil {
		return err
	}
	packageName = strings.TrimSpace(packageName)
	if packageName == "" {
		return newError(service.ErrorKindInvalidRequest, "invalid-request", "package name is required")
	}
	rules, err := s.syncRules.ListCharmhubSyncRulesByPackageName(ctx, packageName)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return newError(service.ErrorKindNotFound, "not-found", "package is not configured for Charmhub synchronization")
	}
	slog.InfoContext(ctx, "charmhub sync manually triggered",
		"package", packageName,
		"rule_count", len(rules),
		"account_id", identity.Account.ID,
	)
	s.enqueue(packageName)
	return nil
}

func (s *Service) enqueue(packageName string) {
	if s.manager != nil {
		s.manager.Enqueue(packageName)
	}
}

func requireAdmin(identity core.Identity) error {
	if identity.System {
		return nil
	}
	if !identity.Authenticated {
		return newError(service.ErrorKindUnauthorized, "unauthorized", "authentication required")
	}
	if identity.Account.IsAdmin {
		return nil
	}
	return newError(service.ErrorKindForbidden, "forbidden", "admin access is required")
}

func normalizeSyncTrack(track string) (string, error) {
	track = strings.TrimSpace(track)
	if track == "" {
		return "", newError(service.ErrorKindInvalidRequest, "invalid-request", "track is required")
	}
	if strings.Contains(track, "/") {
		return "", newError(service.ErrorKindInvalidRequest, "invalid-request", "track must not include a risk")
	}
	return track, nil
}

func normalizeSyncBases(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, err := parseSyncBaseSelector(value); err != nil {
			return nil, err
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	slices.Sort(out)
	return out, nil
}

func normalizeSyncArchitectures(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

func parseSyncBaseSelector(value string) (core.Base, error) {
	name, channel, ok := strings.Cut(value, "@")
	if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(channel) == "" {
		return core.Base{}, newError(
			service.ErrorKindInvalidRequest,
			"invalid-request",
			"base filters must use name@channel syntax, for example ubuntu@22.04",
		)
	}
	return core.Base{Name: strings.TrimSpace(name), Channel: strings.TrimSpace(channel)}, nil
}

func isCharmhubManagedPackage(pkg core.Package) bool {
	return pkg.Authority != nil && *pkg.Authority == charmhubAuthority
}

func (s *Service) charmhubSyncIdentity(ctx context.Context) (core.Identity, error) {
	account, err := s.accounts.EnsureAccount(ctx, core.Account{
		ID:          charmhubSyncAccountID,
		Subject:     charmhubSyncAccountSubject,
		Username:    charmhubSyncAccountName,
		DisplayName: "Charmhub",
		Email:       "charmhub-sync@example.invalid",
		Validation:  "verified",
		IsAdmin:     true,
		CreatedAt:   s.now(),
	})
	if err != nil {
		return core.Identity{}, err
	}
	return core.NewSystemIdentity(account), nil
}

func timePtr(value time.Time) *time.Time {
	return &value
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func firstLink(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func checkContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func extractBases(manifest core.CharmManifest) []core.Base {
	var bases []core.Base
	for _, base := range manifest.Bases {
		if len(base.Architectures) > 0 {
			for _, arch := range base.Architectures {
				bases = append(bases, core.Base{Name: base.Name, Channel: base.Channel, Architecture: arch})
			}
			continue
		}
		if base.Architecture != "" {
			bases = append(bases, core.Base{Name: base.Name, Channel: base.Channel, Architecture: base.Architecture})
		}
	}
	if len(bases) == 0 {
		bases = []core.Base{{Name: "ubuntu", Channel: "22.04", Architecture: "amd64"}}
	}
	return bases
}

func mergeLinks(existing map[string][]string, docs, issues, source string, websites []string) map[string][]string {
	out := map[string][]string{}
	for key, values := range existing {
		out[key] = append([]string(nil), values...)
	}
	if docs != "" {
		out["docs"] = uniqueAppend(out["docs"], docs)
	}
	if issues != "" {
		out["issues"] = uniqueAppend(out["issues"], issues)
	}
	if source != "" {
		out["source"] = uniqueAppend(out["source"], source)
	}
	for _, website := range websites {
		out["website"] = uniqueAppend(out["website"], website)
	}
	return out
}

func uniqueAppend(values []string, candidate string) []string {
	for _, value := range values {
		if value == candidate {
			return values
		}
	}
	return append(values, candidate)
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

func newError(kind service.ErrorKind, code, message string) error {
	return &service.Error{Kind: kind, Code: code, Message: message}
}

func newErrorWithCause(kind service.ErrorKind, code, message string, cause error) error {
	return &service.Error{Kind: kind, Code: code, Message: message, Cause: cause}
}

func translateRepoError(err error, message string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, repo.ErrNotFound):
		return newError(service.ErrorKindNotFound, "not-found", message)
	case errors.Is(err, repo.ErrConflict):
		return newError(service.ErrorKindConflict, "already-registered", message)
	default:
		return err
	}
}
