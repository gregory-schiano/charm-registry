package auth

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
)

func TestHashTokenConsistent(t *testing.T) {
	t.Parallel()

	hash1 := HashToken("test-token")
	hash2 := HashToken("test-token")

	assert.Equal(t, hash1, hash2)
	assert.Len(t, hash1, 64) // SHA-256 hex = 64 chars
}

func TestHashTokenDifferentInputs(t *testing.T) {
	t.Parallel()
	assert.NotEqual(t, HashToken("token-a"), HashToken("token-b"))
}

func TestNewOpaqueToken(t *testing.T) {
	t.Parallel()

	raw, hash, err := NewOpaqueToken()

	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(raw, "cr_"), "token should start with cr_ prefix")
	assert.Equal(t, HashToken(raw), hash)
	assert.Len(t, hash, 64)
}

func TestNewOpaqueTokenUniqueness(t *testing.T) {
	t.Parallel()

	raw1, _, err := NewOpaqueToken()
	require.NoError(t, err)
	raw2, _, err := NewOpaqueToken()
	require.NoError(t, err)

	assert.NotEqual(t, raw1, raw2)
}

func TestAuthenticateEmptyHeader(t *testing.T) {
	t.Parallel()

	a := &Authenticator{config: config.Config{}}
	req := httptest.NewRequest("GET", "/", nil)

	claims, token, err := a.Authenticate(req)

	require.NoError(t, err)
	assert.Empty(t, claims.Subject)
	assert.Nil(t, token)
}

func TestAuthenticateUnsupportedScheme(t *testing.T) {
	t.Parallel()

	a := &Authenticator{config: config.Config{}}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

	_, _, err := a.Authenticate(req)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported authorization scheme")
}

func TestAuthenticateBearerWithOnlyWhitespace(t *testing.T) {
	t.Parallel()

	// "Bearer   " is trimmed to "Bearer" which lacks the "Bearer " prefix
	a := &Authenticator{config: config.Config{}}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer   ")

	_, _, err := a.Authenticate(req)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported authorization scheme")
}

func TestAuthenticateInsecureDevToken(t *testing.T) {
	t.Parallel()

	a := &Authenticator{config: config.Config{EnableInsecureDevAuth: true}}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer dev:alice:Alice")

	claims, token, err := a.Authenticate(req)

	require.NoError(t, err)
	assert.Equal(t, "alice", claims.Subject)
	assert.Equal(t, "Alice", claims.Username)
	assert.Equal(t, "Alice", claims.DisplayName)
	assert.Equal(t, "Alice@example.invalid", claims.Email)
	assert.Nil(t, token)
}

func TestAuthenticateInsecureDevTokenDisabled(t *testing.T) {
	t.Parallel()

	// Arrange: dev auth disabled, no OIDC configured
	repository := repo.NewMemory()
	a := &Authenticator{
		config:     config.Config{EnableInsecureDevAuth: false},
		tokenStore: repository,
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer dev:alice:Alice")

	// Act
	_, _, err := a.Authenticate(req)

	// Assert: should fall through to store token, then OIDC, then fail
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot authenticate")
}

func TestAuthenticateInsecureDevTokenTooFewParts(t *testing.T) {
	t.Parallel()

	repository := repo.NewMemory()
	a := &Authenticator{
		config:     config.Config{EnableInsecureDevAuth: true},
		tokenStore: repository,
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer dev:alice")

	_, _, err := a.Authenticate(req)

	// Falls through since dev: with <3 parts is not a valid dev token
	require.Error(t, err)
}

func TestAuthenticateMacaroonScheme(t *testing.T) {
	t.Parallel()

	// Arrange: store a token, then present it using the Macaroon scheme
	// (older charmcraft clients use "Macaroon" instead of "Bearer").
	ctx := context.Background()
	repository := repo.NewMemory()
	account, err := repository.EnsureAccount(ctx, core.Account{
		ID: "acc-mac", Subject: "sub-mac", Username: "mac-user",
		DisplayName: "Mac User", Email: "mac@test.com",
	})
	require.NoError(t, err)

	raw, hash, err := NewOpaqueToken()
	require.NoError(t, err)
	now := time.Now().UTC()
	require.NoError(t, repository.CreateStoreToken(ctx, core.StoreToken{
		SessionID:  "sess-mac",
		TokenHash:  hash,
		AccountID:  account.ID,
		ValidSince: now.Add(-time.Hour),
		ValidUntil: now.Add(time.Hour),
	}))

	a := &Authenticator{config: config.Config{}, tokenStore: repository}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Macaroon "+raw)

	// Act
	claims, storeToken, err := a.Authenticate(req)

	// Assert: Macaroon scheme resolves identically to Bearer
	require.NoError(t, err)
	assert.Equal(t, "mac-user", claims.Username)
	require.NotNil(t, storeToken)
	assert.Equal(t, "sess-mac", storeToken.SessionID)
}

func TestAuthenticateMacaroonSchemeInsecureDevToken(t *testing.T) {
	t.Parallel()

	a := &Authenticator{config: config.Config{EnableInsecureDevAuth: true}}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Macaroon dev:bob:Bob")

	claims, token, err := a.Authenticate(req)

	require.NoError(t, err)
	assert.Equal(t, "bob", claims.Subject)
	assert.Equal(t, "Bob", claims.Username)
	assert.Nil(t, token)
}

func TestAuthenticateValidStoreToken(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	repository := repo.NewMemory()
	account, err := repository.EnsureAccount(ctx, core.Account{
		ID: "acc-1", Subject: "sub-1", Username: "alice",
		DisplayName: "Alice", Email: "alice@test.com",
	})
	require.NoError(t, err)

	raw, hash, err := NewOpaqueToken()
	require.NoError(t, err)
	now := time.Now().UTC()
	require.NoError(t, repository.CreateStoreToken(ctx, core.StoreToken{
		SessionID:  "sess-1",
		TokenHash:  hash,
		AccountID:  account.ID,
		ValidSince: now.Add(-time.Hour),
		ValidUntil: now.Add(time.Hour),
	}))

	a := &Authenticator{config: config.Config{}, tokenStore: repository}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+raw)

	// Act
	claims, storeToken, err := a.Authenticate(req)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "alice", claims.Username)
	assert.Equal(t, "Alice", claims.DisplayName)
	require.NotNil(t, storeToken)
	assert.Equal(t, "sess-1", storeToken.SessionID)
}

func TestAuthenticateRevokedToken(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	repository := repo.NewMemory()
	account, _ := repository.EnsureAccount(ctx, core.Account{
		ID: "acc-1", Subject: "sub-1", Username: "alice",
	})
	raw, hash, _ := NewOpaqueToken()
	now := time.Now().UTC()
	revokedAt := now.Add(-time.Minute)
	_ = repository.CreateStoreToken(ctx, core.StoreToken{
		SessionID:  "sess-1",
		TokenHash:  hash,
		AccountID:  account.ID,
		ValidSince: now.Add(-time.Hour),
		ValidUntil: now.Add(time.Hour),
		RevokedAt:  &revokedAt,
	})

	a := &Authenticator{config: config.Config{}, tokenStore: repository}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+raw)

	// Act
	_, _, err := a.Authenticate(req)

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "revoked or expired")
}

func TestAuthenticateExpiredToken(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	repository := repo.NewMemory()
	account, _ := repository.EnsureAccount(ctx, core.Account{
		ID: "acc-1", Subject: "sub-1", Username: "alice",
	})
	raw, hash, _ := NewOpaqueToken()
	now := time.Now().UTC()
	_ = repository.CreateStoreToken(ctx, core.StoreToken{
		SessionID:  "sess-1",
		TokenHash:  hash,
		AccountID:  account.ID,
		ValidSince: now.Add(-2 * time.Hour),
		ValidUntil: now.Add(-time.Hour),
	})

	a := &Authenticator{config: config.Config{}, tokenStore: repository}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+raw)

	// Act
	_, _, err := a.Authenticate(req)

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "revoked or expired")
}

func TestAuthenticateUnknownTokenNoOIDC(t *testing.T) {
	t.Parallel()

	repository := repo.NewMemory()
	a := &Authenticator{config: config.Config{}, tokenStore: repository}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer unknown-token")

	_, _, err := a.Authenticate(req)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot authenticate")
}

func TestAsString(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", asString(nil))
	assert.Equal(t, "hello", asString("hello"))
	assert.Equal(t, "", asString(42))
	assert.Equal(t, "", asString(true))
}

func TestAuthFirstNonEmpty(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", firstNonEmpty())
	assert.Equal(t, "", firstNonEmpty("", ""))
	assert.Equal(t, "a", firstNonEmpty("", "a", "b"))
	assert.Equal(t, "first", firstNonEmpty("first"))
}

func TestAuthenticateEmptySecretAfterBearer(t *testing.T) {
	t.Parallel()

	// "Bearer " gets TrimSpace'd to "Bearer" (no "Bearer " prefix) → unsupported scheme
	a := &Authenticator{config: config.Config{}}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer ")

	_, _, err := a.Authenticate(req)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported authorization scheme")
}

func TestParseInsecureTokenNotDevPrefix(t *testing.T) {
	t.Parallel()

	a := &Authenticator{config: config.Config{EnableInsecureDevAuth: true}}
	claims, ok := a.parseInsecureToken("notdev:alice:Alice")

	assert.False(t, ok)
	assert.Empty(t, claims.Subject)
}

func TestNewAuthenticatorWithoutOIDC(t *testing.T) {
	t.Parallel()

	a, err := New(context.Background(), config.Config{}, repo.NewMemory())

	require.NoError(t, err)
	assert.Nil(t, a.provider)
	assert.Nil(t, a.verifier)
}

func TestNewAuthenticatorWithInvalidOIDC(t *testing.T) {
	t.Parallel()

	_, err := New(context.Background(), config.Config{
		OIDCIssuerURL: "https://invalid.issuer.example.test",
		OIDCClientID:  "test-client",
	}, repo.NewMemory())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot configure OIDC provider")
}
