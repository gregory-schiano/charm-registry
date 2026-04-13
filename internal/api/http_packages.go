package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gschiano/charm-registry/internal/service"
)

func (a *API) handleRegisterPackage(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req struct {
		Name    string `json:"name"`
		Private *bool  `json:"private"`
		Type    string `json:"type"`
	}
	if err := a.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, invalidRequestError(err))
		return
	}
	private := false
	if req.Private != nil {
		private = *req.Private
	}
	pkg, err := a.svc.RegisterPackage(r.Context(), identity, req.Name, req.Type, private)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, registerPackageResponse{ID: pkg.ID})
}

func (a *API) handleListPackages(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	packages, err := a.svc.ListRegisteredPackages(
		r.Context(),
		identity,
		r.URL.Query().Get("include-collaborations") == "true",
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	results := make([]packageMetadataResponse, 0, len(packages))
	for _, pkg := range packages {
		results = append(results, packageMetadata(pkg))
	}
	writeJSON(w, http.StatusOK, packageListResponse{Results: results})
}

func (a *API) handleGetPackage(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	pkg, err := a.svc.GetPackage(r.Context(), identity, chi.URLParam(r, "name"), true)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, packageMetadataEnvelope{Metadata: packageMetadata(pkg)})
}

func (a *API) handlePatchPackage(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var patch service.MetadataPatch
	if err := a.decodeJSON(w, r, &patch); err != nil {
		writeError(w, r, invalidRequestError(err))
		return
	}
	pkg, err := a.svc.UpdatePackage(r.Context(), identity, chi.URLParam(r, "name"), patch)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, packageMetadataEnvelope{Metadata: packageMetadata(pkg)})
}

func (a *API) handleDeletePackage(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	packageID, err := a.svc.UnregisterPackage(r.Context(), identity, chi.URLParam(r, "name"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, deletePackageResponse{PackageID: packageID})
}
