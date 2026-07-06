package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/service"
)

// uploadErrorStatus maps an upload failure to an HTTP status: 413 when the
// request exceeded the configured body-size limit, otherwise the fallback.
func uploadErrorStatus(err error, fallback int) int {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return http.StatusRequestEntityTooLarge
	}
	return fallback
}

func (a *API) handleListRevisions(w http.ResponseWriter, r *http.Request, identity core.Identity) {
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

func (a *API) handlePushRevision(w http.ResponseWriter, r *http.Request, identity core.Identity) {
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
	writeCreatedJSON(w, statusURL, statusURLResponse{StatusURL: statusURL})
}

func (a *API) handleReviewUpload(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	payload, err := a.svc.ReviewUpload(r.Context(), identity, chi.URLParam(r, "name"), r.URL.Query().Get("upload-id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *API) handleUnscannedUpload(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	if err := a.svc.AuthorizeUpload(identity); err != nil {
		writeError(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MaxUploadBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, uploadErrorStatus(err, http.StatusBadRequest), uploadResultResponse{Successful: false})
		return
	}
	var uploadID *string
	for {
		part, partErr := reader.NextPart()
		if partErr == io.EOF {
			break
		}
		if partErr != nil {
			writeJSON(w, uploadErrorStatus(partErr, http.StatusBadRequest), uploadResultResponse{Successful: false})
			return
		}
		if part.FormName() != "binary" || part.FileName() == "" {
			_, _ = io.Copy(io.Discard, part)
			_ = part.Close()
			continue
		}
		upload, uploadErr := a.svc.CreateUploadStream(r.Context(), part.FileName(), part)
		_ = part.Close()
		if uploadErr != nil {
			writeJSON(w, uploadErrorStatus(uploadErr, http.StatusInternalServerError), uploadResultResponse{Successful: false})
			return
		}
		uploadID = &upload.ID
	}
	if uploadID == nil {
		writeJSON(w, http.StatusBadRequest, uploadResultResponse{Successful: false})
		return
	}
	writeJSON(w, http.StatusOK, uploadResultResponse{
		Successful:     true,
		UploadID:       uploadID,
		UploadIDCompat: uploadID,
	})
}

func (a *API) handleCharmDownload(w http.ResponseWriter, r *http.Request, identity core.Identity) {
	packageID, revision, parseErr := parseCharmDownloadFilename(chi.URLParam(r, "filename"))
	if parseErr != nil {
		writeError(w, r, apiErrorf(http.StatusBadRequest, "invalid-request", parseErr.Error()))
		return
	}
	reader, size, err := a.svc.DownloadCharmStream(r.Context(), identity, packageID, revision)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeAttachment(w, r, "artifact.charm", reader, size)
}
