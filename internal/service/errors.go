package service

import "fmt"

// Error describes an API error response.
type Error struct {
	Kind    ErrorKind
	Code    string
	Message string
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

// Error implements the [error] interface.
func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func newError(kind ErrorKind, code, message string) error {
	return &Error{Kind: kind, Code: code, Message: message}
}
