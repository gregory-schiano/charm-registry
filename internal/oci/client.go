package oci

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/distribution/distribution/v3/configuration"
	"github.com/distribution/distribution/v3/registry/handlers"
	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/distribution/distribution/v3/registry/storage/driver/factory"
	_ "github.com/distribution/distribution/v3/registry/storage/driver/filesystem"
	_ "github.com/distribution/distribution/v3/registry/storage/driver/s3-aws"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
)

var invalidNamePattern = regexp.MustCompile(`[^a-z0-9-]+`)

const (
	keyDerivationIterations = 210_000
	keyDerivationLength     = 32
	defaultMaxManifestBytes = 16 << 20
)

var keyDerivationSalt = []byte("charm-registry/oci-secret/v1")

const ociKeyVersion = "v1"

type Client struct {
	handler          http.Handler
	driver           storagedriver.StorageDriver
	repository       repo.PackageRepo
	publicRegistry   string
	internalRegistry string
	projectPrefix    string
	pullRobotPrefix  string
	pushRobotPrefix  string
	secretKey        []byte
	transport        *http.Transport
	maxManifestBytes int64
}

type ociStorageConfig struct {
	Driver string
	Params configuration.Parameters
}

func New(ctx context.Context, cfg config.Config, repository repo.PackageRepo) (*Client, error) {
	storage := storageParameters(cfg)
	driver, err := factory.Create(ctx, storage.Driver, storage.Params)
	if err != nil {
		return nil, fmt.Errorf("cannot create OCI storage driver: %w", err)
	}
	transport, err := internalTransport(cfg)
	if err != nil {
		return nil, err
	}
	distConfig := &configuration.Configuration{
		Version: configuration.CurrentVersion,
		Log: configuration.Log{
			Level: "info",
		},
		Storage: configuration.Storage{
			storage.Driver: storage.Params,
			"delete": configuration.Parameters{
				"enabled": true,
			},
			"redirect": configuration.Parameters{
				"disable": true,
			},
		},
		HTTP: configuration.HTTP{
			Secret:       cfg.OCISecretKey,
			RelativeURLs: true,
		},
	}
	registry := handlers.NewApp(ctx, distConfig)
	client := &Client{
		driver:           driver,
		repository:       repository,
		publicRegistry:   cfg.PublicRegistryURL,
		internalRegistry: cfg.OCIInternalURL,
		projectPrefix:    cfg.OCIProjectPrefix,
		pullRobotPrefix:  cfg.OCIPullRobotPrefix,
		pushRobotPrefix:  cfg.OCIPushRobotPrefix,
		transport:        transport,
		maxManifestBytes: cfg.OCIMaxManifestBytes,
	}
	secretKey, err := deriveKey(cfg.OCISecretKey)
	if err != nil {
		return nil, fmt.Errorf("cannot derive OCI secret key: %w", err)
	}
	client.secretKey = secretKey
	client.handler = client.authMiddleware(registry)
	return client, nil
}

func internalTransport(cfg config.Config) (*http.Transport, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	parsed, err := url.Parse(cfg.OCIInternalURL)
	if err != nil || parsed.Scheme != "https" || cfg.OCITLSCertFile == "" {
		return transport, nil
	}
	certPEM, err := os.ReadFile(cfg.OCITLSCertFile)
	if err != nil {
		return nil, fmt.Errorf("cannot read OCI TLS certificate for internal registry client: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(certPEM) {
		return nil, fmt.Errorf("cannot trust OCI TLS certificate for internal registry client: no certificates found in %s", cfg.OCITLSCertFile)
	}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = new(tls.Config)
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.RootCAs = roots
	return transport, nil
}

func storageParameters(cfg config.Config) ociStorageConfig {
	if cfg.ResolvedOCIStorageBackend() == config.StorageBackendFilesystem {
		return ociStorageConfig{
			Driver: "filesystem",
			Params: configuration.Parameters{
				"rootdirectory": cfg.OCIStorageDir,
			},
		}
	}
	return ociStorageConfig{
		Driver: "s3",
		Params: configuration.Parameters{
			"accesskey":      cfg.OCIStorageAccessKeyID,
			"secretkey":      cfg.OCIStorageSecretKey,
			"region":         cfg.OCIStorageRegion,
			"regionendpoint": cfg.OCIStorageEndpoint,
			"bucket":         cfg.OCIStorageBucket,
			"secure":         !cfg.S3DisableTLS,
			"skipverify":     cfg.S3DisableTLS,
			"v4auth":         true,
			"forcepathstyle": cfg.OCIStorageUsePathStyle,
			"rootdirectory":  cfg.OCIStoragePrefix,
			"chunksize":      10 << 20,
		},
	}
}

func (c *Client) Handler() http.Handler {
	return c.handler
}

func (c *Client) Close() error {
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
	return nil
}

func (c *Client) SyncPackage(_ context.Context, pkg core.Package) (core.Package, error) {
	if pkg.OCIProject == "" {
		pkg.OCIProject = c.projectName(pkg.Name)
	}
	var err error
	if pkg.OCIPushRobot == nil || pkg.OCIPushRobot.Username == "" || pkg.OCIPushRobot.EncryptedSecret == "" {
		pkg.OCIPushRobot, err = c.newCredential(c.robotName(c.pushRobotPrefix, pkg.ID))
		if err != nil {
			return core.Package{}, err
		}
	}
	if pkg.OCIPullRobot == nil || pkg.OCIPullRobot.Username == "" || pkg.OCIPullRobot.EncryptedSecret == "" {
		pkg.OCIPullRobot, err = c.newCredential(c.robotName(c.pullRobotPrefix, pkg.ID))
		if err != nil {
			return core.Package{}, err
		}
	}
	now := time.Now().UTC()
	pkg.OCISyncedAt = &now
	return pkg, nil
}

func (c *Client) ImageReference(pkg core.Package, resourceName string) (string, error) {
	if pkg.OCIProject == "" {
		return "", fmt.Errorf("cannot resolve image reference: OCI project not configured for package %s", pkg.Name)
	}
	host := registryHost(c.publicRegistry)
	if host == "" {
		return "", fmt.Errorf("cannot resolve image reference: registry host is not configured")
	}
	return host + "/" + pkg.OCIProject + "/" + sanitizeName(resourceName), nil
}

func (c *Client) Credentials(pkg core.Package, pull bool) (string, string, error) {
	credential := pkg.OCIPushRobot
	if pull {
		credential = pkg.OCIPullRobot
	}
	if credential == nil || credential.Username == "" || credential.EncryptedSecret == "" {
		return "", "", fmt.Errorf("cannot read OCI credentials: credentials are not available")
	}
	secret, err := c.decrypt(credential.EncryptedSecret)
	if err != nil {
		return "", "", err
	}
	return credential.Username, secret, nil
}

func (c *Client) MirrorImage(
	ctx context.Context,
	pkg core.Package,
	resourceName, sourceImage, sourceUsername, sourcePassword string,
) (string, error) {
	if sourceImage == "" {
		return "", fmt.Errorf("cannot mirror OCI image: source image reference is required")
	}
	targetRepository, err := c.internalImageReference(pkg, resourceName)
	if err != nil {
		return "", err
	}
	pushUsername, pushPassword, err := c.Credentials(pkg, false)
	if err != nil {
		return "", err
	}
	sourceRef, err := name.ParseReference(sourceImage)
	if err != nil {
		return "", fmt.Errorf("cannot parse source image reference: %w", err)
	}
	sourceOptions := []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuth(authn.FromConfig(authn.AuthConfig{
			Username: sourceUsername,
			Password: sourcePassword,
		})),
	}
	if err := c.checkManifestHead(sourceRef, sourceOptions...); err != nil {
		return "", err
	}
	sourceDescriptor, err := remote.Get(sourceRef, sourceOptions...)
	if err != nil {
		return "", fmt.Errorf("cannot fetch source OCI image: %w", err)
	}
	if err := c.checkManifestDescriptor(sourceDescriptor.Descriptor, int64(len(sourceDescriptor.Manifest))); err != nil {
		return "", err
	}
	sourceImageObject, err := sourceDescriptor.Image()
	if err != nil {
		return "", fmt.Errorf("cannot read source OCI image: %w", err)
	}
	digest, err := sourceImageObject.Digest()
	if err != nil {
		return "", fmt.Errorf("cannot calculate mirrored OCI image digest: %w", err)
	}
	targetRef, err := name.ParseReference(targetRepository+":"+digestTag(digest.String()), c.nameOptions()...)
	if err != nil {
		return "", fmt.Errorf("cannot parse target image reference: %w", err)
	}
	if err := remote.Write(
		targetRef,
		sourceImageObject,
		remote.WithContext(ctx),
		remote.WithTransport(c.transport),
		remote.WithAuth(authn.FromConfig(authn.AuthConfig{
			Username: pushUsername,
			Password: pushPassword,
		})),
	); err != nil {
		return "", fmt.Errorf("cannot push mirrored OCI image: %w", err)
	}
	return digest.String(), nil
}

func (c *Client) checkManifestHead(ref name.Reference, options ...remote.Option) error {
	descriptor, err := remote.Head(ref, options...)
	if err != nil {
		return fmt.Errorf("cannot inspect source OCI image manifest: %w", err)
	}
	return c.checkManifestDescriptor(*descriptor, descriptor.Size)
}

func (c *Client) checkManifestDescriptor(descriptor v1.Descriptor, bodySize int64) error {
	maxBytes := c.maxManifestBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxManifestBytes
	}
	if descriptor.Size > maxBytes {
		return fmt.Errorf("source OCI manifest is %d bytes, exceeds %d bytes", descriptor.Size, maxBytes)
	}
	if bodySize > maxBytes {
		return fmt.Errorf("source OCI manifest body is %d bytes, exceeds %d bytes", bodySize, maxBytes)
	}
	return nil
}

func (c *Client) DeleteImage(ctx context.Context, pkg core.Package, resourceName, digest string) error {
	if digest == "" {
		return nil
	}
	pushUsername, pushPassword, err := c.Credentials(pkg, false)
	if err != nil {
		return err
	}
	imageReference, err := c.internalImageReference(pkg, resourceName)
	if err != nil {
		return err
	}
	ref, err := name.ParseReference(imageReference+"@"+digest, c.nameOptions()...)
	if err != nil {
		return fmt.Errorf("cannot parse OCI image digest reference: %w", err)
	}
	if err := remote.Delete(
		ref,
		remote.WithContext(ctx),
		remote.WithTransport(c.transport),
		remote.WithAuth(authn.FromConfig(authn.AuthConfig{
			Username: pushUsername,
			Password: pushPassword,
		})),
	); err != nil {
		return fmt.Errorf("cannot delete mirrored OCI image: %w", err)
	}
	return nil
}

func (c *Client) DeletePackage(ctx context.Context, pkg core.Package) error {
	if pkg.OCIProject == "" || c.driver == nil {
		return nil
	}
	repositoryPath := path.Join("/docker/registry/v2/repositories", pkg.OCIProject)
	if err := c.driver.Delete(ctx, repositoryPath); err != nil {
		slog.WarnContext(ctx, "best-effort OCI package cleanup failed", "project", pkg.OCIProject, "error", err)
	}
	return nil
}

func (c *Client) internalImageReference(pkg core.Package, resourceName string) (string, error) {
	if pkg.OCIProject == "" {
		return "", fmt.Errorf("cannot resolve image reference: OCI project not configured for package %s", pkg.Name)
	}
	host := registryHost(c.internalRegistry)
	if host == "" {
		return "", fmt.Errorf("cannot resolve image reference: internal registry host is not configured")
	}
	return host + "/" + pkg.OCIProject + "/" + sanitizeName(resourceName), nil
}

func (c *Client) nameOptions() []name.Option {
	parsed, err := url.Parse(c.internalRegistry)
	if err == nil && parsed.Scheme == "http" {
		return []name.Option{name.Insecure}
	}
	return nil
}

func (c *Client) newCredential(username string) (*core.RobotCredential, error) {
	secret, id := c.credentialSecret(username)
	encrypted, err := c.encrypt(secret)
	if err != nil {
		return nil, err
	}
	return &core.RobotCredential{
		ID:              id,
		Username:        username,
		EncryptedSecret: encrypted,
	}, nil
}

func (c *Client) credentialSecret(username string) (string, int64) {
	mac := hmac.New(sha256.New, c.secretKey)
	_, _ = mac.Write([]byte("oci-robot:" + username))
	sum := mac.Sum(nil)
	secret := base64.RawURLEncoding.EncodeToString(sum)
	var id int64
	for _, item := range sum[:6] {
		id = (id << 8) | int64(item)
	}
	return secret, id
}

func (c *Client) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" || r.URL.Path == "/v2" {
			if _, _, ok := r.BasicAuth(); !ok {
				challenge(w)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		projectName, ok := repositoryProject(r.URL.Path)
		if !ok {
			challenge(w)
			return
		}
		pkg, err := c.packageForProject(r.Context(), projectName)
		if err != nil {
			challenge(w)
			return
		}
		username, password, ok := r.BasicAuth()
		if !ok || !c.authorized(pkg, username, password, requiresPush(r.Method)) {
			challenge(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (c *Client) packageForProject(ctx context.Context, projectName string) (core.Package, error) {
	packageName := projectName
	prefix := c.projectPrefix + "-"
	if c.projectPrefix != "" && strings.HasPrefix(packageName, prefix) {
		packageName = strings.TrimPrefix(packageName, prefix)
	}
	pkg, err := c.repository.GetPackageByName(ctx, packageName)
	if err != nil {
		return core.Package{}, err
	}
	if pkg.OCIProject != projectName {
		return core.Package{}, fmt.Errorf("OCI project mismatch")
	}
	return pkg, nil
}

func (c *Client) authorized(pkg core.Package, username, password string, push bool) bool {
	checkCredential := func(credential *core.RobotCredential) bool {
		if credential == nil || credential.Username != username {
			return false
		}
		secret, err := c.decrypt(credential.EncryptedSecret)
		return err == nil && constantTimeEqual(secret, password)
	}
	if push {
		return checkCredential(pkg.OCIPushRobot)
	}
	return checkCredential(pkg.OCIPullRobot) || checkCredential(pkg.OCIPushRobot)
}

func constantTimeEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func repositoryProject(rawPath string) (string, bool) {
	parts := strings.Split(strings.Trim(rawPath, "/"), "/")
	if len(parts) < 4 || parts[0] != "v2" {
		return "", false
	}
	for idx := 2; idx < len(parts); idx++ {
		switch parts[idx] {
		case "blobs", "manifests", "tags", "referrers":
			if idx == 1 {
				return "", false
			}
			return parts[1], true
		}
	}
	return "", false
}

func requiresPush(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="charm-registry-oci"`)
	w.WriteHeader(http.StatusUnauthorized)
}

func (c *Client) projectName(packageName string) string {
	base := sanitizeName(packageName)
	if c.projectPrefix == "" {
		return base
	}
	return sanitizeName(c.projectPrefix + "-" + base)
}

func (c *Client) robotName(prefix, packageID string) string {
	return sanitizeName(prefix + "-" + packageID)
}

func digestTag(digest string) string {
	trimmed := strings.TrimPrefix(digest, "sha256:")
	trimmed = strings.TrimPrefix(trimmed, "sha512:")
	if trimmed == "" {
		return "mirrored"
	}
	return "digest-" + trimmed
}

func sanitizeName(value string) string {
	cleaned := strings.ToLower(strings.TrimSpace(value))
	cleaned = invalidNamePattern.ReplaceAllString(cleaned, "-")
	cleaned = strings.Trim(cleaned, "-")
	if cleaned == "" {
		return "charm"
	}
	return cleaned
}

func registryHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(raw), "https://"), "http://")
}

func deriveKey(value string) ([]byte, error) {
	key, err := pbkdf2.Key(sha256.New, value, keyDerivationSalt, keyDerivationIterations, keyDerivationLength)
	if err != nil {
		return nil, fmt.Errorf("cannot derive OCI secret key: %w", err)
	}
	return key, nil
}

func (c *Client) encrypt(secret string) (string, error) {
	block, err := aes.NewCipher(c.secretKey)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(secret), nil)
	encoded := base64.RawStdEncoding.EncodeToString(sealed)
	return ociKeyVersion + ":" + encoded, nil
}

func (c *Client) decrypt(encrypted string) (string, error) {
	// Strip key version prefix if present.
	if idx := strings.Index(encrypted, ":"); idx >= 0 {
		prefix := encrypted[:idx]
		if prefix != ociKeyVersion {
			return "", fmt.Errorf("cannot decrypt OCI secret: unsupported key version %q (current: %q)", prefix, ociKeyVersion)
		}
		encrypted = encrypted[idx+1:]
	}
	raw, err := base64.RawStdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(c.secretKey)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < aead.NonceSize() {
		return "", fmt.Errorf("cannot decrypt OCI secret: encrypted secret is malformed")
	}
	nonce := raw[:aead.NonceSize()]
	plaintext, err := aead.Open(nil, nonce, raw[aead.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
