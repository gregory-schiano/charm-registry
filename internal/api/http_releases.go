package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/service"
)

func (a *API) handleListReleases(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	payload, err := a.svc.ListReleases(r.Context(), identity, chi.URLParam(r, "name"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *API) handleRelease(w http.ResponseWriter, r *http.Request, identity core.Identity) {
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
	released, err := a.svc.CreateRelease(r.Context(), identity, chi.URLParam(r, "name"), requests)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeCreatedJSON(w, "/v1/charm/"+chi.URLParam(r, "name")+"/releases", releasedResponse{Released: released})
}

func (a *API) handleCreateTracks(w http.ResponseWriter, r *http.Request, identity core.Identity) {
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
	writeCreatedJSON(w, "/v1/charm/"+chi.URLParam(r, "name"), tracksCreatedResponse{NumTracksCreated: created})
}

func (a *API) handleFind(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	payload, err := a.svc.SearchPackages(r.Context(), identity, r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *API) handleInfo(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	name := chi.URLParam(r, "name")
	channel := r.URL.Query().Get("channel")

	var payload any
	var err error
	if channel != "" {
		payload, err = a.svc.GetPackageInfoForChannel(r.Context(), identity, name, channel)
	} else {
		payload, err = a.svc.GetPackageInfo(r.Context(), identity, name)
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *API) handleRefresh(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	var req service.RefreshRequest
	if err := a.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, invalidRequestError(err))
		return
	}
	payload, err := a.svc.ResolveRefresh(r.Context(), identity, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}
