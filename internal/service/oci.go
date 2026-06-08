package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gschiano/charm-registry/internal/core"
)

func (s *Service) syncOCIPackage(ctx context.Context, pkg core.Package) (core.Package, error) {
	slog.DebugContext(ctx, "syncing OCI package metadata",
		"package", pkg.Name,
		"package_id", pkg.ID,
		"oci_project", pkg.OCIProject,
	)
	synced, err := s.oci.SyncPackage(ctx, pkg)
	if err != nil {
		return core.Package{}, err
	}
	if core.PackagesEqualForOCI(pkg, synced) {
		slog.DebugContext(ctx, "OCI package metadata already current",
			"package", pkg.Name,
			"package_id", pkg.ID,
			"oci_project", synced.OCIProject,
		)
		return synced, nil
	}
	synced.UpdatedAt = pkg.UpdatedAt
	if err := s.repo.UpdatePackage(ctx, synced); err != nil {
		return core.Package{}, err
	}
	slog.InfoContext(ctx, "OCI package metadata synced",
		"package", pkg.Name,
		"package_id", pkg.ID,
		"oci_project", synced.OCIProject,
		"push_robot_ready", core.RobotCredentialReady(synced.OCIPushRobot),
		"pull_robot_ready", core.RobotCredentialReady(synced.OCIPullRobot),
	)
	return synced, nil
}

func (s *Service) ensureOCIProvisioned(ctx context.Context, pkg core.Package) (core.Package, error) {
	if core.OCIPackageProvisioned(pkg) {
		slog.DebugContext(ctx, "OCI package already provisioned",
			"package", pkg.Name,
			"package_id", pkg.ID,
			"oci_project", pkg.OCIProject,
		)
		return pkg, nil
	}
	slog.InfoContext(ctx, "OCI package provisioning requested",
		"package", pkg.Name,
		"package_id", pkg.ID,
	)
	provisioned, err := s.syncOCIPackage(ctx, pkg)
	if err != nil {
		slog.ErrorContext(ctx, "OCI package provisioning failed",
			"package", pkg.Name,
			"package_id", pkg.ID,
			"error", err,
		)
		return core.Package{}, newErrorWithCause(
			ErrorKindConflict,
			"oci-provisioning-unavailable",
			fmt.Sprintf("OCI package provisioning unavailable: %s", err),
			err,
		)
	}
	return provisioned, nil
}

func (s *Service) requireOCIPackageReady(pkg core.Package, pull bool) error {
	if !core.OCIPackageProvisioned(pkg) {
		return newError(ErrorKindConflict, "oci-not-provisioned", "OCI package is not provisioned")
	}
	if pull && !core.RobotCredentialReady(pkg.OCIPullRobot) {
		return newError(ErrorKindConflict, "oci-not-provisioned", "OCI package is not provisioned")
	}
	return nil
}
