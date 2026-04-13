package core

import "time"

// ResourceDefinition describes a resource declared for a package.
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

// Download describes a downloadable artifact location and its checksums.
type Download struct {
	URL         string `json:"url"`
	Size        int64  `json:"size"`
	HashSHA256  string `json:"hash-sha-256,omitempty"`
	HashSHA384  string `json:"hash-sha-384,omitempty"`
	HashSHA512  string `json:"hash-sha-512,omitempty"`
	HashSHA3384 string `json:"hash-sha3-384,omitempty"`
}

// ResourceRevision describes a specific uploaded revision of a package resource.
type ResourceRevision struct {
	ID              string    `json:"-"`
	ResourceID      string    `json:"-"`
	Name            string    `json:"name"`
	Type            string    `json:"type"`
	Description     string    `json:"description"`
	Filename        string    `json:"filename"`
	Revision        int       `json:"revision"`
	CreatedAt       time.Time `json:"created-at"`
	Size            int64     `json:"size"`
	SHA256          string    `json:"hash-sha-256,omitempty"`
	SHA384          string    `json:"hash-sha-384,omitempty"`
	SHA512          string    `json:"hash-sha-512,omitempty"`
	SHA3384         string    `json:"hash-sha3-384,omitempty"`
	ObjectKey       string    `json:"-"`
	Bases           []Base    `json:"bases,omitempty"`
	Architectures   []string  `json:"architectures,omitempty"`
	PackageRevision *int      `json:"package-revision,omitempty"`
	OCIImageDigest  string    `json:"oci-image-digest,omitempty"`
	OCIImageBlob    string    `json:"-"`
	Download        Download  `json:"download,omitempty"`
}
