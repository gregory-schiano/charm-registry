package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
)

// --- Authorization guards ---

func (s *Service) requireAuth(identity core.Identity) error {
	if !identity.Authenticated {
		return newError(401, "unauthorized", "authentication required")
	}
	return nil
}

func (s *Service) requirePermission(identity core.Identity, permission string) error {
	if err := s.requireAuth(identity); err != nil {
		return err
	}
	if identity.Token == nil || len(identity.Token.Permissions) == 0 {
		return nil
	}
	for _, item := range identity.Token.Permissions {
		if item == permission || item == permPackageManage && strings.HasPrefix(permission, "package-") {
			return nil
		}
	}
	return newError(403, "forbidden", "token does not grant required permission")
}

func (s *Service) requirePermissionOrAnonymous(identity core.Identity, permission string) error {
	if !identity.Authenticated {
		return nil
	}
	return s.requirePermission(identity, permission)
}

func (s *Service) requirePackageView(
	ctx context.Context,
	identity core.Identity,
	pkg core.Package,
	requireTokenPermission bool,
) error {
	if !pkg.Private {
		if requireTokenPermission {
			return s.requirePermissionOrAnonymous(identity, permPackageView)
		}
		return nil
	}
	if err := s.requireAuth(identity); err != nil {
		return err
	}
	allowed, err := s.repo.CanViewPackage(ctx, pkg.ID, identity.Account.ID)
	if err != nil {
		return err
	}
	if !allowed {
		return newError(403, "forbidden", "package is private")
	}
	if identity.Token != nil && len(identity.Token.Packages) > 0 && !tokenAllowsPackage(identity.Token, pkg) {
		return newError(403, "forbidden", "token does not allow this package")
	}
	if requireTokenPermission {
		return s.requirePermission(identity, permPackageView)
	}
	return nil
}

func (s *Service) requirePackageManage(
	ctx context.Context,
	identity core.Identity,
	pkg core.Package,
	permission string,
) error {
	if err := s.requirePermission(identity, permission); err != nil {
		return err
	}
	allowed, err := s.repo.CanManagePackage(ctx, pkg.ID, identity.Account.ID)
	if err != nil {
		return err
	}
	if !allowed {
		return newError(403, "forbidden", "package management is not allowed")
	}
	if identity.Token != nil && len(identity.Token.Packages) > 0 && !tokenAllowsPackage(identity.Token, pkg) {
		return newError(403, "forbidden", "token does not allow this package")
	}
	return nil
}

func (s *Service) enforceChannelRestriction(identity core.Identity, channel string) error {
	if identity.Token == nil || len(identity.Token.Channels) == 0 {
		return nil
	}
	for _, allowed := range identity.Token.Channels {
		if allowed == channel {
			return nil
		}
	}
	return newError(403, "forbidden", "token does not allow this channel")
}

func (s *Service) canSeePackage(ctx context.Context, identity core.Identity, pkg core.Package) bool {
	return s.requirePackageView(ctx, identity, pkg, false) == nil
}

// --- URL helpers ---

func (s *Service) charmDownloadURL(packageID string, revision int) string {
	return s.cfg.PublicAPIURL + "/api/v1/charms/download/" + packageID + "_" + fmt.Sprintf("%d", revision) + ".charm"
}

func (s *Service) resourceDownloadURL(packageID, resourceName string, revision int) string {
	return s.cfg.PublicAPIURL + "/api/v1/resources/download/charm_" + packageID + "." + resourceName + "_" + fmt.Sprintf(
		"%d",
		revision,
	)
}

// --- Token and package selectors ---

func tokenAllowsPackage(token *core.StoreToken, pkg core.Package) bool {
	for _, candidate := range token.Packages {
		if candidate.ID != "" && candidate.ID == pkg.ID {
			return true
		}
		if candidate.Name != "" && candidate.Name == pkg.Name {
			return true
		}
	}
	return false
}

// --- Registry helpers ---

func registryImageName(cfg config.Config, charmName, resourceName string) string {
	withoutScheme := strings.TrimPrefix(strings.TrimPrefix(cfg.PublicRegistryURL, "https://"), "http://")
	return withoutScheme + "/" + filepath.ToSlash(filepath.Join(cfg.RegistryRepositoryRoot, charmName, resourceName))
}

// --- Manifest helpers ---

func extractBases(manifest core.CharmManifest) []core.Base {
	var bases []core.Base
	type manifestBase struct {
		Architectures []string `yaml:"architectures"`
		Architecture  string   `yaml:"architecture"`
		Channel       string   `yaml:"channel"`
		Name          string   `yaml:"name"`
	}
	var raw struct {
		Bases []manifestBase `yaml:"bases"`
	}
	payload, _ := json.Marshal(manifest)
	_ = json.Unmarshal(payload, &raw)
	if len(raw.Bases) > 0 {
		for _, base := range raw.Bases {
			if len(base.Architectures) > 0 {
				for _, arch := range base.Architectures {
					bases = append(bases, core.Base{Name: base.Name, Channel: base.Channel, Architecture: arch})
				}
				continue
			}
			bases = append(bases, core.Base{Name: base.Name, Channel: base.Channel, Architecture: base.Architecture})
		}
	}
	if len(bases) == 0 {
		bases = []core.Base{{Name: "ubuntu", Channel: "22.04", Architecture: "amd64"}}
	}
	return bases
}

// --- String utilities ---

func compactID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

func sanitizeSubject(subject string) string {
	replacer := strings.NewReplacer(":", "-", "|", "-", "/", "-")
	return replacer.Replace(subject)
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

func channelOrDefault(channel *string) string {
	if channel == nil || *channel == "" {
		return ""
	}
	return *channel
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstLink(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func nullIfEmpty[T any](value []T) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func emptySliceIfNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

// --- Error helpers ---

// translateRepoError converts a repository-layer error into a typed service
// error with an appropriate HTTP status code and Charmhub API error code.
// Unrecognised errors are returned as-is so the API layer can log and return
// a generic 500.
func translateRepoError(err error, message string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, repo.ErrNotFound):
		return newError(404, "not-found", message)
	case errors.Is(err, repo.ErrConflict):
		// HTTP 409 with the Charmhub-specified error code for duplicate
		// registration.
		return newError(409, "already-registered", message)
	default:
		return err
	}
}

func detectUploadKind(filename string) string {
	switch filepath.Ext(filename) {
	case ".charm":
		return "revision"
	default:
		return "resource"
	}
}
