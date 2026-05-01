package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gschiano/charm-registry/internal/core"
)

func (a *API) handleListCharmhubSyncRules(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	rules, err := a.sync.ListCharmhubSyncRules(r.Context(), identity)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, charmhubSyncRuleListResponse{Rules: rules})
}

func (a *API) handleAddCharmhubSyncRule(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	var req struct {
		Name          string   `json:"name"`
		Track         string   `json:"track"`
		Bases         []string `json:"bases"`
		Architectures []string `json:"architectures"`
	}
	if err := a.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, invalidRequestError(err))
		return
	}
	rule, err := a.sync.AddCharmhubSyncRule(r.Context(), identity, req.Name, req.Track, req.Bases, req.Architectures)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, rule)
}

func (a *API) handleDeleteCharmhubSyncRule(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	if err := a.sync.RemoveCharmhubSyncRule(r.Context(), identity, chi.URLParam(r, "name"), chi.URLParam(r, "track")); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, statusResponse{Status: "accepted"})
}

func (a *API) handleRunCharmhubSync(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	if err := a.sync.TriggerCharmhubSync(r.Context(), identity, chi.URLParam(r, "name")); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, statusResponse{Status: "accepted"})
}
