// Package apperr defines transport-agnostic application errors.
//
// The service layer returns *apperr.Error values; the HTTP layer (pkg/httpx)
// maps each Kind to a status code. This keeps business logic free of HTTP
// concerns and lets Week 2 workers (Temporal/Asynq) reuse the same errors.
package apperr

import (
	"errors"
	"fmt"
)

// Kind classifies an error so callers can react without string matching.
type Kind string

const (
	KindValidation Kind = "VALIDATION_ERROR"     // bad input shape or values -> 400
	KindNotFound   Kind = "NOT_FOUND"            // entity does not exist -> 404
	KindConflict   Kind = "CONFLICT"             // uniqueness or stale version -> 409
	KindRule       Kind = "BUSINESS_RULE_FAILED" // valid input, forbidden by domain rules -> 422
	KindInternal   Kind = "INTERNAL_ERROR"       // anything unexpected -> 500
)

// Error is the single error type crossing service boundaries.
type Error struct {
	Kind    Kind
	Message string
	Details map[string]any
	Err     error // wrapped cause, never shown to clients
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

// WithDetails attaches structured context (e.g. field errors) and returns e.
func (e *Error) WithDetails(d map[string]any) *Error {
	e.Details = d
	return e
}

func Validation(msg string) *Error { return &Error{Kind: KindValidation, Message: msg} }
func NotFound(msg string) *Error   { return &Error{Kind: KindNotFound, Message: msg} }
func Conflict(msg string) *Error   { return &Error{Kind: KindConflict, Message: msg} }
func Rule(msg string) *Error       { return &Error{Kind: KindRule, Message: msg} }

// Internal wraps an unexpected error. The cause is logged, not returned to clients.
func Internal(err error) *Error {
	return &Error{Kind: KindInternal, Message: "internal server error", Err: err}
}

// KindOf returns the Kind of err, or KindInternal if err is not an *Error.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindInternal
}
