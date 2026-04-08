package api

const openAPISpec = `openapi: 3.1.0
info:
  title: Private Charm Registry
  version: 0.2.0
  description: |
    Local registry API compatible with stock charmcraft and juju for supported
    charm and resource workflows. Production authentication uses OIDC-backed
    account resolution plus registry-issued store tokens. Development can opt
    into insecure bearer tokens via CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH=true.
paths:
  /v1/charm:
    get:
      summary: List registered charm names for the authenticated account
    post:
      summary: Register a charm name
  /v1/charm/{name}:
    get:
      summary: Get package metadata
    patch:
      summary: Update package metadata
    delete:
      summary: Unregister an unpublished package
  /v1/charm/{name}/revisions:
    get:
      summary: List package revisions
    post:
      summary: Create a package revision from an upload
  /v1/charm/{name}/resources/{resource}/revisions:
    get:
      summary: List resource revisions
    post:
      summary: Create a resource revision from an upload
  /v1/charm/{name}/releases:
    get:
      summary: List releases for a package
    post:
      summary: Release one or more revisions to channels
  /v1/charm/{name}/tracks:
    post:
      summary: Create tracks for a package
  /v1/tokens:
    get:
      summary: List store tokens for the authenticated account
    post:
      summary: Issue a store token after authenticating with OIDC or dev auth
  /v1/tokens/whoami:
    get:
      summary: Describe the currently authenticated store token
  /v1/tokens/dashboard/exchange:
    post:
      summary: Exchange an authenticated session for a store token
  /unscanned-upload/:
    post:
      summary: Upload a charm or resource blob for later publishing
  /v2/charms/find:
    get:
      summary: Search charms
  /v2/charms/info/{name}:
    get:
      summary: Get charm info
  /v2/charms/refresh:
    post:
      summary: Resolve revisions and resources for Juju refresh/install flows
`
