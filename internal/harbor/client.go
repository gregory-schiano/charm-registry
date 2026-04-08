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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
)

var invalidNamePattern = regexp.MustCompile(`[^a-z0-9-]+`)

type Client struct {
	http            *http.Client
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
			return nil, fmt.Errorf("read Harbor CA file: %w", err)
		}
		if ok := rootCAs.AppendCertsFromPEM(pemBytes); !ok {
			return nil, fmt.Errorf("append Harbor CA file: no certificates found")
		}
		tlsConfig.RootCAs = rootCAs
	}
	return &Client{
		http: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
		},
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
	projectName := pkg.HarborProject
	if projectName == "" {
		return "", fmt.Errorf("harbor project not configured for package %s", pkg.Name)
	}
	host := strings.TrimPrefix(strings.TrimPrefix(c.publicRegistry, "https://"), "http://")
	return host + "/" + projectName + "/" + sanitizeName(resourceName), nil
}

func (c *Client) Credentials(pkg core.Package, pull bool) (string, string, error) {
	robot := pkg.HarborPushRobot
	if pull {
		robot = pkg.HarborPullRobot
	}
	if robot == nil || robot.Username == "" || robot.EncryptedSecret == "" {
		return "", "", fmt.Errorf("harbor robot credentials are not available")
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
		var apiErr *apiError
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
		var apiErr *apiError
		if ok := errorAs(err, &apiErr); !ok || apiErr.StatusCode != http.StatusConflict {
			return nil, err
		}
		created, err = c.createRobot(ctx, name+"-"+fmt.Sprintf("%d", time.Now().UTC().Unix()), projectName, allowPush)
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
		access = append(access, map[string]string{
			"resource": "repository",
			"action":   "push",
			"effect":   "allow",
		})
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

type apiError struct {
	StatusCode int
	Body       string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("Harbor API returned %d: %s", e.StatusCode, strings.TrimSpace(e.Body))
}

func (c *Client) doJSON(ctx context.Context, method, path string, reqBody any, out ...any) error {
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
		return &apiError{StatusCode: resp.StatusCode, Body: string(responseBody)}
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
		return "", fmt.Errorf("encrypted Harbor secret is malformed")
	}
	nonce := raw[:aead.NonceSize()]
	plaintext, err := aead.Open(nil, nonce, raw[aead.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func errorAs(err error, target **apiError) bool {
	apiErr, ok := err.(*apiError)
	if !ok {
		return false
	}
	*target = apiErr
	return true
}
