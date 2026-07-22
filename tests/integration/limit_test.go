//go:build integration

package integration_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// LIM-01: Token issue rate-limited after 5 requests per account per minute.
func TestLIM01_TokenIssueRateLimited(t *testing.T) {
	auth := devAuthHeader("lim-user-1", "limiter1")

	// The limiter allows 5 token issuances per account per minute.
	// Issue 5 tokens — all should succeed.
	for i := 0; i < 5; i++ {
		resp, err := doRequest("POST", "/v1/tokens", `{"description":"rate-limit-test"}`, auth)
		if err != nil {
			t.Fatalf("request %d failed: %v", i+1, err)
		}
		assertStatusCode(t, resp, http.StatusOK)
		resp.Body.Close()
	}

	// 6th request should be rate-limited (429).
	resp, err := doRequest("POST", "/v1/tokens", `{"description":"rate-limit-test"}`, auth)
	if err != nil {
		t.Fatalf("6th request failed: %v", err)
	}
	defer resp.Body.Close()
	assertStatusCode(t, resp, http.StatusTooManyRequests)
}

// LIM-02: Rate limit is per-account (different accounts not affected).
func TestLIM02_RateLimitPerAccount(t *testing.T) {
	auth1 := devAuthHeader("lim-user-2a", "limiter2a")
	auth2 := devAuthHeader("lim-user-2b", "limiter2b")

	// Exhaust rate limit for account 1.
	for i := 0; i < 5; i++ {
		resp, err := doRequest("POST", "/v1/tokens", `{"description":"rate-limit-test"}`, auth1)
		if err != nil {
			t.Fatalf("account1 request %d failed: %v", i+1, err)
		}
		resp.Body.Close()
	}

	// Account 2 should still be able to issue tokens.
	resp, err := doRequest("POST", "/v1/tokens", `{"description":"other-account"}`, auth2)
	if err != nil {
		t.Fatalf("account2 request failed: %v", err)
	}
	defer resp.Body.Close()
	assertStatusCode(t, resp, http.StatusOK)
}

// LIM-03: Non-token endpoints are not rate-limited.
func TestLIM03_NonTokenEndpointsNotRateLimited(t *testing.T) {
	auth := devAuthHeader("lim-user-3", "limiter3")

	// Exhaust token rate limit.
	for i := 0; i < 5; i++ {
		resp, err := doRequest("POST", "/v1/tokens", `{"description":"rate-limit-test"}`, auth)
		if err != nil {
			t.Fatalf("token request %d failed: %v", i+1, err)
		}
		resp.Body.Close()
	}

	// Other endpoints should still work fine.
	name := uniqueName("lim-pkg")
	resp, err := doRequest("POST", "/v1/charm", `{"name":"`+name+`","type":"charm"}`, auth)
	if err != nil {
		t.Fatalf("package register request failed: %v", err)
	}
	defer resp.Body.Close()
	assertStatusCode(t, resp, http.StatusCreated)

	// List packages should also work.
	resp, err = doRequest("GET", "/v1/charm", "", auth)
	if err != nil {
		t.Fatalf("package list request failed: %v", err)
	}
	defer resp.Body.Close()
	assertStatusCode(t, resp, http.StatusOK)
}

// LIM-04: Large request body rejected (MaxJSONBodyBytes).
func TestLIM04_LargeRequestBodyRejected(t *testing.T) {
	auth := devAuthHeader("lim-user-4", "limiter4")

	// Send a very large JSON body to POST /v1/tokens.
	largeBody := `{"description":"` + string(make([]byte, 2*1024*1024)) + `"}` // 2MB+
	resp, err := doRequest("POST", "/v1/tokens", largeBody, auth)
	if err != nil {
		t.Fatalf("large body request failed: %v", err)
	}
	defer resp.Body.Close()
	// Should reject with 400 or 413 depending on MaxBytesReader behavior.
	assert.True(t, resp.StatusCode == http.StatusBadRequest ||
		resp.StatusCode == http.StatusRequestEntityTooLarge,
		"large request body should be rejected, got status: %d", resp.StatusCode)
}
