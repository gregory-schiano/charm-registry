package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddress           string
	PublicAPIURL            string
	PublicStorageURL        string
	PublicRegistryURL       string
	DatabaseURL             string
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
	EnableInsecureDevAuth   bool
	RegistryUsername        string
	RegistryPassword        string
	RegistryRepositoryRoot  string
	ServerReadHeaderTimeout time.Duration
	ServerReadTimeout       time.Duration
	ServerWriteTimeout      time.Duration
	ServerIdleTimeout       time.Duration
	ServerShutdownTimeout   time.Duration
	ServerMaxHeaderBytes    int
	MaxJSONBodyBytes        int64
	MaxUploadBytes          int64
}

// Load reads the registry configuration from environment variables.
//
// The following errors may be returned:
// - `CHARM_REGISTRY_DATABASE_URL` is not set.
func Load() (Config, error) {
	cfg := Config{
		ListenAddress: env("CHARM_REGISTRY_LISTEN", ":8080"),
		PublicAPIURL:  strings.TrimRight(env("CHARM_REGISTRY_PUBLIC_API_URL", "http://localhost:8080"), "/"),
		PublicStorageURL: strings.TrimRight(
			env("CHARM_REGISTRY_PUBLIC_STORAGE_URL", "http://localhost:8080"),
			"/",
		),
		PublicRegistryURL: strings.TrimRight(
			env("CHARM_REGISTRY_PUBLIC_REGISTRY_URL", "http://localhost:5000"),
			"/",
		),
		DatabaseURL:             os.Getenv("CHARM_REGISTRY_DATABASE_URL"),
		S3Bucket:                env("CHARM_REGISTRY_S3_BUCKET", "charm-registry"),
		S3Region:                env("CHARM_REGISTRY_S3_REGION", "us-east-1"),
		S3Endpoint:              strings.TrimRight(os.Getenv("CHARM_REGISTRY_S3_ENDPOINT"), "/"),
		S3AccessKeyID:           os.Getenv("CHARM_REGISTRY_S3_ACCESS_KEY_ID"),
		S3SecretAccessKey:       os.Getenv("CHARM_REGISTRY_S3_SECRET_ACCESS_KEY"),
		S3UsePathStyle:          envBool("CHARM_REGISTRY_S3_USE_PATH_STYLE", true),
		S3DisableTLS:            envBool("CHARM_REGISTRY_S3_DISABLE_TLS", false),
		OIDCIssuerURL:           strings.TrimRight(os.Getenv("CHARM_REGISTRY_OIDC_ISSUER_URL"), "/"),
		OIDCClientID:            os.Getenv("CHARM_REGISTRY_OIDC_CLIENT_ID"),
		OIDCUsernameClaim:       env("CHARM_REGISTRY_OIDC_USERNAME_CLAIM", "preferred_username"),
		OIDCDisplayNameClaim:    env("CHARM_REGISTRY_OIDC_DISPLAY_NAME_CLAIM", "name"),
		OIDCEmailClaim:          env("CHARM_REGISTRY_OIDC_EMAIL_CLAIM", "email"),
		EnableInsecureDevAuth:   envBool("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", true),
		RegistryUsername:        env("CHARM_REGISTRY_REGISTRY_USERNAME", "registry"),
		RegistryPassword:        env("CHARM_REGISTRY_REGISTRY_PASSWORD", "registry-secret"),
		RegistryRepositoryRoot:  strings.Trim(env("CHARM_REGISTRY_REGISTRY_REPOSITORY_ROOT", "charms"), "/"),
		ServerReadHeaderTimeout: envDuration("CHARM_REGISTRY_SERVER_READ_HEADER_TIMEOUT", 10*time.Second),
		ServerReadTimeout:       envDuration("CHARM_REGISTRY_SERVER_READ_TIMEOUT", 30*time.Second),
		ServerWriteTimeout:      envDuration("CHARM_REGISTRY_SERVER_WRITE_TIMEOUT", 30*time.Second),
		ServerIdleTimeout:       envDuration("CHARM_REGISTRY_SERVER_IDLE_TIMEOUT", 120*time.Second),
		ServerShutdownTimeout:   envDuration("CHARM_REGISTRY_SERVER_SHUTDOWN_TIMEOUT", 30*time.Second),
		ServerMaxHeaderBytes:    envInt("CHARM_REGISTRY_SERVER_MAX_HEADER_BYTES", 1<<20),
		MaxJSONBodyBytes:        envInt64("CHARM_REGISTRY_MAX_JSON_BODY_BYTES", 1<<20),
		MaxUploadBytes:          envInt64("CHARM_REGISTRY_MAX_UPLOAD_BYTES", 64<<20),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("CHARM_REGISTRY_DATABASE_URL is required")
	}

	return cfg, nil
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envInt64(key string, fallback int64) int64 {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fallback
	}
	return value
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return value
}
