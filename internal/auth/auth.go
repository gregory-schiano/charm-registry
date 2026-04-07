package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
)

type TokenRepository interface {
	FindStoreTokenByHash(ctx context.Context, hash string) (core.StoreToken, core.Account, error)
}

type Claims struct {
	Subject     string
	Username    string
	DisplayName string
	Email       string
}

type Authenticator struct {
	provider   *oidc.Provider
	verifier   *oidc.IDTokenVerifier
	config     config.Config
	tokenStore TokenRepository
}

// New builds an [Authenticator] from the configured auth backends.
//
// The following errors may be returned:
// - Errors from discovering the configured OIDC provider.
func New(ctx context.Context, cfg config.Config, tokenStore TokenRepository) (*Authenticator, error) {
	auth := &Authenticator{
		config:     cfg,
		tokenStore: tokenStore,
	}
	if cfg.OIDCIssuerURL != "" && cfg.OIDCClientID != "" {
		provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuerURL)
		if err != nil {
			return nil, fmt.Errorf("cannot configure OIDC provider: %w", err)
		}
		auth.provider = provider
		auth.verifier = provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID})
	}
	return auth, nil
}

// Authenticate resolves the request identity from bearer credentials.
//
// The following errors may be returned:
// - The authorization scheme is unsupported.
// - The presented store token is expired or revoked.
// - No valid credentials can be verified.
// - OIDC token verification or claim decoding fails.
func (a *Authenticator) Authenticate(r *http.Request) (Claims, *core.StoreToken, error) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return Claims{}, nil, nil
	}
	// Accept both Bearer (standard) and Macaroon (legacy charmcraft) schemes.
	var prefix string
	switch {
	case strings.HasPrefix(header, "Bearer "):
		prefix = "Bearer "
	case strings.HasPrefix(header, "Macaroon "):
		prefix = "Macaroon "
	default:
		return Claims{}, nil, fmt.Errorf("cannot authenticate: unsupported authorization scheme")
	}
	secret := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if secret == "" {
		return Claims{}, nil, nil
	}

	if claims, ok := a.parseInsecureToken(secret); ok {
		return claims, nil, nil
	}

	tokenHash := HashToken(secret)
	storeToken, account, err := a.tokenStore.FindStoreTokenByHash(r.Context(), tokenHash)
	if err == nil {
		if storeToken.RevokedAt != nil || storeToken.ValidUntil.Before(time.Now().UTC()) {
			return Claims{}, nil, fmt.Errorf("cannot authenticate: token revoked or expired")
		}
		return Claims{
			Subject:     account.Subject,
			Username:    account.Username,
			DisplayName: account.DisplayName,
			Email:       account.Email,
		}, &storeToken, nil
	}

	if a.verifier == nil {
		return Claims{}, nil, fmt.Errorf("cannot authenticate: no valid credentials found")
	}
	idToken, err := a.verifier.Verify(r.Context(), secret)
	if err != nil {
		return Claims{}, nil, fmt.Errorf("cannot verify OIDC token: %w", err)
	}
	var rawClaims map[string]any
	if err := idToken.Claims(&rawClaims); err != nil {
		return Claims{}, nil, fmt.Errorf("cannot decode OIDC claims: %w", err)
	}

	return Claims{
		Subject: asString(rawClaims["sub"]),
		Username: firstNonEmpty(
			asString(rawClaims[a.config.OIDCUsernameClaim]),
			asString(rawClaims["preferred_username"]),
			asString(rawClaims["email"]),
		),
		DisplayName: firstNonEmpty(
			asString(rawClaims[a.config.OIDCDisplayNameClaim]),
			asString(rawClaims["name"]),
			asString(rawClaims["preferred_username"]),
		),
		Email: firstNonEmpty(asString(rawClaims[a.config.OIDCEmailClaim]), asString(rawClaims["email"])),
	}, nil, nil
}

// HashToken returns the stable SHA-256 hash for a raw store token.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// NewOpaqueToken creates a random opaque token and its stored hash.
//
// The following errors may be returned:
// - Errors from the secure random source.
func NewOpaqueToken() (raw, hash string, err error) {
	seed := make([]byte, 32)
	if _, err = rand.Read(seed); err != nil {
		return "", "", err
	}
	raw = "cr_" + base64.RawURLEncoding.EncodeToString(seed)
	return raw, HashToken(raw), nil
}

func (a *Authenticator) parseInsecureToken(raw string) (Claims, bool) {
	if !a.config.EnableInsecureDevAuth {
		return Claims{}, false
	}
	if !strings.HasPrefix(raw, "dev:") {
		return Claims{}, false
	}
	parts := strings.Split(raw, ":")
	if len(parts) < 3 {
		return Claims{}, false
	}
	return Claims{
		Subject:     parts[1],
		Username:    parts[2],
		DisplayName: parts[2],
		Email:       parts[2] + "@example.invalid",
	}, true
}

func asString(value any) string {
	if value == nil {
		return ""
	}
	if str, ok := value.(string); ok {
		return str
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
