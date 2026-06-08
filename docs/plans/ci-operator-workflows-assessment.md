# CI Operator-Workflows Reuse Assessment (T07)

## Summary

**Decision:** Write custom GitHub Actions workflows. The `canonical/operator-workflows`
reusable workflows are designed for **Python charms/operators** built with the Juju
operator framework and cannot be reused for charm-registry, a Go HTTP service.

## Fit Analysis

| `canonical/operator-workflows` Workflow | Verdict | Rationale |
|---|---|---|
| `test.yaml` | NOT USED | Runs `tox` with lint/unit/static/coverage-report environments. Python/tox-centric; Go service cannot use it. |
| `integration_test.yaml` | NOT USED | Builds charms via charmcraft, deploys via Juju, runs pytest via tox. Entirely charm+Juju+tox specific for Python operators. |
| `integration_test_run.yaml` | NOT USED | Sub-job of integration_test.yaml; tox+pytest+Juju. |
| `publish_charm.yaml` | NOT USED | Charmhub upload via charmcraft; different tooling needed for rock/snap publishing. |
| `promote_charm.yaml` | NOT USED | Charmhub channel promotion; snap/rock use different promotion mechanisms. |
| `auto_update_charm_libs.yaml` | NOT USED | Charm library concept does not apply to a Go service charm. |
| `terraform_modules_test.yaml` | NOT USED | Requires Juju controller bootstrap; not applicable. |
| `docs.yaml` | MAY USE | Markdown lint (Vale) + link check (Lychee). Language-agnostic. Adopt if documentation grows. |
| `bot_pr_approval.yaml` | MAY USE | Auto-approve dependabot/renovate PRs. Nice-to-have, low priority. |
| `comment_contributing.yaml` | MAY USE | PR welcome comment. Nice-to-have. |

## Why Custom Jobs

### Charm Integration Tests
The charm integration tests (Jubilant) deploy **multiple related charms**
(postgresql-k8s, traefik-k8s, optionally s3-integrator) as prerequisites,
then run functional scenarios via a **compiled Go binary** — not through
tox/pytest unit patterns. The `operator-workflows/integration_test.yaml`
pattern (charmcraft → Juju deploy → tox) does not support this.

### Snap Spread Tests
Spread-based snap testing is a distinct Canonical tool (not part of
operator-workflows). The spread tests install the snap in LXD backends,
configure services, and run functional scenarios through the snap-exposed
endpoint.

### Rock Build
Rockcraft-based OCI image building uses `rockcraft pack` — a completely
different build path from Dockerfile-based images. operator-workflows
has no rock-building workflow.

### Go CI
Go source quality (tidy-check, vet, golangci-lint, govulncheck, gosec,
coverage threshold) uses Go-native tooling that cannot be expressed in
the Python/tox patterns of `test.yaml`.

## Pattern Alignment

Despite not reusing the reusable workflows directly, the CI structure
follows operator-workflows conventions:

- **Multi-job fan-in pattern**: Separate lint/security/test/build jobs
  with a `required-status-checks` gate job (mirrors `test.yaml` structure).
- **Artifact upload pattern**: Build jobs upload artifacts for downstream
  consumption (mirrors `publish_charm.yaml` pattern).
- **Minimal permissions**: `contents: read` on CI; elevated permissions
  only on release workflows.
