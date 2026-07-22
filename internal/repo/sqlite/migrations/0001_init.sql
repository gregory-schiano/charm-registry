CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS accounts (
    id TEXT PRIMARY KEY,
    subject TEXT NOT NULL UNIQUE,
    username TEXT NOT NULL,
    display_name TEXT NOT NULL,
    email TEXT NOT NULL,
    validation TEXT NOT NULL,
    is_admin INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS account_groups (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL
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
    private INTEGER NOT NULL DEFAULT 1,
    status TEXT NOT NULL,
    owner_account_id TEXT NOT NULL REFERENCES accounts(id),
    oci_project TEXT NOT NULL DEFAULT '',
    oci_push_robot_id INTEGER NULL,
    oci_push_robot_name TEXT NOT NULL DEFAULT '',
    oci_push_robot_secret TEXT NOT NULL DEFAULT '',
    oci_pull_robot_id INTEGER NULL,
    oci_pull_robot_name TEXT NOT NULL DEFAULT '',
    oci_pull_robot_secret TEXT NOT NULL DEFAULT '',
    oci_synced_at TIMESTAMP NULL,
    authority TEXT NULL,
    contact TEXT NULL,
    default_track TEXT NULL,
    description TEXT NULL,
    summary TEXT NULL,
    title TEXT NULL,
    website TEXT NULL,
    links TEXT NOT NULL DEFAULT '{}',
    media TEXT NOT NULL DEFAULT '[]',
    track_guardrails TEXT NOT NULL DEFAULT '[]',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
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
    automatic_phasing_percentage REAL NULL,
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (package_id, name)
);

CREATE TABLE IF NOT EXISTS uploads (
    id TEXT PRIMARY KEY,
    filename TEXT NOT NULL,
    object_key TEXT NOT NULL,
    size INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    sha384 TEXT NOT NULL,
    status TEXT NOT NULL,
    kind TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    approved_at TIMESTAMP NULL,
    revision INTEGER NULL,
    errors TEXT NOT NULL DEFAULT '[]'
);

CREATE TABLE IF NOT EXISTS revisions (
    id TEXT PRIMARY KEY,
    package_id TEXT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL,
    version TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    created_by TEXT NOT NULL,
    size INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    sha384 TEXT NOT NULL,
    object_key TEXT NOT NULL,
    metadata_yaml TEXT NOT NULL DEFAULT '',
    config_yaml TEXT NOT NULL DEFAULT '',
    actions_yaml TEXT NOT NULL DEFAULT '',
    bundle_yaml TEXT NOT NULL DEFAULT '',
    readme_md TEXT NOT NULL DEFAULT '',
    bases TEXT NOT NULL DEFAULT '[]',
    attributes TEXT NOT NULL DEFAULT '{}',
    relations TEXT NOT NULL DEFAULT '{}',
    subordinate INTEGER NOT NULL DEFAULT 0,
    UNIQUE (package_id, revision)
);

CREATE TABLE IF NOT EXISTS resource_definitions (
    id TEXT PRIMARY KEY,
    package_id TEXT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    optional INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL,
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
    created_at TIMESTAMP NOT NULL,
    size INTEGER NOT NULL,
    sha256 TEXT NOT NULL DEFAULT '',
    sha384 TEXT NOT NULL DEFAULT '',
    sha512 TEXT NOT NULL DEFAULT '',
    sha3_384 TEXT NOT NULL DEFAULT '',
    object_key TEXT NOT NULL DEFAULT '',
    bases TEXT NOT NULL DEFAULT '[]',
    architectures TEXT NOT NULL DEFAULT '[]',
    oci_image_digest TEXT NOT NULL DEFAULT '',
    oci_image_blob TEXT NOT NULL DEFAULT '',
    UNIQUE (resource_id, revision)
);

CREATE TABLE IF NOT EXISTS releases (
    id TEXT PRIMARY KEY,
    package_id TEXT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    channel TEXT NOT NULL,
    revision INTEGER NOT NULL,
    base TEXT NOT NULL DEFAULT 'null',
    base_key TEXT NOT NULL DEFAULT 'null',
    resources TEXT NOT NULL DEFAULT '[]',
    when_created TIMESTAMP NOT NULL,
    expiration_date TIMESTAMP NULL,
    progressive REAL NULL,
    UNIQUE (package_id, channel, base_key)
);

CREATE TABLE IF NOT EXISTS store_tokens (
    session_id TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    description TEXT NULL,
    packages TEXT NULL,
    channels TEXT NULL,
    permissions TEXT NULL,
    valid_since TIMESTAMP NOT NULL,
    valid_until TIMESTAMP NOT NULL,
    revoked_at TIMESTAMP NULL,
    revoked_by TEXT NULL
);

CREATE TABLE IF NOT EXISTS charmhub_sync_rules (
    package_name TEXT NOT NULL,
    track TEXT NOT NULL,
    bases TEXT NOT NULL DEFAULT '[]',
    architectures TEXT NOT NULL DEFAULT '[]',
    created_by_account_id TEXT NOT NULL REFERENCES accounts(id),
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    last_sync_status TEXT NOT NULL,
    last_sync_started_at TIMESTAMP NULL,
    last_sync_finished_at TIMESTAMP NULL,
    last_sync_error TEXT NULL,
    PRIMARY KEY (package_name, track)
);
