# Integration test fixtures

Committed artifacts used by the Terraform lifecycle test
(`tests/integration/terraform/test_terraform.py`), which publishes them
through the Juju-deployed registry with charmcraft and deploys them in a
consumer model:

- `itest-lifecycle_r{1,2}.charm` — a minimal sidecar charm (dispatch script
  sets active status; the workload container runs only the pebble binary Juju
  mounts into it). One universal archive per revision, declaring amd64 and
  arm64. Each archive also embeds the `charmcraft.yaml` used as the matching
  upload context for `charmcraft upload`.

The spread prepare hook materializes a tiny public image as local
`oci-archive` files under `.bin/`, then the lifecycle test uploads them through
`charmcraft upload-resource`.

Do not edit these files by hand. Regenerate them with:

```sh
make generate-test-fixtures
```

Generation is byte-for-byte deterministic (pinned zip timestamps); CI
regenerates the fixtures and fails if the committed files do not match
`tests/integration/scripts/generate-test-fixtures.py`.
