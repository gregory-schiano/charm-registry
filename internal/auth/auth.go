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
	"golang.org/x/crypto/bcrypt"

	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
)

// TokenHashScheme identifies the hash algorithm used for a store token.
const (
	TokenHashSchemeSHA256 = "sha256"
	TokenHashSchemeBcrypt = "bcrypt"
)

type TokenRepository interface {
	FindStoreTokenByHash(ctx context.Context, hash string) (core.StoreToken, core.Account, error)
	FindStoreTokensByPrefix(ctx context.Context, prefix string) ([]core.StoreTokenCandidate, error)
	UpdateTokenHashScheme(ctx context.Context, sessionID, hash, prefix, scheme string) error
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

	storeToken, account, err := a.findAndVerifyToken(r.Context(), secret)
	if err == nil {
		if storeToken.RevokedAt != nil || storeToken.ValidUntil.Before(time.Now().UTC()) {
			return Claims{}, nil, fmt.Errorf("cannot authenticate: token revoked or expired")
		}
		// Upgrade SHA-256 tokens to bcrypt on successful auth.
		if storeToken.HashScheme == TokenHashSchemeSHA256 {
			bcryptHash, _ := bcryptHashToken(secret)
			tokenPrefix := TokenPrefixFromRaw(secret)
			_ = a.tokenStore.UpdateTokenHashScheme(r.Context(), storeToken.SessionID, bcryptHash, tokenPrefix, TokenHashSchemeBcrypt)
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
		Username: core.FirstNonEmpty(
			asString(rawClaims[a.config.OIDCUsernameClaim]),
			asString(rawClaims["preferred_username"]),
			asString(rawClaims["email"]),
		),
		DisplayName: core.FirstNonEmpty(
			asString(rawClaims[a.config.OIDCDisplayNameClaim]),
			asString(rawClaims["name"]),
			asString(rawClaims["preferred_username"]),
		),
		Email: core.FirstNonEmpty(asString(rawClaims[a.config.OIDCEmailClaim]), asString(rawClaims["email"])),
	}, nil, nil
}

// AuthenticateToken validates a raw store token string and returns the
// associated claims and token record. It is used by the token-exchange handler
// to validate a token extracted from the charmcraft "Macaroons" header.
func (a *Authenticator) AuthenticateToken(ctx context.Context, raw string) (Claims, *core.StoreToken, error) {
	storeToken, account, err := a.findAndVerifyToken(ctx, raw)
	if err != nil {
		return Claims{}, nil, fmt.Errorf("cannot authenticate: token not found")
	}
	if storeToken.RevokedAt != nil || storeToken.ValidUntil.Before(time.Now().UTC()) {
		return Claims{}, nil, fmt.Errorf("cannot authenticate: token revoked or expired")
	}
	// Upgrade SHA-256 tokens to bcrypt on successful auth.
	if storeToken.HashScheme == TokenHashSchemeSHA256 {
		bcryptHash, _ := bcryptHashToken(raw)
		tokenPrefix := TokenPrefixFromRaw(raw)
		_ = a.tokenStore.UpdateTokenHashScheme(ctx, storeToken.SessionID, bcryptHash, tokenPrefix, TokenHashSchemeBcrypt)
	}
	return Claims{
		Subject:     account.Subject,
		Username:    account.Username,
		DisplayName: account.DisplayName,
		Email:       account.Email,
	}, &storeToken, nil
}

// HashToken returns the SHA-256 hash for a raw store token.
//
// Deprecated: use bcryptHashToken for new tokens.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// bcryptHashToken returns the bcrypt hash for a raw store token.
func bcryptHashToken(raw string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(raw), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("cannot hash token: %w", err)
	}
	return string(hash), nil
}

// TokenPrefixFromRaw extracts the first 8 characters of a raw token for indexed lookup.
func TokenPrefixFromRaw(raw string) string {
	if len(raw) < 8 {
		return raw
	}
	return raw[:8]
}

// VerifyTokenHash verifies a raw token against a stored hash using the given scheme.
func VerifyTokenHash(rawToken, storedHash, scheme string) bool {
	switch scheme {
	case TokenHashSchemeBcrypt:
		return bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(rawToken)) == nil
	case TokenHashSchemeSHA256, "":
		return HashToken(rawToken) == storedHash
	default:
		return false
	}
}

// findAndVerifyToken looks up a store token by prefix (bcrypt) or hash (SHA-256)
// and verifies it against the stored hash.
func (a *Authenticator) findAndVerifyToken(ctx context.Context, raw string) (core.StoreToken, core.Account, error) {
	// Try prefix-based lookup first (bcrypt tokens).
	prefix := TokenPrefixFromRaw(raw)
	if len(prefix) >= 4 {
		candidates, err := a.tokenStore.FindStoreTokensByPrefix(ctx, prefix)
		if err == nil {
			for _, candidate := range candidates {
				if VerifyTokenHash(raw, candidate.Token.TokenHash, candidate.Token.HashScheme) {
					return candidate.Token, candidate.Account, nil
				}
			}
		}
	}
	// Fall back to SHA-256 hash lookup (legacy tokens).
	tokenHash := HashToken(raw)
	token, account, err := a.tokenStore.FindStoreTokenByHash(ctx, tokenHash)
	if err != nil {
		return core.StoreToken{}, core.Account{}, err
	}
	if !VerifyTokenHash(raw, token.TokenHash, token.HashScheme) {
		return core.StoreToken{}, core.Account{}, fmt.Errorf("cannot authenticate: token verification failed")
	}
	return token, account, nil
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
	hash, err = bcryptHashToken(raw)
	if err != nil {
		return "", "", err
	}
	return raw, hash, nil
}

func (a *Authenticator) parseInsecureToken(raw string) (Claims, bool) {
	if !a.config.EnableInsecureDevAuth {
		return Claims{}, false
	}
	if !strings.HasPrefix(raw, "dev:") {
		return Claims{}, false
	}
	parts := strings.SplitN(strings.TrimPrefix(raw, "dev:"), ":", 2)
	if len(parts) < 2 {
		return Claims{}, false
	}
	return Claims{
		Subject:     parts[0],
		Username:    parts[1],
		DisplayName: parts[1],
		Email:       parts[1] + "@example.invalid",
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
