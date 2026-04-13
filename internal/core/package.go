package core

import "time"

// Media describes a package media asset.
type Media struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// Track describes a package release track.
type Track struct {
	Name                       string    `json:"name"`
	VersionPattern             *string   `json:"version-pattern"`
	AutomaticPhasingPercentage *float64  `json:"automatic-phasing-percentage"`
	CreatedAt                  time.Time `json:"created-at"`
}

// TrackGuardrail describes a version pattern constraint for a package track.
type TrackGuardrail struct {
	Pattern   string    `json:"pattern"`
	CreatedAt time.Time `json:"created-at"`
}

// Publisher identifies the publisher of a package.
type Publisher struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display-name"`
	Email       string `json:"email,omitempty"`
	Validation  string `json:"validation"`
}

// Package describes a published charm package and its metadata.
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
	HarborProject   string              `json:"-"`
	HarborPushRobot *RobotCredential    `json:"-"`
	HarborPullRobot *RobotCredential    `json:"-"`
	HarborSyncedAt  *time.Time          `json:"-"`
	CreatedAt       time.Time           `json:"created-at"`
	UpdatedAt       time.Time           `json:"updated-at"`
}

// RobotCredential stores Harbor robot credentials associated with a package.
type RobotCredential struct {
	ID              int64  `json:"-"`
	Username        string `json:"-"`
	EncryptedSecret string `json:"-"`
}
