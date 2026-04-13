package core

import "time"

// Upload describes a previously uploaded artifact awaiting or completing review.
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

// APIError describes a structured API error payload entry.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
