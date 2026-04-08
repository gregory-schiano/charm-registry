package app

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/gschiano/charm-registry/internal/api"
	"github.com/gschiano/charm-registry/internal/auth"
	"github.com/gschiano/charm-registry/internal/blob"
	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/harbor"
	"github.com/gschiano/charm-registry/internal/repo"
	"github.com/gschiano/charm-registry/internal/service"
)

type App struct {
	Handler http.Handler
	closers []io.Closer
}

// New wires the application dependencies and returns a ready HTTP app.
//
// The following errors may be returned:
// - Errors from creating the blob store.
// - Errors from opening or migrating PostgreSQL.
// - Errors from configuring authentication.
func New(ctx context.Context, cfg config.Config) (*App, error) {
	var closers []io.Closer
	closeAll := func() error {
		var errs []error
		for i := len(closers) - 1; i >= 0; i-- {
			if err := closers[i].Close(); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}
	storage, err := blob.NewS3Store(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if closer, ok := any(storage).(io.Closer); ok {
		closers = append(closers, closer)
	}
	repository, err := repo.NewPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		_ = closeAll()
		return nil, err
	}
	if closer, ok := any(repository).(io.Closer); ok {
		closers = append(closers, closer)
	}
	if err := repository.Migrate(ctx); err != nil {
		_ = closeAll()
		return nil, err
	}
	authenticator, err := auth.New(ctx, cfg, repository)
	if err != nil {
		_ = closeAll()
		return nil, err
	}
	ociRegistry, err := harbor.New(cfg)
	if err != nil {
		_ = closeAll()
		return nil, err
	}
	if closer, ok := any(ociRegistry).(io.Closer); ok {
		closers = append(closers, closer)
	}
	svc := service.New(cfg, repository, storage, ociRegistry)
	handler := api.New(cfg, svc, authenticator)
	return &App{Handler: handler, closers: closers}, nil
}

// Close releases application resources such as DB pools and idle HTTP clients.
func (a *App) Close() error {
	var errs []error
	for i := len(a.closers) - 1; i >= 0; i-- {
		if err := a.closers[i].Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
