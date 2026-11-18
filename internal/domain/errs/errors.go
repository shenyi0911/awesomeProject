// Package errs defines error categories shared by the domain and application layers,
// so the HTTP layer can map them to status codes without matching error strings.
package errs

import (
	"errors"
	"fmt"
)

// Kind is an error category that determines the HTTP status code.
type Kind int

const (
	KindUnknown Kind = iota
	KindBadRequest
	KindNotFound
	KindConflict
	KindUnauthorized
	KindForbidden
	KindTooMany
	KindUnavailable
	KindBadGateway
	KindInternal
)

// Error is a business error carrying a category.
type Error struct {
	Kind Kind
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	switch {
	case e.Msg != "" && e.Err != nil:
		return e.Msg + ": " + e.Err.Error()
	case e.Msg != "":
		return e.Msg
	case e.Err != nil:
		return e.Err.Error()
	default:
		return "unknown error"
	}
}

func (e *Error) Unwrap() error { return e.Err }

// New creates an error with the given category.
func New(kind Kind, msg string) error { return &Error{Kind: kind, Msg: msg} }

// Wrap wraps an underlying error with a category and message.
func Wrap(kind Kind, err error, msg string) error {
	return &Error{Kind: kind, Msg: msg, Err: err}
}

// NotFoundf creates a not-found error.
func NotFoundf(format string, args ...interface{}) error {
	return &Error{Kind: KindNotFound, Msg: fmt.Sprintf(format, args...)}
}

// KindOf extracts the error category; anything other than *Error is treated as unknown (HTTP 500).
func KindOf(err error) Kind {
	if err == nil {
		return KindUnknown
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindUnknown
}

// PublicOf returns the user-facing message of a categorized error without its wrapped cause,
// or an empty string when there is no message to expose.
func PublicOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Msg
	}
	return ""
}
