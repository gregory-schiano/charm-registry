package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {

	// Arrange
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "")

	// Act
	_, err := Load()

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot load config:")
	assert.Contains(t, err.Error(), "CHARM_REGISTRY_DATABASE_URL is required")

}

func TestLoadDefaults(t *testing.T) {

	// Arrange
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")

	// Act
	cfg, err := Load()

	// Assert
	require.NoError(t, err)
	assert.Equal(t, ":8080", cfg.ListenAddress)
	assert.Equal(t, "http://localhost:8080", cfg.PublicAPIURL)
	assert.Equal(t, "http://localhost:8080", cfg.PublicStorageURL)
	assert.Equal(t, "https://localhost:5000", cfg.PublicRegistryURL)
	assert.Equal(t, "charm-registry", cfg.S3Bucket)
	assert.Equal(t, "us-east-1", cfg.S3Region)
	assert.True(t, cfg.S3UsePathStyle)
	assert.False(t, cfg.S3DisableTLS)
	assert.Equal(t, "preferred_username", cfg.OIDCUsernameClaim)
	assert.Equal(t, "name", cfg.OIDCDisplayNameClaim)
	assert.Equal(t, "email", cfg.OIDCEmailClaim)
	assert.True(t, cfg.EnableInsecureDevAuth)
	assert.Equal(t, ":5000", cfg.OCIListenAddress)
	assert.Equal(t, "https://127.0.0.1:5000", cfg.OCIInternalURL)
	assert.Equal(t, "charm-registry-oci", cfg.OCIStorageBucket)
	assert.Equal(t, "oci", cfg.OCIStoragePrefix)
	assert.Equal(t, "us-east-1", cfg.OCIStorageRegion)
	assert.Equal(t, "", cfg.OCIStorageEndpoint)
	assert.True(t, cfg.OCIStorageUsePathStyle)
	assert.Equal(t, "oci-secret", cfg.OCISecretKey)
	assert.Equal(t, "charm", cfg.OCIProjectPrefix)
	assert.Equal(t, int64(1<<20), cfg.MaxJSONBodyBytes)
	assert.Equal(t, int64(10<<20), cfg.MaxArchiveFileBytes)
	assert.Equal(t, int64(64<<20), cfg.MaxUploadBytes)
	assert.Equal(t, 10*time.Second, cfg.ServerReadHeaderTimeout)
	assert.Equal(t, 30*time.Second, cfg.ServerReadTimeout)
	assert.Equal(t, 30*time.Second, cfg.ServerWriteTimeout)
	assert.Equal(t, 120*time.Second, cfg.ServerIdleTimeout)
	assert.Equal(t, 30*time.Second, cfg.ServerShutdownTimeout)
	assert.Equal(t, 1<<20, cfg.ServerMaxHeaderBytes)

}

func TestLoadCustomValues(t *testing.T) {

	// Arrange
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_LISTEN", ":9090")
	t.Setenv("CHARM_REGISTRY_S3_BUCKET", "custom-bucket")
	t.Setenv("CHARM_REGISTRY_S3_REGION", "eu-west-1")
	t.Setenv("CHARM_REGISTRY_S3_USE_PATH_STYLE", "false")
	t.Setenv("CHARM_REGISTRY_S3_DISABLE_TLS", "true")
	t.Setenv("CHARM_REGISTRY_MAX_JSON_BODY_BYTES", "2048")
	t.Setenv("CHARM_REGISTRY_MAX_ARCHIVE_FILE_BYTES", "4096")
	t.Setenv("CHARM_REGISTRY_MAX_UPLOAD_BYTES", "1024")
	t.Setenv("CHARM_REGISTRY_SERVER_READ_TIMEOUT", "5s")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")

	// Act
	cfg, err := Load()

	// Assert
	require.NoError(t, err)
	assert.Equal(t, ":9090", cfg.ListenAddress)
	assert.Equal(t, "custom-bucket", cfg.S3Bucket)
	assert.Equal(t, "eu-west-1", cfg.S3Region)
	assert.False(t, cfg.S3UsePathStyle)
	assert.True(t, cfg.S3DisableTLS)
	assert.Equal(t, int64(2048), cfg.MaxJSONBodyBytes)
	assert.Equal(t, int64(4096), cfg.MaxArchiveFileBytes)
	assert.Equal(t, int64(1024), cfg.MaxUploadBytes)
	assert.Equal(t, 5*time.Second, cfg.ServerReadTimeout)

}

func TestLoadPaaSCharmEnvironmentFallbacks(t *testing.T) {

	// Arrange
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

	// Act
	cfg, err := Load()

	// Assert
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

	// Arrange
	t.Setenv("POSTGRESQL_DB_CONNECT_STRING", "postgres://postgres:secret@postgresql:5432/registry")
	t.Setenv("APP_SECRET_KEY", "oci-secret")
	t.Setenv("APP_HYDRA_API_BASE_URL", "https://auth.example.com/")
	t.Setenv("APP_HYDRA_CLIENT_ID", "registry")

	// Act
	cfg, err := Load()

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "https://auth.example.com", cfg.OIDCIssuerURL)
	assert.Equal(t, "registry", cfg.OIDCClientID)

}

func TestLoadTrimsTrailingSlashes(t *testing.T) {

	// Arrange
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_PUBLIC_API_URL", "https://api.example.com/")
	t.Setenv("CHARM_REGISTRY_PUBLIC_STORAGE_URL", "https://storage.example.com/")
	t.Setenv("CHARM_REGISTRY_PUBLIC_REGISTRY_URL", "https://oci.example.com/")
	t.Setenv("CHARM_REGISTRY_OCI_INTERNAL_URL", "https://oci-internal.example.com/")
	t.Setenv("CHARM_REGISTRY_S3_ENDPOINT", "https://s3.example.com/")
	t.Setenv("CHARM_REGISTRY_OIDC_ISSUER_URL", "https://auth.example.com/")
	t.Setenv("CHARM_REGISTRY_OIDC_CLIENT_ID", "registry")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")

	// Act
	cfg, err := Load()

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "https://api.example.com", cfg.PublicAPIURL)
	assert.Equal(t, "https://storage.example.com", cfg.PublicStorageURL)
	assert.Equal(t, "https://oci.example.com", cfg.PublicRegistryURL)
	assert.Equal(t, "https://oci-internal.example.com", cfg.OCIInternalURL)
	assert.Equal(t, "https://s3.example.com", cfg.S3Endpoint)
	assert.Equal(t, "https://auth.example.com", cfg.OIDCIssuerURL)

}

func TestEnvBoolInvalidFallsBack(t *testing.T) {

	// Arrange
	t.Setenv("TEST_BOOL", "not-a-bool")

	// Act + Assert
	_, err := envBool("TEST_BOOL", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot parse TEST_BOOL as bool")

}

func TestEnvBoolValid(t *testing.T) {

	// Arrange
	t.Setenv("TEST_BOOL_T", "true")
	t.Setenv("TEST_BOOL_F", "false")

	// Act + Assert
	valueTrue, err := envBool("TEST_BOOL_T", false)
	require.NoError(t, err)
	assert.True(t, valueTrue)
	valueFalse, err := envBool("TEST_BOOL_F", true)
	require.NoError(t, err)
	assert.False(t, valueFalse)

}

func TestEnvIntInvalidFallsBack(t *testing.T) {

	// Act + Assert
	t.Setenv("TEST_INT", "not-a-number")
	_, err := envInt("TEST_INT", 42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot parse TEST_INT as int")
}

func TestEnvInt64InvalidFallsBack(t *testing.T) {

	// Act + Assert
	t.Setenv("TEST_INT64", "not-a-number")
	_, err := envInt64("TEST_INT64", 42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot parse TEST_INT64 as int64")
}

func TestEnvDurationInvalidFallsBack(t *testing.T) {

	// Act + Assert
	t.Setenv("TEST_DUR", "not-a-duration")
	_, err := envDuration("TEST_DUR", 10*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot parse TEST_DUR as duration")
}

func TestEnvMissingKeyFallsBack(t *testing.T) {

	// Act + Assert
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

	// Arrange
	t.Setenv("EMPTY_VAL", "")

	// Act + Assert
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

	// Arrange
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")
	t.Setenv("CHARM_REGISTRY_MAX_UPLOAD_BYTES", "abc")

	// Act
	_, err := Load()

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CHARM_REGISTRY_MAX_UPLOAD_BYTES")

}

func TestLoadRejectsInvalidArchiveFileLimit(t *testing.T) {

	// Arrange
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")
	t.Setenv("CHARM_REGISTRY_MAX_ARCHIVE_FILE_BYTES", "0")

	// Act
	_, err := Load()

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CHARM_REGISTRY_MAX_ARCHIVE_FILE_BYTES")

}

func TestLoadTrimsOCIPrefixes(t *testing.T) {

	// Arrange
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")
	t.Setenv("CHARM_REGISTRY_OCI_PROJECT_PREFIX", "-my-charms-")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")

	// Act
	cfg, err := Load()

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "my-charms", cfg.OCIProjectPrefix)

}

func TestLoadRequiresCompleteOIDCConfig(t *testing.T) {

	// Arrange
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_OIDC_ISSUER_URL", "https://auth.example.com")

	// Act
	_, err := Load()

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be set together")

}

func TestLoadRequiresAuthProviderWhenDevAuthDisabled(t *testing.T) {

	// Arrange
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "false")

	// Act
	_, err := Load()

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "configure OIDC")

}

func TestLoadParsesAdminLists(t *testing.T) {

	// Arrange
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")
	t.Setenv("CHARM_REGISTRY_ADMIN_SUBJECTS", "sub-1, sub-2")
	t.Setenv("CHARM_REGISTRY_ADMIN_EMAILS", "admin@example.com")
	t.Setenv("CHARM_REGISTRY_ADMIN_USERNAMES", "admin")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")

	// Act
	cfg, err := Load()

	// Assert
	require.NoError(t, err)
	assert.Equal(t, []string{"sub-1", "sub-2"}, cfg.AdminSubjects)
	assert.Equal(t, []string{"admin@example.com"}, cfg.AdminEmails)
	assert.Equal(t, []string{"admin"}, cfg.AdminUsernames)
	assert.True(t, cfg.IsAdminIdentity("sub-1", "", ""))
	assert.True(t, cfg.IsAdminIdentity("", "admin@example.com", ""))
	assert.True(t, cfg.IsAdminIdentity("", "", "admin"))

}

func TestLoadRequiresOCISecret(t *testing.T) {

	// Arrange
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")

	// Act
	_, err := Load()

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CHARM_REGISTRY_OCI_SECRET_KEY is required")

}

func TestLoadRequiresCompleteOCITLSConfig(t *testing.T) {

	// Arrange
	t.Setenv("CHARM_REGISTRY_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "true")
	t.Setenv("CHARM_REGISTRY_OCI_SECRET_KEY", "oci-secret")
	t.Setenv("CHARM_REGISTRY_OCI_TLS_CERT_FILE", "cert.pem")

	// Act
	_, err := Load()

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CHARM_REGISTRY_OCI_TLS_CERT_FILE")
	assert.Contains(t, err.Error(), "CHARM_REGISTRY_OCI_TLS_KEY_FILE")

}
