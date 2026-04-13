package core

import "time"

// ReleaseResourceRef identifies the resource revision included in a release.
type ReleaseResourceRef struct {
	Name     string `json:"name"`
	Revision *int   `json:"revision"`
}

// Release assigns a package revision to a channel and optional resources.
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
