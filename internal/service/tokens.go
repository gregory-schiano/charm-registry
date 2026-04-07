package service

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/gschiano/charm-registry/internal/auth"
	"github.com/gschiano/charm-registry/internal/core"
)

// ResolveIdentity upserts the authenticated subject into local state.
//
// The following errors may be returned:
// - Errors from persisting the account record.
func (s *Service) ResolveIdentity(
	ctx context.Context,
	claims auth.Claims,
	storeToken *core.StoreToken,
) (core.Identity, error) {
	if claims.Subject == "" {
		return core.Identity{}, nil
	}
	account, err := s.repo.EnsureAccount(ctx, core.Account{
		ID:          uuid.NewString(),
		Subject:     claims.Subject,
		Username:    firstNonEmpty(claims.Username, strings.ReplaceAll(claims.Subject, "|", "_")),
		DisplayName: firstNonEmpty(claims.DisplayName, claims.Username, claims.Subject),
		Email:       firstNonEmpty(claims.Email, sanitizeSubject(claims.Subject)+"@example.invalid"),
		Validation:  "verified",
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		return core.Identity{}, err
	}
	return core.Identity{
		Account:       account,
		Token:         storeToken,
		Authenticated: true,
	}, nil
}

// IssueStoreToken creates a store token for the authenticated account.
//
// The following errors may be returned:
// - Authentication, token generation, or repository errors.
func (s *Service) IssueStoreToken(
	ctx context.Context,
	identity core.Identity,
	req IssueTokenRequest,
) (string, core.StoreToken, error) {
	if err := s.requireAuth(identity); err != nil {
		return "", core.StoreToken{}, err
	}
	permissions := req.Permissions
	if len(permissions) == 0 {
		permissions = append([]string(nil), defaultPermissions...)
	}
	ttl := 30 * time.Hour
	if req.TTL != nil && *req.TTL > 0 {
		ttl = time.Duration(*req.TTL) * time.Second
	}
	raw, hash, err := auth.NewOpaqueToken()
	if err != nil {
		return "", core.StoreToken{}, err
	}
	now := time.Now().UTC()
	token := core.StoreToken{
		SessionID:   uuid.NewString(),
		TokenHash:   hash,
		AccountID:   identity.Account.ID,
		Description: req.Description,
		Packages:    req.Packages,
		Channels:    req.Channels,
		Permissions: permissions,
		ValidSince:  now,
		ValidUntil:  now.Add(ttl),
	}
	if err := s.repo.CreateStoreToken(ctx, token); err != nil {
		return "", core.StoreToken{}, err
	}
	return raw, token, nil
}

// ExchangeStoreToken exchanges the caller's current token for a new one.
//
// The following errors may be returned:
// - Errors returned by [Service.IssueStoreToken].
func (s *Service) ExchangeStoreToken(ctx context.Context, identity core.Identity, description *string) (string, error) {
	req := IssueTokenRequest{Description: description}
	if identity.Token != nil {
		req.Channels = append([]string(nil), identity.Token.Channels...)
		req.Permissions = append([]string(nil), identity.Token.Permissions...)
		req.Packages = append([]core.PackageSelector(nil), identity.Token.Packages...)
	}
	raw, _, err := s.IssueStoreToken(ctx, identity, req)
	return raw, err
}

// ListStoreTokens lists store tokens for the authenticated account.
//
// The following errors may be returned:
// - Authentication or repository errors.
func (s *Service) ListStoreTokens(
	ctx context.Context,
	identity core.Identity,
	includeInactive bool,
) ([]core.StoreToken, error) {
	if err := s.requireAuth(identity); err != nil {
		return nil, err
	}
	return s.repo.ListStoreTokens(ctx, identity.Account.ID, includeInactive)
}

// RevokeStoreToken revokes a store token for the authenticated account.
//
// The following errors may be returned:
// - Authentication or repository errors.
func (s *Service) RevokeStoreToken(ctx context.Context, identity core.Identity, sessionID string) error {
	if err := s.requireAuth(identity); err != nil {
		return err
	}
	return s.repo.RevokeStoreToken(ctx, identity.Account.ID, sessionID, identity.Account.ID)
}

// MacaroonInfo returns Charmhub-compatible token account details.
//
// The following errors may be returned:
// - Authentication errors.
func (s *Service) MacaroonInfo(identity core.Identity) (map[string]any, error) {
	if err := s.requireAuth(identity); err != nil {
		return nil, err
	}
	var packages []core.PackageSelector
	var channels []string
	var permissions []string
	if identity.Token != nil {
		packages = identity.Token.Packages
		channels = identity.Token.Channels
		permissions = identity.Token.Permissions
	}
	return map[string]any{
		"account": map[string]any{
			"display-name": identity.Account.DisplayName,
			"email":        identity.Account.Email,
			"id":           identity.Account.ID,
			"username":     identity.Account.Username,
			"validation":   identity.Account.Validation,
		},
		"packages":    emptySliceIfNil(packages),
		"channels":    emptySliceIfNil(channels),
		"permissions": emptySliceIfNil(permissions),
	}, nil
}

// DeprecatedWhoAmI returns the legacy whoami response shape.
//
// The following errors may be returned:
// - Authentication errors.
func (s *Service) DeprecatedWhoAmI(identity core.Identity) (map[string]any, error) {
	if err := s.requireAuth(identity); err != nil {
		return nil, err
	}
	return map[string]any{
		"display-name": identity.Account.DisplayName,
		"email":        identity.Account.Email,
		"id":           identity.Account.ID,
		"username":     identity.Account.Username,
		"validation":   identity.Account.Validation,
	}, nil
}
