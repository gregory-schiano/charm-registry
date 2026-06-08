# Charm Integration Tests (Jubilant)

Jubilant-driven integration tests for charm-registry. These tests deploy the
charm with Juju, exercise the shared functional test harness, and verify
restart/persistence behaviour through unit reschedule.

## Prerequisites

- `juju` CLI 3.x+ installed on PATH
- `charmcraft` installed on PATH
- A bootstrapped Juju controller (k8s or LXD)
- Python packages: `pip install jubilant pytest requests`

## Running

```sh
# Via Makefile (recommended):
make charm-integration-test

# Directly:
cd tests/integration/charm
python3 -m pytest -v -s --tb native

# Via tox (from charm/):
cd charm && tox run -e integration
```

## Environment Variables

| Variable | Description | Default |
|---|---|---|
| `JUB_MODEL` | Reuse an existing Juju model | _(creates temp model)_ |
| `JUB_POSTGRES_CHARM` | PostgreSQL charm name | `postgresql-k8s` |
| `JUB_POSTGRES_CHANNEL` | PostgreSQL charm channel | `14/stable` |
| `JUB_TRAEFIK_CHARM` | Ingress charm name | `traefik-k8s` |
| `JUB_TRAEFIK_CHANNEL` | Ingress charm channel | `latest/stable` |
| `JUB_S3_INTEGRATOR` | S3 charm name | `s3-integrator` |
| `JUB_S3_CHANNEL` | S3 charm channel | `latest/stable` |
| `JUB_S3_ENDPOINT` | S3 endpoint URL | _(S3 tests skipped)_ |
| `JUB_S3_BUCKET` | S3 bucket name | `charm-registry-test` |
| `JUB_S3_REGION` | S3 region | `us-east-1` |
| `JUB_S3_ACCESS_KEY` | S3 access key | _(required for S3)_ |
| `JUB_S3_SECRET_KEY` | S3 secret key | _(required for S3)_ |
| `JUB_API_URL` | Override discovered API URL | `http://<unit-address>:8080` |
| `JUB_OCI_URL` | Override discovered OCI URL | `http://<traefik-address>:80` |

## Test Structure

| Test Class | What it verifies |
|---|---|
| `TestCharmDeployment` | Charm deploys, reaches active/idle, health/ready/root endpoints respond |
| `TestFunctionalScenarios` | All 16 shared Go functional scenarios pass against the deployed charm |
| `TestRestartPersistence` | Health recovers after unit reschedule; registered packages persist across restarts |

## Architecture

```
┌────────────────────────────────────────────────┐
│                   Juju Model                   │
│                                                │
│  ┌─────────────┐  relate  ┌───────────────┐   │
│  │charm-registry│◄────────│postgresql-k8s  │   │
│  │ (Go service) │  relate  └───────────────┘   │
│  │              │◄────────┌───────────────┐    │
│  │              │  relate  │s3-integrator  │    │
│  │              │◄────────┌───────────────┐    │
│  │              │         │traefik-k8s    │    │
│  └──────────────┘         └───────────────┘    │
└────────────────────────────────────────────────┘
          ▲
          │ HTTP
          ▼
   functional-test binary (16 scenarios)
   + direct health/persistence assertions
```

## Design Decisions

- **No Docker/Compose**: All deployment is Juju-native. The Go functional-test
  binary runs as a separate process, not inside any container.
- **Session-scoped deployment**: The charm is deployed once per pytest session.
  Tests share the same running model to avoid repeated pack/deploy overhead.
- **Graceful degradation**: Missing S3 credentials skip S3 scenarios; unreachable
  endpoints produce `pytest.skip` rather than hard failures for setup issues.
- **Jubilant over pytest-operator**: Jubilant wraps the Juju CLI with a clean
  Python API, avoids websocket issues, and does not require async.
