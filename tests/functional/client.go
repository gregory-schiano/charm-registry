// Package functional provides endpoint-driven functional test helpers for
// charm-registry. It can target any running instance — Juju-deployed charm,
// snap-installed service, or a local process — by supplying the API base URL,
// admin credentials, and OCI registry endpoint via environment variables or
// the Config struct.
//
// No helper in this package invokes Docker or Docker Compose.
package functional

import (
	"archive/zip"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ---------- Configuration ----------

// Config holds the connection parameters for a charm-registry functional test run.
// Populate from environment variables via ConfigFromEnv or set fields directly.
type Config struct {
	// APIURL is the base URL of the charm-registry API (e.g. "http://localhost:8080").
	APIURL string

	// OCIURL is the base URL of the OCI registry (e.g. "https://localhost:15000").
	// May be empty if OCI scenarios are not needed.
	OCIURL string

	// OCICertPath is the path to a PEM CA certificate for the OCI registry.
	// May be empty for plain HTTP or system-trusted registries.
	OCICertPath string

	// AdminSubject and AdminUsername are used to build the dev-auth bearer token.
	AdminSubject  string
	AdminUsername string
}

// ConfigFromEnv builds a Config from environment variables, falling back to
// sensible defaults for a local dev instance.
//
// Recognised variables:
//
//	FTEST_API_URL       – API base URL           (default: http://localhost:8080)
//	FTEST_OCI_URL       – OCI registry base URL  (default: https://localhost:15000)
//	FTEST_OCI_CERT_PATH – OCI CA cert PEM path   (default: "")
//	FTEST_ADMIN_SUBJECT – dev-auth subject       (default: "admin")
//	FTEST_ADMIN_USER    – dev-auth username       (default: "admin")
func ConfigFromEnv() Config {
	cfg := Config{
		APIURL:        envOr("FTEST_API_URL", "http://localhost:8080"),
		OCIURL:        envOr("FTEST_OCI_URL", "https://localhost:15000"),
		OCICertPath:   os.Getenv("FTEST_OCI_CERT_PATH"),
		AdminSubject:  envOr("FTEST_ADMIN_SUBJECT", "admin"),
		AdminUsername: envOr("FTEST_ADMIN_USER", "admin"),
	}
	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ---------- Client ----------

// Client is the HTTP client that drives functional scenarios against a
// running charm-registry instance.
type Client struct {
	cfg    Config
	http   *http.Client
	ociTLS *http.Client // lazily built when OCICertPath is set
}

// NewClient returns a Client configured with cfg. The underlying HTTP client
// has a 30-second default timeout; override via the returned Client if needed.
func NewClient(cfg Config) *Client {
	return &Client{
		cfg: cfg,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewClientFromEnv is a convenience wrapper: ConfigFromEnv → NewClient.
func NewClientFromEnv() *Client {
	return NewClient(ConfigFromEnv())
}

// ---------- Auth helpers ----------

// DevAuthHeader returns a Bearer dev-auth header with the given subject and
// username (e.g. "Bearer dev:<subject>:<username>").
func DevAuthHeader(subject, username string) string {
	return "Bearer dev:" + subject + ":" + username
}

// AdminAuthHeader returns the dev-auth header for the AdminSubject/AdminUsername
// configured in the Client.
func (c *Client) AdminAuthHeader() string {
	return DevAuthHeader(c.cfg.AdminSubject, c.cfg.AdminUsername)
}

// ---------- HTTP request helpers ----------

// DoRequest performs an HTTP request against the API. path may include a
// query string (split correctly; url.JoinPath does not encode '?').
// body is sent as the JSON request body when non-empty (POST/PUT/PATCH only).
// authHeader is set as the Authorization header when non-empty.
func (c *Client) DoRequest(method, path, body, authHeader string) (*http.Response, error) {
	rawPath, query, _ := strings.Cut(path, "?")
	u, _ := url.JoinPath(c.cfg.APIURL, rawPath)
	if query != "" {
		u += "?" + query
	}
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, u, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("building %s %s: %w", method, u, err)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	if body != "" && method != "GET" {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

// DoRequestRaw performs an HTTP request with full caller control over headers
// and body. Useful for multipart uploads.
func (c *Client) DoRequestRaw(method, path string, headers map[string]string, body io.Reader) (*http.Response, error) {
	u, _ := url.JoinPath(c.cfg.APIURL, path)
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, fmt.Errorf("building %s %s: %w", method, u, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return c.http.Do(req)
}

// ---------- Response parsing helpers ----------

// ReadJSON decodes the response body into a map and closes the body.
func ReadJSON(resp *http.Response) (map[string]any, error) {
	defer resp.Body.Close()
	var result map[string]any
	err := json.NewDecoder(resp.Body).Decode(&result)
	return result, err
}

// ReadAllBytes reads the entire response body and closes it.
func ReadAllBytes(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// ---------- Upload helpers ----------

// UploadMultipart sends a multipart form upload to the given API path.
func (c *Client) UploadMultipart(path, fieldName, filename string, data []byte, authHeader string) (map[string]any, int, error) {
	u, _ := url.JoinPath(c.cfg.APIURL, path)
	reqBody := &bytes.Buffer{}
	mpw := multipart.NewWriter(reqBody)
	part, err := mpw.CreateFormFile(fieldName, filename)
	if err != nil {
		return nil, 0, fmt.Errorf("creating form file: %w", err)
	}
	if _, err = part.Write(data); err != nil {
		return nil, 0, fmt.Errorf("writing form file: %w", err)
	}
	if err = mpw.Close(); err != nil {
		return nil, 0, fmt.Errorf("closing multipart writer: %w", err)
	}

	req, err := http.NewRequest("POST", u, reqBody)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", mpw.FormDataContentType())
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	body, err := ReadJSON(resp)
	return body, resp.StatusCode, err
}

// ---------- TLS helpers ----------

// OCITLSClient returns an HTTP client that trusts the OCI CA certificate
// configured in the Client. Returns nil if no certificate is configured.
func (c *Client) OCITLSClient() (*http.Client, error) {
	if c.ociTLS != nil {
		return c.ociTLS, nil
	}
	if c.cfg.OCICertPath == "" {
		return nil, fmt.Errorf("FTEST_OCI_CERT_PATH not set; cannot build TLS client")
	}
	certData, err := os.ReadFile(c.cfg.OCICertPath)
	if err != nil {
		return nil, fmt.Errorf("reading OCI cert %s: %w", c.cfg.OCICertPath, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certData) {
		return nil, fmt.Errorf("failed to append OCI CA cert to pool")
	}
	c.ociTLS = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}
	return c.ociTLS, nil
}

// ---------- Archive builders ----------

// BuildTestCharmArchive creates a minimal valid charm .charm archive in memory.
func BuildTestCharmArchive(name string) ([]byte, error) {
	metadata := fmt.Sprintf("name: %s\nsummary: Functional test charm\ndescription: A charm for functional testing\n", name)
	manifest := "bases:\n  - name: ubuntu\n    channel: \"22.04\"\n    architectures:\n      - amd64\n"
	return BuildZipArchive(map[string]string{
		"metadata.yaml": metadata,
		"manifest.yaml": manifest,
	})
}

// BuildTestCharmArchiveWithResources creates a charm archive with a declared file resource.
func BuildTestCharmArchiveWithResources(name string) ([]byte, error) {
	metadata := fmt.Sprintf(`name: %s
summary: Functional test charm with resources
description: A charm for functional testing
resources:
  config:
    type: file
    filename: config.yaml
    description: Config file
`, name)
	manifest := "bases:\n  - name: ubuntu\n    channel: \"22.04\"\n    architectures:\n      - amd64\n"
	return BuildZipArchive(map[string]string{
		"metadata.yaml": metadata,
		"manifest.yaml": manifest,
	})
}

// BuildZipArchive creates a zip archive in memory from a map of filename→content.
func BuildZipArchive(files map[string]string) ([]byte, error) {
	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			return nil, fmt.Errorf("creating zip entry %s: %w", name, err)
		}
		if _, err = f.Write([]byte(content)); err != nil {
			return nil, fmt.Errorf("writing zip entry %s: %w", name, err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---------- Encoding helpers ----------

// Base64URLEncode encodes a string as base64url with padding.
func Base64URLEncode(s string) string {
	var buf bytes.Buffer
	encoder := base64.NewEncoder(base64.URLEncoding, &buf)
	_, _ = encoder.Write([]byte(s))
	encoder.Close()
	return buf.String()
}

// UniqueName generates a unique name with a timestamp suffix to avoid collisions.
func UniqueName(base string) string {
	return fmt.Sprintf("ftest-%s-%d", base, time.Now().UnixNano())
}
