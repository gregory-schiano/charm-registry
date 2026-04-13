package core

import "time"

// Base describes a supported operating-system base for a package or resource.
type Base struct {
	Architecture  string   `json:"architecture,omitempty"  yaml:"architecture,omitempty"`
	Architectures []string `json:"architectures,omitempty" yaml:"architectures,omitempty"`
	Channel       string   `json:"channel,omitempty"       yaml:"channel,omitempty"`
	Name          string   `json:"name,omitempty"          yaml:"name,omitempty"`
}

// Relation describes a named charm relation endpoint.
type Relation struct {
	Interface string `json:"interface" yaml:"interface"`
}

// Revision describes a published charm revision and its extracted metadata.
type Revision struct {
	ID           string                         `json:"-"`
	PackageID    string                         `json:"-"`
	Revision     int                            `json:"revision"`
	Version      string                         `json:"version"`
	Status       string                         `json:"status"`
	CreatedAt    time.Time                      `json:"created-at"`
	CreatedBy    string                         `json:"created-by"`
	Size         int64                          `json:"size"`
	SHA256       string                         `json:"sha256,omitempty"`
	SHA384       string                         `json:"sha3-384,omitempty"`
	ObjectKey    string                         `json:"-"`
	MetadataYAML string                         `json:"metadata-yaml,omitempty"`
	ConfigYAML   string                         `json:"config-yaml,omitempty"`
	ActionsYAML  string                         `json:"actions-yaml,omitempty"`
	BundleYAML   string                         `json:"bundle-yaml,omitempty"`
	ReadmeMD     string                         `json:"readme-md,omitempty"`
	Bases        []Base                         `json:"bases,omitempty"`
	Attributes   map[string]string              `json:"attributes,omitempty"`
	Relations    map[string]map[string]Relation `json:"relations,omitempty"`
	Subordinate  bool                           `json:"subordinate,omitempty"`
	Resources    []ResourceRevision             `json:"resources,omitempty"`
}
