package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os/signal"
	"syscall"

	"github.com/gschiano/charm-registry/internal/app"
	"github.com/gschiano/charm-registry/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	application, err := app.New(ctx, cfg)
	if err != nil {
		log.Fatalf("build application: %v", err)
	}

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
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ServerShutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("private charm registry listening on %s", cfg.ListenAddress)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}
