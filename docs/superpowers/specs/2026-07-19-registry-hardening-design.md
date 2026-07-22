# Registry Security, Consistency, and Operations Hardening

Date: 2026-07-19

Status: Approved for implementation

## Context

The registry currently has several related correctness and security defects across authorization, artifact publication, Charmhub synchronization, and process lifecycle. Fixing each defect independently would preserve the underlying causes: authorization policy is duplicated between database backends, multi-record mutations do not consistently share a transaction boundary, and external artifact deletion is not durably coordinated with database changes.

This design addresses the reviewed P0, P1, and P2 findings as four staged vertical slices:

1. Authorization and token security.
2. Transactional upload and publication lifecycle.
3. Charmhub synchronization correctness and coordination.
4. Operational correctness and bounded read performance.

The implementation will preserve current HTTP response shapes where practical. It will add forward-only PostgreSQL and SQLite migrations and focused repository/use-case primitives. It will not perform unrelated file moves or a general repository rewrite.

## Approved policy decisions

Two migration and compatibility decisions were explicitly approved:

- Existing ownerless pending uploads will be invalidated. They will become rejected and must be uploaded again. Existing approved upload history remains preserved but cannot be reused.
- An authenticated store token cannot issue another token through `POST /v1/tokens`. Store-token rotation must use the existing exchange operation, which will preserve authority and cap the new token's expiry at the parent expiry.

The approved multi-unit coordination model is a database-backed per-package synchronization lease, implemented using the strongest safe primitive available in each backend.

## Goals

- Make package mutation authorization identical in PostgreSQL, SQLite, and memory implementations.
- Ensure store tokens cannot increase their own permissions, package/channel scope, or lifetime.
- Prevent cross-project OCI blob mounts from reading another package's repository.
- Bind every new upload to one account and allow exactly one terminal consumption.
- Make revision, resource, release, and unregister mutations atomic at the package aggregate boundary.
- Make external artifact deletion durable and retryable without rolling back committed metadata.
- Make Charmhub synchronization idempotent, safe across multiple units, and resistant to partial imports.
- Bound archive decompression and search work.
- Make server and CLI cancellation, timeout, and shutdown behavior deterministic.

## Non-goals

- Changing the public JSON shapes of existing upload, release, info, or download responses.
- Replacing the Distribution registry, blob store, SQL generator, or authentication provider.
- Converting every string-valued domain field to a new type in one change.
- Reorganizing large files unless a new focused component naturally belongs in a separate file.
- Providing rollback migrations. Existing operations guidance requires backup and forward recovery.

## 1. Authorization and token security

### Repository facts and service policy

Database repositories will stop deciding whether an operation is a view or management operation. They will expose authorization facts only.

A typed package role will be introduced with the ordered values:

- `none`
- `viewer`
- `editor`
- `owner`

`PackageRepo.GetPackageRole(ctx, packageID, accountID)` will return the strongest direct or group ACL role for the account. Package ownership remains available on `core.Package.OwnerAccountID`; the service policy treats the owning account as `owner` without requiring an ACL row. Unknown persisted roles grant no access.

The existing `CanViewPackage` and `CanManagePackage` methods will be removed after all callers use the centralized policy. This eliminates the SQLite defect where public visibility was treated as management authorization and eliminates its invalid `admin` ACL role.

The service authorization policy will take an explicit request containing:

- the package;
- whether anonymous public access is allowed;
- the minimum package role, if any;
- the required token permission or permissions;
- an optional channel.

It will evaluate, in order:

1. System identities bypass user authorization.
2. Anonymous access is accepted only when the endpoint allows it and the package is public.
3. Authenticated identities must satisfy package visibility or the requested role.
4. A presented store token must match the package selector even when the package is public or the account is an administrator.
5. A presented store token must contain the required permission and channel scope.
6. Direct OIDC/dev identities retain their account-level authority without synthetic token scopes.

Administrator status expands the account's package role, but it does not erase restrictions on a store token presented by that administrator.

### Fine-grained permissions

Read endpoints will use their declared domain permission:

- Package metadata uses `package-view-metadata`.
- Revision, resource, and artifact-download data uses `package-view-revisions`.
- Release and channel data uses `package-view-releases`.
- Composite info, find, and refresh data requires each domain permission represented in the response.

Compatibility umbrellas remain:

- `package-view` grants all `package-view-*` permissions.
- `package-manage` grants all package permissions.
- A domain management permission grants the corresponding domain view permission.

An anonymous request to an endpoint that already permits public access remains valid. If the caller presents a scoped token, that token is authoritative and must satisfy its package and permission restrictions. Search filters inaccessible results; direct lookups return forbidden.

### Token issuance and exchange

Public `IssueStoreToken` will require an authenticated identity with `identity.Token == nil`. A store token calling it receives a forbidden error with code `token-delegation-forbidden`, and no database row is created.

Token construction will move to a private minting helper so exchange does not call the public issuance policy. Exchange will:

- copy permissions exactly, including an empty permission set;
- copy package selectors exactly;
- copy channel restrictions exactly;
- set validity no later than the parent token's `ValidUntil`;
- reject exchange when no positive validity remains.

Direct OIDC/dev issuance retains the existing default permissions when permissions are omitted. Requested permissions will be checked against the known permission set, TTL conversion will reject non-positive or overflowing values, and package selectors will be structurally validated.

### OCI cross-repository mounts

The OCI middleware will inspect `mount` and `from` on blob-upload initiation requests before forwarding them to Distribution.

- Same-project mounts continue after normal destination push authentication.
- Cross-project, duplicated, incomplete, or malformed mount parameters are removed from a cloned request before forwarding.

Removing the parameters makes Distribution start a normal upload instead of mounting a source blob. This preserves client fallback behavior without revealing or reading the source repository. The original request object is not mutated.

## 2. Upload and publication lifecycle

### Upload state machine and schema

Uploads become owner-bound, immutable-key, one-use state machines:

```text
uploading -> pending -> approved
                     -> rejected
```

Terminal states never transition again. Core constants will replace repeated status literals in the touched paths.

Every new upload contains:

- `created_by_account_id`;
- an optional `consumed_by_package_id`;
- a server-generated immutable object key `artifacts/<upload UUID>/payload`;
- the original filename as metadata only.

The object key never contains a client-controlled filename and never changes after creation. A successfully consumed file upload's key is adopted directly by its charm or resource revision, avoiding an external copy or rename inside a database transaction.

PostgreSQL already has a nullable `created_by_account_id`; the new migration completes the lifecycle fields and indexes. SQLite receives the corresponding columns and indexes. Owner remains nullable only for historical terminal rows. New constructors and writes require an owner.

`consumed_by_package_id` is nullable and uses `ON DELETE SET NULL` where the backend supports the foreign key, preserving terminal upload history when a package is purged.

Both migrations will update every ownerless `pending` row to `rejected` and record a structured `legacy-upload-invalidated` error. Historical approved rows are preserved. Conditional finalization requires a nonempty matching owner and `pending` status, so historical rows cannot be reused.

### Upload creation

Upload authorization and identity will be carried into `CreateUploadStream` instead of being separated from persistence.

The creation sequence is:

1. Authorize the identity.
2. Stream the request into a temporary file while computing hashes and size.
3. Insert an `uploading` database row, making the future object key and owner durable.
4. Put the object in blob storage.
5. Conditionally activate the row to `pending`.

If the blob put fails, the row is rejected and any partial object is deleted best-effort. If activation fails, the object is deleted best-effort and the tracked row remains terminal. A process crash can therefore leave a tracked stale row or deterministic orphan, never a usable unowned upload.

Invalid archive or descriptor review conditionally changes the creator's pending upload to rejected before best-effort blob deletion.

Upload review lookup is also scoped. A pending or rejected upload is visible only to its creator. An approved upload is visible through a package review endpoint only when its consumed package matches that endpoint. The migration backfills that association from charm and file-resource object-key references where it can do so unambiguously. Unassociated historical terminal rows remain preserved and administrator-inspectable but are not exposed through an arbitrary package endpoint. Supplying an unrelated upload UUID cannot disclose another package's upload state.

### Package mutation boundary

A focused `withLockedPackageTransaction` helper will combine `WithinTransaction` with a repository package lock.

- PostgreSQL locks the package row with `SELECT ... FOR UPDATE`.
- SQLite performs a no-op package update inside the transaction to acquire the database writer before reading mutable allocation state.
- Memory uses its existing mutex semantics.

The boundary is used by revision publication, resource publication, release batches, unregister, purge metadata deletion, and other directly affected batch mutations.

Long-running parsing, hashing, downloads, and OCI calls happen before the transaction. The transaction re-fetches mutable state and its conditional predicates remain authoritative.

### Revision publication

Revision publication performs archive parsing outside the package lock. Inside the locked transaction it:

1. Re-fetches the package and pending upload.
2. Conditionally consumes the upload for its owner and package.
3. Allocates the next revision from current state under the package lock.
4. Inserts the revision.
5. Upserts all manifest resource definitions.
6. Updates package publication metadata.

Any error rolls back upload consumption and all package records. A concurrent consumer receives a conflict and cannot create a second reference.

### Resource publication

Resource publication follows the same locked transaction and conditional upload consumption. It obtains the latest resource revision without listing all revisions and allocates the next value while the package is locked.

The stored resource definition is authoritative:

- An omitted request type uses the definition type.
- A nonempty mismatching request type is rejected.
- Touched constructors accept only supported resource types, currently `file` and `oci-image`.

OCI descriptor payloads are read through a small independent limit, decoded as exactly one JSON object using the currently supported descriptor fields, and must contain a nonempty syntactically valid digest. Trailing JSON is rejected without breaking the existing `ImageName`, registry, username, password, and digest payload shape. Before any database write, the OCI client performs an authenticated manifest `HEAD` against the exact package/resource repository and digest. The endpoint that renders OCI descriptors validates its digest too, but `PushResource` independently verifies untrusted uploaded payloads.

After a successful OCI resource publication, the no-longer-needed descriptor blob is deleted through durable cleanup. The client-pushed OCI manifest remains the final artifact.

### Release and track batches

An empty release batch is invalid. A nonempty batch runs in one locked transaction with two phases:

1. Normalize and validate every release, channel restriction, package revision, resource reference, compatibility constraint, and duplicate slot.
2. Replace every release and update package status.

No writes occur until every item validates. Repository or validation failure leaves releases and package status unchanged. Audit and informational logs are emitted only after commit.

Track creation is similarly wrapped in one transaction after all tracks are constructed and validated, preventing a partially created request batch.

### Unregister and purge

Unregister locks and re-fetches the package, checks for revisions, and deletes within one transaction. Revision publication uses the same lock. The serialized outcome is therefore either:

- the revision commits first and unregister rejects; or
- unregister commits first and publication fails because the package no longer exists.

Purge commits package metadata deletion and cleanup jobs together. New one-use keys cannot be shared, but historical data may contain shared object keys. Candidate blob keys are filtered against all remaining revision and resource references after metadata deletion; only unreferenced targets are scheduled.

### Multipart behavior

The upload endpoint accepts exactly one `binary` part. If it encounters a second binary or a later multipart parse failure after creating the first upload, it conditionally abandons the first upload, makes it rejected and non-consumable, and schedules its blob for deletion. It never returns the last of multiple upload IDs.

### Archive limits

Archive parsing will use an `ArchiveLimits` value with defaults:

- 10 MiB maximum decompressed bytes per relevant entry, preserving the current limit;
- 64 MiB cumulative decompressed relevant content;
- 10,000 ZIP entries.

The parser first checks the entry count, then identifies relevant root entries before opening them. Code, vendor, and other irrelevant entries are never decompressed. Relevant names are matched case-insensitively, duplicates are rejected, actual bytes read count against per-entry and total limits, and malformed `manifest.yaml` is returned as an error rather than ignored.

Existing convenience parse functions remain and use defaults. Config-backed callers pass explicit limits.

### Durable external cleanup

A shared cleanup-job table records destructive external effects in the same transaction as their authoritative metadata deletion. Supported job kinds are:

- blob object;
- OCI image manifest;
- OCI package/project.

Jobs contain an idempotent target, attempt count, next-attempt timestamp, and last error. Active targets are unique. A small application worker claims due jobs, performs deletion, marks success, and retries failures with bounded backoff. Services may trigger an immediate attempt after commit, but failure never resurrects deleted metadata and never loses the durable job.

Claiming is an atomic leased transition with a worker ID and claim expiry, so multiple application units can safely run cleanup workers and recover work from a crashed claimant. Blob targets contain the object key. OCI image targets contain package/project, resource repository, and digest; the worker re-checks current references before deletion. OCI project targets contain the project name and are sufficient after the package row has been removed.

Blob uploads performed before a database commit still use synchronous compensation. Deterministic keys make a crash-left object safe to overwrite on retry and eligible for orphan cleanup.

## 3. Charmhub synchronization

### Per-package coordination

The manager acquires a package-scoped `SyncLease` around the complete reconciliation, including zero-rule cleanup.

- PostgreSQL uses a dedicated pooled connection and a session advisory lock derived from the package name. It holds one connection for the serial manager's active reconciliation and releases with a detached bounded context.
- SQLite uses a lease table keyed by package name with holder ID, fencing value, and expiry. It renews while work is active and verifies ownership at database mutation boundaries.
- Memory uses a keyed process mutex.

Failure to acquire means another unit owns the work. It is logged as a neutral skip and does not increment backoff. Renewal or ownership failure cancels the reconciliation context. Unlock failure closes the physical PostgreSQL connection instead of returning a possibly locked session to the pool.

Sync-rule completion updates are conditional so an in-flight run cannot overwrite a concurrently requested `deleting` state.

### Transactional import stages

Network and blob/OCI preparation remains outside database transactions. Persistence is split into explicit retryable units.

Revision stage:

1. Download, verify, and parse the charm.
2. Put the deterministic revision blob.
3. In one transaction update package metadata, create the revision, and upsert every manifest resource definition.
4. On transaction failure, compensate the new blob.

Resource and release stage:

1. Prepare every missing resource artifact.
2. Mirror or verify OCI content as necessary.
3. In one transaction create all missing resource revisions and replace the release that references them.
4. Compensate uncommitted file blobs and leave content-addressed OCI data for safe retry or reference-aware cleanup.

A release is never visible before all referenced database artifacts exist. A completed revision without a release is a valid retryable intermediate state.

An existing revision no longer causes an unconditional early return. Its stored metadata is parsed and missing resource definitions are repaired transactionally before the revision is considered complete. This repairs legacy poisoned rows without a separate import-state schema.

### Release pruning

PostgreSQL keep variants carry `base` as JSON instead of JSON serialized into a string. The generated query extracts `item->'base'` and compares it directly with the release `jsonb` value. JSON key order or whitespace can no longer cause a retained variant to be removed.

Track names are escaped before building a `LIKE` prefix in both backends, and both queries specify an escape character. `%`, `_`, and backslash in a track name cannot select another track.

Empty-track pruning runs release and track deletion in one transaction. It tolerates only not-found results, propagates all other failures, and updates caches only after commit.

### Reference-aware OCI pruning

Before deleting resource revisions, synchronization loads all revisions for that resource definition and builds the set of digests retained by kept revisions.

- A stale row sharing a digest with a retained row deletes only its database row.
- Stale manifest deletion is deduplicated by resource repository and digest.
- Identical digests in different resource repositories are independently deleted.

Database deletion and the corresponding cleanup job commit together. The cleanup worker re-checks retained references before deleting an OCI manifest.

## 4. Operations and performance

### Bounded search read model

Search will use a focused repository projection containing package, default release, and default revision. PostgreSQL and SQLite queries will:

- filter the name query;
- apply coarse public/owner/ACL visibility and token package selectors before limiting;
- select only released packages;
- resolve the default stable release with existing fallback semantics;
- join the revision;
- order deterministically;
- apply `offset` and a bounded `limit`.

The service repeats authoritative token policy checks after loading results. This changes search from an unbounded all-package load plus per-result release/revision queries to a constant number of bounded queries.

The API accepts optional `limit` and `offset` query parameters. The default limit is 100 and the hard maximum is 500. The response JSON remains `{ "results": [...] }`; clients that need additional results increment the offset.

### Transaction cleanup

Every PostgreSQL and SQLite `BeginTx` installs a rollback defer immediately. A callback panic is not recovered; rollback runs and the original panic propagates. PostgreSQL rollback uses a detached bounded context so caller cancellation does not leave an open transaction.

Migration advisory unlock also uses a detached bounded context and checks the returned boolean. An unlock failure or uncertain result closes the physical connection instead of returning it to the pool. Per-migration transaction code is factored through the same immediate-rollback discipline.

### OCI internal URL and TLS

When no explicit internal URL is configured, its default is derived from the embedded listener:

- `http://127.0.0.1:<listen port>` without OCI certificate/key files;
- `https://127.0.0.1:<listen port>` with certificate/key files.

Explicit internal URLs remain supported for a trusted proxy or separate registry. URLs are parsed and restricted to HTTP(S); loopback URLs that target the embedded listener must match its TLS mode. Certificate and key pairing remains mandatory.

### Server lifecycle

Startup moves into a `run` function that returns an error after deferred application cleanup has completed. `main` is the only place that calls `os.Exit`.

API and OCI listener results are coordinated through an error channel. An unexpected listener error cancels and shuts down its sibling. Signal-driven shutdown waits for both listeners, treats `http.ErrServerClosed` as success, and returns zero. Unexpected listener or shutdown errors return nonzero after closers run.

### CLI cancellation and timeouts

The administrative CLI uses a signal-derived root context and a dedicated HTTP client with a configurable request timeout, defaulting to 30 seconds. It does not use `http.DefaultClient`.

`sync wait --timeout` creates a context deadline for the whole wait operation. The same context is passed to every poll and retry so expiry interrupts an active HTTP request, not merely the delay between requests.

### Charmhub redirects

The Charmhub client retains its allowed-host and private-address checks. In addition, a request that started over HTTPS cannot redirect to HTTP, including on the same allowed host. Redirect hop limits remain unchanged.

## Error and compatibility behavior

- Store-token direct issuance becomes a deliberate 403 response.
- Ownerless pending uploads become rejected after migration and must be re-uploaded.
- Upload consumption races return conflict/not-found semantics without partial publication.
- Empty release batches and resource type mismatches return invalid-request errors.
- Search is bounded by default; clients can use `limit` and `offset` without a response-shape change.
- Public anonymous behavior remains where endpoints currently permit it. Presented tokens can no longer bypass their own package or permission restrictions on public data.
- Cross-project OCI mounts fall back to an ordinary upload rather than failing the entire client operation.

## Verification strategy

Implementation follows test-driven development. Each production behavior begins with a focused failing test, then the smallest production change, then targeted and broader verification.

Required invariants include:

- A stranger cannot manage another owner's public package in any backend.
- Viewer, editor, owner, admin, system, anonymous, and scoped-token decisions match across backends.
- Token issuance and exchange never increase store-token authority or lifetime.
- Package A credentials cannot mount a blob from package B.
- Only an upload's creator can consume it, and at most one concurrent consumer succeeds.
- A failed publication transaction leaves the upload pending and creates no partial artifact records.
- Concurrent revision/resource publication allocates distinct ordered revisions.
- Invalid or nonexistent OCI digests cause no database mutation.
- Mixed-validity release batches and empty batches change nothing.
- Unregister racing publication produces one of the two serialized valid outcomes.
- Purge never deletes a blob still referenced by historical data.
- ZIP bombs, duplicate metadata, cumulative overflow, and malformed manifests are rejected.
- Multiple multipart binaries leave no consumable upload.
- Sync retries repair legacy incomplete imports and never expose releases with missing artifacts.
- Kept release variants survive JSON normalization differences.
- Track wildcard characters cannot broaden pruning.
- Retained OCI digests are never deleted.
- Two units cannot reconcile the same package concurrently.
- Search query count is constant and results never exceed the cap.
- Transaction panics roll back, migration cancellation releases or discards the locked connection, CLI deadlines cancel requests, and normal shutdown runs all closers and exits successfully.

Focused service, repository, OCI, archive, sync, configuration, and command tests will be run throughout. Broad Go formatting, build, vet, and unit-test verification will run before completion. Existing integration suites will only be used when a backend or lifecycle behavior cannot be adequately verified at unit scope.

## Delivery sequence

1. Authorization facts, centralized policy, token restrictions, and OCI mount handling.
2. Upload migrations/state machine, package locks, atomic publication/release/unregister, OCI validation, multipart and archive limits.
3. Sync leases, transactional import repair, pruning correctness, and durable cleanup jobs.
4. Search read model, TLS defaults, process/CLI lifecycle, redirect, and transaction cleanup.

Each slice remains buildable and is reviewed before the next slice. Generated SQL is regenerated only from reviewed query changes. Unrelated working-tree changes, if any appear, are preserved.
