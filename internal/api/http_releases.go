package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/service"
)

func (a *API) handleListReleases(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	payload, err := a.svc.ListReleases(r.Context(), identity, chi.URLParam(r, "name"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *API) handleRelease(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req []struct {
		Channel   string                    `json:"channel"`
		Revision  int                       `json:"revision"`
		Resources []core.ReleaseResourceRef `json:"resources"`
	}
	if err := a.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, invalidRequestError(err))
		return
	}
	requests := make([]core.Release, 0, len(req))
	for _, item := range req {
		requests = append(requests, core.Release{
			ID:        "",
			Channel:   item.Channel,
			Revision:  item.Revision,
			Resources: item.Resources,
		})
	}
	released, err := a.svc.Release(r.Context(), identity, chi.URLParam(r, "name"), requests)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"released": released})
}

func (a *API) handleCreateTracks(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req []core.Track
	if err := a.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, invalidRequestError(err))
		return
	}
	created, err := a.svc.CreateTracks(r.Context(), identity, chi.URLParam(r, "name"), req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"num-tracks-created": created})
}

func (a *API) handleFind(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	payload, err := a.svc.Find(r.Context(), identity, r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *API) handleInfo(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	payload, err := a.svc.Info(r.Context(), identity, chi.URLParam(r, "name"))
	if channel := r.URL.Query().Get("channel"); channel != "" {
		payload, err = a.svc.InfoForChannel(r.Context(), identity, chi.URLParam(r, "name"), channel)
	}
	if err != nil {
		var serviceErr *service.Error
		if errors.As(err, &serviceErr) && serviceErr.Kind == service.ErrorKindNotFound {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"code":    serviceErr.Code,
				"message": serviceErr.Message,
			})
			return
		}
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *API) handleRefresh(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req service.RefreshRequest
	if err := a.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, invalidRequestError(err))
		return
	}
	payload, err := a.svc.Refresh(r.Context(), identity, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}
