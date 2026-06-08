package registrysync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/service"
)

func (s *Service) syncOCIPackage(ctx context.Context, pkg core.Package) (core.Package, error) {
	synced, err := s.oci.SyncPackage(ctx, pkg)
	if err != nil {
		return core.Package{}, err
	}
	if packagesEqualForOCI(pkg, synced) {
		return synced, nil
	}
	synced.UpdatedAt = pkg.UpdatedAt
	if err := s.repo.UpdatePackage(ctx, synced); err != nil {
		return core.Package{}, err
	}
	return synced, nil
}

func (s *Service) ensureOCIProvisioned(ctx context.Context, pkg core.Package) (core.Package, error) {
	if ociPackageProvisioned(pkg) {
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
