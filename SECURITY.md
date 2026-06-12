# Security Policy

## Scope

This is a private charm registry service written in Go. It stores charm metadata in Postgres or SQLite, charm/resource artifacts in S3 or on the filesystem, and runs an embedded OCI Distribution registry for image resources. Security fixes should favor:

- Secure-by-default runtime configuration
- Least-privilege deployment settings
- Short-lived credentials and token revocation
- Dependency and toolchain hygiene

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Report security issues privately to the maintainers with:

- A description of the issue
- Affected endpoints or packages
- Reproduction steps or proof of concept
- Impact assessment
- Any suggested mitigation

If you are deploying this service internally, treat the following as confidential and rotate them immediately after any suspected exposure:

- OCI push/pull credentials
- `CHARM_REGISTRY_OCI_SECRET_KEY` (used to encrypt OCI credentials at rest)
- OIDC client secrets
- Database connection URLs (may contain passwords)
- Object-store access keys and secret keys

## Supported posture

### Application-level

- OIDC-backed authentication with store-token issuance and revocation
- Per-package OCI push/pull credentials encrypted with `CHARM_REGISTRY_OCI_SECRET_KEY`
- Macaroon token support for charmcraft compatibility
- Token TTL enforcement and revocation
- Rate limiting on token issuance (5 per minute per identity by default) and per-IP request limiting, both configurable
- Explicit HTTP server timeouts, header limits, and body-size limits
- Security response headers (`Content-Security-Policy`, `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`)
- Authenticated uploads and protected OCI credential/blob endpoints
- Private-by-default packages with owner-only management and admin override

### Code-level

- `golangci-lint` with a curated rule set inspired by Juju's Go linting configuration
- `govulncheck` for dependency and standard-library vulnerability scanning
- `gosec` for Go-focused static security analysis
- `go.mod` `tool` block pinning all lint and security tooling versions

### Container-level

- Non-root execution in the rock image (`ubuntu:24.04` base with non-root user)
- No shell in the production rock image
- Hardened build: security linters, govulncheck, gosec

## Hardening expectations

Production deployments should additionally provide:

- **TLS for the main API server** — natively via `CHARM_REGISTRY_API_TLS_CERT_FILE` / `CHARM_REGISTRY_API_TLS_KEY_FILE` (the snap's `tls.enabled` option), or terminated at a reverse proxy / ingress
- **OIDC configuration** for end-user authentication (`CHARM_REGISTRY_OIDC_ISSUER_URL`, `CHARM_REGISTRY_OIDC_CLIENT_ID`)
- **Admin identity configuration** via `CHARM_REGISTRY_ADMIN_SUBJECTS`, `CHARM_REGISTRY_ADMIN_EMAILS`, or `CHARM_REGISTRY_ADMIN_USERNAMES`
- **Network-level access control** for private registry traffic (the registry does not implement IP-based ACLs)
- **Secret management** outside the repository (no credentials in `.env` or snap/charm config files)
- **A strong, unique `CHARM_REGISTRY_OCI_SECRET_KEY`** — this key encrypts all OCI push/pull credentials at rest. Changing it invalidates all existing credentials.
- **Least-privilege S3/object-store permissions** for both charm blobs and embedded OCI registry storage
- **Database TLS** (`sslmode` should not be `disable` in production)
- **Regular Go patch upgrades** and routine vulnerability scanning of container images and dependencies

## Snap security model

When deployed as a snap:

- `confinement: strict` — the snap runs in a restricted sandbox with only declared interfaces
- `plugs: network, network-bind` — the snap can open listening sockets and make outbound network connections, but cannot access the filesystem or other snaps beyond `$SNAP_COMMON`
- Data lives under `$SNAP_COMMON/data/` (writable, persistent across upgrades)
- TLS certificates live under `$SNAP_COMMON/certs/` with `0600` permissions on private keys
- The service installs disabled (`install-mode: disable`) so it never starts before it is configured; start it explicitly with `snap start charm-registry`

## Unsafe development mode

`CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH=true` is intended for local development only. When enabled, the registry accepts insecure development bearer tokens and may allow token minting flows that are not suitable for production.

**Never enable this mode on an internet-reachable deployment.**

The application validates at startup that either OIDC is configured or insecure dev auth is explicitly enabled. If neither is set, the service refuses to start.
