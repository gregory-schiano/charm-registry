# CI and Release Workflow Design — charm-registry (use-harbor branch)

## Overview

This document designs the complete CI and release workflow for charm-registry, a Go HTTP service that provides a private Charmhub-compatible registry with an embedded OCI registry. The service is packaged as both a Docker container and a snap, and requires PostgreSQL and S3-compatible object storage at runtime.

The design accounts for:
- Go-native tooling (go vet, golangci-lint, go test, govulncheck, gosec)
- Docker container image build and push
- Snap build and publish to the Snap Store
- Embedded OCI registry with TLS certificates
- Integration tests requiring PostgreSQL + MinIO + the service itself
- Release tagging and provenance artifacts
- Evaluation of canonical/operator-workflows reuse

---

## 1. Operator-Workflows Fit Assessment

### 1.1 Summary

| operator-workflows file | Verdict | Rationale |
|---|---|---|
| `test.yaml` | **NOT USED** | Core lint-and-unit-test job runs `tox` with `lint`/`unit`/`static`/`coverage-report` environments. Fundamentally Python/tox-centric; a Go service cannot use it. |
| `integration_test.yaml` | **NOT USED** | Builds charms via charmcraft, deploys via Juju, runs pytest via tox. Entirely charm+Juju+tox specific. |
| `integration_test_run.yaml` | **NOT USED** | Sub-job of integration_test.yaml; tox+pytest+Juju. |
| `publish_charm.yaml` | **NOT USED** | Charmhub upload via charmcraft; snap publishing uses different tooling. |
| `promote_charm.yaml` | **NOT USED** | Charmhub channel promotion; snap uses `snapcraft promote`. |
| `auto_update_charm_libs.yaml` | **NOT USED** | Charm library concept does not apply to Go service. |
| `terraform_modules_test.yaml` | **NOT USED** | Requires Juju controller bootstrap; not applicable. |
| `docs.yaml` | **MAY USE** | Markdown lint (Vale) + link check (Lychee). Language-agnostic. Worth adopting for docs quality if documentation grows. |
| `bot_pr_approval.yaml` | **MAY USE** | Auto-approve dependabot/renovate PRs. Language-agnostic. Useful but low priority. |
| `comment_contributing.yaml` | **MAY USE** | PR welcome comment. Nice-to-have. |
| `generate_terraform_docs.yaml` | **MAY USE** | Only if Terraform deployment files are added. Not currently applicable. |
| `metadata-lint` (from test.yaml) | **PARTIAL USE** | Validates metadata.yaml against charm schema. charm-registry IS a charm and has metadata.yaml, so this job is relevant. However, it cannot be called independently — it's embedded in test.yaml. Replicate the check locally. |

### 1.2 Core Finding

**canonical/operator-workflows is fundamentally designed for Python charms/operators** built with the Juju operator framework. The core CI pipeline (lint+unit via tox), integration test pipeline (charmcraft build, Juju deploy, pytest), and release pipeline (Charmhub publish/promote) are all deeply Python/tox/charm-specific and cannot be reused by a Go service.

The directly reusable workflows are limited to 3-4 language-agnostic helper workflows (docs lint, bot PR approval, PR commenting). These are worth adopting for convenience but do not replace the need for custom Go-native CI and release workflows.

**Decision: Write custom GitHub Actions workflows. Borrow the multi-job structural pattern from test.yaml (separate lint/security/test/build jobs + required_status_checks gate) but implement all jobs with Go tooling.**

---

## 2. CI Workflow Design — `.github/workflows/ci.yml`

### 2.1 Triggers

```yaml
on:
  push:
    branches: [main, use-harbor]
  pull_request:
    branches: [main, use-harbor]
```

Rationale: Include `use-harbor` branch so production-readiness work gets CI coverage before merge.

### 2.2 Permissions

```yaml
permissions:
  contents: read
```

Minimal read-only access. Release workflow uses separate elevated permissions.

### 2.3 Jobs

#### Job 1: lint

```yaml
lint:
  name: Lint
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: "1.26.2"
    - name: Verify module tidiness
      run: make tidy-check
    - name: Run go vet
      run: make vet
    - name: Run golangci-lint
      run: make lint
    - name: Verify sqlc generated code is up-to-date
      run: |
        if ! make sqlc-diff; then
          echo "::error::sqlc-generated code is out of sync. Run 'sqlc generate' and commit."
          exit 1
        fi
```

Preserves the existing lint job from the current ci.yml. No changes needed.

#### Job 2: security

```yaml
security:
  name: Security
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: "1.26.2"
    - name: Run govulncheck
      run: make vuln
    - name: Run gosec
      run: make gosec
```

Preserves the existing security job. No changes needed.

#### Job 3: test (unit tests + coverage gate)

```yaml
test:
  name: Test
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: "1.26.2"
    - name: Run tests with coverage
      run: make coverage
    - name: Check coverage threshold
      run: |
        COVERAGE=$(go tool cover -func=coverage.out | grep '^total:' | awk '{print $NF}' | tr -d '%')
        echo "Total test coverage: ${COVERAGE}%"
        THRESHOLD=70
        if [ "$(echo "${COVERAGE} < ${THRESHOLD}" | bc -l)" -eq 1 ]; then
          echo "::error::Coverage ${COVERAGE}% is below the ${THRESHOLD}% threshold"
          exit 1
        fi
    - name: Upload coverage artifact
      if: github.event_name == 'pull_request'
      uses: actions/upload-artifact@v4
      with:
        name: coverage
        path: coverage.out
        retention-days: 7
```

Preserves existing test job. Added retention-days to avoid artifact sprawl.

#### Job 4: integration (NEW)

```yaml
integration:
  name: Integration Tests
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: "1.26.2"

    - name: Create shared Docker network
      run: docker network create charm-registry-shared

    - name: Start PostgreSQL
      run: |
        docker compose up -d postgres
      env:
        CHARM_REGISTRY_S3_BUCKET: charm-registry-blobs
        CHARM_REGISTRY_S3_ACCESS_KEY_ID: registry-app
        CHARM_REGISTRY_S3_SECRET_ACCESS_KEY: registry-app-secret
        CHARM_REGISTRY_OCI_S3_BUCKET: charm-registry-oci

    - name: Wait for PostgreSQL
      run: |
        for i in $(seq 1 30); do
          if docker compose exec -T postgres pg_isready -U postgres -d charm_registry; then
            echo "PostgreSQL ready"
            break
          fi
          echo "Waiting for PostgreSQL... ($i/30)"
          sleep 2
        done

    - name: Start MinIO + bucket creation + charm-registry
      run: |
        docker compose up -d minio create-buckets
        sleep 5
        docker compose up -d charm-registry --build
      env:
        CHARM_REGISTRY_S3_BUCKET: charm-registry-blobs
        CHARM_REGISTRY_S3_ACCESS_KEY_ID: registry-app
        CHARM_REGISTRY_S3_SECRET_ACCESS_KEY: registry-app-secret
        CHARM_REGISTRY_OCI_S3_BUCKET: charm-registry-oci
        CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH: "true"

    - name: Wait for charm-registry
      run: |
        for i in $(seq 1 60); do
          if curl -sf http://localhost:8080/v1/health; then
            echo "charm-registry ready"
            break
          fi
          echo "Waiting for charm-registry... ($i/60)"
          sleep 2
        done

    - name: Generate OCI TLS certificates
      run: make generate-cert

    - name: Run integration tests
      run: |
        go test -tags=integration -timeout 5m ./tests/integration/...
      env:
        CHARM_REGISTRY_API_URL: http://localhost:8080
        CHARM_REGISTRY_OCI_URL: https://localhost:5000

    - name: Collect logs on failure
      if: failure()
      run: docker compose logs

    - name: Tear down
      if: always()
      run: docker compose down -v
```

**Rationale for Docker Compose integration rather than operator-workflows:**
- charm-registry's integration tests require PostgreSQL + MinIO + the service itself, not Juju models
- Docker Compose is the existing local-dev and test stack
- The `operator-workflows/integration_test.yaml` builds charms via charmcraft and deploys via Juju — entirely wrong tooling for a Go HTTP service
- Docker Compose integration is the standard Go service pattern

**Notes:**
- `make generate-cert` creates the embedded OCI registry's TLS certificate for the integration test environment
- `CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH=true` allows tests to authenticate without OIDC
- Tests are gated behind the `integration` build tag to separate from unit tests
- The `tests/integration/` directory was created in the parent task (t_263ee4bc)

#### Job 5: build (binary + container image)

```yaml
build:
  name: Build
  runs-on: ubuntu-latest
  needs: [lint, security, test]
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: "1.26.2"

    - name: Build binaries
      run: make build

    - name: Upload binary artifacts
      uses: actions/upload-artifact@v4
      with:
        name: charm-registry-binaries
        path: |
          .bin/charm-registry
          .bin/charm-registryctl
        retention-days: 7

    - name: Build Docker image
      run: docker build -t charm-registry:ci .

    - name: Run Trivy vulnerability scan on container image
      uses: aquasecurity/trivy-action@master
      with:
        image-ref: charm-registry:ci
        format: table
        exit-code: 1
        severity: CRITICAL,HIGH
```

**New additions over the current ci.yml:**
1. Upload binary artifacts for downstream use (release workflow)
2. Build Docker image to verify Dockerfile works on every PR
3. Trivy container image scanning — borrowed conceptually from `operator-workflows/integration_test.yaml`'s scan job, but applied to our Docker image instead of a charm/rock

#### Job 6: snap-build (NEW — optional, non-blocking)

```yaml
snap-build:
  name: Snap Build
  runs-on: ubuntu-latest
  needs: [lint, security, test]
  steps:
    - uses: actions/checkout@v4

    - name: Install snapcraft
      run: sudo snap install snapcraft --classic

    - name: Build snap
      uses: snapcore/action-build@v1
      with:
        snapcraft-args: "--destructive-mode"

    - name: Upload snap artifact
      uses: actions/upload-artifact@v4
      with:
        name: charm-registry-snap
        path: "*.snap"
        retention-days: 7
```

**Rationale:**
- charm-registry is packaged as a snap (snap/snapcraft.yaml exists)
- Building the snap on every PR catches snapcraft.yaml breakage early
- This job is NOT a required status check — snap build failures are advisory during development
- `--destructive-mode` avoids needing a LXD environment in CI
- `snapcore/action-build` is the standard GitHub Action for snap building

---

## 3. Integration Workflow Design — `.github/workflows/integration.yml`

A standalone integration workflow for on-demand or scheduled integration runs (separate from the CI workflow's integration job):

```yaml
name: Integration

on:
  workflow_dispatch:
  schedule:
    - cron: "0 6 * * 1"  # Monday 06:00 UTC

permissions:
  contents: read

env:
  GO_VERSION: "1.26.2"

jobs:
  integration:
    name: Full Integration Suite
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}

      - name: Create shared Docker network
        run: docker network create charm-registry-shared

      - name: Start full stack
        run: |
          make generate-cert
          docker compose up -d --build
        env:
          CHARM_REGISTRY_S3_BUCKET: charm-registry-blobs
          CHARM_REGISTRY_S3_ACCESS_KEY_ID: registry-app
          CHARM_REGISTRY_S3_SECRET_ACCESS_KEY: registry-app-secret
          CHARM_REGISTRY_OCI_S3_BUCKET: charm-registry-oci
          CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH: "true"

      - name: Wait for charm-registry
        run: |
          for i in $(seq 1 60); do
            if curl -sf http://localhost:8080/v1/health; then
              echo "charm-registry ready"
              break
            fi
            echo "Waiting for charm-registry... ($i/60)"
            sleep 2
          done

      - name: Run integration tests
        run: go test -tags=integration -timeout 10m -v ./tests/integration/...
        env:
          CHARM_REGISTRY_API_URL: http://localhost:8080
          CHARM_REGISTRY_OCI_URL: https://localhost:5000

      - name: Collect logs on failure
        if: failure()
        run: docker compose logs

      - name: Tear down
        if: always()
        run: docker compose down -v
```

**Rationale:** Separate from CI because:
- Full stack integration is slower (5-10 min) and shouldn't block every PR
- `workflow_dispatch` allows manual trigger for debugging
- Weekly schedule catches regressions even if not triggered by PRs

---

## 4. Release Workflow Design — `.github/workflows/release.yml`

### 4.1 Triggers

```yaml
on:
  push:
    tags:
      - "v*"
  workflow_dispatch:
    inputs:
      version:
        description: "Release version (e.g., v0.2.0)"
        required: true
        type: string
```

Tag-based release is the standard Go convention. `workflow_dispatch` with explicit version input allows manual releases.

### 4.2 Permissions

```yaml
permissions:
  contents: write     # Create release, tag
  packages: write      # Push container image to GHCR
```

Elevated permissions for release artifacts. This workflow only runs on tags or manual dispatch, not on PRs, so the elevated permissions are scoped.

### 4.3 Jobs

#### Job 1: build-and-test

```yaml
build-and-test:
  name: Build & Test
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
      with:
        fetch-depth: 0  # Full history for buildvcs
    - uses: actions/setup-go@v5
      with:
        go-version: "1.26.2"

    - name: Run audit
      run: make audit

    - name: Build binaries
      run: |
        make build
        mkdir -p dist
        cp .bin/charm-registry dist/charm-registry-linux-amd64
        cp .bin/charm-registryctl dist/charm-registryctl-linux-amd64

    - name: Build binaries (arm64)
      run: |
        GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
          go build -trimpath -buildvcs=true -o dist/charm-registry-linux-arm64 ./cmd/charm-registry
        GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
          go build -trimpath -buildvcs=true -o dist/charm-registryctl-linux-arm64 ./cmd/charm-registryctl

    - name: Generate SBOM
      uses: anchore/sbom-action@v0
      with:
        image: charm-registry:ci
        format: spdx-json
        output-file: dist/charm-registry-sbom.spdx.json

    - name: Upload build artifacts
      uses: actions/upload-artifact@v4
      with:
        name: release-artifacts
        path: dist/
        retention-days: 5
```

#### Job 2: container-image

```yaml
container-image:
  name: Container Image
  runs-on: ubuntu-latest
  needs: [build-and-test]
  steps:
    - uses: actions/checkout@v4

    - name: Set up Docker Buildx
      uses: docker/setup-buildx-action@v3

    - name: Login to GitHub Container Registry
      uses: docker/login-action@v3
      with:
        registry: ghcr.io
        username: ${{ github.actor }}
        password: ${{ secrets.GITHUB_TOKEN }}

    - name: Extract version
      id: meta
      uses: docker/metadata-action@v5
      with:
        images: ghcr.io/${{ github.repository }}
        tags: |
          type=semver,pattern={{version}}
          type=semver,pattern={{major}}.{{minor}}
          type=sha

    - name: Build and push
      uses: docker/build-push-action@v6
      with:
        context: .
        push: true
        tags: ${{ steps.meta.outputs.tags }}
        labels: ${{ steps.meta.outputs.labels }}
        provenance: true    # SLSA provenance
        sbom: true          # SBOM attestation
        cache-from: type=gha
        cache-to: type=gha,mode=max
```

**Key decisions:**
- **GHCR** (GitHub Container Registry) rather than Docker Hub — stays within the GitHub ecosystem, no extra credentials
- **SLSA provenance** — `provenance: true` in build-push-action generates SLSA v1.0 provenance attestation
- **SBOM attestation** — `sbom: true` attaches the SBOM to the image manifest
- **Buildx cache** — GHA cache backend for faster subsequent builds
- Multi-arch (amd64 + arm64) builds via Buildx QEMU emulation

#### Job 3: snap-publish

```yaml
snap-publish:
  name: Snap Publish
  runs-on: ubuntu-latest
  needs: [build-and-test]
  steps:
    - uses: actions/checkout@v4

    - name: Install snapcraft
      run: sudo snap install snapcraft --classic

    - name: Build snap
      uses: snapcore/action-build@v1
      with:
        snapcraft-args: "--destructive-mode"

    - name: Publish snap to Snap Store (stable)
      if: startsWith(github.ref, 'refs/tags/v') && !contains(github.ref, 'rc') && !contains(github.ref, 'beta') && !contains(github.ref, 'alpha')
      uses: snapcore/action-publish@v1
      with:
        store-login: ${{ secrets.SNAP_STORE_LOGIN }}
        snap: "*.snap"
        release: stable

    - name: Publish snap to Snap Store (candidate)
      if: startsWith(github.ref, 'refs/tags/v') && contains(github.ref, 'rc')
      uses: snapcore/action-publish@v1
      with:
        store-login: ${{ secrets.SNAP_STORE_LOGIN }}
        snap: "*.snap"
        release: candidate

    - name: Publish snap to Snap Store (beta)
      if: startsWith(github.ref, 'refs/tags/v') && contains(github.ref, 'beta')
      uses: snapcore/action-publish@v1
      with:
        store-login: ${{ secrets.SNAP_STORE_LOGIN }}
        snap: "*.snap"
        release: beta

    - name: Publish snap to Snap Store (edge)
      if: github.event_name == 'workflow_dispatch'
      uses: snapcore/action-publish@v1
      with:
        store-login: ${{ secrets.SNAP_STORE_LOGIN }}
        snap: "*.snap"
        release: edge
```

**Key decisions:**
- Snap Store channel selection based on tag name convention:
  - `v1.0.0` → stable
  - `v1.0.0-rc1` → candidate
  - `v1.0.0-beta1` → beta
  - workflow_dispatch → edge
- Requires `SNAP_STORE_LOGIN` secret (from `snapcraft login --export`)
- `--destructive-mode` for CI environments without LXD
- This replaces `operator-workflows/publish_charm.yaml` (Charmhub upload) with snap-specific publishing

#### Job 4: github-release

```yaml
github-release:
  name: GitHub Release
  runs-on: ubuntu-latest
  needs: [build-and-test, container-image]
  permissions:
    contents: write
  steps:
    - uses: actions/checkout@v4
      with:
        fetch-depth: 0

    - name: Download build artifacts
      uses: actions/download-artifact@v4
      with:
        name: release-artifacts
        path: dist/

    - name: Generate release notes
      id: notes
      run: |
        PREV_TAG=$(git describe --tags --abbrev=0 HEAD^ 2>/dev/null || echo "")
        if [ -n "$PREV_TAG" ]; then
          NOTES=$(git log "${PREV_TAG}..HEAD" --pretty=format:"- %s (%h)" --no-merges)
        else
          NOTES=$(git log --pretty=format:"- %s (%h)" --no-merges -20)
        fi
        # Write to file for multiline output
        echo "$NOTES" > /tmp/release-notes.txt
        echo "notes_file=/tmp/release-notes.txt" >> "$GITHUB_OUTPUT"

    - name: Create GitHub Release
      uses: softprops/action-gh-release@v2
      with:
        tag_name: ${{ github.event.inputs.version || github.ref_name }}
        body_path: ${{ steps.notes.outputs.notes_file }}
        files: |
          dist/charm-registry-linux-amd64
          dist/charm-registry-linux-arm64
          dist/charm-registryctl-linux-amd64
          dist/charm-registryctl-linux-arm64
          dist/charm-registry-sbom.spdx.json
        draft: false
        prerelease: ${{ contains(github.ref, 'rc') || contains(github.ref, 'beta') || contains(github.ref, 'alpha') }}
```

---

## 5. Optional: Docs Workflow — `.github/workflows/docs.yml`

```yaml
name: Docs

on:
  pull_request:
    paths:
      - "**/*.md"
      - "docs/**"

permissions:
  contents: read

jobs:
  docs:
    name: Lint & Link Check
    uses: canonical/operator-workflows/.github/workflows/docs.yaml@main
```

This is the ONE operator-workflows workflow that can be directly reused. It runs Vale (markdown style) and Lychee (link checking) — both language-agnostic. Only triggers when markdown files change, so it doesn't add latency to code-only PRs.

---

## 6. Required Secrets

| Secret | Used by | How to obtain |
|---|---|---|
| `SNAP_STORE_LOGIN` | `release.yml` (snap-publish job) | `snapcraft login --export charm-registry` on a trusted machine |
| `GITHUB_TOKEN` | `release.yml` (container-image job) | Automatic — GitHub provides this |
| No other secrets needed | CI runs with `contents: read` only | — |

---

## 7. Branch Protection Recommendations

For `main` and `use-harbor` branches, configure these required status checks:

- `Lint` (from ci.yml)
- `Security` (from ci.yml)
- `Test` (from ci.yml)
- `Build` (from ci.yml)

The `Integration Tests` and `Snap Build` jobs are intentionally NOT required status checks — they are slower and advisory. The `Integration` workflow (weekly/ondemand) is a separate safety net.

---

## 8. Workflow File Summary

| File | Purpose | Trigger |
|---|---|---|
| `.github/workflows/ci.yml` | PR/push gate: lint, security, unit test, coverage, build, integration, snap build, trivy scan | push/PR to main, use-harbor |
| `.github/workflows/integration.yml` | Full-stack integration on schedule or demand | schedule (weekly), workflow_dispatch |
| `.github/workflows/release.yml` | Binary + container + snap + GitHub release on tag | tag push `v*`, workflow_dispatch |
| `.github/workflows/docs.yml` | Markdown lint + link check | PR touching .md files |

---

## 9. What This Replaces from operator-workflows

| operator-workflows pattern | charm-registry replacement |
|---|---|
| `test.yaml` (tox lint+unit) | Custom ci.yml with Go-native: `make tidy-check`, `make vet`, `make lint`, `make coverage` |
| `test.yaml` (metadata-lint) | Not yet included; add `charmcraft metadata-lint` step if charm metadata validation is needed |
| `test.yaml` (shellcheck-lint) | Not yet included; add if shell scripts in snap/local/ grow |
| `test.yaml` (docker-lint) | Not yet included; add `hadolint` step if Dockerfile gets complex |
| `test.yaml` (required_status_checks) | Replicated via branch protection settings |
| `integration_test.yaml` (charm+Juju) | Custom ci.yml integration job using Docker Compose |
| `integration_test.yaml` (Trivy scan) | `aquasecurity/trivy-action` on Docker image in build job |
| `publish_charm.yaml` | `snapcore/action-publish` for Snap Store |
| `promote_charm.yaml` | Tag-convention-based channel selection in release.yml |
| `docs.yaml` | **Directly reused** via `uses: canonical/operator-workflows/.github/workflows/docs.yaml@main` |

---

## 10. Release Process

1. Ensure all CI checks pass on the release branch
2. Tag the commit: `git tag v0.2.0 -m "Release v0.2.0"`
3. Push the tag: `git push origin v0.2.0`
4. Release workflow triggers automatically:
   - Runs full audit (lint + security + test + build)
   - Builds binaries for linux/amd64 + linux/arm64
   - Generates SBOM
   - Pushes container image to GHCR with SLSA provenance
   - Builds and publishes snap to the Snap Store (channel based on tag)
   - Creates GitHub Release with binaries, SBOM, and auto-generated release notes
5. Verify the release on GitHub Releases page and GHCR
6. For manual/edge releases: use `workflow_dispatch` with a version string

### Tag naming convention for channels:

| Tag pattern | Snap channel | GitHub Release |
|---|---|---|
| `vX.Y.Z` | stable | release (not pre-release) |
| `vX.Y.Z-rcN` | candidate | pre-release |
| `vX.Y.Z-betaN` | beta | pre-release |
| `vX.Y.Z-alphaN` | edge (manual only) | pre-release |
