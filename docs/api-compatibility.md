# API Compatibility

Charm Registry implements a subset of the Charmhub API that is sufficient for stock `juju` and stock `charmcraft` to publish and consume charms.

## Publisher API (charmcraft)

These endpoints are used by `charmcraft` when pushing charms to a registry.

### Token lifecycle

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/v1/tokens` | List tokens for the authenticated identity |
| `POST` | `/v1/tokens` | Issue a new store token |
| `POST` | `/v1/tokens/exchange` | Exchange an OIDC token for a store token |
| `POST` | `/v1/tokens/offline/exchange` | Offline token exchange (same handler) |
| `POST` | `/v1/tokens/revoke` | Revoke a store token |
| `GET` | `/v1/tokens/whoami` | Return the authenticated identity |
| `POST` | `/v1/tokens/dashboard/exchange` | Dashboard token exchange |
| `GET` | `/v1/whoami` | Return the authenticated identity (shorthand) |

Token issuance is rate-limited to 5 tokens per minute per identity by default, configurable via `CHARM_REGISTRY_TOKEN_RATE_LIMIT` and `CHARM_REGISTRY_TOKEN_RATE_WINDOW`.

### Package management

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/v1/charm` | List packages the identity can see |
| `POST` | `/v1/charm` | Register a new package |
| `GET` | `/v1/charm/{name}` | Get package metadata |
| `PATCH` | `/v1/charm/{name}` | Update package metadata (summary, description, private, etc.) |
| `DELETE` | `/v1/charm/{name}` | Delete a package |

### Revisions

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/v1/charm/{name}/revisions` | List revisions for a package |
| `POST` | `/v1/charm/{name}/revisions` | Push a new revision (references an upload-id) |
| `GET` | `/v1/charm/{name}/revisions/review` | Review an upload |

### Resources

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/v1/charm/{name}/resources` | List resources for a package |
| `GET` | `/v1/charm/{name}/resources/{resource}/revisions` | List resource revisions |
| `POST` | `/v1/charm/{name}/resources/{resource}/revisions` | Push a new resource revision |
| `PATCH` | `/v1/charm/{name}/resources/{resource}/revisions` | Update resource revision metadata |
| `GET` | `/v1/charm/{name}/resources/{resource}/oci-image/upload-credentials` | Get OCI push credentials for an image resource |
| `POST` | `/v1/charm/{name}/resources/{resource}/oci-image/blob` | Direct OCI image blob upload |

### Releases and tracks

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/v1/charm/{name}/releases` | List releases |
| `POST` | `/v1/charm/{name}/releases` | Create or update a release |
| `POST` | `/v1/charm/{name}/tracks` | Create a new track |

### Upload

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/unscanned-upload/` | Upload a charm archive (multipart) |

### Charmhub sync (admin only)

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/v1/admin/charmhub-sync` | List sync rules |
| `POST` | `/v1/admin/charmhub-sync` | Add a sync rule |
| `DELETE` | `/v1/admin/charmhub-sync/{name}/{track}` | Remove a sync rule |
| `POST` | `/v1/admin/charmhub-sync/{name}/run` | Trigger immediate sync |

### Charm libraries

| Method | Path | Access | Purpose |
|--------|------|--------|---------|
| `GET` | `/v1/charm/libraries/{charm}/{library-id}` | Public | Single library lookup (returns not found — hosting is not implemented) |
| `POST` | `/v1/charm/libraries/bulk` | Public | Bulk library query (returns empty list — hosting is not implemented) |

## Consumer API (juju)

These endpoints are used by `juju` when deploying and refreshing charms.

| Method | Path | Access | Purpose |
|--------|------|--------|---------|
| `GET` | `/v2/charms/find` | Public, optional auth | Find charms matching a query |
| `GET` | `/v2/charms/info/{name}` | Public, optional auth | Get charm info |
| `POST` | `/v2/charms/refresh` | Public, optional auth | Resolve refresh actions |
| `GET` | `/v2/charms/resources/{name}/{resource}/revisions` | Public, optional auth | List resource revisions |

### Artifact download

| Method | Path | Access | Purpose |
|--------|------|--------|---------|
| `GET` | `/api/v1/charms/download/{name}_{revision}.charm` | Public, optional auth | Download a charm archive |
| `GET` | `/api/v1/resources/download/{filename}` | Public, optional auth | Download a resource artifact |

## Infrastructure endpoints

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/` | Root document (service metadata, URLs) |
| `GET` | `/healthz` | Liveness check |
| `GET` | `/readyz` | Readiness check (verifies database connectivity) |
| `GET` | `/openapi.yaml` | OpenAPI specification |
| `GET` | `/docs` | API documentation (HTML) |

## Compatibility notes

### Public consumer authentication contract

Juju's Charmhub client does not send credentials for discovery, info, refresh,
resource-revision lookup, or artifact downloads. Those consumer endpoints are
therefore a stable public compatibility contract:

- requests without credentials can see and download public packages only;
- valid credentials additionally expose private packages the identity is
  authorized to view;
- malformed, expired, revoked, or otherwise invalid supplied credentials return
  `401` instead of falling back to anonymous access;
- publishing, package management, uploads, token management, OCI credentials,
  and administration remain authenticated.

Do not place the consumer endpoints behind mandatory authentication. Regression
tests cover the anonymous Juju flow and private-package isolation.

### Upload flow

The push-revision endpoint accepts a JSON body with an `upload-id` field, not a multipart upload. This works with current `charmcraft` but may differ from Charmhub's multi-step upload flow. Monitor for compatibility issues with future charmcraft versions.

### Libraries

Charmcraft uses an anonymous client for both single and bulk library lookups.
The routes stay public for client compatibility, but this registry does not host
libraries: single lookup returns `404` and bulk lookup returns an empty list.
Charm libraries should be vendored or shared via git.

### Bundles

There is no `type=bundle` handling. Bundles are deprecated in modern Juju.

### Progressive rollout

The `releases` table has `progressive` and `expiration_date` columns but there is no API to configure progressive rollout. Releases are always full (100% immediately).
