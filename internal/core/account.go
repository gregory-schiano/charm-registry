package core

import "time"

// Account identifies a registry account resolved from the identity provider.
type Account struct {
	ID          string    `json:"id"`
	Subject     string    `json:"-"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display-name"`
	Email       string    `json:"email"`
	Validation  string    `json:"validation"`
	IsAdmin     bool      `json:"is-admin,omitempty"`
	CreatedAt   time.Time `json:"created-at"`
}

// PackageSelector restricts a token to a package by ID or name.
type PackageSelector struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Type string `json:"type"`
}

// StoreToken describes a persisted store-token session and its restrictions.
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

// Identity carries the authenticated account and optional store token.
type Identity struct {
	Account       Account
	Token         *StoreToken
	Authenticated bool
}
