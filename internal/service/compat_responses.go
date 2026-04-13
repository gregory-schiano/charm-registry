package service

import (
	"time"

	"github.com/gschiano/charm-registry/internal/core"
)

type infoResponse struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	Type           string                `json:"type"`
	DefaultRelease infoReleaseResponse   `json:"default-release"`
	ChannelMap     []infoChannelMapItem  `json:"channel-map"`
	Result         packageResultResponse `json:"result"`
}

type infoReleaseResponse struct {
	Channel   infoChannelResponse     `json:"channel"`
	Resources []core.ResourceRevision `json:"resources"`
	Revision  infoRevisionResponse    `json:"revision"`
}

type infoChannelMapItem struct {
	Channel  infoChannelResponse  `json:"channel"`
	Revision infoRevisionResponse `json:"revision"`
}

type infoChannelResponse struct {
	Base       *core.Base `json:"base"`
	Name       string     `json:"name"`
	ReleasedAt time.Time  `json:"released-at"`
	Risk       string     `json:"risk"`
	Track      string     `json:"track"`
}

type infoRevisionResponse struct {
	ActionsYAML  string                              `json:"actions-yaml"`
	Attributes   map[string]string                   `json:"attributes"`
	Bases        []core.Base                         `json:"bases"`
	BundleYAML   string                              `json:"bundle-yaml"`
	ConfigYAML   string                              `json:"config-yaml"`
	CreatedAt    time.Time                           `json:"created-at"`
	Download     core.Download                       `json:"download"`
	MetadataYAML string                              `json:"metadata-yaml"`
	ReadmeMD     string                              `json:"readme-md"`
	Relations    map[string]map[string]core.Relation `json:"relations"`
	Revision     int                                 `json:"revision"`
	Subordinate  bool                                `json:"subordinate"`
	Version      string                              `json:"version"`
}

type packageResultResponse struct {
	BugsURL      string              `json:"bugs-url"`
	Categories   []any               `json:"categories"`
	DeployableOn []string            `json:"deployable-on"`
	Description  string              `json:"description"`
	License      string              `json:"license"`
	Links        map[string][]string `json:"links"`
	Media        []core.Media        `json:"media"`
	Publisher    core.Publisher      `json:"publisher"`
	StoreURL     string              `json:"store-url"`
	StoreURLOld  string              `json:"store-url-old"`
	Summary      string              `json:"summary"`
	Title        string              `json:"title"`
	Unlisted     bool                `json:"unlisted"`
	UsedBy       []any               `json:"used-by"`
	Website      string              `json:"website"`
}

type refreshResponse struct {
	ErrorList []any                   `json:"error-list"`
	Results   []refreshActionResponse `json:"results"`
}

type refreshActionResponse struct {
	Charm            *refreshEntityResponse `json:"charm,omitempty"`
	EffectiveChannel string                 `json:"effective-channel,omitempty"`
	Error            *core.APIError         `json:"error,omitempty"`
	ID               string                 `json:"id,omitempty"`
	InstanceKey      string                 `json:"instance-key"`
	Name             string                 `json:"name,omitempty"`
	RedirectChannel  string                 `json:"redirect-channel,omitempty"`
	ReleasedAt       *time.Time             `json:"released-at,omitempty"`
	Result           string                 `json:"result"`
}

type refreshEntityResponse struct {
	CreatedAt    time.Time               `json:"created-at"`
	Download     core.Download           `json:"download"`
	ID           string                  `json:"id"`
	License      string                  `json:"license"`
	Name         string                  `json:"name"`
	Publisher    core.Publisher          `json:"publisher"`
	Resources    []core.ResourceRevision `json:"resources"`
	Revision     int                     `json:"revision"`
	Summary      string                  `json:"summary"`
	Type         string                  `json:"type"`
	Version      string                  `json:"version"`
	Bases        []core.Base             `json:"bases"`
	ConfigYAML   string                  `json:"config-yaml"`
	MetadataYAML string                  `json:"metadata-yaml"`
}
