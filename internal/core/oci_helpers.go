package core

import "time"

// OCIPackageProvisioned checks whether a package has a fully provisioned
// OCI project with both push and pull robot credentials ready.
func OCIPackageProvisioned(pkg Package) bool {
	return pkg.OCIProject != "" &&
		RobotCredentialReady(pkg.OCIPushRobot) &&
		RobotCredentialReady(pkg.OCIPullRobot)
}

// PackagesEqualForOCI reports whether two packages have identical OCI
// project, robot credentials, and sync timestamps.
func PackagesEqualForOCI(left, right Package) bool {
	return left.OCIProject == right.OCIProject &&
		RobotEqual(left.OCIPushRobot, right.OCIPushRobot) &&
		RobotEqual(left.OCIPullRobot, right.OCIPullRobot) &&
		TimePtrEqual(left.OCISyncedAt, right.OCISyncedAt)
}

// RobotCredentialReady checks that a robot credential has both a username
// and an encrypted secret populated.
func RobotCredentialReady(robot *RobotCredential) bool {
	return robot != nil && robot.Username != "" && robot.EncryptedSecret != ""
}

// RobotEqual reports whether two robot credentials are identical by ID,
// username, and encrypted secret.
func RobotEqual(left, right *RobotCredential) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.ID == right.ID &&
		left.Username == right.Username &&
		left.EncryptedSecret == right.EncryptedSecret
}

// TimePtrEqual reports whether two optional time pointers hold equal values.
func TimePtrEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Equal(*right)
}
