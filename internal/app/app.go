package app

import (
	"context"
	"net/http"

	"github.com/gschiano/charm-registry/internal/api"
	"github.com/gschiano/charm-registry/internal/auth"
	"github.com/gschiano/charm-registry/internal/blob"
	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/repo"
	"github.com/gschiano/charm-registry/internal/service"
)

type App struct {
	Handler http.Handler
}

// New wires the application dependencies and returns a ready HTTP app.
//
// The following errors may be returned:
// - Errors from creating the blob store.
// - Errors from opening or migrating PostgreSQL.
// - Errors from configuring authentication.
func New(ctx context.Context, cfg config.Config) (*App, error) {
	storage, err := blob.NewS3Store(ctx, cfg)
	if err != nil {
		return nil, err
	}
	repository, err := repo.NewPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := repository.Migrate(ctx); err != nil {
		return nil, err
	}
	authenticator, err := auth.New(ctx, cfg, repository)
	if err != nil {
		return nil, err
	}
	svc := service.New(cfg, repository, storage)
	handler := api.New(cfg, svc, authenticator)
	return &App{Handler: handler}, nil
}
