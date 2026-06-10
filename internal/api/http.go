package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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

type syncAdminService interface {
	ListCharmhubSyncRules(ctx context.Context, identity core.Identity) ([]core.CharmhubSyncRule, error)
	AddCharmhubSyncRule(
		ctx context.Context,
		identity core.Identity,
		packageName, track string,
		bases, architectures []string,
	) (core.CharmhubSyncRule, error)
	RemoveCharmhubSyncRule(ctx context.Context, identity core.Identity, packageName, track string) error
	TriggerCharmhubSync(ctx context.Context, identity core.Identity, packageName string) error
}

// API is the HTTP handler for the registry.
type API struct {
	cfg          config.Config
	svc          *service.Service
	sync         syncAdminService
	auth         *auth.Authenticator
	tokenLimiter *tokenIssueLimiter
	ipLimiter    *ipRateLimiter
}

// New builds the HTTP handler for the registry API.
func New(cfg config.Config, svc *service.Service, syncSvc syncAdminService, authenticator *auth.Authenticator) http.Handler {
	api := &API{
		cfg:          cfg,
		svc:          svc,
		sync:         syncSvc,
		auth:         authenticator,
		tokenLimiter: newTokenIssueLimiter(cfg.TokenRateLimit, cfg.TokenRateWindow),
		ipLimiter:    newIPRateLimiter(cfg.IPRateLimit, cfg.IPRateWindow),
	}
	router := chi.NewRouter()
	router.Use(chimiddleware.RequestID)
	router.Use(chimiddleware.RealIP)
	router.Use(api.logRequests)
	router.Use(chimiddleware.Timeout(cfg.RequestTimeout))
	router.Use(chimiddleware.Recoverer)
	router.Use(api.securityHeaders)
	router.Use(api.rateLimit)
	router.NotFound(api.handleNotFound)
	router.MethodNotAllowed(api.handleMethodNotAllowed)

	router.Get("/", api.handleRoot)
	router.Get("/healthz", api.handleHealthz)
	router.Get("/readyz", api.handleReadyz)
	router.Get("/metrics", metricsHandler().ServeHTTP)
	router.Get("/openapi.yaml", api.handleOpenAPI)
	router.Get("/docs", api.handleDocs)

	router.Group(func(r chi.Router) {
		r.Post("/v1/charm/libraries/bulk", api.handleLibrariesBulk)
	})
	router.Group(func(r chi.Router) {
		r.Get("/v1/tokens", api.requireIdentity(api.handleGetTokens))
		r.Post("/v1/tokens", api.requireIdentity(api.handleIssueToken))
		r.Post("/v1/tokens/exchange", api.requireIdentity(api.handleExchangeToken))
		r.Post("/v1/tokens/offline/exchange", api.requireIdentity(api.handleExchangeToken))
		r.Post("/v1/tokens/revoke", api.requireIdentity(api.handleRevokeToken))
		r.Get("/v1/tokens/whoami", api.requireIdentity(api.handleTokenWhoAmI))
		r.Post("/v1/tokens/dashboard/exchange", api.requireIdentity(api.handleDashboardExchange))
		r.Get("/v1/whoami", api.requireIdentity(api.handleWhoAmI))
		r.Get("/v1/admin/charmhub-sync", api.requireIdentity(api.handleListCharmhubSyncRules))
		r.Post("/v1/admin/charmhub-sync", api.requireIdentity(api.handleAddCharmhubSyncRule))
		r.Delete("/v1/admin/charmhub-sync/{name}/{track}", api.requireIdentity(api.handleDeleteCharmhubSyncRule))
		r.Post("/v1/admin/charmhub-sync/{name}/run", api.requireIdentity(api.handleRunCharmhubSync))

		r.Get("/v1/charm", api.requireIdentity(api.handleListPackages))
		r.Post("/v1/charm", api.requireIdentity(api.handleRegisterPackage))
		r.Get("/v1/charm/{name}", api.requireIdentity(api.handleGetPackage))
		r.Patch("/v1/charm/{name}", api.requireIdentity(api.handlePatchPackage))
		r.Delete("/v1/charm/{name}", api.requireIdentity(api.handleDeletePackage))

		r.Get("/v1/charm/{name}/revisions", api.requireIdentity(api.handleListRevisions))
		r.Post("/v1/charm/{name}/revisions", api.requireIdentity(api.handlePushRevision))
		r.Get("/v1/charm/{name}/revisions/review", api.requireIdentity(api.handleReviewUpload))
		r.Get("/v1/charm/{name}/resources", api.requireIdentity(api.handleListResources))
		r.Get("/v1/charm/{name}/resources/{resource}/revisions", api.requireIdentity(api.handleListResourceRevisions))
		r.Post("/v1/charm/{name}/resources/{resource}/revisions", api.requireIdentity(api.handlePushResource))
		r.Patch("/v1/charm/{name}/resources/{resource}/revisions", api.requireIdentity(api.handleUpdateResourceRevisions))
		r.Get("/v1/charm/{name}/resources/{resource}/oci-image/upload-credentials", api.requireIdentity(api.handleOCIUploadCredentials))
		r.Post("/v1/charm/{name}/resources/{resource}/oci-image/blob", api.requireIdentity(api.handleOCIImageBlob))
		r.Get("/v1/charm/{name}/releases", api.requireIdentity(api.handleListReleases))
		r.Post("/v1/charm/{name}/releases", api.requireIdentity(api.handleRelease))
		r.Post("/v1/charm/{name}/tracks", api.requireIdentity(api.handleCreateTracks))

		r.Post("/unscanned-upload/", api.requireIdentity(api.handleUnscannedUpload))

		r.Get("/v2/charms/find", api.requireIdentity(api.handleFind))
		r.Get("/v2/charms/info/{name}", api.requireIdentity(api.handleInfo))
		r.Post("/v2/charms/refresh", api.requireIdentity(api.handleRefresh))
		r.Get("/v2/charms/resources/{name}/{resource}/revisions", api.requireIdentity(api.handleListResourceRevisions))

		r.Get("/api/v1/charms/download/{filename}", api.requireIdentity(api.handleCharmDownload))
		r.Get("/api/v1/resources/download/{filename}", api.requireIdentity(api.handleResourceDownload))
	})
	return router
}

type tokenIssueLimiter struct {
	mu              sync.Mutex
	entries         map[string][]time.Time
	limit           int
	window          time.Duration
	cleanupInterval time.Duration
	lastCleanup     time.Time
	now             func() time.Time
}

func newTokenIssueLimiter(limit int, window time.Duration) *tokenIssueLimiter {
	return &tokenIssueLimiter{
		entries:         make(map[string][]time.Time),
		limit:           limit,
		window:          window,
		cleanupInterval: window,
		now:             time.Now,
	}
}

func (l *tokenIssueLimiter) Allow(key string) bool {
	if l == nil || key == "" {
		return true
	}
	// A limit of 0 means rate limiting is disabled (unlimited requests).
	// Negative limits are rejected by config validation.
	if l.limit == 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-l.window)
	if l.lastCleanup.IsZero() {
		l.lastCleanup = now
	} else if now.Sub(l.lastCleanup) >= l.cleanupInterval {
		l.cleanup(cutoff)
		l.lastCleanup = now
	}
	timestamps := l.pruneKey(key, cutoff)
	if len(timestamps) >= l.limit {
		return false
	}
	l.entries[key] = append(timestamps, now)
	return true
}

func (l *tokenIssueLimiter) pruneKey(key string, cutoff time.Time) []time.Time {
	timestamps := l.entries[key][:0]
	for _, ts := range l.entries[key] {
		if ts.After(cutoff) {
			timestamps = append(timestamps, ts)
		}
	}
	if len(timestamps) == 0 {
		delete(l.entries, key)
		return nil
	}
	l.entries[key] = timestamps
	return timestamps
}

func (l *tokenIssueLimiter) cleanup(cutoff time.Time) {
	for key := range l.entries {
		l.pruneKey(key, cutoff)
	}
}

func (a *API) resolveIdentity(r *http.Request) (core.Identity, error) {
	claims, token, err := a.auth.Authenticate(r)
	if err != nil {
		return core.Identity{}, apiErrorf(http.StatusUnauthorized, "unauthorized", "authentication required")
	}
	return a.svc.ResolveIdentity(r.Context(), claims, token)
}

func (a *API) requireIdentity(next func(w http.ResponseWriter, r *http.Request, identity core.Identity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, err := a.resolveIdentity(r)
		if err != nil {
			writeError(w, r, err)
			return
		}
		next(w, r, identity)
	}
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
			return apiErrorf(
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
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("json encode", "error", err)
	}
}

func writeCreatedJSON(w http.ResponseWriter, location string, payload any) {
	if location != "" {
		w.Header().Set("Location", location)
	}
	writeJSON(w, http.StatusCreated, payload)
}

func writeAttachment(w http.ResponseWriter, r *http.Request, filename string, body io.ReadCloser, size int64) {
	defer body.Close()
	w.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeFilename(filename)+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	if _, err := io.Copy(w, body); err != nil {
		slog.ErrorContext(r.Context(), "stream attachment",
			"request_id", chimiddleware.GetReqID(r.Context()),
			"error", err,
		)
	}
}

// sanitizeFilename strips characters that could cause Content-Disposition
// header injection (quotes, backslashes, CRLF) and replaces non-printable
// characters with underscores.
func sanitizeFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r == '"' || r == '\\' || r == '\r' || r == '\n':
			return -1 // strip
		case r < 32 || r == 0x7f:
			return '_' // replace control chars
		default:
			return r
		}
	}, name)
	// Truncate to a safe length.
	if len(name) > 255 {
		name = name[:255]
	}
	return name
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		return
	}
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		writeJSON(w, apiErr.Status, newErrorListResponse(apiErr.Code, apiErr.Message))
		return
	}
	var serviceErr *service.Error
	if errors.As(err, &serviceErr) {
		writeJSON(w, serviceErrorStatus(serviceErr), newErrorListResponse(serviceErr.Code, serviceErr.Message))
		return
	}
	if errors.Is(err, repo.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, newErrorListResponse("not-found", "resource not found"))
		return
	}
	slog.ErrorContext(r.Context(), "internal error",
		"request_id", chimiddleware.GetReqID(r.Context()),
		"error", err,
	)
	writeJSON(w, http.StatusInternalServerError, newErrorListResponse("internal-error", "internal server error"))
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

func (a *API) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, newErrorListResponse("not-found", "endpoint not found"))
}

func (a *API) handleMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusMethodNotAllowed, newErrorListResponse("method-not-allowed", "method not allowed"))
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
	return apiErrorf(http.StatusBadRequest, "invalid-request", err.Error())
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

// ipRateLimiter provides per-IP request rate limiting using a sliding window.
// Note: this is in-memory only; in a multi-instance deployment, use a shared
// store (Redis, etc.) instead.
type ipRateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	entries map[string]*ipWindow
}

type ipWindow struct {
	timestamps []time.Time
}

func newIPRateLimiter(limit int, window time.Duration) *ipRateLimiter {
	return &ipRateLimiter{
		limit:   limit,
		window:  window,
		entries: make(map[string]*ipWindow),
	}
}

func (l *ipRateLimiter) Allow(ip string) bool {
	// A limit of 0 means rate limiting is disabled (unlimited requests).
	// Negative limits are rejected by config validation.
	if l.limit == 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-l.window)
	w, ok := l.entries[ip]
	if !ok {
		w = &ipWindow{}
		l.entries[ip] = w
	}
	// Prune old entries.
	i := 0
	for i < len(w.timestamps) && w.timestamps[i].Before(cutoff) {
		i++
	}
	w.timestamps = w.timestamps[i:]
	if len(w.timestamps) >= l.limit {
		return false
	}
	w.timestamps = append(w.timestamps, now)
	return true
}

// Cleanup removes stale entries. Call periodically.
func (l *ipRateLimiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-l.window)
	for ip, w := range l.entries {
		i := 0
		for i < len(w.timestamps) && w.timestamps[i].Before(cutoff) {
			i++
		}
		w.timestamps = w.timestamps[i:]
		if len(w.timestamps) == 0 {
			delete(l.entries, ip)
		}
	}
}

func (api *API) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			ip = strings.SplitN(forwarded, ",", 2)[0]
		}
		if !api.ipLimiter.Allow(ip) {
			writeJSON(w, http.StatusTooManyRequests, newErrorListResponse("too-many-requests", "rate limit exceeded"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
