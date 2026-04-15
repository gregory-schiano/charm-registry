package harbor

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
)

var invalidNamePattern = regexp.MustCompile(`[^a-z0-9-]+`)
var authRealmPattern = regexp.MustCompile(`realm="([^"]+)"`)

type Client struct {
	http            *http.Client
	transport       *http.Transport
	registryRT      http.RoundTripper
	requestTimeout  time.Duration
	apiURL          string
	publicRegistry  string
	adminUsername   string
	adminPassword   string
	projectPrefix   string
	pullRobotPrefix string
	pushRobotPrefix string
	secretKey       []byte
}

func New(cfg config.Config) (*Client, error) {
	// #nosec G402 -- Local development can opt into Harbor TLS verification bypass explicitly.
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.HarborInsecureTLS}
	if cfg.HarborCAFile != "" {
		rootCAs, err := x509.SystemCertPool()
		if err != nil || rootCAs == nil {
			rootCAs = x509.NewCertPool()
		}
		pemBytes, err := os.ReadFile(cfg.HarborCAFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read Harbor CA file: %w", err)
		}
		if ok := rootCAs.AppendCertsFromPEM(pemBytes); !ok {
			return nil, fmt.Errorf("cannot append Harbor CA file: no certificates found")
		}
		tlsConfig.RootCAs = rootCAs
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig}
	internalRegistry := registryBaseURL(cfg.HarborAPIURL, cfg.HarborURL)
	registryRT := http.RoundTripper(transport)
	if realmBase, err := url.Parse(internalRegistry); err == nil && realmBase.Host != "" {
		registryRT = &rewriteAuthenticateRealmTransport{
			inner:     transport,
			realmBase: realmBase,
		}
	}
	return &Client{
		http: &http.Client{
			Transport: transport,
		},
		transport:       transport,
		registryRT:      registryRT,
		requestTimeout:  15 * time.Second,
		apiURL:          cfg.HarborAPIURL,
		publicRegistry:  cfg.PublicRegistryURL,
		adminUsername:   cfg.HarborAdminUsername,
		adminPassword:   cfg.HarborAdminPassword,
		projectPrefix:   cfg.HarborProjectPrefix,
		pullRobotPrefix: cfg.HarborPullRobotPrefix,
		pushRobotPrefix: cfg.HarborPushRobotPrefix,
		secretKey:       deriveKey(cfg.HarborSecretKey),
	}, nil
}

// Close releases idle HTTP connections held by the Harbor client transport.
func (c *Client) Close() error {
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
	return nil
}

func (c *Client) SyncPackage(ctx context.Context, pkg core.Package) (core.Package, error) {
	projectName := pkg.HarborProject
	if projectName == "" {
		projectName = c.projectName(pkg.Name)
	}
	if err := c.ensureProject(ctx, projectName); err != nil {
		return core.Package{}, err
	}
	pushRobot, err := c.ensureRobot(ctx, pkg.HarborPushRobot, c.robotName(c.pushRobotPrefix, pkg.ID), projectName, true)
	if err != nil {
		return core.Package{}, err
	}
	pullRobot, err := c.ensureRobot(ctx, pkg.HarborPullRobot, c.robotName(c.pullRobotPrefix, pkg.ID), projectName, false)
	if err != nil {
		return core.Package{}, err
	}
	now := time.Now().UTC()
	pkg.HarborProject = projectName
	pkg.HarborPushRobot = pushRobot
	pkg.HarborPullRobot = pullRobot
	pkg.HarborSyncedAt = &now
	return pkg, nil
}

func (c *Client) ImageReference(pkg core.Package, resourceName string) (string, error) {
	return c.imageReference(c.publicRegistry, pkg, resourceName)
}

func (c *Client) imageReference(registryURL string, pkg core.Package, resourceName string) (string, error) {
	projectName := pkg.HarborProject
	if projectName == "" {
		return "", fmt.Errorf("cannot resolve image reference: harbor project not configured for package %s", pkg.Name)
	}
	host := registryHost(registryURL)
	if host == "" {
		return "", fmt.Errorf("cannot resolve image reference: registry host is not configured")
	}
	return host + "/" + projectName + "/" + sanitizeName(resourceName), nil
}

func (c *Client) Credentials(pkg core.Package, pull bool) (string, string, error) {
	robot := pkg.HarborPushRobot
	if pull {
		robot = pkg.HarborPullRobot
	}
	if robot == nil || robot.Username == "" || robot.EncryptedSecret == "" {
		return "", "", fmt.Errorf("cannot read harbor robot credentials: credentials are not available")
	}
	secret, err := c.decrypt(robot.EncryptedSecret)
	if err != nil {
		return "", "", err
	}
	return robot.Username, secret, nil
}

type robotResponse struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Secret  string `json:"secret"`
	Disable bool   `json:"disable"`
}

func (c *Client) ensureProject(ctx context.Context, projectName string) error {
	query := url.Values{}
	query.Set("name", projectName)
	query.Set("with_detail", "false")
	var projects []struct {
		Name string `json:"name"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/projects?"+query.Encode(), nil, &projects); err != nil {
		return err
	}
	for _, project := range projects {
		if project.Name == projectName {
			return nil
		}
	}
	req := map[string]any{
		"project_name": projectName,
		"metadata": map[string]string{
			"public":    "false",
			"auto_scan": "false",
		},
	}
	if err := c.doJSON(ctx, http.MethodPost, "/projects", req, nil); err != nil {
		var apiErr *harborAPIError
		if ok := errorAs(err, &apiErr); ok && apiErr.StatusCode == http.StatusConflict {
			return nil
		}
		return err
	}
	return nil
}

func (c *Client) ensureRobot(
	ctx context.Context,
	existing *core.RobotCredential,
	name, projectName string,
	allowPush bool,
) (*core.RobotCredential, error) {
	if existing != nil && existing.ID != 0 && existing.Username != "" && existing.EncryptedSecret != "" {
		var robot robotResponse
		if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/robots/%d", existing.ID), nil, &robot); err == nil && !robot.Disable {
			return existing, nil
		}
	}
	created, err := c.createRobot(ctx, name, projectName, allowPush)
	if err != nil {
		var apiErr *harborAPIError
		if ok := errorAs(err, &apiErr); !ok || apiErr.StatusCode != http.StatusConflict {
			return nil, err
		}
		created, err = c.createRobot(ctx, name+"-"+strconv.FormatInt(time.Now().UTC().Unix(), 10), projectName, allowPush)
		if err != nil {
			return nil, err
		}
	}
	encryptedSecret, err := c.encrypt(created.Secret)
	if err != nil {
		return nil, err
	}
	return &core.RobotCredential{
		ID:              created.ID,
		Username:        created.Name,
		EncryptedSecret: encryptedSecret,
	}, nil
}

func (c *Client) createRobot(ctx context.Context, name, projectName string, allowPush bool) (robotResponse, error) {
	access := []map[string]string{
		{
			"resource": "repository",
			"action":   "pull",
			"effect":   "allow",
		},
	}
	if allowPush {
		access = append(access,
			map[string]string{
				"resource": "repository",
				"action":   "push",
				"effect":   "allow",
			},
			map[string]string{
				"resource": "repository",
				"action":   "delete",
				"effect":   "allow",
			},
		)
	}
	req := map[string]any{
		"name":        sanitizeName(name),
		"description": fmt.Sprintf("managed by charm-registry for Harbor project %s", projectName),
		"level":       "project",
		"disable":     false,
		"duration":    -1,
		"permissions": []map[string]any{{
			"kind":      "project",
			"namespace": projectName,
			"access":    access,
		}},
	}
	var created robotResponse
	if err := c.doJSON(ctx, http.MethodPost, "/robots", req, &created); err != nil {
		return robotResponse{}, err
	}
	return created, nil
}

func (c *Client) MirrorImage(
	ctx context.Context,
	pkg core.Package,
	resourceName, sourceImage, sourceUsername, sourcePassword string,
) (string, error) {
	if sourceImage == "" {
		return "", fmt.Errorf("cannot mirror OCI image: source image reference is required")
	}
	targetRepository, err := c.ImageReference(pkg, resourceName)
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
	sourceImageObject, err := remote.Image(
		sourceRef,
		remote.WithContext(ctx),
		remote.WithTransport(c.transport),
		remote.WithAuth(authn.FromConfig(authn.AuthConfig{
			Username: sourceUsername,
			Password: sourcePassword,
		})),
	)
	if err != nil {
		return "", fmt.Errorf("cannot fetch source OCI image: %w", err)
	}
	digest, err := sourceImageObject.Digest()
	if err != nil {
		return "", fmt.Errorf("cannot calculate mirrored OCI image digest: %w", err)
	}
	targetRef, err := name.ParseReference(targetRepository + ":" + digestTag(digest.String()))
	if err != nil {
		return "", fmt.Errorf("cannot parse target image reference: %w", err)
	}
	if err := remote.Write(
		targetRef,
		sourceImageObject,
		remote.WithContext(ctx),
		remote.WithTransport(c.registryRT),
		remote.WithAuth(authn.FromConfig(authn.AuthConfig{
			Username: pushUsername,
			Password: pushPassword,
		})),
	); err != nil {
		return "", fmt.Errorf("cannot push mirrored OCI image: %w", err)
	}
	return digest.String(), nil
}

func (c *Client) DeleteImage(ctx context.Context, pkg core.Package, resourceName, digest string) error {
	if digest == "" {
		return nil
	}
	pushUsername, pushPassword, err := c.Credentials(pkg, false)
	if err != nil {
		return err
	}
	imageReference, err := c.ImageReference(pkg, resourceName)
	if err != nil {
		return err
	}
	ref, err := name.ParseReference(imageReference + "@" + digest)
	if err != nil {
		return fmt.Errorf("cannot parse OCI image digest reference: %w", err)
	}
	if err := remote.Delete(
		ref,
		remote.WithContext(ctx),
		remote.WithTransport(c.registryRT),
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
	if pkg.HarborProject == "" {
		return nil
	}
	if err := c.doJSON(ctx, http.MethodDelete, path.Join("/projects", pkg.HarborProject), nil, nil); err != nil {
		var apiErr *harborAPIError
		if errorAs(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return nil
		}
		return err
	}
	return nil
}

type harborAPIError struct {
	StatusCode int
	Body       string
}

func (e *harborAPIError) Error() string {
	return fmt.Sprintf("Harbor API returned %d: %s", e.StatusCode, strings.TrimSpace(e.Body))
}

func (c *Client) doJSON(ctx context.Context, method, path string, reqBody any, out ...any) error {
	if c.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.requestTimeout)
		defer cancel()
	}
	var body io.Reader
	if reqBody != nil {
		payload, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.apiURL+path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.adminUsername, c.adminPassword)
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return &harborAPIError{StatusCode: resp.StatusCode, Body: string(responseBody)}
	}
	if len(out) == 0 || out[0] == nil || len(responseBody) == 0 {
		return nil
	}
	return json.Unmarshal(responseBody, out[0])
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

func deriveKey(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
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
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(secret), nil)
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (c *Client) decrypt(encrypted string) (string, error) {
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
		return "", fmt.Errorf("cannot decrypt Harbor secret: encrypted secret is malformed")
	}
	nonce := raw[:aead.NonceSize()]
	plaintext, err := aead.Open(nil, nonce, raw[aead.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func errorAs(err error, target **harborAPIError) bool {
	return errors.As(err, target)
}

type rewriteAuthenticateRealmTransport struct {
	inner     http.RoundTripper
	realmBase *url.URL
}

func (t *rewriteAuthenticateRealmTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.inner.RoundTrip(req)
	if err != nil || resp == nil || t.realmBase == nil {
		return resp, err
	}
	header := resp.Header.Get("Www-Authenticate")
	rewritten, changed := rewriteAuthenticateRealm(header, t.realmBase)
	if changed {
		resp.Header.Set("Www-Authenticate", rewritten)
	}
	return resp, nil
}

func rewriteAuthenticateRealm(header string, realmBase *url.URL) (string, bool) {
	if realmBase == nil || strings.TrimSpace(header) == "" {
		return header, false
	}
	matches := authRealmPattern.FindStringSubmatch(header)
	if len(matches) != 2 {
		return header, false
	}
	realmURL, err := url.Parse(matches[1])
	if err != nil {
		return header, false
	}
	host := realmURL.Hostname()
	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsPrivate()) {
		return header, false
	}
	realmURL.Scheme = realmBase.Scheme
	realmURL.Host = realmBase.Host
	return strings.Replace(header, matches[1], realmURL.String(), 1), true
}

func registryBaseURL(apiURL, harborURL string) string {
	for _, raw := range []string{apiURL, harborURL} {
		if base := trimURLToSchemeAndHost(raw); base != "" {
			return base
		}
	}
	return ""
}

func registryHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(raw), "https://"), "http://")
}

func trimURLToSchemeAndHost(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}
