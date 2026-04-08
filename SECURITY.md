# Security Policy

## Scope

This repository is a private local charm registry service written in Go. Security fixes should favor:

- secure-by-default runtime configuration
- least-privilege deployment settings
- short-lived credentials and token revocation
- dependency and toolchain hygiene

## Reporting a vulnerability

Please do not open a public issue for a suspected vulnerability.

Report security issues privately to the maintainers with:

- a description of the issue
- affected endpoints or packages
- reproduction steps or proof of concept
- impact assessment
- any suggested mitigation

If you are deploying this service internally, treat Harbor admin credentials, Harbor robot secrets, OIDC secrets, database URLs, and object-store credentials as confidential and rotate them immediately after any suspected exposure.

## Supported posture

The repository currently includes:

- `golangci-lint` with a curated rule set inspired by Juju's Go linting configuration
- `govulncheck` for dependency and standard-library vulnerability scanning
- `gosec` for Go-focused static security analysis
- explicit HTTP server timeouts and header/body limits
- non-root container execution and a hardened compose profile for the application container
- authenticated uploads and protected OCI credential/blob endpoints
- Harbor-backed OCI access with per-package robot credentials

## Hardening expectations

Production deployments should additionally provide:

- TLS termination
- OIDC configuration for end-user authentication
- configured admin identities via `CHARM_REGISTRY_ADMIN_SUBJECTS`, `CHARM_REGISTRY_ADMIN_EMAILS`, or `CHARM_REGISTRY_ADMIN_USERNAMES`
- network-level access control for private registry traffic
- secret management outside the repository
- a non-empty `CHARM_REGISTRY_HARBOR_SECRET_KEY`
- a dedicated Harbor admin account for control-plane API access
- regular Go patch upgrades
- routine vulnerability scanning of container images and dependencies

## Unsafe development mode

`CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH=true` is intended for local development only. When enabled, the registry accepts insecure development bearer tokens and may allow token minting flows that are not suitable for production. Never enable this mode on an internet-reachable deployment.
