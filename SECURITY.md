# Security Policy

## Scope

This repository is a private Charmhub-compatible registry service written in Go. Security fixes should favor:

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

If you are deploying this service internally, treat registry credentials, OIDC secrets, database URLs, and object-store credentials as confidential and rotate them immediately after any suspected exposure.

## Supported posture

The repository currently includes:

- `golangci-lint` with a curated rule set inspired by Juju's Go linting configuration
- `govulncheck` for dependency and standard-library vulnerability scanning
- `gosec` for Go-focused static security analysis
- explicit HTTP server timeouts and header/body limits
- non-root container execution and a hardened compose profile for the application container

## Hardening expectations

Production deployments should additionally provide:

- TLS termination
- network-level access control for private registry traffic
- secret management outside the repository
- regular Go patch upgrades
- routine vulnerability scanning of container images and dependencies
