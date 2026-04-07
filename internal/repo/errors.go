package repo

import "errors"

// ErrNotFound is returned by the repository when a requested record does not
// exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned by the repository when an insert would violate a
// uniqueness constraint (e.g. duplicate package name).
var ErrConflict = errors.New("conflict")
