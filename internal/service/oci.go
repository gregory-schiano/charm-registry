package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

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
	if packagesEqualForOCI(pkg, synced) {
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
		"push_robot_ready", robotCredentialReady(synced.OCIPushRobot),
		"pull_robot_ready", robotCredentialReady(synced.OCIPullRobot),
	)
	return synced, nil
}

func (s *Service) ensureOCIProvisioned(ctx context.Context, pkg core.Package) (core.Package, error) {
	if ociPackageProvisioned(pkg) {
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
		return core.Package{}, newError(
			ErrorKindConflict,
			"oci-provisioning-unavailable",
			fmt.Sprintf("OCI package provisioning unavailable: %s", err),
		)
	}
	return provisioned, nil
}

func (s *Service) requireOCIPackageReady(pkg core.Package, pull bool) error {
	if !ociPackageProvisioned(pkg) {
		return newError(ErrorKindConflict, "oci-not-provisioned", "OCI package is not provisioned")
	}
	if pull && !robotCredentialReady(pkg.OCIPullRobot) {
		return newError(ErrorKindConflict, "oci-not-provisioned", "OCI package is not provisioned")
	}
	return nil
}

func ociPackageProvisioned(pkg core.Package) bool {
	return pkg.OCIProject != "" &&
		robotCredentialReady(pkg.OCIPushRobot) &&
		robotCredentialReady(pkg.OCIPullRobot)
}

func packagesEqualForOCI(left, right core.Package) bool {
	return left.OCIProject == right.OCIProject &&
		robotEqual(left.OCIPushRobot, right.OCIPushRobot) &&
		robotEqual(left.OCIPullRobot, right.OCIPullRobot) &&
		timePtrEqual(left.OCISyncedAt, right.OCISyncedAt)
}

func robotCredentialReady(robot *core.RobotCredential) bool {
	return robot != nil && robot.Username != "" && robot.EncryptedSecret != ""
}

func robotEqual(left, right *core.RobotCredential) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.ID == right.ID &&
		left.Username == right.Username &&
		left.EncryptedSecret == right.EncryptedSecret
}

func timePtrEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Equal(*right)
}
