package service

import (
	"errors"
	"fmt"

	"github.com/gschiano/charm-registry/internal/repo"
)

// Error describes an API error response.
type Error struct {
	Kind    ErrorKind
	Code    string
	Message string
	Cause   error
}

// ErrorKind classifies service-layer errors for HTTP translation.
type ErrorKind string

const (
	// ErrorKindInvalidRequest indicates that the caller sent an invalid request.
	ErrorKindInvalidRequest ErrorKind = "invalid-request"
	// ErrorKindUnauthorized indicates that authentication is required or failed.
	ErrorKindUnauthorized ErrorKind = "unauthorized"
	// ErrorKindForbidden indicates that the caller is not allowed to perform the action.
	ErrorKindForbidden ErrorKind = "forbidden"
	// ErrorKindNotFound indicates that the requested entity does not exist.
	ErrorKindNotFound ErrorKind = "not-found"
	// ErrorKindConflict indicates that the requested action conflicts with existing state.
	ErrorKindConflict ErrorKind = "conflict"
)

const (
	messageNoReleasedRevisionsFound = "no released revisions found"
	messagePackageAlreadyExists     = "package already exists"
	messagePackageNotFound          = "package not found"
	messagePackageRevisionNotFound  = "package revision not found"
	messageReleaseNotFound          = "release not found"
	messageResourceNotDeclared      = "resource not declared"
	messageResourceNotFound         = "resource not found"
	messageResourceRevisionNotFound = "resource revision not found"
	messageRevisionNotFound         = "revision not found"
	messageSyncRuleAlreadyExists    = "sync rule already exists"
	messageSyncRuleNotFound         = "sync rule not found"
	messageUploadNotFound           = "upload not found"
)

// Error implements the [error] interface.
func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap returns the underlying cause, enabling [errors.Is] and [errors.As].
func (e *Error) Unwrap() error {
	return e.Cause
}

// NewError creates a typed service error.
func NewError(kind ErrorKind, code, message string) error {
	return &Error{Kind: kind, Code: code, Message: message}
}

// NewErrorWithCause creates a typed service error that wraps a lower-level cause.
func NewErrorWithCause(kind ErrorKind, code, message string, cause error) error {
	return &Error{Kind: kind, Code: code, Message: message, Cause: cause}
}

// TranslateRepoError converts a repository-layer error into a typed service
// error with an appropriate HTTP status code and Charmhub API error code.
// Unrecognised errors are returned as-is so the API layer can log and return
// a generic 500.
func TranslateRepoError(err error, message string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, repo.ErrNotFound):
		return NewError(ErrorKindNotFound, "not-found", message)
	case errors.Is(err, repo.ErrConflict):
		return NewError(ErrorKindConflict, "already-registered", message)
	default:
		return err
	}
}

func newError(kind ErrorKind, code, message string) error {
	return NewError(kind, code, message)
}

func newErrorWithCause(kind ErrorKind, code, message string, cause error) error {
	return NewErrorWithCause(kind, code, message, cause)
}

func translateRepoError(err error, message string) error {
	return TranslateRepoError(err, message)
}
