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
	if pkg.OCIProject == "" {
		pkg.OCIProject = "charm-" + pkg.Name
	}
	if pkg.OCIPushRobot == nil {
		pkg.OCIPushRobot = &core.RobotCredential{ID: 1, Username: "robot$push-" + pkg.ID, EncryptedSecret: "push"}
	}
	if pkg.OCIPullRobot == nil {
		pkg.OCIPullRobot = &core.RobotCredential{ID: 2, Username: "robot$pull-" + pkg.ID, EncryptedSecret: "pull"}
	}
	now := time.Now().UTC()
	pkg.OCISyncedAt = &now
	return pkg, nil
}

func (o OCIRegistry) ImageReference(pkg core.Package, resourceName string) (string, error) {
	return o.registryHost() + "/" + pkg.OCIProject + "/" + resourceName, nil
}

func (o OCIRegistry) Credentials(pkg core.Package, pull bool) (string, string, error) {
	if pull {
		return pkg.OCIPullRobot.Username, "pull-secret", nil
	}
	return pkg.OCIPushRobot.Username, "push-secret", nil
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
