package service

import (
	"context"
	"time"

	"github.com/gschiano/charm-registry/internal/core"
)

func (s *Service) syncOCIPackage(ctx context.Context, pkg core.Package) (core.Package, error) {
	if s.oci == nil {
		return core.Package{}, newError(ErrorKindConflict, "oci-unavailable", "OCI registry is unavailable")
	}
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
	if s.oci == nil {
		return core.Package{}, newError(ErrorKindConflict, "oci-unavailable", "OCI registry is unavailable")
	}
	if ociPackageProvisioned(pkg) {
		return pkg, nil
	}
	provisioned, err := s.syncOCIPackage(ctx, pkg)
	if err != nil {
		return core.Package{}, newError(
			ErrorKindConflict,
			"oci-provisioning-unavailable",
			"OCI package provisioning is unavailable",
		)
	}
	return provisioned, nil
}

func (s *Service) requireOCIPackageReady(pkg core.Package, pull bool) error {
	if s.oci == nil {
		return newError(ErrorKindConflict, "oci-unavailable", "OCI registry is unavailable")
	}
	if !ociPackageProvisioned(pkg) {
		return newError(ErrorKindConflict, "oci-not-provisioned", "OCI package is not provisioned")
	}
	if pull && !robotCredentialReady(pkg.HarborPullRobot) {
		return newError(ErrorKindConflict, "oci-not-provisioned", "OCI package is not provisioned")
	}
	return nil
}

func ociPackageProvisioned(pkg core.Package) bool {
	return pkg.HarborProject != "" &&
		robotCredentialReady(pkg.HarborPushRobot) &&
		robotCredentialReady(pkg.HarborPullRobot)
}

func packagesEqualForOCI(left, right core.Package) bool {
	return left.HarborProject == right.HarborProject &&
		robotEqual(left.HarborPushRobot, right.HarborPushRobot) &&
		robotEqual(left.HarborPullRobot, right.HarborPullRobot) &&
		timePtrEqual(left.HarborSyncedAt, right.HarborSyncedAt)
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
