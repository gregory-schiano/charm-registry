package core

import "time"

// ReleaseResourceRef identifies the resource revision included in a release.
type ReleaseResourceRef struct {
	Name     string `json:"name"`
	Revision *int   `json:"revision"`
}

// Release assigns a package revision to a channel and optional resources.
//
// Nil-base semantics: when Base is nil the release is a channel-wide
// singleton. A nil Base means "this package has no OS/architecture variant",
// so at most one release per (package, channel) pair may carry a nil base.
// ReplaceRelease treats the nil-base slot as a single key — a second
// nil-base release on the same channel silently overwrites the first.
//
// Base-variant releases and the nil-base release coexist on the same channel
// without conflict because the storage layer keys on the serialized base
// (PostgreSQL generated column base::text; SQLite stored column base_key).
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
