package api

import (
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/gschiano/charm-registry/internal/service"
)

func (a *API) handleListRevisions(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var revision *int
	if raw := r.URL.Query().Get("revision"); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil {
			writeError(w, r, apiErrorf(http.StatusBadRequest, "invalid-request", parseErr.Error()))
			return
		}
		revision = &value
	}
	revisions, err := a.svc.ListRevisions(r.Context(), identity, chi.URLParam(r, "name"), revision)
	if err != nil {
		writeError(w, r, err)
		return
	}
	rows := make([]revisionListItemResponse, 0, len(revisions))
	for _, revision := range revisions {
		rows = append(rows, revisionListItemResponse{
			Bases:     revision.Bases,
			CreatedAt: revision.CreatedAt,
			CreatedBy: revision.CreatedBy,
			Errors:    []any{},
			Revision:  revision.Revision,
			SHA384:    revision.SHA384,
			Size:      revision.Size,
			Status:    revision.Status,
			Version:   revision.Version,
		})
	}
	writeJSON(w, http.StatusOK, revisionListResponse{Revisions: rows})
}

func (a *API) handlePushRevision(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req service.PushRevisionRequest
	if err := a.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, invalidRequestError(err))
		return
	}
	statusURL, err := a.svc.PushRevision(r.Context(), identity, chi.URLParam(r, "name"), req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, statusURLResponse{StatusURL: statusURL})
}

func (a *API) handleReviewUpload(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	payload, err := a.svc.ReviewUpload(r.Context(), identity, chi.URLParam(r, "name"), r.URL.Query().Get("upload-id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *API) handleUnscannedUpload(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := a.svc.AuthorizeUpload(identity); err != nil {
		writeError(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MaxUploadBytes)
	// #nosec G120 -- MaxBytesReader bounds the multipart body size.
	if err := r.ParseMultipartForm(a.cfg.MaxUploadBytes); err != nil {
		writeJSON(w, http.StatusBadRequest, uploadResultResponse{Successful: false})
		return
	}
	file, header, err := r.FormFile("binary")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, uploadResultResponse{Successful: false})
		return
	}
	defer file.Close()
	payload, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, uploadResultResponse{Successful: false})
		return
	}
	upload, err := a.svc.CreateUpload(r.Context(), header.Filename, payload)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, uploadResultResponse{Successful: false})
		return
	}
	writeJSON(w, http.StatusOK, uploadResultResponse{Successful: true, UploadID: &upload.ID})
}

func (a *API) handleCharmDownload(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	packageID, revision, parseErr := parseCharmDownloadFilename(chi.URLParam(r, "filename"))
	if parseErr != nil {
		writeError(w, r, apiErrorf(http.StatusBadRequest, "invalid-request", parseErr.Error()))
		return
	}
	payload, err := a.svc.DownloadCharm(r.Context(), identity, packageID, revision)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="artifact.charm"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	// #nosec G705 -- This endpoint streams attachment bytes.
	_, _ = w.Write(payload)
}
