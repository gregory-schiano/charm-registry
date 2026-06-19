# Public Charmhub Consumer Endpoints

## Goal

Make the registry compatible with Juju's unauthenticated Charmhub client while
preserving authenticated access to private packages and keeping publishing and
administrative operations protected.

The compatibility boundary is an explicit API contract, not an incidental
middleware choice. Regression tests and API documentation must prevent public
consumer endpoints from being placed behind mandatory authentication again.

## Upstream client contract

Juju's current Charmhub client constructs requests without an authorization
header for these operations:

- `GET /v2/charms/find`
- `GET /v2/charms/info/{name}`
- `POST /v2/charms/refresh`
- `GET /v2/charms/resources/{name}/{resource}/revisions`
- charm and resource download URLs returned by info and refresh responses

The relevant upstream implementation is in:

- [`internal/charmhub/client.go`](https://github.com/juju/juju/blob/dd668f8bbf0e06b6a4c6a2afe1fd8f5557d11faa/internal/charmhub/client.go)
- [`internal/charmhub/http.go`](https://github.com/juju/juju/blob/dd668f8bbf0e06b6a4c6a2afe1fd8f5557d11faa/internal/charmhub/http.go)
- [`internal/charmhub/info.go`](https://github.com/juju/juju/blob/dd668f8bbf0e06b6a4c6a2afe1fd8f5557d11faa/internal/charmhub/info.go)
- [`internal/charmhub/find.go`](https://github.com/juju/juju/blob/dd668f8bbf0e06b6a4c6a2afe1fd8f5557d11faa/internal/charmhub/find.go)
- [`internal/charmhub/refresh.go`](https://github.com/juju/juju/blob/dd668f8bbf0e06b6a4c6a2afe1fd8f5557d11faa/internal/charmhub/refresh.go)
- [`internal/charmhub/resources.go`](https://github.com/juju/juju/blob/dd668f8bbf0e06b6a4c6a2afe1fd8f5557d11faa/internal/charmhub/resources.go)
- [`internal/charmhub/download.go`](https://github.com/juju/juju/blob/dd668f8bbf0e06b6a4c6a2afe1fd8f5557d11faa/internal/charmhub/download.go)

Charmcraft intentionally uses an anonymous store client for public library
reads:

- `GET /v1/charm/libraries/{charm}/{library-id}`
- `POST /v1/charm/libraries/bulk`

The relevant upstream implementation is
[`charmcraft/store/client.py`](https://github.com/canonical/charmcraft/blob/3e6cd6909b1d51614113104cf348e4fc8970eb6a/charmcraft/store/client.py).
This registry already exposes the bulk endpoint anonymously and intentionally
does not host libraries. The single-library route therefore remains an
unauthenticated `404` until library hosting is implemented.

## Authentication behavior

Introduce optional identity resolution for the Juju consumer endpoints.

- A request with no credentials receives an anonymous `core.Identity`.
- A request with valid credentials receives its resolved identity and may see
  private packages it is authorized to view.
- A request that supplies malformed, expired, revoked, or otherwise invalid
  credentials receives `401`; it must not silently fall back to anonymous
  access.

This optional-auth middleware applies only to:

- `GET /v2/charms/find`
- `GET /v2/charms/info/{name}`
- `POST /v2/charms/refresh`
- `GET /v2/charms/resources/{name}/{resource}/revisions`
- `GET /api/v1/charms/download/{filename}`
- `GET /api/v1/resources/download/{filename}`

All publishing, token, package-management, upload, OCI credential, and
administrative endpoints retain mandatory authentication. The existing public
infrastructure and Charmcraft library-bulk endpoints remain unchanged.

## Service authorization

The service layer remains the authority for package visibility.

- Public packages are visible to anonymous callers.
- Private packages require an authenticated identity with package access.
- Search omits packages the caller cannot see.
- Info, resource revision listing, and downloads reject inaccessible private
  packages.
- Refresh returns its existing per-action authorization error for inaccessible
  private packages rather than failing the complete batch.

Remove only the redundant top-level `requireAuth` calls from consumer service
methods whose package visibility is already enforced by `requirePackageView`.
Do not relax any mutation method or package-management read method.

## Error behavior

- Missing public packages retain the existing not-found response.
- Anonymous access to private info, resources, or downloads must not reveal
  private metadata. It follows the existing service authorization error
  contract.
- Private packages are absent from anonymous search results.
- Invalid supplied credentials return `401` before handler execution.
- Request decoding, size limits, rate limiting, and security headers remain
  unchanged.

## Regression tests

Add HTTP-level tests covering the complete compatibility contract:

1. Anonymous `find`, `info`, `refresh`, resource-revision listing, charm
   download, and resource download succeed for a released public package.
2. Anonymous search excludes a private package.
3. Anonymous private-package info, resource listing, and downloads do not
   succeed.
4. Anonymous refresh returns a per-action error for a private package.
5. Valid authorized credentials retain private-package access on the optional
   routes.
6. Invalid credentials on an optional route return `401`, proving optional
   authentication is not authentication bypass.
7. Representative publishing, upload, token, OCI, and admin endpoints remain
   `401` without credentials.
8. Charmcraft's library-bulk endpoint remains anonymously accessible.

Add focused service tests where needed to pin anonymous public access and
private-package rejection independently of HTTP routing.

## Documentation

Update `docs/api-compatibility.md` to:

- mark the Juju consumer and artifact download endpoints as optionally
  authenticated;
- explain that anonymous callers see public packages only;
- state that invalid supplied credentials still fail;
- identify Juju and Charmcraft compatibility as the reason for this stable
  public contract;
- remove the obsolete roadmap note claiming all v2 endpoints require
  authentication.

The embedded OpenAPI summary should also describe the consumer endpoints as
public reads with optional credentials for private-package access.

## Scope exclusions

- No anonymous access to private packages.
- No changes to token issuance or authentication formats.
- No implementation of Charmcraft library hosting.
- No changes to OCI registry authentication.
- No unrelated routing or service refactoring.
