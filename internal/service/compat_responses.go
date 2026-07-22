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

type rootDocumentResponse struct {
	ServiceName string `json:"service-name"`
	Version     string `json:"version"`
	APIURL      string `json:"api-url"`
	StorageURL  string `json:"storage-url"`
	RegistryURL string `json:"registry-url"`
}

type accountResponse struct {
	DisplayName string `json:"display-name"`
	Email       string `json:"email"`
	ID          string `json:"id"`
	IsAdmin     bool   `json:"is-admin"`
	Username    string `json:"username"`
	Validation  string `json:"validation"`
}

type macaroonInfoResponse struct {
	Account     accountResponse        `json:"account"`
	Packages    []core.PackageSelector `json:"packages"`
	Channels    []string               `json:"channels"`
	Permissions []string               `json:"permissions"`
}

type findResponse struct {
	Results []findResultResponse `json:"results"`
}

type findResultResponse struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	Type           string                `json:"type"`
	DefaultRelease findReleaseResponse   `json:"default-release"`
	Result         packageResultResponse `json:"result"`
}

type findReleaseResponse struct {
	Channel  infoChannelResponse  `json:"channel"`
	Revision findRevisionResponse `json:"revision"`
}

type findRevisionResponse struct {
	Attributes map[string]string `json:"attributes"`
	Bases      []core.Base       `json:"bases"`
	CreatedAt  time.Time         `json:"created-at"`
	Download   core.Download     `json:"download"`
	Revision   int               `json:"revision"`
	Version    string            `json:"version"`
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

type reviewUploadResponse struct {
	Revisions []uploadReviewResponse `json:"revisions"`
}

type uploadReviewResponse struct {
	Errors   []core.APIError `json:"errors"`
	Revision *int            `json:"revision"`
	Status   string          `json:"status"`
	UploadID string          `json:"upload-id"`
}

type listReleasesResponse struct {
	ChannelMap      []listReleaseChannelMapItem `json:"channel-map"`
	CraftChannelMap []any                       `json:"craft-channel-map"`
	Package         listReleasesPackageResponse `json:"package"`
	Revisions       []listReleasesRevisionRow   `json:"revisions"`
}

type listReleaseChannelMapItem struct {
	Base           *core.Base                `json:"base"`
	Channel        string                    `json:"channel"`
	ExpirationDate *time.Time                `json:"expiration-date"`
	Resources      []core.ReleaseResourceRef `json:"resources"`
	Revision       int                       `json:"revision"`
	When           time.Time                 `json:"when"`
}

type listReleasesPackageResponse struct {
	Channels []releaseChannelDescriptorResponse `json:"channels"`
}

type listReleasesRevisionRow struct {
	Bases     []core.Base `json:"bases"`
	CreatedAt time.Time   `json:"created-at"`
	CreatedBy string      `json:"created-by"`
	Errors    []any       `json:"errors"`
	Revision  int         `json:"revision"`
	SHA384    string      `json:"sha3-384"`
	Size      int64       `json:"size"`
	Status    string      `json:"status"`
	Version   string      `json:"version"`
}

type releaseChannelDescriptorResponse struct {
	Name     string  `json:"name"`
	Track    string  `json:"track"`
	Risk     string  `json:"risk"`
	Branch   *string `json:"branch"`
	Fallback *string `json:"fallback"`
}

type ociImageUploadCredentialsResponse struct {
	ImageName string `json:"image-name"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}

// ResourceListItemResponse describes one resource returned by ListResources.
type ResourceListItemResponse struct {
	Name     string `json:"name"`
	Optional bool   `json:"optional"`
	Revision int    `json:"revision"`
	Type     string `json:"type"`
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
