package charmhub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gschiano/charm-registry/internal/core"
)

type Client struct {
	baseURL string
	http    *http.Client
}

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
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{
			Timeout: 30 * time.Second,
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

	body, err := io.ReadAll(resp.Body)
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

	body, err := io.ReadAll(resp.Body)
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

	body, err := io.ReadAll(resp.Body)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifactURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return body, nil
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
