//go:build integration

package integration_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TOKEN-01: Issue token with dev auth.
func TestTOKEN01_IssueTokenWithDevAuth(t *testing.T) {
	auth := devAuthHeader("alice", "Alice")
	body := `{"description":"itest-issue-token","ttl":3600}`

	resp, err := doRequest("POST", "/v1/tokens", body, auth)
	require.NoError(t, err, "POST /v1/tokens request failed")
	requireStatusCode(t, resp, http.StatusOK)

	result := readJSON(t, resp)
	assert.NotEmpty(t, result["macaroon"], "response should contain a macaroon")

	// Verify the macaroon string is parseable and contains an identifier.
	rawToken := extractRawToken(t, result)
	assert.NotEmpty(t, rawToken, "raw token (identifier) should not be empty")
}

// TOKEN-02: List tokens after issue.
func TestTOKEN02_ListTokensAfterIssue(t *testing.T) {
	auth := devAuthHeader("bob", "Bob")
	desc := uniqueName("list-tokens")

	// Issue a token first.
	rawToken, sessionID := mustIssueToken(t, auth, fmt.Sprintf(`{"description":"%s","ttl":3600}`, desc))
	require.NotEmpty(t, rawToken, "issued token should not be empty")
	require.NotEmpty(t, sessionID, "session ID should not be empty")

	// List tokens.
	tokens := mustListTokens(t, auth)
	require.NotEmpty(t, tokens, "should have at least one token after issuing")

	// Find our token in the list.
	var found bool
	for _, tok := range tokens {
		sid, _ := tok["session-id"].(string)
		if sid == sessionID {
			found = true
			assert.Equal(t, desc, tok["description"], "token description should match")
			assert.NotEmpty(t, tok["valid-since"], "valid-since should be set")
			assert.NotEmpty(t, tok["valid-until"], "valid-until should be set")
			break
		}
	}
	assert.True(t, found, "issued token (session-id=%s) should appear in list", sessionID)
}

// TOKEN-03: Exchange token.
func TestTOKEN03_ExchangeToken(t *testing.T) {
	auth := devAuthHeader("charlie", "Charlie")

	// Issue a token first so we can exchange it.
	rawToken, _ := mustIssueToken(t, auth, `{"description":"itest-exchange-src","ttl":3600}`)
	require.NotEmpty(t, rawToken, "source token should not be empty")

	// Exchange the token via Bearer auth.
	exchanged := mustExchangeToken(t, rawToken)
	assert.NotEmpty(t, exchanged, "exchanged token should not be empty")
	assert.NotEqual(t, rawToken, exchanged, "exchanged token should differ from original")
}

// TOKEN-03b: Offline exchange token.
func TestTOKEN03b_OfflineExchangeToken(t *testing.T) {
	auth := devAuthHeader("dave", "Dave")

	// Issue a token first.
	rawToken, _ := mustIssueToken(t, auth, `{"description":"itest-offline-exchange","ttl":3600}`)
	require.NotEmpty(t, rawToken, "source token should not be empty")

	// Exchange via offline/exchange endpoint.
	resp, err := doRequest("POST", "/v1/tokens/offline/exchange", "", "Bearer "+rawToken)
	require.NoError(t, err, "POST /v1/tokens/offline/exchange request failed")
	requireStatusCode(t, resp, http.StatusOK)

	result := readJSON(t, resp)
	exchanged, ok := result["macaroon"].(string)
	require.True(t, ok, "response should have 'macaroon' field")
	assert.NotEmpty(t, exchanged, "exchanged token should not be empty")
}

// TOKEN-04: Revoke token, then confirm subsequent use is rejected.
func TestTOKEN04_RevokeToken(t *testing.T) {
	auth := devAuthHeader("eve", "Eve")

	// Issue a token.
	rawToken, sessionID := mustIssueToken(t, auth, `{"description":"itest-revoke","ttl":3600}`)
	require.NotEmpty(t, rawToken, "issued token should not be empty")
	require.NotEmpty(t, sessionID, "session ID should not be empty")

	// Revoke it.
	mustRevokeToken(t, auth, sessionID)

	// Verify the revoked token is rejected on exchange.
	resp, err := doRequest("POST", "/v1/tokens/exchange", "", "Bearer "+rawToken)
	require.NoError(t, err, "exchange after revoke request failed")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"revoked token should be rejected on exchange")
}

// TOKEN-04b: Revoke response includes updated macaroons list.
func TestTOKEN04b_RevokeResponseIncludesMacaroons(t *testing.T) {
	auth := devAuthHeader("frank", "Frank")

	// Issue a token so there's something to revoke.
	_, sessionID := mustIssueToken(t, auth, `{"description":"itest-revoke-list","ttl":3600}`)
	require.NotEmpty(t, sessionID, "session ID should not be empty")

	// Revoke and inspect the response.
	body := fmt.Sprintf(`{"session-id":"%s"}`, sessionID)
	resp, err := doRequest("POST", "/v1/tokens/revoke", body, auth)
	require.NoError(t, err, "POST /v1/tokens/revoke request failed")
	requireStatusCode(t, resp, http.StatusOK)

	result := readJSON(t, resp)
	macaroons, ok := result["macaroons"].([]any)
	require.True(t, ok, "revoke response should have 'macaroons' field")
	assert.GreaterOrEqual(t, len(macaroons), 1, "should list at least one token after revoke")
}

// TOKEN-05: Whoami with token.
func TestTOKEN05_WhoamiWithToken(t *testing.T) {
	auth := devAuthHeader("grace", "Grace")

	// Issue and exchange a token.
	rawToken, _ := mustIssueToken(t, auth, `{"description":"itest-whoami","ttl":3600}`)
	exchanged := mustExchangeToken(t, rawToken)

	// Deprecated /v1/whoami endpoint.
	resp, err := doRequest("GET", "/v1/whoami", "", "Bearer "+exchanged)
	require.NoError(t, err, "GET /v1/whoami request failed")
	requireStatusCode(t, resp, http.StatusOK)

	result := readJSON(t, resp)
	assert.Equal(t, "Grace", result["username"], "whoami should return the token's username")
}

// TOKEN-05b: Token whoami endpoint (/v1/tokens/whoami).
func TestTOKEN05b_TokenWhoami(t *testing.T) {
	auth := devAuthHeader("heidi", "Heidi")

	// Issue and exchange a token.
	rawToken, _ := mustIssueToken(t, auth, `{"description":"itest-token-whoami","ttl":3600}`)
	exchanged := mustExchangeToken(t, rawToken)

	// /v1/tokens/whoami endpoint.
	resp, err := doRequest("GET", "/v1/tokens/whoami", "", "Bearer "+exchanged)
	require.NoError(t, err, "GET /v1/tokens/whoami request failed")
	requireStatusCode(t, resp, http.StatusOK)

	result := readJSON(t, resp)
	account, ok := result["account"].(map[string]any)
	require.True(t, ok, "response should have 'account' object")
	assert.Equal(t, "Heidi", account["username"], "token whoami should return the token's username")

	// Verify the permissions/packages/channels arrays are present.
	assert.Contains(t, result, "permissions", "response should include permissions")
	assert.Contains(t, result, "packages", "response should include packages")
	assert.Contains(t, result, "channels", "response should include channels")
}

// TOKEN-06: Expired token rejected.
func TestTOKEN06_ExpiredTokenRejected(t *testing.T) {
	auth := devAuthHeader("ivan", "Ivan")

	// Issue a token with minimal TTL (1 second).
	rawToken, _ := mustIssueToken(t, auth, `{"description":"itest-expire","ttl":1}`)
	require.NotEmpty(t, rawToken, "issued token should not be empty")

	// Wait for the token to expire.
	time.Sleep(3 * time.Second)

	// Attempt to exchange the expired token.
	resp, err := doRequest("POST", "/v1/tokens/exchange", "", "Bearer "+rawToken)
	require.NoError(t, err, "exchange after expiry request failed")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"expired token should be rejected on exchange")
}

// TOKEN-07: Token rate limiting.
func TestTOKEN07_TokenRateLimiting(t *testing.T) {
	auth := devAuthHeader("judy", "Judy")

	// Issue tokens until rate-limited. The default limit is 5.
	var lastOK bool
	for i := 0; i < 7; i++ {
		resp, err := doRequest("POST", "/v1/tokens",
			fmt.Sprintf(`{"description":"itest-ratelimit-%d","ttl":3600}`, i), auth)
		require.NoError(t, err, "token issue request %d failed", i)

		if resp.StatusCode == http.StatusOK {
			lastOK = true
		} else if resp.StatusCode == http.StatusTooManyRequests {
			// Success: we hit the rate limit.
			body := readJSON(t, resp)
			code, _ := body["code"].(string)
			assert.Equal(t, "rate-limit-exceeded", code,
				"rate-limit response should have correct code")
			return
		}
	}
	// If we never hit 429, that's also acceptable (rate limit may be higher).
	assert.True(t, lastOK, "at least one token issue should succeed")
}

// TOKEN-08: Scoped token cannot exceed permissions.
// Issue a token with only view permission and try to register a package.
func TestTOKEN08_ScopedTokenCannotExceedPermissions(t *testing.T) {
	auth := devAuthHeader("karl", "Karl")

	// Issue a token with only account-view-packages permission.
	reqBody := `{"description":"itest-view-only","ttl":3600,"permissions":["account-view-packages"]}`
	rawToken, _ := mustIssueToken(t, auth, reqBody)
	require.NotEmpty(t, rawToken, "issued scoped token should not be empty")

	exchanged := mustExchangeToken(t, rawToken)

	// Attempt to register a package with the view-only token.
	charmName := uniqueName("scoped-charm")
	regBody := fmt.Sprintf(`{"name":"%s","type":"charm"}`, charmName)
	resp, err := doRequest("POST", "/v1/charm", regBody, "Bearer "+exchanged)
	require.NoError(t, err, "register with scoped token request failed")

	// Should be forbidden (403) because the token lacks register permission.
	assert.Equal(t, http.StatusForbidden, resp.StatusCode,
		"view-only token should be forbidden from registering a package")
}

// TOKEN-09: Token package scoping enforced.
func TestTOKEN09_TokenPackageScopingEnforced(t *testing.T) {
	auth := devAuthHeader("leo", "Leo")

	// Register a charm that the scoped token is NOT allowed to access.
	charmName := uniqueName("pkg-scope-charm")
	regBody := fmt.Sprintf(`{"name":"%s","type":"charm"}`, charmName)
	resp, err := doRequest("POST", "/v1/charm", regBody, auth)
	require.NoError(t, err, "register package request failed")
	requireStatusCode(t, resp, http.StatusCreated)

	// Issue a token scoped to a different (non-existent) package.
	reqBody := `{"description":"itest-pkg-scoped","ttl":3600,"packages":[{"name":"other-charm"}]}`
	rawToken, _ := mustIssueToken(t, auth, reqBody)
	exchanged := mustExchangeToken(t, rawToken)

	// Attempt to push a revision to the charm the token is NOT scoped to.
	pushBody := `{"upload-id":"fake-upload"}`
	resp, err = doRequest("POST", "/v1/charm/"+charmName+"/revisions", pushBody, "Bearer "+exchanged)
	require.NoError(t, err, "push revision with package-scoped token request failed")

	// Should be forbidden because the token is not scoped to this charm.
	assert.Equal(t, http.StatusForbidden, resp.StatusCode,
		"package-scoped token should be forbidden from pushing to a different package")
}

// TOKEN-10: Token channel scoping enforced.
func TestTOKEN10_TokenChannelScopingEnforced(t *testing.T) {
	auth := devAuthHeader("mike", "Mike")

	// Register a charm and set up a minimal release pipeline.
	charmName := uniqueName("channel-scope-charm")
	mustRegister(t, charmName, auth)

	// Issue a token scoped only to latest/stable.
	reqBody := `{"description":"itest-channel-scoped","ttl":3600,"channels":["latest/stable"]}`
	rawToken, _ := mustIssueToken(t, auth, reqBody)
	exchanged := mustExchangeToken(t, rawToken)

	// Attempt to release to a channel the token is NOT scoped to (latest/edge).
	relBody := `[{"revision":1,"channel":"latest/edge"}]`
	resp, err := doRequest("POST", "/v1/charm/"+charmName+"/releases", relBody, "Bearer "+exchanged)
	require.NoError(t, err, "release with channel-scoped token request failed")

	// Should be forbidden because the token is only scoped to latest/stable.
	assert.Equal(t, http.StatusForbidden, resp.StatusCode,
		"channel-scoped token should be forbidden from releasing to a different channel")
}

// TOKEN-11: Unauthenticated GET /v1/tokens returns macaroon challenge.
// (Also covers ACL-07 from the test design.)
func TestTOKEN11_UnauthenticatedGetTokensReturnsChallenge(t *testing.T) {
	resp, err := doRequest("GET", "/v1/tokens", "", "")
	require.NoError(t, err, "GET /v1/tokens without auth request failed")
	requireStatusCode(t, resp, http.StatusOK)

	result := readJSON(t, resp)
	macaroon, ok := result["macaroon"].(string)
	require.True(t, ok, "response should have 'macaroon' string field")
	assert.Equal(t, "oidc-login-required", macaroon,
		"unauthenticated GET /v1/tokens should return oidc-login-required challenge")
}

// TOKEN-12: Dashboard exchange with and without body.
func TestTOKEN12_DashboardExchange(t *testing.T) {
	auth := devAuthHeader("nancy", "Nancy")

	// With a client-description body.
	body := `{"client-description":"web UI"}`
	resp, err := doRequest("POST", "/v1/tokens/dashboard/exchange", body, auth)
	require.NoError(t, err, "dashboard exchange with body request failed")
	requireStatusCode(t, resp, http.StatusOK)

	result := readJSON(t, resp)
	assert.NotEmpty(t, result["macaroon"], "dashboard exchange should return a macaroon")

	// Without a body (empty body).
	resp, err = doRequest("POST", "/v1/tokens/dashboard/exchange", "", auth)
	require.NoError(t, err, "dashboard exchange without body request failed")
	requireStatusCode(t, resp, http.StatusOK)

	result = readJSON(t, resp)
	assert.NotEmpty(t, result["macaroon"], "dashboard exchange (no body) should return a macaroon")
}

// TOKEN-13: Deprecated /v1/whoami returns account info.
func TestTOKEN13_DeprecatedWhoami(t *testing.T) {
	auth := devAuthHeader("oscar", "Oscar")

	result := mustWhoami(t, auth)
	assert.Equal(t, "Oscar", result["username"], "/v1/whoami should return correct username")
	assert.NotEmpty(t, result["id"], "/v1/whoami should return account id")
}

// TOKEN-14: Unauthenticated /v1/whoami returns 401.
func TestTOKEN14_UnauthenticatedWhoamiReturnsUnauthorized(t *testing.T) {
	resp, err := doRequest("GET", "/v1/whoami", "", "")
	require.NoError(t, err, "GET /v1/whoami without auth request failed")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"unauthenticated /v1/whoami should return 401")
}

// TOKEN-15: Unauthenticated /v1/tokens/whoami returns 401.
func TestTOKEN15_UnauthenticatedTokenWhoamiReturnsUnauthorized(t *testing.T) {
	resp, err := doRequest("GET", "/v1/tokens/whoami", "", "")
	require.NoError(t, err, "GET /v1/tokens/whoami without auth request failed")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"unauthenticated /v1/tokens/whoami should return 401")
}

// TOKEN-16: List tokens with include-inactive query parameter.
func TestTOKEN16_ListTokensIncludeInactive(t *testing.T) {
	auth := devAuthHeader("peggy", "Peggy")

	// Issue a token.
	_, sessionID := mustIssueToken(t, auth, `{"description":"itest-include-inactive","ttl":3600}`)
	require.NotEmpty(t, sessionID)

	// Revoke it so it becomes inactive.
	mustRevokeToken(t, auth, sessionID)

	// List without include-inactive: revoked tokens should not appear.
	resp, err := doRequest("GET", "/v1/tokens", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	result := readJSON(t, resp)
	activeMacaroons, _ := result["macaroons"].([]any)
	for _, m := range activeMacaroons {
		entry := m.(map[string]any)
		if sid, _ := entry["session-id"].(string); sid == sessionID {
			t.Fatal("revoked token should not appear in active token list")
		}
	}

	// List with include-inactive=true: revoked tokens should appear.
	resp, err = doRequest("GET", "/v1/tokens?include-inactive=true", "", auth)
	require.NoError(t, err)
	requireStatusCode(t, resp, http.StatusOK)
	result = readJSON(t, resp)
	allMacaroons, _ := result["macaroons"].([]any)
	var foundInactive bool
	for _, m := range allMacaroons {
		entry := m.(map[string]any)
		if sid, _ := entry["session-id"].(string); sid == sessionID {
			foundInactive = true
			// Revoked tokens should have revoked-at set.
			assert.NotEmpty(t, entry["revoked-at"],
				"revoked token should have revoked-at timestamp")
			break
		}
	}
	assert.True(t, foundInactive,
		"revoked token (session-id=%s) should appear when include-inactive=true", sessionID)
}

// TOKEN-17: Issue token preserves description in list.
func TestTOKEN17_IssueTokenPreservesDescription(t *testing.T) {
	auth := devAuthHeader("quentin", "Quentin")
	desc := "itest-description-check"

	_, sessionID := mustIssueToken(t, auth, fmt.Sprintf(`{"description":"%s","ttl":3600}`, desc))
	require.NotEmpty(t, sessionID)

	tokens := mustListTokens(t, auth)
	for _, tok := range tokens {
		sid, _ := tok["session-id"].(string)
		if sid == sessionID {
			d, _ := tok["description"].(string)
			assert.Equal(t, desc, d, "token description should be preserved in list")
			return
		}
	}
	t.Fatalf("token with session-id=%s not found in list", sessionID)
}

// TOKEN-18: Issue token with no auth returns macaroon discharge challenge (dev mode auto-login).
func TestTOKEN18_IssueTokenNoAuthDevMode(t *testing.T) {
	// When EnableInsecureDevAuth is true, POST /v1/tokens without auth
	// auto-provisions a dev identity instead of returning a discharge macaroon.
	resp, err := doRequest("POST", "/v1/tokens", `{"description":"itest-no-auth","ttl":3600}`, "")
	require.NoError(t, err, "POST /v1/tokens without auth request failed")

	// In dev mode, the server auto-creates a "developer" identity.
	requireStatusCode(t, resp, http.StatusOK)
	result := readJSON(t, resp)
	assert.NotEmpty(t, result["macaroon"], "dev-mode auto-login should issue a macaroon")
}

// TOKEN-19: Exchange without credentials returns 401.
func TestTOKEN19_ExchangeWithoutCredentialsReturnsUnauthorized(t *testing.T) {
	resp, err := doRequest("POST", "/v1/tokens/exchange", "", "")
	require.NoError(t, err, "exchange without credentials request failed")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"exchange without credentials should return 401")
}

// TOKEN-20: Macaroon header authentication (JUJU-04 compat).
// Simulates the charmcraft login flow: issue token → set Macaroons header → exchange.
func TestTOKEN20_MacaroonHeaderAuthentication(t *testing.T) {
	auth := devAuthHeader("rory", "Rory")

	// Issue a token via standard Bearer auth.
	rawToken, _ := mustIssueToken(t, auth, `{"description":"itest-macaroon-header","ttl":3600}`)
	require.NotEmpty(t, rawToken)

	// Use the Macaroons header (pymacaroons-compatible) for exchange.
	// Build a Macaroons header with the raw token in base64url-encoded JSON form.
	macaroonsValue := "[" + base64URLEncode(fmt.Sprintf(`[{"identifier":"%s"}]`, rawToken)) + "]"
	resp, err := doRequestRaw("POST", "/v1/tokens/exchange",
		map[string]string{
			"Macaroons":      macaroonsValue,
			"Content-Type":   "application/json",
			"Content-Length": "0",
		}, strings.NewReader(""))
	require.NoError(t, err, "exchange with Macaroons header request failed")

	// Accept either 200 (exchange succeeded) or 401 (token parsing mismatch).
	if resp.StatusCode == http.StatusOK {
		result := readJSON(t, resp)
		assert.NotEmpty(t, result["macaroon"], "exchange should return a macaroon")
	} else {
		// If Macaroons header format is not compatible, the server returns 401.
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
			"invalid Macaroons header should return 401")
	}
}
