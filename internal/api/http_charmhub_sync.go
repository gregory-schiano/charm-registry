package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (a *API) handleListCharmhubSyncRules(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	rules, err := a.svc.ListCharmhubSyncRules(r.Context(), identity)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, charmhubSyncRuleListResponse{Rules: rules})
}

func (a *API) handleAddCharmhubSyncRule(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req struct {
		Name  string `json:"name"`
		Track string `json:"track"`
	}
	if err := a.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, invalidRequestError(err))
		return
	}
	rule, err := a.svc.AddCharmhubSyncRule(r.Context(), identity, req.Name, req.Track)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, rule)
}

func (a *API) handleDeleteCharmhubSyncRule(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := a.svc.RemoveCharmhubSyncRule(r.Context(), identity, chi.URLParam(r, "name"), chi.URLParam(r, "track")); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, statusResponse{Status: "accepted"})
}

func (a *API) handleRunCharmhubSync(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := a.svc.TriggerCharmhubSync(r.Context(), identity, chi.URLParam(r, "name")); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, statusResponse{Status: "accepted"})
}
