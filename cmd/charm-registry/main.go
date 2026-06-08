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

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           application.Handler,
		ReadHeaderTimeout: cfg.ServerReadHeaderTimeout,
		ReadTimeout:       cfg.ServerReadTimeout,
		WriteTimeout:      cfg.ServerWriteTimeout,
		IdleTimeout:       cfg.ServerIdleTimeout,
		MaxHeaderBytes:    cfg.ServerMaxHeaderBytes,
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}
	var ociServer *http.Server
	if application.OCIHandler != nil {
		ociServer = &http.Server{
			Addr:              cfg.OCIListenAddress,
			Handler:           application.OCIHandler,
			ReadHeaderTimeout: cfg.ServerReadHeaderTimeout,
			ReadTimeout:       cfg.ServerReadTimeout,
			WriteTimeout:      cfg.ServerWriteTimeout,
			IdleTimeout:       cfg.ServerIdleTimeout,
			MaxHeaderBytes:    cfg.ServerMaxHeaderBytes,
			BaseContext: func(_ net.Listener) context.Context {
				return ctx
			},
		}
		go func() {
			slog.Info("embedded OCI registry listening", "listen_address", cfg.OCIListenAddress)
			var serveErr error
			if cfg.OCITLSCertFile != "" || cfg.OCITLSKeyFile != "" {
				serveErr = ociServer.ListenAndServeTLS(cfg.OCITLSCertFile, cfg.OCITLSKeyFile)
			} else {
				serveErr = ociServer.ListenAndServe()
			}
			if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				slog.Error("serve embedded OCI registry", "error", serveErr)
				stop()
			}
		}()
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ServerShutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		if ociServer != nil {
			_ = ociServer.Shutdown(shutdownCtx)
		}
	}()

	slog.Info("private charm registry listening", "listen_address", cfg.ListenAddress)
	if cfg.APITLSCertFile != "" || cfg.APITLSKeyFile != "" {
		if err := server.ListenAndServeTLS(cfg.APITLSCertFile, cfg.APITLSKeyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("serve TLS", "error", err)
			os.Exit(1)
		}
	} else {
		slog.Warn("API server running without TLS — use a reverse proxy in production")
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("serve", "error", err)
			os.Exit(1)
		}
	}
}
