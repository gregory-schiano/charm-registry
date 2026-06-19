# Release Process

This document describes how charm-registry releases are built, published, and verified.

## Overview

The release workflow (`.github/workflows/release.yml`) builds all production artifacts,
creates a GitHub release, and publishes to the appropriate package stores. Artifacts:

| Artifact | Format | Published To |
|----------|--------|-------------|
| Go binaries | `charm-registry` + `charm-registryctl` | GitHub Release assets |
| OCI rock | `.rock` (OCI archive) | GHCR or Harbor |
| Charm | `.charm` | Charmhub (charmhub.io) |
| Snap | `.snap` | Snap Store |
| SBOM | SPDX + CycloneDX JSON | GitHub Release assets |

## Triggering a Release

### Option 1: Tag Push (production releases)

Push a semver tag to the `main` branch:

```bash
git tag v1.2.3
git push origin v1.2.3
```

- Stable tags (`v1.2.3`) publish to `latest/stable` channels and create a full release.
- RC tags (`v1.2.3-rc1`) publish to `latest/edge` channels and create a **draft** release.

Tag push always publishes (dry_run=false).

### Option 2: Manual Dispatch (testing / dry runs)

1. Go to **Actions → Release → Run workflow**
2. Set inputs:
   - `dry_run`: `true` (default) to test the build without publishing, `false` to publish
   - `tag`: the release tag (e.g. `v1.2.3-rc1`)
   - `rock_registry`: OCI registry override (default: `ghcr.io`)
3. Click **Run workflow**

Manual dispatch defaults to dry-run mode for safety.

## Dry-Run Mode

When `dry_run=true`:

- ✓ All build jobs run (test, integration-smoke, build-binaries, build-rock, build-charm, build-snap, generate-sbom)
- ✓ GitHub release is created as a **draft** with all artifacts attached
- ✗ Publish jobs are **skipped** (charm, rock, snap)
- ✗ Attestations are **skipped**

A `dry-run-summary` job prints a formatted report of what would have been published.

## Channel / Track Strategy

| Tag Pattern | Charm Channel | Snap Channel | Rock Tag |
|------------|---------------|-------------|----------|
| `v1.2.3` | `latest/stable` | `latest/stable` | `v1.2.3` + `latest` |
| `v1.2.3-rc1` | `latest/edge` | `latest/edge` | `v1.2.3-rc1` only |

The rock image is tagged with the git tag on every release. The `latest` tag is only
updated for stable (non-RC) releases.

## Required GitHub Secrets

| Secret | Purpose | Required | How to Create |
|--------|---------|----------|---------------|
| `CHARMCRAFT_AUTH` | Charmhub upload credentials | Yes (for charm publish) | See [Charmhub Credentials](#charmhub-credentials) |
| `SNAPCRAFT_STORE_CREDENTIALS` | Snap Store upload credentials | Yes (for snap publish) | See [Snap Store Credentials](#snap-store-credentials) |
| `HARBOR_USERNAME` | Harbor OCI registry username | Only if using Harbor | Harbor admin or robot account |
| `HARBOR_PASSWORD` | Harbor OCI registry password | Only if using Harbor | Harbor admin or robot account |

`GITHUB_TOKEN` is automatically available and used for GHCR authentication.

### Charmhub Credentials

Generate a long-lived macaroon for CI:

```bash
# Install charmcraft if not present
snap install charmcraft --classic

# Login interactively (opens browser)
charmcraft login

# Export credentials to a file
charmcraft login --export auth.json

# Copy the contents of auth.json into the CHARMCRAFT_AUTH secret
cat auth.json
```

The exported credentials file contains a base64-encoded macaroon. Copy its entire
contents into the `CHARMCRAFT_AUTH` repository secret.

**Recommended permissions**: package_access for `charm-registry`, plus upload and
release permissions.

### Snap Store Credentials

Generate store credentials for CI:

```bash
# Install snapcraft if not present
snap install snapcraft --classic

# Export login credentials (follow prompts)
snapcraft export-login creds.txt \
  --snaps charm-registry \
  --channels latest/stable,latest/edge \
  --acls package_upload,package_release

# Copy the contents of creds.txt into the SNAPCRAFT_STORE_CREDENTIALS secret
cat creds.txt
```

**Note**: Exported credentials expire. Regenerate before expiry (typically 1 year).

### Harbor OCI Credentials

If pushing the rock to a Harbor registry instead of GHCR:

1. Create a **robot account** in Harbor with `push` permission on the target project
2. Set `HARBOR_USERNAME` to the robot account name (e.g. `robot$ci-push`)
3. Set `HARBOR_PASSWORD` to the robot account secret
4. Trigger the release with `rock_registry` set to your Harbor instance
   (e.g. `harbor.example.com/myproject`)

## Rock Publishing (skopeo)

The rock is published using `skopeo copy` directly from the `.rock` OCI archive.
Publishing uses `skopeo` directly against the OCI registry; no daemon or GitHub container-login action is required.

```
skopeo copy oci-archive:charm-registry_*.rock docker://ghcr.io/<org>/<repo>:<tag>
```

This works because `.rock` files produced by rockcraft are valid OCI layout archives.

## Attestations

For non-dry-run releases, the workflow generates:

- **Build provenance** — SLSA provenance attestation for all binary artifacts
  (uses `actions/attest-build-provenance`)
- **SBOM attestation** — Links the SPDX SBOM to the linux/amd64 binary
  (uses `actions/attest-sbom`)

These attestations are visible on the GitHub release page and can be verified with:

```bash
gh attestation verify oci://ghcr.io/<org>/charm-registry:<tag> --repo <org>/<repo>
```

## Verifying a Release

### Charm

```bash
charmcraft status charm-registry
# Check the target channel shows the expected revision
```

### Rock (OCI Image)

```bash
# Inspect the manifest
skopeo inspect docker://ghcr.io/<org>/charm-registry:<tag>

# Pull and verify with skopeo
skopeo copy docker://ghcr.io/<org>/charm-registry:<tag> oci-archive:local.rock
```

### Snap

```bash
snap info spellbook
# Check channel map shows the expected revision on the target channel
```

### Binary Integrity

```bash
# Download from GitHub release
gh release download <tag> --pattern 'charm-registry-linux-amd64'

# Verify attestation
gh attestation verify charm-registry-linux-amd64 --repo <org>/<repo>
```

## Rollback Procedure

### Charm Rollback

```bash
# List current revisions
charmcraft revisions charm-registry

# Release a previous revision to the target channel
charmcraft release charm-registry --revision=<PREV_REVISION> --channel=latest/stable
```

### Snap Rollback

```bash
# List revisions
snapcraft list-revisions charm-registry

# Close the problematic channel and release old revision
snapcraft close-channel charm-registry/latest/stable
snapcraft release charm-registry <PREV_REVISION> latest/stable
```

### Rock Rollback

```bash
# Re-tag a previous version as latest (if still available in registry)
skopeo copy \
  docker://ghcr.io/<org>/charm-registry:<OLD_TAG> \
  docker://ghcr.io/<org>/charm-registry:latest
```

### GitHub Release

1. Navigate to the release on GitHub
2. Delete the erroneous release
3. Re-run the workflow with the correct tag, or push a corrected tag

## Troubleshooting

### "CHARMCRAFT_AUTH secret is not set"

The publish-charm job requires the `CHARMCRAFT_AUTH` secret. Follow the
[Charmhub Credentials](#charmhub-credentials) section to generate and configure it.

### "SNAPCRAFT_STORE_CREDENTIALS secret is not set"

The publish-snap job requires the `SNAPCRAFT_STORE_CREDENTIALS` secret. Follow the
[Snap Store Credentials](#snap-store-credentials) section to generate and configure it.

### Rock push fails with "unauthorized"

- For GHCR: verify that `GITHUB_TOKEN` has `packages: write` permission
  (set in workflow permissions).
- For Harbor: verify `HARBOR_USERNAME` / `HARBOR_PASSWORD` are correct and
  the robot account has push access on the target project.

### "No .rock file produced" / "No .charm file found"

The build step failed to produce the artifact. Check the build job logs for errors
in `rockcraft pack`, `charmcraft pack`, or `snapcraft pack`.
