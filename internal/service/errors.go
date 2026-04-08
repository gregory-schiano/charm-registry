package service

import "fmt"

// Error describes an API error response.
type Error struct {
	Kind    ErrorKind
	Code    string
	Message string
}

type ErrorKind string

const (
	ErrorKindInvalidRequest ErrorKind = "invalid-request"
	ErrorKindUnauthorized   ErrorKind = "unauthorized"
	ErrorKindForbidden      ErrorKind = "forbidden"
	ErrorKindNotFound       ErrorKind = "not-found"
	ErrorKindConflict       ErrorKind = "conflict"
)

// Error implements the [error] interface.
func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func newError(kind ErrorKind, code, message string) error {
	return &Error{Kind: kind, Code: code, Message: message}
}
