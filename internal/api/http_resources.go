package api

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/service"
)

func (a *API) handleListResources(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	resources, err := a.svc.ListResources(r.Context(), identity, chi.URLParam(r, "name"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resourceListResponse{Resources: resources})
}

func (a *API) handleListResourceRevisions(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	revisions, err := a.svc.ListResourceRevisions(
		r.Context(),
		identity,
		chi.URLParam(r, "name"),
		chi.URLParam(r, "resource"),
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	rows := make([]resourceRevisionListItemResponse, 0, len(revisions))
	for _, revision := range revisions {
		bases := revision.Bases
		if bases == nil {
			// craft-store's CharmResourceRevision rejects a null bases list.
			bases = []core.Base{}
		}
		rows = append(rows, resourceRevisionListItemResponse{
			Architectures:   revision.Architectures,
			Bases:           bases,
			CreatedAt:       revision.CreatedAt,
			Description:     revision.Description,
			Download:        revision.Download,
			Filename:        revision.Filename,
			Name:            revision.Name,
			PackageRevision: revision.PackageRevision,
			Revision:        revision.Revision,
			SHA256:          revision.SHA256,
			SHA3384:         revision.SHA3384,
			SHA384:          revision.SHA384,
			SHA512:          revision.SHA512,
			Size:            revision.Size,
			Type:            revision.Type,
		})
	}
	writeJSON(w, http.StatusOK, resourceRevisionListResponse{Revisions: rows})
}

func (a *API) handlePushResource(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	var req service.PushResourceRequest
	if err := a.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, invalidRequestError(err))
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
		writeError(w, r, err)
		return
	}
	writeCreatedJSON(w, statusURL, statusURLResponse{StatusURL: statusURL})
}

func (a *API) handleUpdateResourceRevisions(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	var req service.UpdateResourceRevisionRequest
	if err := a.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, invalidRequestError(err))
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
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resourceRevisionUpdatesResponse{NumResourceRevisionsUpdated: updated})
}

func (a *API) handleOCIUploadCredentials(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	payload, err := a.svc.OCIImageUploadCredentials(
		r.Context(),
		identity,
		chi.URLParam(r, "name"),
		chi.URLParam(r, "resource"),
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *API) handleOCIImageBlob(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	var req struct {
		ImageDigest string `json:"image-digest"`
	}
	if err := a.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, invalidRequestError(err))
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
		writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="oci-image-blob.json"`)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// #nosec G705 -- This endpoint serves a non-HTML attachment.
	_, _ = io.WriteString(w, content)
}

func (a *API) handleResourceDownload(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	packageID, resourceName, revision, parseErr := parseResourceDownloadFilename(chi.URLParam(r, "filename"))
	if parseErr != nil {
		writeError(w, r, apiErrorf(http.StatusBadRequest, "invalid-request", parseErr.Error()))
		return
	}
	reader, size, err := a.svc.DownloadResourceStream(r.Context(), identity, packageID, resourceName, revision)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeAttachment(w, r, "resource.bin", reader, size)
}
