package core

import "time"

type Account struct {
	ID          string    `json:"id"`
	Subject     string    `json:"-"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display-name"`
	Email       string    `json:"email"`
	Validation  string    `json:"validation"`
	CreatedAt   time.Time `json:"created-at"`
}

type PackageSelector struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Type string `json:"type"`
}

type StoreToken struct {
	SessionID   string            `json:"session-id"`
	TokenHash   string            `json:"-"`
	AccountID   string            `json:"-"`
	Description *string           `json:"description,omitempty"`
	Packages    []PackageSelector `json:"packages,omitempty"`
	Channels    []string          `json:"channels,omitempty"`
	Permissions []string          `json:"permissions,omitempty"`
	ValidSince  time.Time         `json:"valid-since"`
	ValidUntil  time.Time         `json:"valid-until"`
	RevokedAt   *time.Time        `json:"revoked-at,omitempty"`
	RevokedBy   *string           `json:"revoked-by,omitempty"`
}

type Media struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type Track struct {
	Name                       string    `json:"name"`
	VersionPattern             *string   `json:"version-pattern"`
	AutomaticPhasingPercentage *float64  `json:"automatic-phasing-percentage"`
	CreatedAt                  time.Time `json:"created-at"`
}

type TrackGuardrail struct {
	Pattern   string    `json:"pattern"`
	CreatedAt time.Time `json:"created-at"`
}

type Publisher struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display-name"`
	Email       string `json:"email,omitempty"`
	Validation  string `json:"validation"`
}

type Package struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Type            string              `json:"type"`
	Private         bool                `json:"private"`
	Status          string              `json:"status"`
	OwnerAccountID  string              `json:"-"`
	Authority       *string             `json:"authority,omitempty"`
	Contact         *string             `json:"contact,omitempty"`
	DefaultTrack    *string             `json:"default-track,omitempty"`
	Description     *string             `json:"description,omitempty"`
	Summary         *string             `json:"summary,omitempty"`
	Title           *string             `json:"title,omitempty"`
	Website         *string             `json:"website,omitempty"`
	Links           map[string][]string `json:"links,omitempty"`
	Media           []Media             `json:"media,omitempty"`
	TrackGuardrails []TrackGuardrail    `json:"track-guardrails,omitempty"`
	Tracks          []Track             `json:"tracks,omitempty"`
	Publisher       Publisher           `json:"publisher"`
	Store           string              `json:"store"`
	CreatedAt       time.Time           `json:"created-at"`
	UpdatedAt       time.Time           `json:"updated-at"`
}

type Base struct {
	Architecture string `json:"architecture" yaml:"architecture"`
	Channel      string `json:"channel" yaml:"channel"`
	Name         string `json:"name" yaml:"name"`
}

type Relation struct {
	Interface string `json:"interface" yaml:"interface"`
}

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

type ResourceDefinition struct {
	ID          string    `json:"-"`
	PackageID   string    `json:"-"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Description string    `json:"description"`
	Filename    string    `json:"filename"`
	Optional    bool      `json:"optional"`
	CreatedAt   time.Time `json:"created-at"`
}

type Download struct {
	URL         string `json:"url"`
	Size        int64  `json:"size"`
	HashSHA256  string `json:"hash-sha-256,omitempty"`
	HashSHA384  string `json:"hash-sha-384,omitempty"`
	HashSHA512  string `json:"hash-sha-512,omitempty"`
	HashSHA3384 string `json:"hash-sha3-384,omitempty"`
}

type ResourceRevision struct {
	ID             string    `json:"-"`
	ResourceID     string    `json:"-"`
	Name           string    `json:"name"`
	Type           string    `json:"type"`
	Description    string    `json:"description"`
	Filename       string    `json:"filename"`
	Revision       int       `json:"revision"`
	CreatedAt      time.Time `json:"created-at"`
	Size           int64     `json:"size"`
	SHA256         string    `json:"hash-sha-256,omitempty"`
	SHA384         string    `json:"hash-sha-384,omitempty"`
	SHA512         string    `json:"hash-sha-512,omitempty"`
	SHA3384        string    `json:"hash-sha3-384,omitempty"`
	ObjectKey      string    `json:"-"`
	Bases          []Base    `json:"bases,omitempty"`
	Architectures  []string  `json:"architectures,omitempty"`
	OCIImageDigest string    `json:"oci-image-digest,omitempty"`
	OCIImageBlob   string    `json:"-"`
	Download       Download  `json:"download,omitempty"`
}

type ReleaseResourceRef struct {
	Name     string `json:"name"`
	Revision *int   `json:"revision"`
}

type Release struct {
	ID             string               `json:"-"`
	PackageID      string               `json:"-"`
	Channel        string               `json:"channel"`
	Revision       int                  `json:"revision"`
	Base           *Base                `json:"base,omitempty"`
	Resources      []ReleaseResourceRef `json:"resources,omitempty"`
	When           time.Time            `json:"when"`
	ExpirationDate *time.Time           `json:"expiration-date"`
	Progressive    *float64             `json:"progressive,omitempty"`
}

type Upload struct {
	ID         string     `json:"upload-id"`
	Filename   string     `json:"filename"`
	ObjectKey  string     `json:"-"`
	Size       int64      `json:"size"`
	SHA256     string     `json:"sha256"`
	SHA384     string     `json:"sha384"`
	Status     string     `json:"status"`
	Kind       string     `json:"kind"`
	CreatedAt  time.Time  `json:"created-at"`
	ApprovedAt *time.Time `json:"approved-at,omitempty"`
	Revision   *int       `json:"revision,omitempty"`
	Errors     []APIError `json:"errors,omitempty"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Identity struct {
	Account       Account
	Token         *StoreToken
	Authenticated bool
}

type CharmArchive struct {
	MetadataYAML string
	ConfigYAML   string
	ActionsYAML  string
	BundleYAML   string
	ReadmeMD     string
	Manifest     CharmManifest
}

type CharmManifest struct {
	Name        string
	DisplayName string `yaml:"display-name"`
	Summary     string
	Description string
	Docs        string
	Issues      string
	Source      string
	Website     any
	Subordinate bool
	Resources   map[string]CharmResourceDeclaration
	Containers  map[string]CharmContainer
	Provides    map[string]Relation
	Requires    map[string]Relation
	Peers       map[string]Relation
	Assumes     any
}

type CharmResourceDeclaration struct {
	Type           string `yaml:"type"`
	Description    string `yaml:"description"`
	Filename       string `yaml:"filename"`
	UpstreamSource string `yaml:"upstream-source"`
}

type CharmContainer struct {
	Resource string `yaml:"resource"`
}
