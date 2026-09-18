package kernel

import (
	"errors"
	"fmt"
	"net/http"
)

// Error is an error that the API shows to the user as RFC 9457 problem details.
// Code is stable; Detail says what happened, why, and what to do next (BUILD §7).
type Error struct {
	Status int
	Code   string
	Detail string
	Err    error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Detail + ": " + e.Err.Error()
	}
	return e.Detail
}

func (e *Error) Unwrap() error { return e.Err }

// AsError returns the *Error in err's chain, if there is one.
func AsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// NotFound is a 404 error.
func NotFound(code, format string, args ...any) *Error {
	return &Error{Status: http.StatusNotFound, Code: code, Detail: fmt.Sprintf(format, args...)}
}

// Invalid is a 400 error for input that breaks a rule.
func Invalid(code, format string, args ...any) *Error {
	return &Error{Status: http.StatusBadRequest, Code: code, Detail: fmt.Sprintf(format, args...)}
}

// Conflict is a 409 error.
func Conflict(code, format string, args ...any) *Error {
	return &Error{Status: http.StatusConflict, Code: code, Detail: fmt.Sprintf(format, args...)}
}

// TooLarge is a 413 error.
func TooLarge(code, format string, args ...any) *Error {
	return &Error{Status: http.StatusRequestEntityTooLarge, Code: code, Detail: fmt.Sprintf(format, args...)}
}
