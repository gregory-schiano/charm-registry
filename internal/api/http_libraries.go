package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
)

// handleLibrariesBulk handles POST /v1/charm/libraries/bulk.
//
// charmcraft calls this endpoint (without auth, via its anonymous client)
// during upload to check whether any charm libraries embedded in the charm
// are already published in the store and need to be updated.
//
// This registry does not host charm libraries, so the handler always returns
// an empty list. charmcraft interprets that as "no store-side libraries
// found" and continues the upload without attempting to publish any libs.
func (a *API) handleLibrariesBulk(w http.ResponseWriter, r *http.Request) {
	// Drain and discard the request body so the connection is reusable, but cap
	// the unauthenticated endpoint to avoid unbounded reads.
	r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MaxJSONBodyBytes)
	_, err := io.Copy(io.Discard, r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeError(w, r, apiErrorf(
				http.StatusRequestEntityTooLarge,
				"request-too-large",
				fmt.Sprintf("request body exceeds %d bytes", a.cfg.MaxJSONBodyBytes),
			))
			return
		}
		writeError(w, r, apiErrorf(http.StatusBadRequest, "invalid-request-body", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"libraries": []any{}})
}
