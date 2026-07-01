# Charm Integration Tests

Jubilant-driven integration tests for charm-registry. These tests deploy the
built charm with Juju, attach the built `app-image` resource, relate the
mandatory ingress endpoints, and exercise the shared functional test harness.

## Prerequisites

- `juju` CLI 3.x+ installed on PATH
- a built charm and `app-image` resource from `charm-ci`, or `JUB_APP_IMAGE`
  plus `charmcraft` for local fallback packing
- A bootstrapped Juju controller (k8s or LXD)
- Python packages from `requirements.txt` plus `opcli`

## Running

```sh
# Via tox (recommended, from repo root):
uv tool run tox -c charm/tox.ini -e integration

# Directly:
JUB_APP_IMAGE=<image-ref> python3 -m pytest -v -s --tb native tests/integration/charm
```

## Environment Variables

| Variable | Description | Default |
|---|---|---|
| `JUB_MODEL` | Reuse an existing Juju model | _(creates temp model)_ |
| `JUB_APP_IMAGE` | Local fallback image resource when not using opcli fixtures | _(opcli fixture)_ |
| `JUB_INGRESS_CHARM` | Ingress charm name | `traefik-k8s` |
| `JUB_INGRESS_CHANNEL` | Ingress charm channel | `latest/stable` |
| `JUB_API_URL` | Override discovered API URL | `http://<unit-address>:8080` |
| `JUB_OCI_URL` | Override OCI URL used by functional tests | `http://<unit-address>:5000` |

## Test Structure

| Test Class | What it verifies |
|---|---|
| `TestCharmDeployment` | Charm deploys with its OCI resource, reaches active status, and health/ready/root endpoints respond |
| `TestFunctionalScenarios` | All shared Go functional scenarios pass against the deployed charm |

## Architecture

```
┌──────────────────────────────────────────────────────┐
│                      Juju Model                      │
│                                                      │
│  ┌──────────────┐   ingress    ┌──────────────────┐  │
│  │charm-registry│◄─────────────│ ingress-api      │  │
│  │              │              │ (traefik-k8s)    │  │
│  │              │ oci-ingress  ├──────────────────┤  │
│  │              │◄─────────────│ ingress-oci      │  │
│  └──────────────┘              │ (traefik-k8s)    │  │
│                                └──────────────────┘  │
└──────────────────────────────────────────────────────┘
          ▲
          │ HTTP
          ▼
   functional-test binary
   + direct health/readiness assertions
```

Both ingress relations are mandatory: the charm derives its public API,
storage, and OCI registry URLs from them and stays blocked until both are
related. Two Traefik applications are deployed because each relation publishes
a route keyed on the same model/app name.

## Design Decisions

- **Juju-native deployment**: The Go functional-test
  binary runs as a separate process, not inside any container.
- **Session-scoped deployment**: The charm is deployed once per pytest session.
  Tests share the same running model to avoid repeated deploy overhead.
- **Artifact-backed deploys**: In CI, `opcli` provides the charm file and
  `app-image` resource built by `canonical/charm-ci`.
- **Jubilant over pytest-operator**: Jubilant wraps the Juju CLI with a clean
  Python API, avoids websocket issues, and does not require async.
