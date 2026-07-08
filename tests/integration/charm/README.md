# Charm Integration Tests

Jubilant-driven integration tests for charm-registry. These tests deploy the
built charm with Juju, attach the built `app-image` resource, relate the
mandatory ingress endpoints through Gateway API, PostgreSQL, S3-compatible
artifact storage, and self-signed certificates, then exercise the shared
functional test harness.

## Prerequisites

- `juju` CLI 3.x+ installed on PATH
- a built charm and `app-image` resource from `charm-ci`, or `JUB_APP_IMAGE`
  plus `charmcraft` for local fallback packing
- A bootstrapped Juju controller (k8s or LXD)
- A reachable S3-compatible endpoint supplied via `JUB_S3_ENDPOINT`,
  `JUB_S3_ACCESS_KEY`, and `JUB_S3_SECRET_KEY`. The tests never provision host
  services themselves; in CI the spread suite prepare hook
  (`tests/integration/scripts/setup-microceph-rgw.sh`) provisions a
  single-node MicroCeph RGW and exports these variables
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
| `JUB_INGRESS_CHARM` | Ingress configurator charm name | `ingress-configurator` |
| `JUB_INGRESS_CHANNEL` | Ingress configurator charm channel | `latest/edge` |
| `JUB_GATEWAY_CHARM` | Gateway charm name | `gateway-api-integrator` |
| `JUB_GATEWAY_CHANNEL` | Gateway charm channel | `1/edge` |
| `JUB_GATEWAY_CLASS` | Kubernetes GatewayClass used by the gateway charm | `ck-gateway` |
| `JUB_API_HOSTNAME` | Hostname configured for the API ingress route | `api.charm-registry.test` |
| `JUB_OCI_HOSTNAME` | Hostname configured for the OCI ingress route | `oci.charm-registry.test` |
| `JUB_POSTGRESQL_CHARM` | PostgreSQL charm name | `postgresql-k8s` |
| `JUB_POSTGRESQL_CHANNEL` | PostgreSQL charm channel | `14/stable` |
| `JUB_S3_INTEGRATOR_CHARM` | S3 provider charm name | `s3-integrator` |
| `JUB_S3_INTEGRATOR_CHANNEL` | S3 provider charm channel; must stay on track 2 | `2/stable` |
| `JUB_S3_ENDPOINT` | S3 endpoint used by the charm workload (required) | _(none)_ |
| `JUB_S3_BUCKET` | Bucket used for charm and resource artifacts | `charm-registry-artifacts` |
| `JUB_S3_REGION` | S3 region | `us-east-1` |
| `JUB_S3_ACCESS_KEY` | S3 access key (required) | _(none)_ |
| `JUB_S3_SECRET_KEY` | S3 secret key (required) | _(none)_ |
| `JUB_S3_PATH` | Optional S3 key prefix provided by s3-integrator | _(empty)_ |
| `JUB_S3_URI_STYLE` | S3 URI style configured on s3-integrator | `path` |
| `JUB_S3_MICROCEPH` | Set to `true` when the endpoint is the spread-provisioned MicroCeph, enabling the RGW object-count assertion | `false` |
| `JUB_CERTIFICATES_CHARM` | TLS provider charm name | `self-signed-certificates` |
| `JUB_CERTIFICATES_CHANNEL` | TLS provider charm channel | `1/stable` |
| `JUB_API_URL` | Override discovered API URL | `http://<unit-address>:8080` |
| `JUB_OCI_URL` | Override OCI URL used by functional tests | `http://<unit-address>:5000` |

## Test Structure

| Test Class | What it verifies |
|---|---|
| `TestCharmDeployment` | Charm deploys with its OCI resource, PostgreSQL, S3, and self-signed TLS integrations, reaches active status, health/ready/root endpoints respond, charm and OCI image artifacts land in RGW (artifact and OCI buckets) while metadata lands in PostgreSQL |
| `TestFunctionalScenarios` | All shared Go functional scenarios pass against the deployed charm |

## Architecture

```
┌──────────────────────────────────────────────────────┐
│                      Juju Model                      │
│                                                      │
│  ┌──────────────┐   ingress    ┌──────────────────┐  │
│  │charm-registry│◄─────────────│ ingress-api      │  │
│  │              │              │                  │  │
│  │              │ oci-ingress  ├──────────────────┤  │
│  │              │◄─────────────│ ingress-oci      │  │
│  └──────────────┘              │                  │  │
│         ▲                      └────────┬─────────┘  │
│         │ postgresql                    │ gateway-route
│  ┌──────────────┐   s3         ┌────────▼─────────┐  │
│  │postgresql-k8s│  ┌───────────│gateway-api-      │  │
│  └──────────────┘  │           │integrator        │  │
│                    ▼           └────────▲─────────┘  │
│              ┌───────────────┐          │ certificates
│              │s3-integrator  │  ┌───────┴──────────┐ │
│              │track 2        │  │self-signed-      │ │
│              └───────┬───────┘  │certificates      │ │
│                      │          └──────────────────┘ │
│                      │ S3 credentials/endpoint        │
└──────────────────────────────────────────────────────┘
          ▲
          │ HTTP + RGW
          ▼
   functional-test binary
   + direct health/readiness/S3/PostgreSQL assertions
          │
          ▼
   MicroCeph RGW on the test runner
```

Both ingress relations are mandatory: the charm derives its public API,
storage, and OCI registry URLs from them and stays blocked until both are
related. Two ingress-configurator applications are deployed because each charm
relation needs its own ingress provider, and both forward their routes to the
same gateway-api-integrator application. PostgreSQL verifies the charm's
database relation path, s3-integrator track 2 points artifact storage at
MicroCeph RGW, and self-signed certificates exercise TLS termination on the
Gateway API application.

By default the tests provision a single-node MicroCeph cluster following the
snap how-to: install the `microceph` snap, hold refreshes, bootstrap the
cluster, add three `loop,4G,3` OSDs, then enable an RGW service instance with
`microceph enable rgw --target <node> --port 8081`. The charm workload receives
the runner's non-loopback host IP as the RGW endpoint so pods can reach RGW from
Canonical K8s. Set `JUB_S3_ENDPOINT`, `JUB_S3_ACCESS_KEY`, and
`JUB_S3_SECRET_KEY` together to use an external S3-compatible service instead.

## Design Decisions

- **Juju-native deployment**: The Go functional-test
  binary runs as a separate process, not inside any container.
- **Session-scoped deployment**: The charm is deployed once per pytest session.
  Tests share the same running model to avoid repeated deploy overhead.
- **Artifact-backed deploys**: In CI, `opcli` provides the charm file and
  `app-image` resource built by `canonical/charm-ci`.
- **Jubilant over pytest-operator**: Jubilant wraps the Juju CLI with a clean
  Python API, avoids websocket issues, and does not require async.
