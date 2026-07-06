package charmhub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gschiano/charm-registry/internal/core"
)

type Client struct {
	baseURL             string
	http                *http.Client
	maxAPIResponseBytes int64
	maxArtifactBytes    int64
}

const (
	defaultMaxAPIResponseBytes = 4 << 20
	defaultMaxArtifactBytes    = 64 << 20
	defaultHTTPTimeout         = 5 * time.Minute
)

type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Charmhub API returned %d: %s", e.StatusCode, strings.TrimSpace(e.Body))
}

type PackageChannel struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	ChannelMap     []ChannelMap   `json:"channel-map"`
	Result         PackageResult  `json:"result"`
	DefaultRelease DefaultRelease `json:"default-release"`
	Type           string         `json:"type"`
}

type PackageResult struct {
	Description string              `json:"description"`
	Links       map[string][]string `json:"links"`
	Media       []Media             `json:"media"`
	Summary     string              `json:"summary"`
	Title       string              `json:"title"`
	Website     string              `json:"website"`
}

type Media struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type DefaultRelease struct {
	Channel   ReleaseChannel    `json:"channel"`
	Resources []ReleaseResource `json:"resources"`
	Revision  ReleaseRevision   `json:"revision"`
}

func (r DefaultRelease) Present() bool {
	return r.Channel.Name != "" && r.Revision.Revision > 0
}

type ReleaseChannel struct {
	Base       *core.Base `json:"base"`
	Name       string     `json:"name"`
	ReleasedAt time.Time  `json:"released-at"`
	Risk       string     `json:"risk"`
	Track      string     `json:"track"`
}

type ReleaseResource struct {
	CreatedAt   time.Time     `json:"created-at"`
	Description string        `json:"description"`
	Download    core.Download `json:"download"`
	Filename    string        `json:"filename"`
	Name        string        `json:"name"`
	Revision    int           `json:"revision"`
	Type        string        `json:"type"`
}

type ReleaseRevision struct {
	ActionsYAML  string                         `json:"actions-yaml"`
	Attributes   map[string]string              `json:"attributes"`
	Bases        []core.Base                    `json:"bases"`
	BundleYAML   string                         `json:"bundle-yaml"`
	ConfigYAML   string                         `json:"config-yaml"`
	CreatedAt    time.Time                      `json:"created-at"`
	Download     core.Download                  `json:"download"`
	MetadataYAML string                         `json:"metadata-yaml"`
	ReadmeMD     string                         `json:"readme-md"`
	Relations    map[string]map[string]Relation `json:"relations"`
	Revision     int                            `json:"revision"`
	Subordinate  bool                           `json:"subordinate"`
	Version      string                         `json:"version"`
}

type Relation struct {
	Interface string `json:"interface"`
}

type ChannelMap struct {
	Channel  ReleaseChannel  `json:"channel"`
	Revision ReleaseRevision `json:"revision"`
}

func (c *ReleaseChannel) UnmarshalJSON(data []byte) error {
	type releaseChannelJSON struct {
		Base       *core.Base `json:"base"`
		Name       string     `json:"name"`
		ReleasedAt string     `json:"released-at"`
		Risk       string     `json:"risk"`
		Track      string     `json:"track"`
	}
	var raw releaseChannelJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	releasedAt, err := parseCharmhubTime(raw.ReleasedAt)
	if err != nil {
		return err
	}
	*c = ReleaseChannel{
		Base:       raw.Base,
		Name:       raw.Name,
		ReleasedAt: releasedAt,
		Risk:       raw.Risk,
		Track:      raw.Track,
	}
	return nil
}

func (r *ReleaseResource) UnmarshalJSON(data []byte) error {
	type releaseResourceJSON struct {
		CreatedAt   string        `json:"created-at"`
		Description string        `json:"description"`
		Download    core.Download `json:"download"`
		Filename    string        `json:"filename"`
		Name        string        `json:"name"`
		Revision    int           `json:"revision"`
		Type        string        `json:"type"`
	}
	var raw releaseResourceJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	createdAt, err := parseCharmhubTime(raw.CreatedAt)
	if err != nil {
		return err
	}
	*r = ReleaseResource{
		CreatedAt:   createdAt,
		Description: raw.Description,
		Download:    raw.Download,
		Filename:    raw.Filename,
		Name:        raw.Name,
		Revision:    raw.Revision,
		Type:        raw.Type,
	}
	return nil
}

func (r *ReleaseRevision) UnmarshalJSON(data []byte) error {
	type releaseRevisionJSON struct {
		ActionsYAML  string                         `json:"actions-yaml"`
		Attributes   map[string]string              `json:"attributes"`
		Bases        []core.Base                    `json:"bases"`
		BundleYAML   string                         `json:"bundle-yaml"`
		ConfigYAML   string                         `json:"config-yaml"`
		CreatedAt    string                         `json:"created-at"`
		Download     core.Download                  `json:"download"`
		MetadataYAML string                         `json:"metadata-yaml"`
		ReadmeMD     string                         `json:"readme-md"`
		Relations    map[string]map[string]Relation `json:"relations"`
		Revision     int                            `json:"revision"`
		Subordinate  bool                           `json:"subordinate"`
		Version      string                         `json:"version"`
	}
	var raw releaseRevisionJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	createdAt, err := parseCharmhubTime(raw.CreatedAt)
	if err != nil {
		return err
	}
	*r = ReleaseRevision{
		ActionsYAML:  raw.ActionsYAML,
		Attributes:   raw.Attributes,
		Bases:        raw.Bases,
		BundleYAML:   raw.BundleYAML,
		ConfigYAML:   raw.ConfigYAML,
		CreatedAt:    createdAt,
		Download:     raw.Download,
		MetadataYAML: raw.MetadataYAML,
		ReadmeMD:     raw.ReadmeMD,
		Relations:    raw.Relations,
		Revision:     raw.Revision,
		Subordinate:  raw.Subordinate,
		Version:      raw.Version,
	}
	return nil
}

func New(baseURL string) *Client {
	return NewWithLimits(baseURL, defaultMaxAPIResponseBytes, defaultMaxArtifactBytes)
}

func NewWithLimits(baseURL string, maxAPIResponseBytes, maxArtifactBytes int64) *Client {
	if maxAPIResponseBytes <= 0 {
		maxAPIResponseBytes = defaultMaxAPIResponseBytes
	}
	if maxArtifactBytes <= 0 {
		maxArtifactBytes = defaultMaxArtifactBytes
	}
	return &Client{
		baseURL:             strings.TrimRight(baseURL, "/"),
		maxAPIResponseBytes: maxAPIResponseBytes,
		maxArtifactBytes:    maxArtifactBytes,
		http: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

func (c *Client) GetChannel(ctx context.Context, name, channel string) (PackageChannel, error) {
	query := url.Values{}
	query.Set("fields", "default-release,result")
	query.Set("channel", channel)
	endpoint := c.baseURL + "/v2/charms/info/" + url.PathEscape(name) + "?" + query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return PackageChannel{}, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return PackageChannel{}, err
	}
	defer resp.Body.Close()

	body, err := readAllLimited(resp.Body, c.maxAPIResponseBytes, "Charmhub API response")
	if err != nil {
		return PackageChannel{}, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return PackageChannel{}, &APIError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var out PackageChannel
	if err := json.Unmarshal(body, &out); err != nil {
		return PackageChannel{}, err
	}
	return out, nil
}

func (c *Client) GetInfo(ctx context.Context, name string) (PackageChannel, error) {
	query := url.Values{}
	query.Set("fields", "channel-map,result")
	endpoint := c.baseURL + "/v2/charms/info/" + url.PathEscape(name) + "?" + query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return PackageChannel{}, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return PackageChannel{}, err
	}
	defer resp.Body.Close()

	body, err := readAllLimited(resp.Body, c.maxAPIResponseBytes, "Charmhub API response")
	if err != nil {
		return PackageChannel{}, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return PackageChannel{}, &APIError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var out PackageChannel
	if err := json.Unmarshal(body, &out); err != nil {
		return PackageChannel{}, err
	}
	return out, nil
}

func (c *Client) RefreshChannel(ctx context.Context, name, channel string, base core.Base) (PackageChannel, error) {
	request := map[string]any{
		"context": []any{},
		"fields": []string{
			"bases",
			"config-yaml",
			"download",
			"id",
			"metadata-yaml",
			"name",
			"resources",
			"revision",
			"summary",
			"type",
			"version",
		},
		"actions": []any{map[string]any{
			"action":       "install",
			"instance-key": "charmhub-sync",
			"name":         name,
			"channel":      channel,
			"base": map[string]string{
				"architecture": base.Architecture,
				"name":         base.Name,
				"channel":      base.Channel,
			},
		}},
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return PackageChannel{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v2/charms/refresh", strings.NewReader(string(payload)))
	if err != nil {
		return PackageChannel{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return PackageChannel{}, err
	}
	defer resp.Body.Close()

	body, err := readAllLimited(resp.Body, c.maxAPIResponseBytes, "Charmhub API response")
	if err != nil {
		return PackageChannel{}, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return PackageChannel{}, &APIError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var refresh refreshResponseEnvelope
	if err := json.Unmarshal(body, &refresh); err != nil {
		return PackageChannel{}, err
	}
	if len(refresh.ErrorList) > 0 {
		return PackageChannel{}, fmt.Errorf("charmhub refresh failed: %s", refresh.ErrorList[0].Message)
	}
	if len(refresh.Results) == 0 {
		return PackageChannel{}, fmt.Errorf("charmhub refresh returned no results")
	}
	result := refresh.Results[0]
	if result.Error != nil {
		return PackageChannel{}, fmt.Errorf("charmhub refresh failed: %s", result.Error.Message)
	}
	releasedAt, err := parseCharmhubTime(result.ReleasedAt)
	if err != nil {
		return PackageChannel{}, err
	}
	createdAt, err := parseCharmhubTime(result.Charm.CreatedAt)
	if err != nil {
		return PackageChannel{}, err
	}
	resources := make([]ReleaseResource, 0, len(result.Charm.Resources))
	for _, resource := range result.Charm.Resources {
		resources = append(resources, ReleaseResource{
			Description: resource.Description,
			Download:    resource.Download,
			Filename:    resource.Filename,
			Name:        resource.Name,
			Revision:    resource.Revision,
			Type:        resource.Type,
		})
	}
	return PackageChannel{
		ID:   result.ID,
		Name: result.Name,
		Type: result.Charm.Type,
		DefaultRelease: DefaultRelease{
			Channel: ReleaseChannel{
				Base:       &base,
				Name:       result.EffectiveChannel,
				ReleasedAt: releasedAt,
				Risk:       riskFromChannel(result.EffectiveChannel),
				Track:      trackFromChannel(result.EffectiveChannel),
			},
			Resources: resources,
			Revision: ReleaseRevision{
				Bases:        result.Charm.Bases,
				ConfigYAML:   result.Charm.ConfigYAML,
				CreatedAt:    createdAt,
				Download:     result.Charm.Download,
				MetadataYAML: result.Charm.MetadataYAML,
				Revision:     result.Charm.Revision,
				Version:      result.Charm.Version,
			},
		},
	}, nil
}

func (c *Client) Download(ctx context.Context, artifactURL string) ([]byte, error) {
	var payload bytes.Buffer
	if _, err := c.DownloadTo(ctx, artifactURL, &payload); err != nil {
		return nil, err
	}
	return payload.Bytes(), nil
}

func (c *Client) DownloadTo(ctx context.Context, artifactURL string, dst io.Writer) (int64, error) {
	resp, err := c.openArtifact(ctx, artifactURL)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		body, err := readAllLimited(resp.Body, c.maxAPIResponseBytes, "Charmhub artifact error response")
		if err != nil {
			return 0, err
		}
		return 0, &APIError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return copyLimited(dst, resp.Body, c.maxArtifactBytes, "Charmhub artifact")
}

func (c *Client) openArtifact(ctx context.Context, artifactURL string) (*http.Response, error) {
	parsedURL, err := url.Parse(artifactURL)
	if err != nil {
		return nil, fmt.Errorf("invalid artifact URL: %w", err)
	}
	if parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("artifact URL must use https, got %q", parsedURL.Scheme)
	}
	// Validate that the artifact URL is under the configured Charmhub base URL
	// or a known allowed domain. This prevents a compromised upstream from
	// redirecting to internal services.
	if isAllowedDownloadHost(parsedURL.Hostname(), c.baseURL) {
		// Host matches the configured base URL or a known Charmhub CDN domain.
		// Allow even if it resolves to loopback (dev/test setups where baseURL
		// points to a local test server).
	} else if isPrivateHost(parsedURL.Hostname()) {
		// Block RFC 1918 private/reserved addresses for unknown hosts.
		return nil, fmt.Errorf("artifact URL refers to private/reserved address: %s", parsedURL.Hostname())
	} else {
		return nil, fmt.Errorf("artifact URL host %q is not an allowed Charmhub domain", parsedURL.Hostname())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifactURL, nil)
	if err != nil {
		return nil, err
	}

	// Limit redirect hops and validate redirect targets.
	c.http.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects for artifact download")
		}
		redirectHost := req.URL.Hostname()
		if isAllowedDownloadHost(redirectHost, c.baseURL) {
			return nil
		}
		if isPrivateHost(redirectHost) {
			return fmt.Errorf("redirect to private/reserved address blocked: %s", redirectHost)
		}
		return fmt.Errorf("redirect to disallowed host blocked: %s", redirectHost)
	}

	return c.http.Do(req)
}

// isPrivateHost checks whether a hostname resolves to an RFC 1918 or
// loopback address. Returns true for IP addresses in private/reserved ranges
// and for "localhost".
func isPrivateHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Could be a hostname; resolve it.
		ips, err := net.LookupIP(host)
		if err != nil || len(ips) == 0 {
			// Can't resolve — treat as potentially dangerous.
			return false
		}
		ip = ips[0]
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast()
}

// isAllowedDownloadHost validates that a download host matches the
// configured Charmhub base URL or a known Charmhub CDN domain.
func isAllowedDownloadHost(host, baseURL string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	if host == parsed.Hostname() {
		return true
	}
	// Known Charmhub CDN / storage hosts.
	allowedSuffixes := []string{
		".charmhub.io",
		".cdn.snapcraftcontent.com",
		".juju.is",
		".canonical.com",
	}
	lower := strings.ToLower(host)
	for _, suffix := range allowedSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func readAllLimited(reader io.Reader, maxBytes int64, label string) ([]byte, error) {
	if maxBytes <= 0 {
		return io.ReadAll(reader)
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", label, maxBytes)
	}
	return body, nil
}

func copyLimited(dst io.Writer, src io.Reader, maxBytes int64, label string) (int64, error) {
	if maxBytes <= 0 {
		return io.Copy(dst, src)
	}
	written, err := io.Copy(dst, io.LimitReader(src, maxBytes+1))
	if err != nil {
		return written, err
	}
	if written > maxBytes {
		return written, fmt.Errorf("%s exceeds %d bytes", label, maxBytes)
	}
	return written, nil
}

type refreshResponseEnvelope struct {
	Results   []refreshResult `json:"results"`
	ErrorList []apiError      `json:"error-list"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type refreshResult struct {
	Charm            refreshCharm `json:"charm"`
	EffectiveChannel string       `json:"effective-channel"`
	Error            *apiError    `json:"error"`
	ID               string       `json:"id"`
	Name             string       `json:"name"`
	ReleasedAt       string       `json:"released-at"`
}

type refreshCharm struct {
	Bases        []core.Base       `json:"bases"`
	ConfigYAML   string            `json:"config-yaml"`
	CreatedAt    string            `json:"created-at"`
	Download     core.Download     `json:"download"`
	ID           string            `json:"id"`
	MetadataYAML string            `json:"metadata-yaml"`
	Name         string            `json:"name"`
	Resources    []ReleaseResource `json:"resources"`
	Revision     int               `json:"revision"`
	Summary      string            `json:"summary"`
	Type         string            `json:"type"`
	Version      string            `json:"version"`
}

func trackFromChannel(channel string) string {
	track, _, _ := strings.Cut(channel, "/")
	return track
}

func riskFromChannel(channel string) string {
	_, risk, ok := strings.Cut(channel, "/")
	if !ok {
		return ""
	}
	return risk
}

func parseCharmhubTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		var (
			parsed time.Time
			err    error
		)
		if layout == time.RFC3339Nano {
			parsed, err = time.Parse(layout, raw)
		} else {
			parsed, err = time.ParseInLocation(layout, raw, time.UTC)
		}
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("parse Charmhub time %q", raw)
}
