package api

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gschiano/charm-registry/internal/service"
)

func (a *API) handleRoot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.svc.RootDocument())
}

func (a *API) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = io.WriteString(w, openAPISpec)
}

func (a *API) handleDocs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, `<!doctype html>
<html>
  <head><meta charset="utf-8"><title>Charm Registry Docs</title></head>
  <body>
    <h1>Private Charm Registry</h1>
    <p>OpenAPI document: <a href="/openapi.yaml">/openapi.yaml</a></p>
    <p>This MVP intentionally exposes the API first and keeps the browse UI out of scope.</p>
  </body>
</html>`)
}

func (a *API) handleGetTokens(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !identity.Authenticated {
		writeJSON(w, http.StatusOK, map[string]any{"macaroon": "oidc-login-required"})
		return
	}
	tokens, err := a.svc.ListStoreTokens(r.Context(), identity, r.URL.Query().Get("include-inactive") == "true")
	if err != nil {
		writeError(w, err)
		return
	}
	type tokenView struct {
		Description *string    `json:"description,omitempty"`
		RevokedAt   *time.Time `json:"revoked-at,omitempty"`
		RevokedBy   *string    `json:"revoked-by,omitempty"`
		SessionID   string     `json:"session-id"`
		ValidSince  time.Time  `json:"valid-since"`
		ValidUntil  time.Time  `json:"valid-until"`
	}
	out := make([]tokenView, 0, len(tokens))
	for _, token := range tokens {
		out = append(out, tokenView{
			Description: token.Description,
			RevokedAt:   token.RevokedAt,
			RevokedBy:   token.RevokedBy,
			SessionID:   token.SessionID,
			ValidSince:  token.ValidSince,
			ValidUntil:  token.ValidUntil,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"macaroons": out})
}

func (a *API) handleIssueToken(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req service.IssueTokenRequest
	if err := a.decodeJSON(w, r, &req); err != nil {
		writeError(w, invalidRequestError(err))
		return
	}
	raw, _, err := a.svc.IssueStoreToken(r.Context(), identity, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"macaroon": raw})
}

func (a *API) handleExchangeToken(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, err)
		return
	}
	raw, err := a.svc.ExchangeStoreToken(r.Context(), identity, nil)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"macaroon": raw})
}

func (a *API) handleDashboardExchange(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		ClientDescription *string `json:"client-description"`
	}
	if err := a.decodeJSON(w, r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, invalidRequestError(err))
		return
	}
	raw, err := a.svc.ExchangeStoreToken(r.Context(), identity, req.ClientDescription)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"macaroon": raw})
}

func (a *API) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		SessionID string `json:"session-id"`
	}
	if err := a.decodeJSON(w, r, &req); err != nil {
		writeError(w, invalidRequestError(err))
		return
	}
	if err := a.svc.RevokeStoreToken(r.Context(), identity, req.SessionID); err != nil {
		writeError(w, err)
		return
	}
	tokens, err := a.svc.ListStoreTokens(r.Context(), identity, true)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"macaroons": tokens})
}

func (a *API) handleTokenWhoAmI(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, err)
		return
	}
	payload, err := a.svc.MacaroonInfo(identity)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *API) handleWhoAmI(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, err)
		return
	}
	payload, err := a.svc.DeprecatedWhoAmI(identity)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}
