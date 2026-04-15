package testutil

import (
	"context"
	"strings"
	"time"

	"github.com/gschiano/charm-registry/internal/core"
)

// OCIRegistry is a deterministic test double for the service OCI registry dependency.
type OCIRegistry struct {
	RegistryHost string
}

func (o OCIRegistry) SyncPackage(_ context.Context, pkg core.Package) (core.Package, error) {
	if pkg.HarborProject == "" {
		pkg.HarborProject = "charm-" + pkg.Name
	}
	if pkg.HarborPushRobot == nil {
		pkg.HarborPushRobot = &core.RobotCredential{ID: 1, Username: "robot$push-" + pkg.ID, EncryptedSecret: "push"}
	}
	if pkg.HarborPullRobot == nil {
		pkg.HarborPullRobot = &core.RobotCredential{ID: 2, Username: "robot$pull-" + pkg.ID, EncryptedSecret: "pull"}
	}
	now := time.Now().UTC()
	pkg.HarborSyncedAt = &now
	return pkg, nil
}

func (o OCIRegistry) ImageReference(pkg core.Package, resourceName string) (string, error) {
	return o.registryHost() + "/" + pkg.HarborProject + "/" + resourceName, nil
}

func (o OCIRegistry) Credentials(pkg core.Package, pull bool) (string, string, error) {
	if pull {
		return pkg.HarborPullRobot.Username, "pull-secret", nil
	}
	return pkg.HarborPushRobot.Username, "push-secret", nil
}

func (o OCIRegistry) MirrorImage(
	_ context.Context,
	_ core.Package,
	_ string,
	sourceImage, _, _ string,
) (string, error) {
	if idx := strings.LastIndex(sourceImage, "@"); idx >= 0 {
		return sourceImage[idx+1:], nil
	}
	return "", nil
}

func (o OCIRegistry) DeleteImage(_ context.Context, _ core.Package, _ string, _ string) error {
	return nil
}

func (o OCIRegistry) DeletePackage(_ context.Context, _ core.Package) error {
	return nil
}

func (o OCIRegistry) registryHost() string {
	if o.RegistryHost != "" {
		return o.RegistryHost
	}
	return "oci.example.test"
}
