# Snap Spread Test Suite

This directory contains [spread](https://spread.run/) tests for the
charm-registry snap. These tests install the snap locally, configure it,
and exercise the shared functional scenarios from `tests/functional/`.

## Prerequisites

- **spread**: `snap install spread --classic`
- **LXD**: for the spread backend (default) or **QEMU** for nested VMs
- **snapcraft**: to pack the snap (or a pre-built `.snap` file)
- **Go 1.26+**: to build the functional-test binary

## Running

```bash
# From the repo root:
make snap-integration-test

# Or directly with spread:
spread -v ./tests/spread/...
```

## Architecture

1. **spread.yaml** — Project-level configuration: backends (LXD/QEMU),
   systems (ubuntu-24.04-amd64), env vars matching the snap's defaults.

2. **tests/spread/charm-registry/task.yaml** — The test task:
   - **prepare**: Packs the snap, installs it with `--dangerous`, sets
     `insecure-dev-auth=true` and public URLs, starts the service, waits
     for `/healthz`.
   - **execute**: Runs the compiled `functional-test` binary against the
     running snap endpoints.
   - **restore**: Removes the snap with `--purge`.

3. **Suite prepare** (in spread.yaml): Installs Go, builds the
   `functional-test` binary from `cmd/functional-test/`.

## Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `FTEST_API_URL` | `http://localhost:8080` | Charm registry API |
| `FTEST_OCI_URL` | `https://127.0.0.1:5000` | OCI registry (TLS) |
| `FTEST_OCI_CERT_PATH` | `/var/snap/spellbook/common/certs/oci.crt` | OCI CA cert |
| `FTEST_ADMIN_SUBJECT` | `admin` | Dev-auth subject |
| `FTEST_ADMIN_USER` | `admin` | Dev-auth username |

## Native snap test flow

The snap is installed directly on the spread VM with `snap install --dangerous`,
and the service runs as a systemd unit managed by snapd.
