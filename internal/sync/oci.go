package registrysync

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/service"
)

func (s *Service) syncOCIPackage(ctx context.Context, pkg core.Package) (core.Package, error) {
	synced, err := s.oci.SyncPackage(ctx, pkg)
	if err != nil {
		return core.Package{}, err
	}
	if core.PackagesEqualForOCI(pkg, synced) {
		return synced, nil
	}
	synced.UpdatedAt = pkg.UpdatedAt
	if err := s.repo.UpdatePackage(ctx, synced); err != nil {
		return core.Package{}, err
	}
	return synced, nil
}

func (s *Service) ensureOCIProvisioned(ctx context.Context, pkg core.Package) (core.Package, error) {
	if core.OCIPackageProvisioned(pkg) {
		return pkg, nil
	}
	provisioned, err := s.syncOCIPackage(ctx, pkg)
	if err != nil {
		slog.ErrorContext(ctx, "OCI package provisioning failed",
			"package", pkg.Name,
			"package_id", pkg.ID,
			"error", err,
		)
		return core.Package{}, newError(
			service.ErrorKindConflict,
			"oci-provisioning-unavailable",
			fmt.Sprintf("OCI package provisioning unavailable: %s", err),
		)
	}
	return provisioned, nil
}
