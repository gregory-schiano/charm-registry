CREATE TABLE IF NOT EXISTS accounts (
    id TEXT PRIMARY KEY,
    subject TEXT NOT NULL UNIQUE,
    username TEXT NOT NULL,
    display_name TEXT NOT NULL,
    email TEXT NOT NULL,
    validation TEXT NOT NULL,
    is_admin BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS account_groups (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS account_group_members (
    group_id TEXT NOT NULL REFERENCES account_groups(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, account_id)
);

CREATE TABLE IF NOT EXISTS packages (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    type TEXT NOT NULL,
    private BOOLEAN NOT NULL DEFAULT TRUE,
    status TEXT NOT NULL,
    owner_account_id TEXT NOT NULL REFERENCES accounts(id),
    harbor_project TEXT NOT NULL DEFAULT '',
    harbor_push_robot_id BIGINT NULL,
    harbor_push_robot_name TEXT NOT NULL DEFAULT '',
    harbor_push_robot_secret TEXT NOT NULL DEFAULT '',
    harbor_pull_robot_id BIGINT NULL,
    harbor_pull_robot_name TEXT NOT NULL DEFAULT '',
    harbor_pull_robot_secret TEXT NOT NULL DEFAULT '',
    harbor_synced_at TIMESTAMPTZ NULL,
    authority TEXT NULL,
    contact TEXT NULL,
    default_track TEXT NULL,
    description TEXT NULL,
    summary TEXT NULL,
    title TEXT NULL,
    website TEXT NULL,
    links JSONB NOT NULL DEFAULT '{}'::jsonb,
    media JSONB NOT NULL DEFAULT '[]'::jsonb,
    track_guardrails JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS package_acl (
    package_id TEXT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    principal_type TEXT NOT NULL,
    principal_id TEXT NOT NULL,
    role TEXT NOT NULL,
    PRIMARY KEY (package_id, principal_type, principal_id)
);

CREATE TABLE IF NOT EXISTS tracks (
    package_id TEXT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    version_pattern TEXT NULL,
    automatic_phasing_percentage DOUBLE PRECISION NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (package_id, name)
);

CREATE TABLE IF NOT EXISTS uploads (
    id TEXT PRIMARY KEY,
    filename TEXT NOT NULL,
    object_key TEXT NOT NULL,
    size BIGINT NOT NULL,
    sha256 TEXT NOT NULL,
    sha384 TEXT NOT NULL,
    status TEXT NOT NULL,
    kind TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    approved_at TIMESTAMPTZ NULL,
    revision INTEGER NULL,
    errors JSONB NOT NULL DEFAULT '[]'::jsonb
);

CREATE TABLE IF NOT EXISTS revisions (
    id TEXT PRIMARY KEY,
    package_id TEXT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL,
    version TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    created_by TEXT NOT NULL,
    size BIGINT NOT NULL,
    sha256 TEXT NOT NULL,
    sha384 TEXT NOT NULL,
    object_key TEXT NOT NULL,
    metadata_yaml TEXT NOT NULL DEFAULT '',
    config_yaml TEXT NOT NULL DEFAULT '',
    actions_yaml TEXT NOT NULL DEFAULT '',
    bundle_yaml TEXT NOT NULL DEFAULT '',
    readme_md TEXT NOT NULL DEFAULT '',
    bases JSONB NOT NULL DEFAULT '[]'::jsonb,
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    relations JSONB NOT NULL DEFAULT '{}'::jsonb,
    subordinate BOOLEAN NOT NULL DEFAULT FALSE,
    UNIQUE (package_id, revision)
);

CREATE TABLE IF NOT EXISTS resource_definitions (
    id TEXT PRIMARY KEY,
    package_id TEXT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    optional BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (package_id, name)
);

CREATE TABLE IF NOT EXISTS resource_revisions (
    id TEXT PRIMARY KEY,
    resource_id TEXT NOT NULL REFERENCES resource_definitions(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL,
    package_revision INTEGER NULL,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    size BIGINT NOT NULL,
    sha256 TEXT NOT NULL DEFAULT '',
    sha384 TEXT NOT NULL DEFAULT '',
    sha512 TEXT NOT NULL DEFAULT '',
    sha3_384 TEXT NOT NULL DEFAULT '',
    object_key TEXT NOT NULL DEFAULT '',
    bases JSONB NOT NULL DEFAULT '[]'::jsonb,
    architectures JSONB NOT NULL DEFAULT '[]'::jsonb,
    oci_image_digest TEXT NOT NULL DEFAULT '',
    oci_image_blob TEXT NOT NULL DEFAULT '',
    UNIQUE (resource_id, revision)
);

CREATE TABLE IF NOT EXISTS releases (
    id TEXT PRIMARY KEY,
    package_id TEXT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    channel TEXT NOT NULL,
    revision INTEGER NOT NULL,
    base JSONB NOT NULL DEFAULT 'null'::jsonb,
    resources JSONB NOT NULL DEFAULT '[]'::jsonb,
    when_created TIMESTAMPTZ NOT NULL,
    expiration_date TIMESTAMPTZ NULL,
    progressive DOUBLE PRECISION NULL,
    UNIQUE (package_id, channel)
);

CREATE TABLE IF NOT EXISTS store_tokens (
    session_id TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    description TEXT NULL,
    packages JSONB NULL,
    channels JSONB NULL,
    permissions JSONB NULL,
    valid_since TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ NULL,
    revoked_by TEXT NULL
);
