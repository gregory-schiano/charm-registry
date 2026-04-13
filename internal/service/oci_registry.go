package service

import (
	"context"

	"github.com/gschiano/charm-registry/internal/core"
)

// OCIRegistry abstracts the OCI registry operations used by the service layer.
type OCIRegistry interface {
	SyncPackage(ctx context.Context, pkg core.Package) (core.Package, error)
	ImageReference(pkg core.Package, resourceName string) (string, error)
	Credentials(pkg core.Package, pull bool) (username, password string, err error)
}
