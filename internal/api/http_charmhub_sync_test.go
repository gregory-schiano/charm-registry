package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestListCharmhubSyncRulesRequiresAdmin(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, testCfg)
	resp := doRequest(t, handler, http.MethodGet, "/v1/admin/charmhub-sync", nil, "Bearer dev:user:user")

	assert.Equal(t, http.StatusForbidden, resp.Code)
}

func TestAddAndListCharmhubSyncRulesAsAdmin(t *testing.T) {
	t.Parallel()

	cfg := testCfg
	cfg.AdminUsernames = []string{"admin"}
	handler := newTestHandler(t, cfg)

	addResp := doRequest(t, handler, http.MethodPost, "/v1/admin/charmhub-sync", map[string]string{
		"name":  "demo",
		"track": "latest",
	}, "Bearer dev:admin:admin")
	assert.Equal(t, http.StatusAccepted, addResp.Code)
	addBody := decodeJSON(t, addResp)
	assert.Equal(t, "demo", addBody["name"])
	assert.Equal(t, "latest", addBody["track"])
	assert.Equal(t, "pending", addBody["status"])

	listResp := doRequest(t, handler, http.MethodGet, "/v1/admin/charmhub-sync", nil, "Bearer dev:admin:admin")
	assert.Equal(t, http.StatusOK, listResp.Code)
	listBody := decodeJSON(t, listResp)
	rules := listBody["rules"].([]any)
	assert.Len(t, rules, 1)
	first := rules[0].(map[string]any)
	assert.Equal(t, "demo", first["name"])
	assert.Equal(t, "latest", first["track"])
}

func TestDeleteCharmhubSyncRuleAsAdmin(t *testing.T) {
	t.Parallel()

	cfg := testCfg
	cfg.AdminUsernames = []string{"admin"}
	handler := newTestHandler(t, cfg)

	_ = doRequest(t, handler, http.MethodPost, "/v1/admin/charmhub-sync", map[string]string{
		"name":  "demo",
		"track": "latest",
	}, "Bearer dev:admin:admin")

	deleteResp := doRequest(t, handler, http.MethodDelete, "/v1/admin/charmhub-sync/demo/latest", nil, "Bearer dev:admin:admin")
	assert.Equal(t, http.StatusAccepted, deleteResp.Code)

	listResp := doRequest(t, handler, http.MethodGet, "/v1/admin/charmhub-sync", nil, "Bearer dev:admin:admin")
	assert.Equal(t, http.StatusOK, listResp.Code)
	listBody := decodeJSON(t, listResp)
	assert.Empty(t, listBody["rules"])
}

func TestRunCharmhubSyncAsAdmin(t *testing.T) {
	t.Parallel()

	cfg := testCfg
	cfg.AdminUsernames = []string{"admin"}
	handler := newTestHandler(t, cfg)

	_ = doRequest(t, handler, http.MethodPost, "/v1/admin/charmhub-sync", map[string]string{
		"name":  "demo",
		"track": "latest",
	}, "Bearer dev:admin:admin")

	runResp := doRequest(t, handler, http.MethodPost, "/v1/admin/charmhub-sync/demo/run", nil, "Bearer dev:admin:admin")
	assert.Equal(t, http.StatusAccepted, runResp.Code)
	runBody := decodeJSON(t, runResp)
	assert.Equal(t, "accepted", runBody["status"])
}

func TestRunCharmhubSyncRequiresExistingRule(t *testing.T) {
	t.Parallel()

	cfg := testCfg
	cfg.AdminUsernames = []string{"admin"}
	handler := newTestHandler(t, cfg)

	runResp := doRequest(t, handler, http.MethodPost, "/v1/admin/charmhub-sync/demo/run", nil, "Bearer dev:admin:admin")
	assert.Equal(t, http.StatusNotFound, runResp.Code)
}
