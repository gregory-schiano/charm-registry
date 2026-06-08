# Release Process

The release workflow (`.github/workflows/release.yml`) builds all production artifacts, creates a GitHub release, and publishes to the appropriate package stores.

## Artifacts

| Artifact | Format | Published To |
|----------|--------|-------------|
| Go binaries | `charm-registry` + `charm-registryctl` | GitHub Release assets |
| OCI rock | `.rock` (OCI archive) | GHCR or Harbor |
| Charm | `.charm` | Charmhub |
| Snap | `.snap` | Snap Store |
| SBOM | SPDX + CycloneDX JSON | GitHub Release assets |

## Triggering a Release

### Tag push (production releases)

Push a semver tag:

```bash
git tag v1.2.3
git push origin v1.2.3
```

- Stable tags (`v1.2.3`) publish to `latest/stable` channels and create a full release.
- RC tags (`v1.2.3-rc1`) publish to `latest/edge` channels and create a **draft** release.

Tag push always publishes (dry_run=false).

### Manual dispatch (testing / dry runs)

1. Go to **Actions → Release → Run workflow**
2. Set inputs:
   - `dry_run`: `true` (default) to test without publishing, `false` to publish
   - `tag`: the release tag (e.g. `v1.2.3-rc1`)
   - `rock_registry`: OCI registry override (default: `ghcr.io`)

Manual dispatch defaults to dry-run mode for safety.

## Dry-Run Mode

When `dry_run=true`:

- All build jobs run (test, binaries, rock, charm, snap, SBOM)
- GitHub release is created as a **draft** with all artifacts attached
- Publish jobs are **skipped** (charm, rock, snap)
- Attestations are **skipped**

A summary job prints a report of what would have been published.

## Channel Strategy

| Tag Pattern | Charm Channel | Snap Channel | Rock Tag |
|------------|---------------|-------------|----------|
| `v1.2.3` | `latest/stable` | `latest/stable` | `v1.2.3` + `latest` |
| `v1.2.3-rc1` | `latest/edge` | `latest/edge` | `v1.2.3-rc1` only |

The rock gets the `latest` tag only on stable (non-RC) releases.

## Required GitHub Secrets

| Secret | Purpose | Required |
|--------|---------|----------|
| `CHARMCRAFT_AUTH` | Charmhub upload credentials | For charm publish |
| `SNAPCRAFT_STORE_CREDENTIALS` | Snap Store upload credentials | For snap publish |
| `HARBOR_USERNAME` | Harbor OCI registry username | Only if using Harbor |
| `HARBOR_PASSWORD` | Harbor OCI registry password | Only if using Harbor |

`GITHUB_TOKEN` is automatically available for GHCR authentication.

### Charmhub credentials

```bash
charmcraft login --export auth.json
# Copy auth.json contents into CHARMCRAFT_AUTH secret
```

### Snap Store credentials

```bash
snapcraft export-login creds.txt \
  --snaps charm-registry \
  --channels latest/stable,latest/edge \
  --acls package_upload,package_release
# Copy creds.txt contents into SNAPCRAFT_STORE_CREDENTIALS secret
```

Credentials expire — regenerate before expiry (typically 1 year).

### Harbor OCI credentials

If using Harbor instead of GHCR: create a robot account with `push` permission, set `HARBOR_USERNAME` and `HARBOR_PASSWORD`, and trigger the release with `rock_registry` pointing to your Harbor instance.

## Rock publishing

The rock is published using `skopeo copy` from the `.rock` OCI archive — no Docker daemon involved:

```bash
skopeo copy oci-archive:charm-registry_*.rock docker://ghcr.io/<org>/<repo>:<tag>
```

The `docker://` in the skopeo command refers to the OCI registry transport protocol, not the Docker daemon.

## Attestations

Non-dry-run releases generate:

- **Build provenance** — SLSA provenance attestation (`actions/attest-build-provenance`)
- **SBOM attestation** — Links the SPDX SBOM to the linux/amd64 binary (`actions/attest-sbom`)

Verify with:

```bash
gh attestation verify oci://ghcr.io/<org>/charm-registry:<tag> --repo <org>/<repo>
```

## Verifying a Release

### Charm

```bash
charmcraft status charm-registry
```

### Rock

```bash
skopeo inspect docker://ghcr.io/<org>/charm-registry:<tag>
```

### Snap

```bash
snap info charm-registry
```

### Binary integrity

```bash
gh release download <tag> --pattern 'charm-registry-linux-amd64'
gh attestation verify charm-registry-linux-amd64 --repo <org>/<repo>
```

## Rollback

### Charm

```bash
charmcraft revisions charm-registry
charmcraft release charm-registry --revision=<N> --channel=latest/stable
```

### Rock

Re-tag the previous good image:

```bash
skopeo copy docker://ghcr.io/<org>/charm-registry:<old-tag> \
            docker://ghcr.io/<org>/charm-registry:latest
```

### Snap

```bash
snap info charm-registry                    # find the previous good revision
snapcraft release charm-registry <N> latest/stable
```
