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
	AdminSubjects           []string
	AdminEmails             []string
	AdminUsernames          []string
	EnableInsecureDevAuth   bool
	HarborURL               string
	HarborAPIURL            string
	HarborAdminUsername     string
	HarborAdminPassword     string
	HarborProjectPrefix     string
	HarborPullRobotPrefix   string
	HarborPushRobotPrefix   string
	HarborSecretKey         string
	HarborCAFile            string
	HarborInsecureTLS       bool
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
	s3UsePathStyle, err := envBool("CHARM_REGISTRY_S3_USE_PATH_STYLE", true)
	if err != nil {
		return Config{}, err
	}
	s3DisableTLS, err := envBool("CHARM_REGISTRY_S3_DISABLE_TLS", false)
	if err != nil {
		return Config{}, err
	}
	enableInsecureDevAuth, err := envBool("CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH", false)
	if err != nil {
		return Config{}, err
	}
	harborInsecureTLS, err := envBool("CHARM_REGISTRY_HARBOR_INSECURE_SKIP_VERIFY", false)
	if err != nil {
		return Config{}, err
	}
	serverReadHeaderTimeout, err := envDuration("CHARM_REGISTRY_SERVER_READ_HEADER_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	serverReadTimeout, err := envDuration("CHARM_REGISTRY_SERVER_READ_TIMEOUT", 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	serverWriteTimeout, err := envDuration("CHARM_REGISTRY_SERVER_WRITE_TIMEOUT", 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	serverIdleTimeout, err := envDuration("CHARM_REGISTRY_SERVER_IDLE_TIMEOUT", 120*time.Second)
	if err != nil {
		return Config{}, err
	}
	serverShutdownTimeout, err := envDuration("CHARM_REGISTRY_SERVER_SHUTDOWN_TIMEOUT", 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	serverMaxHeaderBytes, err := envInt("CHARM_REGISTRY_SERVER_MAX_HEADER_BYTES", 1<<20)
	if err != nil {
		return Config{}, err
	}
	maxJSONBodyBytes, err := envInt64("CHARM_REGISTRY_MAX_JSON_BODY_BYTES", 1<<20)
	if err != nil {
		return Config{}, err
	}
	maxUploadBytes, err := envInt64("CHARM_REGISTRY_MAX_UPLOAD_BYTES", 64<<20)
	if err != nil {
		return Config{}, err
	}

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
		S3UsePathStyle:          s3UsePathStyle,
		S3DisableTLS:            s3DisableTLS,
		OIDCIssuerURL:           strings.TrimRight(os.Getenv("CHARM_REGISTRY_OIDC_ISSUER_URL"), "/"),
		OIDCClientID:            os.Getenv("CHARM_REGISTRY_OIDC_CLIENT_ID"),
		OIDCUsernameClaim:       env("CHARM_REGISTRY_OIDC_USERNAME_CLAIM", "preferred_username"),
		OIDCDisplayNameClaim:    env("CHARM_REGISTRY_OIDC_DISPLAY_NAME_CLAIM", "name"),
		OIDCEmailClaim:          env("CHARM_REGISTRY_OIDC_EMAIL_CLAIM", "email"),
		AdminSubjects:           envCSV("CHARM_REGISTRY_ADMIN_SUBJECTS"),
		AdminEmails:             envCSV("CHARM_REGISTRY_ADMIN_EMAILS"),
		AdminUsernames:          envCSV("CHARM_REGISTRY_ADMIN_USERNAMES"),
		EnableInsecureDevAuth:   enableInsecureDevAuth,
		HarborURL:               strings.TrimRight(os.Getenv("CHARM_REGISTRY_HARBOR_URL"), "/"),
		HarborAPIURL:            strings.TrimRight(os.Getenv("CHARM_REGISTRY_HARBOR_API_URL"), "/"),
		HarborAdminUsername:     os.Getenv("CHARM_REGISTRY_HARBOR_ADMIN_USERNAME"),
		HarborAdminPassword:     os.Getenv("CHARM_REGISTRY_HARBOR_ADMIN_PASSWORD"),
		HarborProjectPrefix:     strings.Trim(env("CHARM_REGISTRY_HARBOR_PROJECT_PREFIX", "charm"), "-"),
		HarborPullRobotPrefix:   strings.Trim(env("CHARM_REGISTRY_HARBOR_PULL_ROBOT_PREFIX", "pull"), "-"),
		HarborPushRobotPrefix:   strings.Trim(env("CHARM_REGISTRY_HARBOR_PUSH_ROBOT_PREFIX", "push"), "-"),
		HarborSecretKey:         os.Getenv("CHARM_REGISTRY_HARBOR_SECRET_KEY"),
		HarborCAFile:            os.Getenv("CHARM_REGISTRY_HARBOR_CA_FILE"),
		HarborInsecureTLS:       harborInsecureTLS,
		ServerReadHeaderTimeout: serverReadHeaderTimeout,
		ServerReadTimeout:       serverReadTimeout,
		ServerWriteTimeout:      serverWriteTimeout,
		ServerIdleTimeout:       serverIdleTimeout,
		ServerShutdownTimeout:   serverShutdownTimeout,
		ServerMaxHeaderBytes:    serverMaxHeaderBytes,
		MaxJSONBodyBytes:        maxJSONBodyBytes,
		MaxUploadBytes:          maxUploadBytes,
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("CHARM_REGISTRY_DATABASE_URL is required")
	}
	if (cfg.OIDCIssuerURL == "") != (cfg.OIDCClientID == "") {
		return Config{}, fmt.Errorf(
			"CHARM_REGISTRY_OIDC_ISSUER_URL and CHARM_REGISTRY_OIDC_CLIENT_ID must be set together",
		)
	}
	if !cfg.EnableInsecureDevAuth && cfg.OIDCIssuerURL == "" && cfg.OIDCClientID == "" {
		return Config{}, fmt.Errorf(
			"configure OIDC or explicitly enable CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH for development",
		)
	}
	if cfg.HarborURL == "" {
		return Config{}, fmt.Errorf("CHARM_REGISTRY_HARBOR_URL is required")
	}
	if cfg.HarborAPIURL == "" {
		cfg.HarborAPIURL = cfg.HarborURL
	}
	if cfg.HarborAdminUsername == "" || cfg.HarborAdminPassword == "" {
		return Config{}, fmt.Errorf(
			"CHARM_REGISTRY_HARBOR_ADMIN_USERNAME and CHARM_REGISTRY_HARBOR_ADMIN_PASSWORD are required",
		)
	}
	if cfg.HarborSecretKey == "" {
		return Config{}, fmt.Errorf("CHARM_REGISTRY_HARBOR_SECRET_KEY is required")
	}

	return cfg, nil
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

func envBool(key string, fallback bool) (bool, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("parse %s as bool: %w", key, err)
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
		return 0, fmt.Errorf("parse %s as int: %w", key, err)
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
		return 0, fmt.Errorf("parse %s as int64: %w", key, err)
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
		return 0, fmt.Errorf("parse %s as duration: %w", key, err)
	}
	return value, nil
}

func envCSV(key string) []string {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
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
