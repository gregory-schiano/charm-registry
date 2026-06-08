package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gschiano/charm-registry/internal/app"
	"github.com/gschiano/charm-registry/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	if cfg.EnableInsecureDevAuth {
		slog.Warn("INSECURE DEV AUTH ENABLED — not for production use")
	}
	application, err := app.New(ctx, cfg)
	if err != nil {
		slog.Error("build application", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := application.Close(); err != nil {
			slog.Error("close application", "error", err)
		}
	}()

	server := newAPIServer(cfg, application.Handler)
	var ociServer *http.Server
	if application.OCIHandler != nil {
		ociServer = newOCIServer(cfg, application.OCIHandler)
		go serveOCI(ociServer, cfg, stop)
	}
	go gracefulShutdown(ctx, server, ociServer, cfg.ServerShutdownTimeout)

	slog.Info("private charm registry listening", "listen_address", cfg.ListenAddress)
	if err := serveAPI(server, cfg); err != nil {
		slog.Error("serve", "error", err)
		os.Exit(1)
	}
}

func newAPIServer(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ServerReadHeaderTimeout,
		ReadTimeout:       cfg.ServerReadTimeout,
		WriteTimeout:      cfg.ServerWriteTimeout,
		IdleTimeout:       cfg.ServerIdleTimeout,
		MaxHeaderBytes:    cfg.ServerMaxHeaderBytes,
		BaseContext: func(_ net.Listener) context.Context {
			return context.Background()
		},
	}
}

func newOCIServer(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.OCIListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ServerReadHeaderTimeout,
		ReadTimeout:       cfg.ServerReadTimeout,
		WriteTimeout:      cfg.ServerWriteTimeout,
		IdleTimeout:       cfg.ServerIdleTimeout,
		MaxHeaderBytes:    cfg.ServerMaxHeaderBytes,
		BaseContext: func(_ net.Listener) context.Context {
			return context.Background()
		},
	}
}

func serveOCI(srv *http.Server, cfg config.Config, stop context.CancelFunc) {
	slog.Info("embedded OCI registry listening", "listen_address", cfg.OCIListenAddress)
	var err error
	if cfg.OCITLSCertFile != "" || cfg.OCITLSKeyFile != "" {
		err = srv.ListenAndServeTLS(cfg.OCITLSCertFile, cfg.OCITLSKeyFile)
	} else {
		err = srv.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("serve embedded OCI registry", "error", err)
		stop()
	}
}

func serveAPI(srv *http.Server, cfg config.Config) error {
	if cfg.APITLSCertFile != "" || cfg.APITLSKeyFile != "" {
		return srv.ListenAndServeTLS(cfg.APITLSCertFile, cfg.APITLSKeyFile)
	}
	slog.Warn("API server running without TLS — use a reverse proxy in production")
	return srv.ListenAndServe()
}

func gracefulShutdown(ctx context.Context, server, ociServer *http.Server, timeout time.Duration) {
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	if ociServer != nil {
		_ = ociServer.Shutdown(shutdownCtx)
	}
}
