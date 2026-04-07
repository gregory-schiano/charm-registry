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
		writeError(w, err)
		return
	}
	var revision *int
	if raw := r.URL.Query().Get("revision"); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil {
			writeError(w, serviceError(http.StatusBadRequest, "invalid-request", parseErr.Error()))
			return
		}
		revision = &value
	}
	revisions, err := a.svc.ListRevisions(r.Context(), identity, chi.URLParam(r, "name"), revision)
	if err != nil {
		writeError(w, err)
		return
	}
	rows := make([]map[string]any, 0, len(revisions))
	for _, revision := range revisions {
		rows = append(rows, map[string]any{
			"bases":      revision.Bases,
			"created-at": revision.CreatedAt,
			"created-by": revision.CreatedBy,
			"errors":     []any{},
			"revision":   revision.Revision,
			"sha3-384":   revision.SHA384,
			"size":       revision.Size,
			"status":     revision.Status,
			"version":    revision.Version,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": rows})
}

func (a *API) handlePushRevision(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req service.PushRevisionRequest
	if err := a.decodeJSON(w, r, &req); err != nil {
		writeError(w, invalidRequestError(err))
		return
	}
	statusURL, err := a.svc.PushRevision(r.Context(), identity, chi.URLParam(r, "name"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status-url": statusURL})
}

func (a *API) handleReviewUpload(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, err)
		return
	}
	payload, err := a.svc.ReviewUpload(r.Context(), identity, chi.URLParam(r, "name"), r.URL.Query().Get("upload-id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *API) handleUnscannedUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MaxUploadBytes)
	// #nosec G120 -- MaxBytesReader bounds the multipart body size.
	if err := r.ParseMultipartForm(a.cfg.MaxUploadBytes); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"successful": false, "upload_id": nil})
		return
	}
	file, header, err := r.FormFile("binary")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"successful": false, "upload_id": nil})
		return
	}
	defer file.Close()
	payload, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"successful": false, "upload_id": nil})
		return
	}
	upload, err := a.svc.CreateUpload(r.Context(), header.Filename, payload)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"successful": false, "upload_id": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"successful": true, "upload_id": upload.ID})
}

func (a *API) handleCharmDownload(w http.ResponseWriter, r *http.Request) {
	identity, err := a.identity(r)
	if err != nil {
		writeError(w, err)
		return
	}
	packageID, revision, parseErr := parseCharmDownloadFilename(chi.URLParam(r, "filename"))
	if parseErr != nil {
		writeError(w, serviceError(http.StatusBadRequest, "invalid-request", parseErr.Error()))
		return
	}
	payload, err := a.svc.DownloadCharm(r.Context(), identity, packageID, revision)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="artifact.charm"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	// #nosec G705 -- This endpoint streams attachment bytes.
	_, _ = w.Write(payload)
}
