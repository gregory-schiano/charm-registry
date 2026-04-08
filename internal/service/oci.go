package service

import (
	"context"
	"time"

	"github.com/gschiano/charm-registry/internal/core"
)

func (s *Service) syncOCIPackage(ctx context.Context, pkg core.Package) (core.Package, error) {
	if s.oci == nil {
		return pkg, nil
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

func packagesEqualForOCI(left, right core.Package) bool {
	return left.HarborProject == right.HarborProject &&
		robotEqual(left.HarborPushRobot, right.HarborPushRobot) &&
		robotEqual(left.HarborPullRobot, right.HarborPullRobot) &&
		timePtrEqual(left.HarborSyncedAt, right.HarborSyncedAt)
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
