package service

import "fmt"

// Error describes an API error response.
type Error struct {
	Status  int
	Code    string
	Message string
}

// Error implements the [error] interface.
func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func newError(status int, code, message string) error {
	return &Error{Status: status, Code: code, Message: message}
}
