package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DatabaseBackendAuto     = "auto"
	DatabaseBackendPostgres = "postgres"
	DatabaseBackendSQLite   = "sqlite"

	StorageBackendAuto       = "auto"
	StorageBackendS3         = "s3"
	StorageBackendFilesystem = "filesystem"
)

type Config struct {
	ListenAddress           string
	APITLSCertFile          string
	APITLSKeyFile           string
	PublicAPIURL            string
	PublicStorageURL        string
	PublicRegistryURL       string
	DatabaseBackend         string
	DatabaseURL             string
	DataDir                 string
	SQLitePath              string
	StorageBackend          string
	BlobDir                 string
	S3Bucket                string
	S3Region                string
	S3Endpoint              string
	S3AccessKeyID           string
	S3SecretAccessKey       string
	S3UsePathStyle          bool
	S3DisableTLS            bool
	OIDCIssuerURL           string
	OIDCClientID            string
	OIDCUsernameClaim       string
	OIDCDisplayNameClaim    string
	OIDCEmailClaim          string
	AdminSubjects           []string
	AdminEmails             []string
	AdminUsernames          []string
	EnableInsecureDevAuth   bool
	OCIListenAddress        string
	OCIInternalURL          string
	OCIStorageBackend       string
	OCIStorageDir           string
	OCIStorageBucket        string
	OCIStoragePrefix        string
	OCIStorageRegion        string
	OCIStorageEndpoint      string
	OCIStorageAccessKeyID   string
	OCIStorageSecretKey     string
	OCIStorageUsePathStyle  bool
	OCISecretKey            string
	OCIProjectPrefix        string
	OCIPullRobotPrefix      string
	OCIPushRobotPrefix      string
	OCITLSCertFile          string
	OCITLSKeyFile           string
	CharmhubURL             string
	CharmhubSyncInterval    time.Duration
	ServerReadHeaderTimeout time.Duration
	ServerReadTimeout       time.Duration
	ServerWriteTimeout      time.Duration
	ServerIdleTimeout       time.Duration
	ServerShutdownTimeout   time.Duration
	ServerMaxHeaderBytes    int
	MaxJSONBodyBytes        int64
	MaxArchiveFileBytes     int64
	MaxUploadBytes          int64

	CharmhubMaxResponseBytes int64
	CharmhubMaxArtifactBytes int64
	OCIMaxManifestBytes      int64
}

type parsedConfig struct {
	s3UsePathStyle          bool
	s3DisableTLS            bool
	ociStorageUsePathStyle  bool
	enableInsecureDevAuth   bool
	charmhubSyncInterval    time.Duration
	serverReadHeaderTimeout time.Duration
	serverReadTimeout       time.Duration
	serverWriteTimeout      time.Duration
	serverIdleTimeout       time.Duration
	serverShutdownTimeout   time.Duration
	serverMaxHeaderBytes    int
	maxJSONBodyBytes        int64
	maxArchiveFileBytes     int64
	maxUploadBytes          int64

	charmhubMaxResponseBytes int64
	charmhubMaxArtifactBytes int64
	ociMaxManifestBytes      int64
}

type parsedByteLimits struct {
	maxJSONBodyBytes         int64
	maxArchiveFileBytes      int64
	maxUploadBytes           int64
	charmhubMaxResponseBytes int64
	charmhubMaxArtifactBytes int64
	ociMaxManifestBytes      int64
}

// Load reads the registry configuration from environment variables.
func Load() (Config, error) {
	parsed, err := loadParsedConfig()
	if err != nil {
		return Config{}, err
	}
	dataDir := env("CHARM_REGISTRY_DATA_DIR", "data")
	databaseURL := envFallback("CHARM_REGISTRY_DATABASE_URL", "POSTGRESQL_DB_CONNECT_STRING", "")
	s3Endpoint := strings.TrimRight(envFallback("CHARM_REGISTRY_S3_ENDPOINT", "S3_ENDPOINT", ""), "/")
	s3AccessKey := envFallback("CHARM_REGISTRY_S3_ACCESS_KEY_ID", "S3_ACCESS_KEY", "")
	s3SecretKey := envFallback("CHARM_REGISTRY_S3_SECRET_ACCESS_KEY", "S3_SECRET_KEY", "")
	ociStorageEndpoint := strings.TrimRight(envFallback("CHARM_REGISTRY_OCI_S3_ENDPOINT", "APP_OCI_S3_ENDPOINT", envFallback("CHARM_REGISTRY_S3_ENDPOINT", "S3_ENDPOINT", "")), "/")
	ociStorageAccessKey := envFallback("CHARM_REGISTRY_OCI_S3_ACCESS_KEY", "APP_OCI_S3_ACCESS_KEY", envFallback("CHARM_REGISTRY_S3_ACCESS_KEY_ID", "S3_ACCESS_KEY", ""))
	ociStorageSecretKey := envFallback("CHARM_REGISTRY_OCI_S3_SECRET_KEY", "APP_OCI_S3_SECRET_KEY", envFallback("CHARM_REGISTRY_S3_SECRET_ACCESS_KEY", "S3_SECRET_KEY", ""))

	cfg := Config{
		ListenAddress:  listenAddress(),
		APITLSCertFile: os.Getenv("CHARM_REGISTRY_API_TLS_CERT_FILE"),
		APITLSKeyFile:  os.Getenv("CHARM_REGISTRY_API_TLS_KEY_FILE"),
		PublicAPIURL:   strings.TrimRight(envFallback("CHARM_REGISTRY_PUBLIC_API_URL", "APP_PUBLIC_API_URL", "http://localhost:8080"), "/"),
		PublicStorageURL: strings.TrimRight(
			envFallback("CHARM_REGISTRY_PUBLIC_STORAGE_URL", "APP_PUBLIC_STORAGE_URL", "http://localhost:8080"),
			"/",
		),
		PublicRegistryURL: strings.TrimRight(
			envFallback("CHARM_REGISTRY_PUBLIC_REGISTRY_URL", "APP_PUBLIC_REGISTRY_URL", "https://localhost:5000"),
			"/",
		),
		DatabaseBackend:         env("CHARM_REGISTRY_DATABASE_BACKEND", DatabaseBackendAuto),
		DatabaseURL:             databaseURL,
		DataDir:                 dataDir,
		SQLitePath:              env("CHARM_REGISTRY_SQLITE_PATH", filepath.Join(dataDir, "registry.sqlite")),
		StorageBackend:          env("CHARM_REGISTRY_STORAGE_BACKEND", StorageBackendAuto),
		BlobDir:                 env("CHARM_REGISTRY_BLOB_DIR", filepath.Join(dataDir, "blobs")),
		S3Bucket:                envFallback("CHARM_REGISTRY_S3_BUCKET", "S3_BUCKET", "charm-registry"),
		S3Region:                envFallback("CHARM_REGISTRY_S3_REGION", "S3_REGION", "us-east-1"),
		S3Endpoint:              s3Endpoint,
		S3AccessKeyID:           s3AccessKey,
		S3SecretAccessKey:       s3SecretKey,
		S3UsePathStyle:          parsed.s3UsePathStyle,
		S3DisableTLS:            parsed.s3DisableTLS,
		OIDCIssuerURL:           strings.TrimRight(envFallback("CHARM_REGISTRY_OIDC_ISSUER_URL", oauthEnv("API_BASE_URL"), ""), "/"),
		OIDCClientID:            envFallback("CHARM_REGISTRY_OIDC_CLIENT_ID", oauthEnv("CLIENT_ID"), ""),
		OIDCUsernameClaim:       env("CHARM_REGISTRY_OIDC_USERNAME_CLAIM", "preferred_username"),
		OIDCDisplayNameClaim:    env("CHARM_REGISTRY_OIDC_DISPLAY_NAME_CLAIM", "name"),
		OIDCEmailClaim:          env("CHARM_REGISTRY_OIDC_EMAIL_CLAIM", "email"),
		AdminSubjects:           envCSVFallback("CHARM_REGISTRY_ADMIN_SUBJECTS", "APP_ADMIN_SUBJECTS"),
		AdminEmails:             envCSVFallback("CHARM_REGISTRY_ADMIN_EMAILS", "APP_ADMIN_EMAILS"),
		AdminUsernames:          envCSVFallback("CHARM_REGISTRY_ADMIN_USERNAMES", "APP_ADMIN_USERNAMES"),
		EnableInsecureDevAuth:   parsed.enableInsecureDevAuth,
		OCIListenAddress:        envFallback("CHARM_REGISTRY_OCI_LISTEN", "APP_OCI_LISTEN", ":5000"),
		OCIInternalURL:          strings.TrimRight(envFallback("CHARM_REGISTRY_OCI_INTERNAL_URL", "APP_OCI_INTERNAL_URL", "https://127.0.0.1:5000"), "/"),
		OCIStorageBackend:       env("CHARM_REGISTRY_OCI_STORAGE_BACKEND", StorageBackendAuto),
		OCIStorageDir:           env("CHARM_REGISTRY_OCI_STORAGE_DIR", filepath.Join(dataDir, "oci-registry")),
		OCIStorageBucket:        envFallback("CHARM_REGISTRY_OCI_S3_BUCKET", "APP_OCI_S3_BUCKET", "charm-registry-oci"),
		OCIStoragePrefix:        strings.Trim(envFallback("CHARM_REGISTRY_OCI_S3_PREFIX", "APP_OCI_S3_PREFIX", "oci"), "/"),
		OCIStorageRegion:        envFallback("CHARM_REGISTRY_OCI_S3_REGION", "APP_OCI_S3_REGION", envFallback("CHARM_REGISTRY_S3_REGION", "S3_REGION", "us-east-1")),
		OCIStorageEndpoint:      ociStorageEndpoint,
		OCIStorageAccessKeyID:   ociStorageAccessKey,
		OCIStorageSecretKey:     ociStorageSecretKey,
		OCIStorageUsePathStyle:  parsed.ociStorageUsePathStyle,
		OCISecretKey:            envFallback("CHARM_REGISTRY_OCI_SECRET_KEY", "APP_SECRET_KEY", ""),
		OCIProjectPrefix:        strings.Trim(envFallback("CHARM_REGISTRY_OCI_PROJECT_PREFIX", "APP_OCI_PROJECT_PREFIX", "charm"), "-"),
		OCIPullRobotPrefix:      strings.Trim(envFallback("CHARM_REGISTRY_OCI_PULL_ROBOT_PREFIX", "APP_OCI_PULL_ROBOT_PREFIX", "pull"), "-"),
		OCIPushRobotPrefix:      strings.Trim(envFallback("CHARM_REGISTRY_OCI_PUSH_ROBOT_PREFIX", "APP_OCI_PUSH_ROBOT_PREFIX", "push"), "-"),
		OCITLSCertFile:          os.Getenv("CHARM_REGISTRY_OCI_TLS_CERT_FILE"),
		OCITLSKeyFile:           os.Getenv("CHARM_REGISTRY_OCI_TLS_KEY_FILE"),
		CharmhubURL:             strings.TrimRight(envFallback("CHARM_REGISTRY_CHARMHUB_URL", "APP_CHARMHUB_URL", "https://api.charmhub.io"), "/"),
		CharmhubSyncInterval:    parsed.charmhubSyncInterval,
		ServerReadHeaderTimeout: parsed.serverReadHeaderTimeout,
		ServerReadTimeout:       parsed.serverReadTimeout,
		ServerWriteTimeout:      parsed.serverWriteTimeout,
		ServerIdleTimeout:       parsed.serverIdleTimeout,
		ServerShutdownTimeout:   parsed.serverShutdownTimeout,
		ServerMaxHeaderBytes:    parsed.serverMaxHeaderBytes,
		MaxJSONBodyBytes:        parsed.maxJSONBodyBytes,
		MaxArchiveFileBytes:     parsed.maxArchiveFileBytes,
		MaxUploadBytes:          parsed.maxUploadBytes,

		CharmhubMaxResponseBytes: parsed.charmhubMaxResponseBytes,
		CharmhubMaxArtifactBytes: parsed.charmhubMaxArtifactBytes,
		OCIMaxManifestBytes:      parsed.ociMaxManifestBytes,
	}

	return validateConfig(cfg)
}

func loadParsedConfig() (parsedConfig, error) {
	s3UsePathStyle, err := envBoolFallback("CHARM_REGISTRY_S3_USE_PATH_STYLE", "APP_S3_USE_PATH_STYLE", true)
	if err != nil {
		return parsedConfig{}, err
	}
	ociStorageUsePathStyle, err := envBoolFallback(
		"CHARM_REGISTRY_OCI_S3_USE_PATH_STYLE",
		"APP_OCI_S3_USE_PATH_STYLE",
		s3UsePathStyle,
	)
	if err != nil {
		return parsedConfig{}, err
	}
	s3DisableTLS, err := envBool("CHARM_REGISTRY_S3_DISABLE_TLS", false)
	if err != nil {
		return parsedConfig{}, err
	}
	enableInsecureDevAuth, err := envBoolFallback("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", "APP_ENABLE_INSECURE_DEV_AUTH", false)
	if err != nil {
		return parsedConfig{}, err
	}
	charmhubSyncInterval, err := envDurationFallback("CHARM_REGISTRY_CHARMHUB_SYNC_INTERVAL", "APP_CHARMHUB_SYNC_INTERVAL", 15*time.Minute)
	if err != nil {
		return parsedConfig{}, err
	}
	serverReadHeaderTimeout, err := envDuration("CHARM_REGISTRY_SERVER_READ_HEADER_TIMEOUT", 10*time.Second)
	if err != nil {
		return parsedConfig{}, err
	}
	serverReadTimeout, err := envDuration("CHARM_REGISTRY_SERVER_READ_TIMEOUT", 30*time.Second)
	if err != nil {
		return parsedConfig{}, err
	}
	serverWriteTimeout, err := envDuration("CHARM_REGISTRY_SERVER_WRITE_TIMEOUT", 30*time.Second)
	if err != nil {
		return parsedConfig{}, err
	}
	serverIdleTimeout, err := envDuration("CHARM_REGISTRY_SERVER_IDLE_TIMEOUT", 120*time.Second)
	if err != nil {
		return parsedConfig{}, err
	}
	serverShutdownTimeout, err := envDuration("CHARM_REGISTRY_SERVER_SHUTDOWN_TIMEOUT", 30*time.Second)
	if err != nil {
		return parsedConfig{}, err
	}
	serverMaxHeaderBytes, err := envInt("CHARM_REGISTRY_SERVER_MAX_HEADER_BYTES", 1<<20)
	if err != nil {
		return parsedConfig{}, err
	}
	byteLimits, err := loadParsedByteLimits()
	if err != nil {
		return parsedConfig{}, err
	}

	return parsedConfig{
		s3UsePathStyle:          s3UsePathStyle,
		s3DisableTLS:            s3DisableTLS,
		ociStorageUsePathStyle:  ociStorageUsePathStyle,
		enableInsecureDevAuth:   enableInsecureDevAuth,
		charmhubSyncInterval:    charmhubSyncInterval,
		serverReadHeaderTimeout: serverReadHeaderTimeout,
		serverReadTimeout:       serverReadTimeout,
		serverWriteTimeout:      serverWriteTimeout,
		serverIdleTimeout:       serverIdleTimeout,
		serverShutdownTimeout:   serverShutdownTimeout,
		serverMaxHeaderBytes:    serverMaxHeaderBytes,
		maxJSONBodyBytes:        byteLimits.maxJSONBodyBytes,
		maxArchiveFileBytes:     byteLimits.maxArchiveFileBytes,
		maxUploadBytes:          byteLimits.maxUploadBytes,

		charmhubMaxResponseBytes: byteLimits.charmhubMaxResponseBytes,
		charmhubMaxArtifactBytes: byteLimits.charmhubMaxArtifactBytes,
		ociMaxManifestBytes:      byteLimits.ociMaxManifestBytes,
	}, nil
}

func loadParsedByteLimits() (parsedByteLimits, error) {
	maxJSONBodyBytes, err := envInt64("CHARM_REGISTRY_MAX_JSON_BODY_BYTES", 1<<20)
	if err != nil {
		return parsedByteLimits{}, err
	}
	maxArchiveFileBytes, err := envInt64("CHARM_REGISTRY_MAX_ARCHIVE_FILE_BYTES", 10<<20)
	if err != nil {
		return parsedByteLimits{}, err
	}
	maxUploadBytes, err := envInt64("CHARM_REGISTRY_MAX_UPLOAD_BYTES", 64<<20)
	if err != nil {
		return parsedByteLimits{}, err
	}
	charmhubMaxResponseBytes, err := envInt64("CHARM_REGISTRY_CHARMHUB_MAX_RESPONSE_BYTES", 4<<20)
	if err != nil {
		return parsedByteLimits{}, err
	}
	charmhubMaxArtifactBytes, err := envInt64("CHARM_REGISTRY_CHARMHUB_MAX_ARTIFACT_BYTES", maxUploadBytes)
	if err != nil {
		return parsedByteLimits{}, err
	}
	ociMaxManifestBytes, err := envInt64("CHARM_REGISTRY_OCI_MAX_MANIFEST_BYTES", 16<<20)
	if err != nil {
		return parsedByteLimits{}, err
	}
	return parsedByteLimits{
		maxJSONBodyBytes:         maxJSONBodyBytes,
		maxArchiveFileBytes:      maxArchiveFileBytes,
		maxUploadBytes:           maxUploadBytes,
		charmhubMaxResponseBytes: charmhubMaxResponseBytes,
		charmhubMaxArtifactBytes: charmhubMaxArtifactBytes,
		ociMaxManifestBytes:      ociMaxManifestBytes,
	}, nil
}

func validateConfig(cfg Config) (Config, error) {
	if err := validateBackendConfig(cfg); err != nil {
		return Config{}, err
	}
	if err := validateAuthConfig(cfg); err != nil {
		return Config{}, err
	}
	if err := validateOCIConfig(cfg); err != nil {
		return Config{}, err
	}
	if cfg.MaxArchiveFileBytes <= 0 {
		return Config{}, fmt.Errorf("cannot load config: CHARM_REGISTRY_MAX_ARCHIVE_FILE_BYTES must be greater than zero")
	}
	if cfg.CharmhubMaxResponseBytes <= 0 {
		return Config{}, fmt.Errorf("cannot load config: CHARM_REGISTRY_CHARMHUB_MAX_RESPONSE_BYTES must be greater than zero")
	}
	if cfg.CharmhubMaxArtifactBytes <= 0 {
		return Config{}, fmt.Errorf("cannot load config: CHARM_REGISTRY_CHARMHUB_MAX_ARTIFACT_BYTES must be greater than zero")
	}
	if cfg.OCIMaxManifestBytes <= 0 {
		return Config{}, fmt.Errorf("cannot load config: CHARM_REGISTRY_OCI_MAX_MANIFEST_BYTES must be greater than zero")
	}

	return cfg, nil
}

func validateBackendConfig(cfg Config) error {
	switch cfg.DatabaseBackend {
	case DatabaseBackendAuto, DatabaseBackendPostgres, DatabaseBackendSQLite:
	default:
		return fmt.Errorf("cannot load config: CHARM_REGISTRY_DATABASE_BACKEND must be auto, postgres, or sqlite")
	}
	switch cfg.StorageBackend {
	case StorageBackendAuto, StorageBackendS3, StorageBackendFilesystem:
	default:
		return fmt.Errorf("cannot load config: CHARM_REGISTRY_STORAGE_BACKEND must be auto, s3, or filesystem")
	}
	switch cfg.OCIStorageBackend {
	case StorageBackendAuto, StorageBackendS3, StorageBackendFilesystem:
	default:
		return fmt.Errorf("cannot load config: CHARM_REGISTRY_OCI_STORAGE_BACKEND must be auto, s3, or filesystem")
	}
	resolvedDatabaseBackend := cfg.ResolvedDatabaseBackend()
	resolvedStorageBackend := cfg.ResolvedStorageBackend()
	resolvedOCIStorageBackend := cfg.ResolvedOCIStorageBackend()
	if resolvedDatabaseBackend == DatabaseBackendPostgres && cfg.DatabaseURL == "" {
		return fmt.Errorf("cannot load config: CHARM_REGISTRY_DATABASE_URL is required when CHARM_REGISTRY_DATABASE_BACKEND=postgres")
	}
	if resolvedDatabaseBackend == DatabaseBackendSQLite && cfg.SQLitePath == "" {
		return fmt.Errorf("cannot load config: CHARM_REGISTRY_SQLITE_PATH is required when database backend resolves to sqlite")
	}
	if resolvedStorageBackend == StorageBackendFilesystem && cfg.BlobDir == "" {
		return fmt.Errorf("cannot load config: CHARM_REGISTRY_BLOB_DIR is required when storage backend resolves to filesystem")
	}
	if resolvedOCIStorageBackend == StorageBackendFilesystem && cfg.OCIStorageDir == "" {
		return fmt.Errorf("cannot load config: CHARM_REGISTRY_OCI_STORAGE_DIR is required when OCI storage backend resolves to filesystem")
	}
	return nil
}

func validateAuthConfig(cfg Config) error {
	if (cfg.OIDCIssuerURL == "") != (cfg.OIDCClientID == "") {
		return fmt.Errorf(
			"cannot load config: CHARM_REGISTRY_OIDC_ISSUER_URL and CHARM_REGISTRY_OIDC_CLIENT_ID must be set together",
		)
	}
	if !cfg.EnableInsecureDevAuth && cfg.OIDCIssuerURL == "" && cfg.OIDCClientID == "" {
		return fmt.Errorf(
			"cannot load config: configure OIDC or explicitly enable CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH for development",
		)
	}
	return nil
}

func validateOCIConfig(cfg Config) error {
	if cfg.OCISecretKey == "" {
		return fmt.Errorf("cannot load config: CHARM_REGISTRY_OCI_SECRET_KEY is required")
	}
	if cfg.ResolvedOCIStorageBackend() == StorageBackendS3 && cfg.OCIStorageBucket == "" {
		return fmt.Errorf("cannot load config: CHARM_REGISTRY_OCI_S3_BUCKET is required")
	}
	if (cfg.OCITLSCertFile == "") != (cfg.OCITLSKeyFile == "") {
		return fmt.Errorf(
			"cannot load config: CHARM_REGISTRY_OCI_TLS_CERT_FILE and CHARM_REGISTRY_OCI_TLS_KEY_FILE must be set together",
		)
	}
	if (cfg.APITLSCertFile == "") != (cfg.APITLSKeyFile == "") {
		return fmt.Errorf(
			"cannot load config: CHARM_REGISTRY_API_TLS_CERT_FILE and CHARM_REGISTRY_API_TLS_KEY_FILE must be set together",
		)
	}
	return nil
}

func (c Config) ResolvedDatabaseBackend() string {
	if c.DatabaseBackend != DatabaseBackendAuto {
		return c.DatabaseBackend
	}
	if c.DatabaseURL != "" {
		return DatabaseBackendPostgres
	}
	return DatabaseBackendSQLite
}

func (c Config) ResolvedStorageBackend() string {
	if c.StorageBackend != StorageBackendAuto {
		return c.StorageBackend
	}
	if c.S3Endpoint != "" || c.S3AccessKeyID != "" || c.S3SecretAccessKey != "" {
		return StorageBackendS3
	}
	return StorageBackendFilesystem
}

func (c Config) ResolvedOCIStorageBackend() string {
	if c.OCIStorageBackend != StorageBackendAuto {
		return c.OCIStorageBackend
	}
	if c.OCIStorageEndpoint != "" || c.OCIStorageAccessKeyID != "" || c.OCIStorageSecretKey != "" {
		return StorageBackendS3
	}
	return StorageBackendFilesystem
}

func (c Config) HasOIDC() bool {
	return c.OIDCIssuerURL != "" && c.OIDCClientID != ""
}

func (c Config) IsAdminIdentity(subject, email, username string) bool {
	return stringInSlice(subject, c.AdminSubjects) ||
		stringInSlice(email, c.AdminEmails) ||
		stringInSlice(username, c.AdminUsernames)
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}

func envFallback(primary, secondary, fallback string) string {
	if value, ok := os.LookupEnv(primary); ok && value != "" {
		return value
	}
	if secondary != "" {
		if value, ok := os.LookupEnv(secondary); ok && value != "" {
			return value
		}
	}
	return fallback
}

func listenAddress() string {
	if value := envFallback("CHARM_REGISTRY_LISTEN", "", ""); value != "" {
		return value
	}
	if port := envFallback("APP_PORT", "", ""); port != "" {
		return ":" + strings.TrimPrefix(port, ":")
	}
	return ":8080"
}

func envBool(key string, fallback bool) (bool, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("cannot parse %s as bool: %w", key, err)
	}
	return value, nil
}

func envBoolFallback(primary, secondary string, fallback bool) (bool, error) {
	if raw, ok := os.LookupEnv(primary); ok && raw != "" {
		return parseBoolEnv(primary, raw)
	}
	if raw, ok := os.LookupEnv(secondary); ok && raw != "" {
		return parseBoolEnv(secondary, raw)
	}
	return fallback, nil
}

func parseBoolEnv(key, raw string) (bool, error) {
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("cannot parse %s as bool: %w", key, err)
	}
	return value, nil
}

func envInt(key string, fallback int) (int, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %s as int: %w", key, err)
	}
	return value, nil
}

func envInt64(key string, fallback int64) (int64, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %s as int64: %w", key, err)
	}
	return value, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %s as duration: %w", key, err)
	}
	return value, nil
}

func envDurationFallback(primary, secondary string, fallback time.Duration) (time.Duration, error) {
	if raw, ok := os.LookupEnv(primary); ok && raw != "" {
		return parseDurationEnv(primary, raw)
	}
	if raw, ok := os.LookupEnv(secondary); ok && raw != "" {
		return parseDurationEnv(secondary, raw)
	}
	return fallback, nil
}

func parseDurationEnv(key, raw string) (time.Duration, error) {
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %s as duration: %w", key, err)
	}
	return value, nil
}

func envCSVFallback(primary, secondary string) []string {
	if raw, ok := os.LookupEnv(primary); ok && raw != "" {
		return csvValues(raw)
	}
	return csvValues(os.Getenv(secondary))
}

func csvValues(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			values = append(values, trimmed)
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func oauthEnv(suffix string) string {
	for _, entry := range os.Environ() {
		key, value, found := strings.Cut(entry, "=")
		if !found || value == "" {
			continue
		}
		if strings.HasPrefix(key, "APP_") && strings.HasSuffix(key, "_"+suffix) {
			return key
		}
	}
	return ""
}

func stringInSlice(candidate string, values []string) bool {
	if candidate == "" {
		return false
	}
	for _, value := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
