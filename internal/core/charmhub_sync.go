package core

import "time"

// CharmhubSyncRule describes a configured Charmhub track mirror.
type CharmhubSyncRule struct {
	PackageName        string     `json:"name"`
	Track              string     `json:"track"`
	CreatedByAccountID string     `json:"-"`
	CreatedAt          time.Time  `json:"created-at"`
	UpdatedAt          time.Time  `json:"updated-at"`
	LastSyncStatus     string     `json:"status"`
	LastSyncStartedAt  *time.Time `json:"last-sync-started-at,omitempty"`
	LastSyncFinishedAt *time.Time `json:"last-sync-finished-at,omitempty"`
	LastSyncError      *string    `json:"last-sync-error,omitempty"`
}
