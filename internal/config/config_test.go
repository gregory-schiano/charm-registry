package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type configSnapshot struct {
	ListenAddress           string
	PublicAPIURL            string
	PublicStorageURL        string
	PublicRegistryURL       string
	DatabaseBackend         string
	ResolvedDatabaseBackend string
	StorageBackend          string
	ResolvedStorageBackend  string
	DataDir                 string
	SQLitePath              string
	BlobDir                 string
	S3Bucket                string
	S3Region                string
	S3UsePathStyle          bool
	S3DisableTLS            bool
	OIDCUsernameClaim       string
	OIDCDisplayNameClaim    string
	OIDCEmailClaim          string
	EnableInsecureDevAuth   bool
	OCIListenAddress        string
	OCIInternalURL          string
	OCIStorageBackend       string
	ResolvedOCIStorage      string
	OCIStorageDir           string
	OCIStorageBucket        string
	OCIStoragePrefix        string
	OCIStorageRegion        string
	OCIStorageEndpoint      string
	OCIStorageUsePathStyle  bool
	OCISecretKey            string
	OCIProjectPrefix        string
	MaxJSONBodyBytes        int64
	MaxArchiveFileBytes     int64
	MaxUploadBytes          int64
	CharmhubMaxResponse     int64
	CharmhubMaxArtifact     int64
	OCIMaxManifestBytes     int64
	ServerReadHeaderTimeout time.Duration
	ServerReadTimeout       time.Duration
	ServerWriteTimeout      time.Duration
	ServerIdleTimeout       time.Duration
	ServerShutdownTimeout   time.Duration
	ServerMaxHeaderBytes    int
}

func snapshotConfig(cfg Config) configSnapshot {
	return configSnapshot{
		ListenAddress:           cfg.ListenAddress,
		PublicAPIURL:            cfg.PublicAPIURL,
		PublicStorageURL:        cfg.PublicStorageURL,
		PublicRegistryURL:       cfg.PublicRegistryURL,
		DatabaseBackend:         cfg.DatabaseBackend,
		ResolvedDatabaseBackend: cfg.ResolvedDatabaseBackend(),
		StorageBackend:          cfg.StorageBackend,
		ResolvedStorageBackend:  cfg.ResolvedStorageBackend(),
		DataDir:                 cfg.DataDir,
		SQLitePath:              cfg.SQLitePath,
		BlobDir:                 cfg.BlobDir,
		S3Bucket:                cfg.S3Bucket,
		S3Region:                cfg.S3Region,
		S3UsePathStyle:          cfg.S3UsePathStyle,
		S3DisableTLS:            cfg.S3DisableTLS,
		OIDCUsernameClaim:       cfg.OIDCUsernameClaim,
		OIDCDisplayNameClaim:    cfg.OIDCDisplayNameClaim,
		OIDCEmailClaim:          cfg.OIDCEmailClaim,
		EnableInsecureDevAuth:   cfg.EnableInsecureDevAuth,
		OCIListenAddress:        cfg.OCIListenAddress,
		OCIInternalURL:          cfg.OCIInternalURL,
		OCIStorageBackend:       cfg.OCIStorageBackend,
		ResolvedOCIStorage:      cfg.ResolvedOCIStorageBackend(),
		OCIStorageDir:           cfg.OCIStorageDir,
		OCIStorageBucket:        cfg.OCIStorageBucket,
		OCIStoragePrefix:        cfg.OCIStoragePrefix,
		OCIStorageRegion:        cfg.OCIStorageRegion,
		OCIStorageEndpoint:      cfg.OCIStorageEndpoint,
		OCIStorageUsePathStyle:  cfg.OCIStorageUsePathStyle,
		OCISecretKey:            cfg.OCISecretKey,
		OCIProjectPrefix:        cfg.OCIProjectPrefix,
		MaxJSONBodyBytes:        cfg.MaxJSONBodyBytes,
		MaxArchiveFileBytes:     cfg.MaxArchiveFileBytes,
		MaxUploadBytes:          cfg.MaxUploadBytes,
		CharmhubMaxResponse:     cfg.CharmhubMaxResponseBytes,
		CharmhubMaxArtifact:     cfg.CharmhubMaxArtifactBytes,
		OCIMaxManifestBytes:     cfg.OCIMaxManifestBytes,
		ServerReadHeaderTimeout: cfg.ServerReadHeaderTimeout,
		ServerReadTimeout:       cfg.ServerReadTimeout,
		ServerWriteTimeout:      cfg.ServerWriteTimeout,
		ServerIdleTimeout:       cfg.ServerIdleTimeout,
		ServerShutdownTimeout:   cfg.ServerShutdownTimeout,
		ServerMaxHeaderBytes:    cfg.ServerMaxHeaderBytes,
	}
}

func TestLoadRequiresDatabaseURLForPostgresBackend(t *testing.T) {
	t.Setenv("CHARM_REGISTRY_DATABASE_BACKEND", "postgres")
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")
	_, err := Load()
	require.Error(t, err)
	assert.ErrorContains(t, err, "cannot load config:")
	assert.ErrorContains(t, err, "CHARM_REGISTRY_DATABASE_URL is required when CHARM_REGISTRY_DATABASE_BACKEND=postgres")

}

func TestLoadAutoDatabaseBackendResolvesToSQLiteWithoutDatabaseURL(t *testing.T) {
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")

	cfg, err := Load()

	require.NoError(t, err)
	assert.Equal(t, DatabaseBackendAuto, cfg.DatabaseBackend)
	assert.Equal(t, DatabaseBackendSQLite, cfg.ResolvedDatabaseBackend())
	assert.Equal(t, "data/registry.sqlite", cfg.SQLitePath)
}

func TestValidateBackendConfigRejectsInvalidBackendValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "database", mutate: func(cfg *Config) { cfg.DatabaseBackend = "invalid" }, wantErr: "CHARM_REGISTRY_DATABASE_BACKEND"},
		{name: "storage", mutate: func(cfg *Config) { cfg.StorageBackend = "invalid" }, wantErr: "CHARM_REGISTRY_STORAGE_BACKEND"},
		{name: "OCI storage", mutate: func(cfg *Config) { cfg.OCIStorageBackend = "invalid" }, wantErr: "CHARM_REGISTRY_OCI_STORAGE_BACKEND"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := Config{
				DatabaseBackend:   DatabaseBackendAuto,
				StorageBackend:    StorageBackendAuto,
				OCIStorageBackend: StorageBackendAuto,
				SQLitePath:        "data/registry.sqlite",
				BlobDir:           "data/blobs",
				OCIStorageDir:     "data/oci-registry",
			}
			tt.mutate(&cfg)

			err := validateBackendConfig(cfg)

			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestValidateBackendConfigChecksResolvedAutoBackends(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name: "sqlite path required when auto resolves to sqlite",
			cfg: Config{
				DatabaseBackend:    DatabaseBackendAuto,
				StorageBackend:     StorageBackendS3,
				OCIStorageBackend:  StorageBackendS3,
				S3Endpoint:         "https://s3.example.com",
				OCIStorageBucket:   "oci",
				OCIStorageEndpoint: "https://oci-s3.example.com",
			},
			wantErr: "CHARM_REGISTRY_SQLITE_PATH is required when database backend resolves to sqlite",
		},
		{
			name: "blob dir required when auto resolves to filesystem",
			cfg: Config{
				DatabaseBackend:    DatabaseBackendPostgres,
				DatabaseURL:        "postgres://localhost/test",
				StorageBackend:     StorageBackendAuto,
				OCIStorageBackend:  StorageBackendS3,
				OCIStorageEndpoint: "https://oci-s3.example.com",
			},
			wantErr: "CHARM_REGISTRY_BLOB_DIR is required when storage backend resolves to filesystem",
		},
		{
			name: "OCI storage dir required when auto resolves to filesystem",
			cfg: Config{
				DatabaseBackend:   DatabaseBackendPostgres,
				DatabaseURL:       "postgres://localhost/test",
				StorageBackend:    StorageBackendS3,
				S3Endpoint:        "https://s3.example.com",
				OCIStorageBackend: StorageBackendAuto,
			},
			wantErr: "CHARM_REGISTRY_OCI_STORAGE_DIR is required when OCI storage backend resolves to filesystem",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateBackendConfig(tt.cfg)

			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")
	cfg, err := Load()

	require.NoError(t, err)
	assert.Equal(t, configSnapshot{
		ListenAddress:           ":8080",
		PublicAPIURL:            "http://localhost:8080",
		PublicStorageURL:        "http://localhost:8080",
		PublicRegistryURL:       "https://localhost:5000",
		DatabaseBackend:         DatabaseBackendAuto,
		ResolvedDatabaseBackend: DatabaseBackendPostgres,
		StorageBackend:          StorageBackendAuto,
		ResolvedStorageBackend:  StorageBackendFilesystem,
		DataDir:                 "data",
		SQLitePath:              "data/registry.sqlite",
		BlobDir:                 "data/blobs",
		S3Bucket:                "charm-registry",
		S3Region:                "us-east-1",
		S3UsePathStyle:          true,
		S3DisableTLS:            false,
		OIDCUsernameClaim:       "preferred_username",
		OIDCDisplayNameClaim:    "name",
		OIDCEmailClaim:          "email",
		EnableInsecureDevAuth:   true,
		OCIListenAddress:        ":5000",
		OCIInternalURL:          "https://127.0.0.1:5000",
		OCIStorageBackend:       StorageBackendAuto,
		ResolvedOCIStorage:      StorageBackendFilesystem,
		OCIStorageDir:           "data/oci-registry",
		OCIStorageBucket:        "charm-registry-oci",
		OCIStoragePrefix:        "oci",
		OCIStorageRegion:        "us-east-1",
		OCIStorageEndpoint:      "",
		OCIStorageUsePathStyle:  true,
		OCISecretKey:            "oci-secret",
		OCIProjectPrefix:        "charm",
		MaxJSONBodyBytes:        1 << 20,
		MaxArchiveFileBytes:     10 << 20,
		MaxUploadBytes:          64 << 20,
		CharmhubMaxResponse:     4 << 20,
		CharmhubMaxArtifact:     64 << 20,
		OCIMaxManifestBytes:     16 << 20,
		ServerReadHeaderTimeout: 10 * time.Second,
		ServerReadTimeout:       30 * time.Second,
		ServerWriteTimeout:      30 * time.Second,
		ServerIdleTimeout:       120 * time.Second,
		ServerShutdownTimeout:   30 * time.Second,
		ServerMaxHeaderBytes:    1 << 20,
	}, snapshotConfig(cfg))

}

func TestLoadCustomValues(t *testing.T) {
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_LISTEN", ":9090")
	// Explicit sqlite selection should override the postgres URL that auto-detection would otherwise use.
	t.Setenv("CHARM_REGISTRY_DATABASE_BACKEND", "sqlite")
	t.Setenv("CHARM_REGISTRY_DATA_DIR", "/var/lib/charm-registry")
	t.Setenv("CHARM_REGISTRY_SQLITE_PATH", "/srv/registry.sqlite")
	t.Setenv("CHARM_REGISTRY_STORAGE_BACKEND", "s3")
	t.Setenv("CHARM_REGISTRY_BLOB_DIR", "/srv/blobs")
	t.Setenv("CHARM_REGISTRY_OCI_STORAGE_BACKEND", "filesystem")
	t.Setenv("CHARM_REGISTRY_OCI_STORAGE_DIR", "/srv/oci")
	t.Setenv("CHARM_REGISTRY_S3_BUCKET", "custom-bucket")
	t.Setenv("CHARM_REGISTRY_S3_REGION", "eu-west-1")
	t.Setenv("CHARM_REGISTRY_S3_USE_PATH_STYLE", "false")
	t.Setenv("CHARM_REGISTRY_S3_DISABLE_TLS", "true")
	t.Setenv("CHARM_REGISTRY_MAX_JSON_BODY_BYTES", "2048")
	t.Setenv("CHARM_REGISTRY_MAX_ARCHIVE_FILE_BYTES", "4096")
	t.Setenv("CHARM_REGISTRY_MAX_UPLOAD_BYTES", "1024")
	t.Setenv("CHARM_REGISTRY_CHARMHUB_MAX_RESPONSE_BYTES", "512")
	t.Setenv("CHARM_REGISTRY_CHARMHUB_MAX_ARTIFACT_BYTES", "768")
	t.Setenv("CHARM_REGISTRY_OCI_MAX_MANIFEST_BYTES", "1536")
	t.Setenv("CHARM_REGISTRY_SERVER_READ_TIMEOUT", "5s")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")
	cfg, err := Load()

	require.NoError(t, err)
	assert.Equal(t, configSnapshot{
		ListenAddress:           ":9090",
		PublicAPIURL:            "http://localhost:8080",
		PublicStorageURL:        "http://localhost:8080",
		PublicRegistryURL:       "https://localhost:5000",
		DatabaseBackend:         DatabaseBackendSQLite,
		ResolvedDatabaseBackend: DatabaseBackendSQLite,
		StorageBackend:          StorageBackendS3,
		ResolvedStorageBackend:  StorageBackendS3,
		DataDir:                 "/var/lib/charm-registry",
		SQLitePath:              "/srv/registry.sqlite",
		BlobDir:                 "/srv/blobs",
		S3Bucket:                "custom-bucket",
		S3Region:                "eu-west-1",
		S3UsePathStyle:          false,
		S3DisableTLS:            true,
		OIDCUsernameClaim:       "preferred_username",
		OIDCDisplayNameClaim:    "name",
		OIDCEmailClaim:          "email",
		EnableInsecureDevAuth:   true,
		OCIListenAddress:        ":5000",
		OCIInternalURL:          "https://127.0.0.1:5000",
		OCIStorageBackend:       StorageBackendFilesystem,
		ResolvedOCIStorage:      StorageBackendFilesystem,
		OCIStorageDir:           "/srv/oci",
		OCIStorageBucket:        "charm-registry-oci",
		OCIStoragePrefix:        "oci",
		OCIStorageRegion:        "eu-west-1",
		OCIStorageEndpoint:      "",
		OCIStorageUsePathStyle:  false,
		OCISecretKey:            "oci-secret",
		OCIProjectPrefix:        "charm",
		MaxJSONBodyBytes:        2048,
		MaxArchiveFileBytes:     4096,
		MaxUploadBytes:          1024,
		CharmhubMaxResponse:     512,
		CharmhubMaxArtifact:     768,
		OCIMaxManifestBytes:     1536,
		ServerReadHeaderTimeout: 10 * time.Second,
		ServerReadTimeout:       5 * time.Second,
		ServerWriteTimeout:      30 * time.Second,
		ServerIdleTimeout:       120 * time.Second,
		ServerShutdownTimeout:   30 * time.Second,
		ServerMaxHeaderBytes:    1 << 20,
	}, snapshotConfig(cfg))

}

func TestLoadPaaSCharmEnvironmentFallbacks(t *testing.T) {
	t.Setenv("APP_PORT", "9090")
	t.Setenv("APP_PUBLIC_API_URL", "https://api.example.com/")
	t.Setenv("APP_PUBLIC_STORAGE_URL", "https://storage.example.com/")
	t.Setenv("APP_PUBLIC_REGISTRY_URL", "https://oci.example.com/")
	t.Setenv("POSTGRESQL_DB_CONNECT_STRING", "postgres://postgres:secret@postgresql:5432/registry")
	t.Setenv("S3_BUCKET", "registry-blobs")
	t.Setenv("S3_REGION", "eu-west-1")
	t.Setenv("S3_ENDPOINT", "https://s3.example.com/")
	t.Setenv("S3_ACCESS_KEY", "access")
	t.Setenv("S3_SECRET_KEY", "secret")
	t.Setenv("APP_ENABLE_INSECURE_DEV_AUTH", "true")
	t.Setenv("APP_OCI_INTERNAL_URL", "https://oci-internal.example.com/")
	t.Setenv("APP_OCI_S3_BUCKET", "registry-oci")
	t.Setenv("APP_OCI_S3_PREFIX", "images")
	t.Setenv("CHARM_REGISTRY_OCI_S3_REGION", "ca-central-1")
	t.Setenv("CHARM_REGISTRY_OCI_S3_ENDPOINT", "https://oci-s3.example.com/")
	t.Setenv("CHARM_REGISTRY_OCI_S3_ACCESS_KEY", "oci-access")
	t.Setenv("CHARM_REGISTRY_OCI_S3_SECRET_KEY", "oci-secret-access")
	t.Setenv("CHARM_REGISTRY_OCI_S3_USE_PATH_STYLE", "false")
	t.Setenv("APP_SECRET_KEY", "oci-secret")
	t.Setenv("APP_OCI_PROJECT_PREFIX", "-custom-")
	t.Setenv("APP_CHARMHUB_URL", "https://charmhub.example.com/")
	t.Setenv("APP_CHARMHUB_SYNC_INTERVAL", "10m")
	t.Setenv("CHARM_REGISTRY_ADMIN_USERNAMES", "")
	t.Setenv("APP_ADMIN_USERNAMES", "admin,owner")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, ":9090", cfg.ListenAddress)
	assert.Equal(t, "https://api.example.com", cfg.PublicAPIURL)
	assert.Equal(t, "https://storage.example.com", cfg.PublicStorageURL)
	assert.Equal(t, "https://oci.example.com", cfg.PublicRegistryURL)
	assert.Equal(t, "postgres://postgres:secret@postgresql:5432/registry", cfg.DatabaseURL)
	assert.Equal(t, "registry-blobs", cfg.S3Bucket)
	assert.Equal(t, "eu-west-1", cfg.S3Region)
	assert.Equal(t, "https://s3.example.com", cfg.S3Endpoint)
	assert.Equal(t, "access", cfg.S3AccessKeyID)
	assert.Equal(t, "secret", cfg.S3SecretAccessKey)
	assert.True(t, cfg.EnableInsecureDevAuth)
	assert.Equal(t, "https://oci-internal.example.com", cfg.OCIInternalURL)
	assert.Equal(t, "registry-oci", cfg.OCIStorageBucket)
	assert.Equal(t, "images", cfg.OCIStoragePrefix)
	assert.Equal(t, "ca-central-1", cfg.OCIStorageRegion)
	assert.Equal(t, "https://oci-s3.example.com", cfg.OCIStorageEndpoint)
	assert.Equal(t, "oci-access", cfg.OCIStorageAccessKeyID)
	assert.Equal(t, "oci-secret-access", cfg.OCIStorageSecretKey)
	assert.False(t, cfg.OCIStorageUsePathStyle)
	assert.Equal(t, "oci-secret", cfg.OCISecretKey)
	assert.Equal(t, "custom", cfg.OCIProjectPrefix)
	assert.Equal(t, "https://charmhub.example.com", cfg.CharmhubURL)
	assert.Equal(t, 10*time.Minute, cfg.CharmhubSyncInterval)
	assert.Equal(t, []string{"admin", "owner"}, cfg.AdminUsernames)

}

func TestLoadPaaSCharmOAuthFallbacks(t *testing.T) {
	t.Setenv("POSTGRESQL_DB_CONNECT_STRING", "postgres://postgres:secret@postgresql:5432/registry")
	t.Setenv("APP_SECRET_KEY", "oci-secret")
	t.Setenv("APP_HYDRA_API_BASE_URL", "https://auth.example.com/")
	t.Setenv("APP_HYDRA_CLIENT_ID", "registry")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "https://auth.example.com", cfg.OIDCIssuerURL)
	assert.Equal(t, "registry", cfg.OIDCClientID)

}

func TestLoadTrimsTrailingSlashes(t *testing.T) {
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_PUBLIC_API_URL", "https://api.example.com/")
	t.Setenv("CHARM_REGISTRY_PUBLIC_STORAGE_URL", "https://storage.example.com/")
	t.Setenv("CHARM_REGISTRY_PUBLIC_REGISTRY_URL", "https://oci.example.com/")
	t.Setenv("CHARM_REGISTRY_OCI_INTERNAL_URL", "https://oci-internal.example.com/")
	t.Setenv("CHARM_REGISTRY_S3_ENDPOINT", "https://s3.example.com/")
	t.Setenv("CHARM_REGISTRY_OIDC_ISSUER_URL", "https://auth.example.com/")
	t.Setenv("CHARM_REGISTRY_OIDC_CLIENT_ID", "registry")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "https://api.example.com", cfg.PublicAPIURL)
	assert.Equal(t, "https://storage.example.com", cfg.PublicStorageURL)
	assert.Equal(t, "https://oci.example.com", cfg.PublicRegistryURL)
	assert.Equal(t, "https://oci-internal.example.com", cfg.OCIInternalURL)
	assert.Equal(t, "https://s3.example.com", cfg.S3Endpoint)
	assert.Equal(t, "https://auth.example.com", cfg.OIDCIssuerURL)

}

func TestEnvBoolInvalidFallsBack(t *testing.T) {
	t.Setenv("TEST_BOOL", "not-a-bool")
	_, err := envBool("TEST_BOOL", true)
	require.Error(t, err)
	assert.ErrorContains(t, err, "cannot parse TEST_BOOL as bool")

}

func TestEnvBoolValid(t *testing.T) {
	t.Setenv("TEST_BOOL_T", "true")
	t.Setenv("TEST_BOOL_F", "false")
	valueTrue, err := envBool("TEST_BOOL_T", false)
	require.NoError(t, err)
	assert.True(t, valueTrue)
	valueFalse, err := envBool("TEST_BOOL_F", true)
	require.NoError(t, err)
	assert.False(t, valueFalse)

}

func TestEnvIntInvalidFallsBack(t *testing.T) {
	t.Setenv("TEST_INT", "not-a-number")
	_, err := envInt("TEST_INT", 42)
	require.Error(t, err)
	assert.ErrorContains(t, err, "cannot parse TEST_INT as int")
}

func TestEnvInt64InvalidFallsBack(t *testing.T) {
	t.Setenv("TEST_INT64", "not-a-number")
	_, err := envInt64("TEST_INT64", 42)
	require.Error(t, err)
	assert.ErrorContains(t, err, "cannot parse TEST_INT64 as int64")
}

func TestEnvDurationInvalidFallsBack(t *testing.T) {
	t.Setenv("TEST_DUR", "not-a-duration")
	_, err := envDuration("TEST_DUR", 10*time.Second)
	require.Error(t, err)
	assert.ErrorContains(t, err, "cannot parse TEST_DUR as duration")
}

func TestEnvMissingKeyFallsBack(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "fallback", env("NONEXISTENT_KEY_XYZZY_12345", "fallback"))
	boolValue, err := envBool("NONEXISTENT_KEY_XYZZY_12345", true)
	require.NoError(t, err)
	assert.True(t, boolValue)
	intValue, err := envInt("NONEXISTENT_KEY_XYZZY_12345", 99)
	require.NoError(t, err)
	assert.Equal(t, 99, intValue)
	int64Value, err := envInt64("NONEXISTENT_KEY_XYZZY_12345", 99)
	require.NoError(t, err)
	assert.Equal(t, int64(99), int64Value)
	durationValue, err := envDuration("NONEXISTENT_KEY_XYZZY_12345", 5*time.Second)
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, durationValue)
}

func TestEnvEmptyValueFallsBack(t *testing.T) {
	t.Setenv("EMPTY_VAL", "")
	assert.Equal(t, "fallback", env("EMPTY_VAL", "fallback"))
	boolValue, err := envBool("EMPTY_VAL", true)
	require.NoError(t, err)
	assert.True(t, boolValue)
	intValue, err := envInt("EMPTY_VAL", 7)
	require.NoError(t, err)
	assert.Equal(t, 7, intValue)
	int64Value, err := envInt64("EMPTY_VAL", 7)
	require.NoError(t, err)
	assert.Equal(t, int64(7), int64Value)
	durationValue, err := envDuration("EMPTY_VAL", 3*time.Second)
	require.NoError(t, err)
	assert.Equal(t, 3*time.Second, durationValue)

}

func TestLoadRejectsInvalidConfiguredValues(t *testing.T) {
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")
	t.Setenv("CHARM_REGISTRY_MAX_UPLOAD_BYTES", "abc")
	_, err := Load()
	require.Error(t, err)
	assert.ErrorContains(t, err, "CHARM_REGISTRY_MAX_UPLOAD_BYTES")

}

func TestLoadRejectsInvalidArchiveFileLimit(t *testing.T) {
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")
	t.Setenv("CHARM_REGISTRY_MAX_ARCHIVE_FILE_BYTES", "0")
	_, err := Load()
	require.Error(t, err)
	assert.ErrorContains(t, err, "CHARM_REGISTRY_MAX_ARCHIVE_FILE_BYTES")

}

func TestLoadTrimsOCIPrefixes(t *testing.T) {
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")
	t.Setenv("CHARM_REGISTRY_OCI_PROJECT_PREFIX", "-my-charms-")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "my-charms", cfg.OCIProjectPrefix)

}

func TestLoadRequiresCompleteOIDCConfig(t *testing.T) {
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_OIDC_ISSUER_URL", "https://auth.example.com")
	_, err := Load()
	require.Error(t, err)
	assert.ErrorContains(t, err, "must be set together")

}

func TestLoadRequiresAuthProviderWhenDevAuthDisabled(t *testing.T) {
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "false")
	_, err := Load()
	require.Error(t, err)
	assert.ErrorContains(t, err, "configure OIDC")

}

func TestLoadParsesAdminLists(t *testing.T) {
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")
	t.Setenv("CHARM_REGISTRY_ADMIN_SUBJECTS", "sub-1, sub-2")
	t.Setenv("CHARM_REGISTRY_ADMIN_EMAILS", "admin@example.com")
	t.Setenv("CHARM_REGISTRY_ADMIN_USERNAMES", "admin")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, []string{"sub-1", "sub-2"}, cfg.AdminSubjects)
	assert.Equal(t, []string{"admin@example.com"}, cfg.AdminEmails)
	assert.Equal(t, []string{"admin"}, cfg.AdminUsernames)
	assert.True(t, cfg.IsAdminIdentity("sub-1", "", ""))
	assert.True(t, cfg.IsAdminIdentity("", "admin@example.com", ""))
	assert.True(t, cfg.IsAdminIdentity("", "", "admin"))

}

func TestLoadRequiresOCISecret(t *testing.T) {
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")
	_, err := Load()
	require.Error(t, err)
	assert.ErrorContains(t, err, "CHARM_REGISTRY_OCI_SECRET_KEY is required")

}

func TestLoadRequiresCompleteOCITLSConfig(t *testing.T) {
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")
	t.Setenv("CHARM_REGISTRY_OCI_TLS_CERT_FILE", "cert.pem")
	_, err := Load()
	require.Error(t, err)
	assert.ErrorContains(t, err, "CHARM_REGISTRY_OCI_TLS_CERT_FILE")
	assert.ErrorContains(t, err, "CHARM_REGISTRY_OCI_TLS_KEY_FILE")

}

func TestValidateConfigRejectsNegativeIPRateLimit(t *testing.T) {
	t.Parallel()
	cfg := validMinConfig()
	cfg.IPRateLimit = -1
	_, err := validateConfig(cfg)
	require.Error(t, err)
	assert.ErrorContains(t, err, "CHARM_REGISTRY_IP_RATE_LIMIT must be >= 0")
}

func TestValidateConfigRejectsNegativeTokenRateLimit(t *testing.T) {
	t.Parallel()
	cfg := validMinConfig()
	cfg.TokenRateLimit = -1
	_, err := validateConfig(cfg)
	require.Error(t, err)
	assert.ErrorContains(t, err, "CHARM_REGISTRY_TOKEN_RATE_LIMIT must be >= 0")
}

func TestValidateConfigAcceptsZeroRateLimitsAsUnlimited(t *testing.T) {
	t.Parallel()
	cfg := validMinConfig()
	cfg.IPRateLimit = 0
	cfg.TokenRateLimit = 0
	_, err := validateConfig(cfg)
	require.NoError(t, err)
}

func TestValidateConfigAcceptsPositiveRateLimits(t *testing.T) {
	t.Parallel()
	cfg := validMinConfig()
	cfg.IPRateLimit = 30
	cfg.TokenRateLimit = 5
	_, err := validateConfig(cfg)
	require.NoError(t, err)
}

func TestValidateConfigRejectsZeroRateWindow(t *testing.T) {
	t.Parallel()
	cfg := validMinConfig()
	cfg.IPRateWindow = 0
	_, err := validateConfig(cfg)
	require.Error(t, err)
	assert.ErrorContains(t, err, "CHARM_REGISTRY_IP_RATE_WINDOW must be greater than zero")

	cfg = validMinConfig()
	cfg.TokenRateWindow = 0
	_, err = validateConfig(cfg)
	require.Error(t, err)
	assert.ErrorContains(t, err, "CHARM_REGISTRY_TOKEN_RATE_WINDOW must be greater than zero")
}

func validMinConfig() Config {
	return Config{
		DatabaseBackend:          DatabaseBackendSQLite,
		StorageBackend:           StorageBackendFilesystem,
		OCIStorageBackend:        StorageBackendFilesystem,
		SQLitePath:               "data/registry.sqlite",
		BlobDir:                  "data/blobs",
		OCIStorageDir:            "data/oci-registry",
		OCISecretKey:             "test-secret",
		EnableInsecureDevAuth:    true,
		MaxArchiveFileBytes:      10 << 20,
		CharmhubMaxResponseBytes: 4 << 20,
		CharmhubMaxArtifactBytes: 4 << 20,
		OCIMaxManifestBytes:      16 << 20,
		IPRateLimit:              30,
		IPRateWindow:             time.Minute,
		TokenRateLimit:           5,
		TokenRateWindow:          time.Minute,
	}
}
