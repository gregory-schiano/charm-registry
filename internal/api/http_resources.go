package api

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gschiano/charm-registry/internal/service"
)

func (a *API) handleListResources(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, err)
		return
	}
	resources, err := a.svc.ListResources(r.Context(), identity, chi.URLParam(r, "name"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resources": resources})
}

func (a *API) handleListResourceRevisions(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, err)
		return
	}
	revisions, err := a.svc.ListResourceRevisions(
		r.Context(),
		identity,
		chi.URLParam(r, "name"),
		chi.URLParam(r, "resource"),
	)
	if err != nil {
		writeError(w, err)
		return
	}
	rows := make([]map[string]any, 0, len(revisions))
	for _, revision := range revisions {
		rows = append(rows, map[string]any{
			"architectures": revision.Architectures,
			"bases":         revision.Bases,
			"created-at":    revision.CreatedAt,
			"download":      revision.Download,
			"filename":      revision.Filename,
			"revision":      revision.Revision,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": rows})
}

func (a *API) handlePushResource(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req service.PushResourceRequest
	if err := a.decodeJSON(w, r, &req); err != nil {
		writeError(w, invalidRequestError(err))
		return
	}
	statusURL, err := a.svc.PushResource(
		r.Context(),
		identity,
		chi.URLParam(r, "name"),
		chi.URLParam(r, "resource"),
		req,
	)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status-url": statusURL})
}

func (a *API) handleUpdateResourceRevisions(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req service.UpdateResourceRevisionRequest
	if err := a.decodeJSON(w, r, &req); err != nil {
		writeError(w, invalidRequestError(err))
		return
	}
	updated, err := a.svc.UpdateResourceRevisions(
		r.Context(),
		identity,
		chi.URLParam(r, "name"),
		chi.URLParam(r, "resource"),
		req,
	)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"num-resource-revisions-updated": updated})
}

func (a *API) handleOCIUploadCredentials(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, err)
		return
	}
	payload, err := a.svc.OCIImageUploadCredentials(
		r.Context(),
		identity,
		chi.URLParam(r, "name"),
		chi.URLParam(r, "resource"),
	)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *API) handleOCIImageBlob(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		ImageDigest string `json:"image-digest"`
	}
	if err := a.decodeJSON(w, r, &req); err != nil {
		writeError(w, invalidRequestError(err))
		return
	}
	content, err := a.svc.OCIImageBlob(
		r.Context(),
		identity,
		chi.URLParam(r, "name"),
		chi.URLParam(r, "resource"),
		req.ImageDigest,
	)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="oci-image-blob.json"`)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// #nosec G705 -- This endpoint serves a non-HTML attachment.
	_, _ = io.WriteString(w, content)
}

func (a *API) handleResourceDownload(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, err)
		return
	}
	packageID, resourceName, revision, parseErr := parseResourceDownloadFilename(chi.URLParam(r, "filename"))
	if parseErr != nil {
		writeError(w, serviceError(http.StatusBadRequest, "invalid-request", parseErr.Error()))
		return
	}
	payload, err := a.svc.DownloadResource(r.Context(), identity, packageID, resourceName, revision)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="resource.bin"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	// #nosec G705 -- This endpoint streams attachment bytes.
	_, _ = w.Write(payload)
}
