package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/gschiano/charm-registry/internal/auth"
	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
	"github.com/gschiano/charm-registry/internal/service"
)

// API is the HTTP handler for the registry.
type API struct {
	cfg          config.Config
	svc          *service.Service
	auth         *auth.Authenticator
	tokenLimiter *tokenIssueLimiter
}

// New builds the HTTP handler for the registry API.
func New(cfg config.Config, svc *service.Service, authenticator *auth.Authenticator) http.Handler {
	api := &API{
		cfg:          cfg,
		svc:          svc,
		auth:         authenticator,
		tokenLimiter: newTokenIssueLimiter(5, time.Minute),
	}
	router := chi.NewRouter()
	router.Use(chimiddleware.RequestID)
	router.Use(chimiddleware.RealIP)
	router.Use(api.logRequests)
	router.Use(chimiddleware.Recoverer)
	router.Use(api.securityHeaders)

	router.Get("/", api.handleRoot)
	router.Get("/healthz", api.handleHealthz)
	router.Get("/readyz", api.handleReadyz)
	router.Get("/openapi.yaml", api.handleOpenAPI)
	router.Get("/docs", api.handleDocs)

	router.Get("/v1/tokens", api.handleGetTokens)
	router.Post("/v1/tokens", api.handleIssueToken)
	router.Post("/v1/tokens/exchange", api.handleExchangeToken)
	router.Post("/v1/tokens/offline/exchange", api.handleExchangeToken)
	router.Post("/v1/tokens/revoke", api.handleRevokeToken)
	router.Get("/v1/tokens/whoami", api.handleTokenWhoAmI)
	router.Post("/v1/tokens/dashboard/exchange", api.handleDashboardExchange)
	router.Get("/v1/whoami", api.handleWhoAmI)

	router.Post("/v1/charm/libraries/bulk", api.handleLibrariesBulk)
	router.Get("/v1/charm", api.handleListPackages)
	router.Post("/v1/charm", api.handleRegisterPackage)
	router.Get("/v1/charm/{name}", api.handleGetPackage)
	router.Patch("/v1/charm/{name}", api.handlePatchPackage)
	router.Delete("/v1/charm/{name}", api.handleDeletePackage)

	router.Get("/v1/charm/{name}/revisions", api.handleListRevisions)
	router.Post("/v1/charm/{name}/revisions", api.handlePushRevision)
	router.Get("/v1/charm/{name}/revisions/review", api.handleReviewUpload)
	router.Get("/v1/charm/{name}/resources", api.handleListResources)
	router.Get("/v1/charm/{name}/resources/{resource}/revisions", api.handleListResourceRevisions)
	router.Post("/v1/charm/{name}/resources/{resource}/revisions", api.handlePushResource)
	router.Patch("/v1/charm/{name}/resources/{resource}/revisions", api.handleUpdateResourceRevisions)
	router.Get("/v1/charm/{name}/resources/{resource}/oci-image/upload-credentials", api.handleOCIUploadCredentials)
	router.Post("/v1/charm/{name}/resources/{resource}/oci-image/blob", api.handleOCIImageBlob)
	router.Get("/v1/charm/{name}/releases", api.handleListReleases)
	router.Post("/v1/charm/{name}/releases", api.handleRelease)
	router.Post("/v1/charm/{name}/tracks", api.handleCreateTracks)

	router.Post("/unscanned-upload/", api.handleUnscannedUpload)

	router.Get("/v2/charms/find", api.handleFind)
	router.Get("/v2/charms/info/{name}", api.handleInfo)
	router.Post("/v2/charms/refresh", api.handleRefresh)

	router.Get("/api/v1/charms/download/{filename}", api.handleCharmDownload)
	router.Get("/api/v1/resources/download/{filename}", api.handleResourceDownload)
	return router
}

type tokenIssueLimiter struct {
	mu      sync.Mutex
	entries map[string][]time.Time
	limit   int
	window  time.Duration
	now     func() time.Time
}

func newTokenIssueLimiter(limit int, window time.Duration) *tokenIssueLimiter {
	return &tokenIssueLimiter{
		entries: make(map[string][]time.Time),
		limit:   limit,
		window:  window,
		now:     time.Now,
	}
}

func (l *tokenIssueLimiter) Allow(key string) bool {
	if l == nil || key == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-l.window)
	timestamps := l.entries[key][:0]
	for _, ts := range l.entries[key] {
		if ts.After(cutoff) {
			timestamps = append(timestamps, ts)
		}
	}
	if len(timestamps) >= l.limit {
		l.entries[key] = timestamps
		return false
	}
	l.entries[key] = append(timestamps, now)
	return true
}

func (a *API) identity(r *http.Request) (core.Identity, error) {
	claims, token, err := a.auth.Authenticate(r)
	if err != nil {
		return core.Identity{}, apiErrorf(http.StatusUnauthorized, "unauthorized", "authentication required")
	}
	return a.svc.ResolveIdentity(r.Context(), claims, token)
}

func (a *API) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().
			Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (a *API) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		slog.InfoContext(r.Context(), "http request",
			"request_id", chimiddleware.GetReqID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"duration_ms", time.Since(start).Milliseconds(),
			"remote_addr", r.RemoteAddr,
		)
	})
}

func (a *API) decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MaxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return serviceError(
				http.StatusRequestEntityTooLarge,
				"request-too-large",
				fmt.Sprintf("request body exceeds %d bytes", a.cfg.MaxJSONBodyBytes),
			)
		}
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("cannot decode request: body must contain a single JSON document")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		return
	}
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		writeJSON(w, apiErr.Status, map[string]any{
			"error-list": []map[string]any{{
				"code":    apiErr.Code,
				"message": apiErr.Message,
			}},
		})
		return
	}
	var serviceErr *service.Error
	if errors.As(err, &serviceErr) {
		writeJSON(w, serviceErrorStatus(serviceErr), map[string]any{
			"error-list": []map[string]any{{
				"code":    serviceErr.Code,
				"message": serviceErr.Message,
			}},
		})
		return
	}
	if errors.Is(err, repo.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error-list": []map[string]any{{"code": "not-found", "message": "resource not found"}},
		})
		return
	}
	slog.ErrorContext(r.Context(), "internal error",
		"request_id", chimiddleware.GetReqID(r.Context()),
		"error", err,
	)
	writeJSON(w, http.StatusInternalServerError, map[string]any{
		"error-list": []map[string]any{{"code": "internal-error", "message": "internal server error"}},
	})
}

type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func apiErrorf(status int, code, message string) error {
	return &apiError{Status: status, Code: code, Message: message}
}

func serviceError(status int, code, message string) error {
	return apiErrorf(status, code, message)
}

func invalidRequestError(err error) error {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return err
	}
	var serviceErr *service.Error
	if errors.As(err, &serviceErr) {
		return err
	}
	return serviceError(http.StatusBadRequest, "invalid-request", err.Error())
}

func serviceErrorStatus(err *service.Error) int {
	switch err.Kind {
	case service.ErrorKindUnauthorized:
		return http.StatusUnauthorized
	case service.ErrorKindForbidden:
		return http.StatusForbidden
	case service.ErrorKindNotFound:
		return http.StatusNotFound
	case service.ErrorKindConflict:
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

func packageMetadata(pkg core.Package) map[string]any {
	tracks := make([]core.Track, len(pkg.Tracks))
	copy(tracks, pkg.Tracks)
	sort.Slice(tracks, func(i, j int) bool { return tracks[i].Name < tracks[j].Name })
	return map[string]any{
		"authority":        pkg.Authority,
		"contact":          pkg.Contact,
		"default-track":    pkg.DefaultTrack,
		"description":      pkg.Description,
		"id":               pkg.ID,
		"links":            pkg.Links,
		"media":            pkg.Media,
		"name":             pkg.Name,
		"private":          pkg.Private,
		"publisher":        pkg.Publisher,
		"status":           pkg.Status,
		"store":            pkg.Store,
		"summary":          pkg.Summary,
		"title":            pkg.Title,
		"track-guardrails": pkg.TrackGuardrails,
		"tracks":           tracks,
		"type":             pkg.Type,
		"website":          pkg.Website,
	}
}

func parseCharmDownloadFilename(filename string) (string, int, error) {
	trimmed := strings.TrimSuffix(filename, ".charm")
	parts := strings.Split(trimmed, "_")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("cannot parse charm download path")
	}
	revision, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, err
	}
	return parts[0], revision, nil
}

func parseResourceDownloadFilename(filename string) (string, string, int, error) {
	trimmed := strings.TrimPrefix(filename, "charm_")
	dot := strings.Index(trimmed, ".")
	if dot < 0 {
		return "", "", 0, fmt.Errorf("cannot parse resource download path")
	}
	packageID := trimmed[:dot]
	resourcePart := trimmed[dot+1:]
	lastUnderscore := strings.LastIndex(resourcePart, "_")
	if lastUnderscore < 0 {
		return "", "", 0, fmt.Errorf("cannot parse resource download path")
	}
	revision, err := strconv.Atoi(resourcePart[lastUnderscore+1:])
	if err != nil {
		return "", "", 0, err
	}
	return packageID, resourcePart[:lastUnderscore], revision, nil
}
