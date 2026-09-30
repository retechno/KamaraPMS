// Package apperr defines the application error model.
//
// Every error that reaches the HTTP boundary is, or is converted into, an *Error
// with a stable machine-readable Code (for example ROOM_NOT_AVAILABLE). The Kind
// decides the HTTP status; the Err cause is logged but never exposed to clients.
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// Kind classifies an error and determines its HTTP status.
type Kind uint8

const (
	KindInternal     Kind = iota // 500: a bug or an unexpected failure
	KindBadRequest               // 400: malformed request
	KindUnauthorized             // 401: not authenticated
	KindForbidden                // 403: authenticated but not permitted
	KindNotFound                 // 404: missing, or not visible to the caller
	KindConflict                 // 409: business rule or state conflict
	KindInvalid                  // 422: well-formed but invalid input
	KindBusy                     // 409: temporary contention (lock timeout, deadlock); retriable
	KindUnavailable              // 503: dependency unavailable
	KindRateLimited              // 429: too many attempts; retry later
)

// HTTPStatus maps the kind to its HTTP status code.
func (k Kind) HTTPStatus() int {
	switch k {
	case KindBadRequest:
		return http.StatusBadRequest
	case KindUnauthorized:
		return http.StatusUnauthorized
	case KindForbidden:
		return http.StatusForbidden
	case KindNotFound:
		return http.StatusNotFound
	case KindConflict, KindBusy:
		return http.StatusConflict
	case KindInvalid:
		return http.StatusUnprocessableEntity
	case KindUnavailable:
		return http.StatusServiceUnavailable
	case KindRateLimited:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

func (k Kind) String() string {
	switch k {
	case KindBadRequest:
		return "bad_request"
	case KindUnauthorized:
		return "unauthorized"
	case KindForbidden:
		return "forbidden"
	case KindNotFound:
		return "not_found"
	case KindConflict:
		return "conflict"
	case KindInvalid:
		return "invalid"
	case KindBusy:
		return "busy"
	case KindUnavailable:
		return "unavailable"
	case KindRateLimited:
		return "rate_limited"
	default:
		return "internal"
	}
}

// FieldError describes one invalid input field.
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// Error is the application error.
type Error struct {
	Kind      Kind
	Code      string         // stable, e.g. ROOM_NOT_AVAILABLE
	Message   string         // human-readable, safe to show to clients
	Fields    []FieldError   // per-field validation problems
	Context   map[string]any // structured, client-safe details (e.g. conflicting nights)
	Retryable bool           // the same request may succeed if retried
	Err       error          // underlying cause; logged, never sent to clients
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// WithContext adds a client-safe detail and returns e for chaining.
func (e *Error) WithContext(key string, value any) *Error {
	if e.Context == nil {
		e.Context = make(map[string]any)
	}
	e.Context[key] = value
	return e
}

// WithCause records the underlying cause and returns e for chaining.
func (e *Error) WithCause(err error) *Error {
	e.Err = err
	return e
}

// New creates an error of the given kind.
func New(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

func BadRequest(code, message string) *Error   { return New(KindBadRequest, code, message) }
func Unauthorized(code, message string) *Error { return New(KindUnauthorized, code, message) }
func Forbidden(code, message string) *Error    { return New(KindForbidden, code, message) }
func NotFound(code, message string) *Error     { return New(KindNotFound, code, message) }
func Conflict(code, message string) *Error     { return New(KindConflict, code, message) }
func Unavailable(code, message string) *Error  { return New(KindUnavailable, code, message) }

// Busy reports temporary contention; the client may retry the same request.
func Busy(code, message string) *Error {
	e := New(KindBusy, code, message)
	e.Retryable = true
	return e
}

// Invalid reports input validation failures.
func Invalid(message string, fields ...FieldError) *Error {
	return &Error{Kind: KindInvalid, Code: "VALIDATION_FAILED", Message: message, Fields: fields}
}

// Internal wraps an unexpected error. Its details are never exposed to clients.
func Internal(err error) *Error {
	return &Error{Kind: KindInternal, Code: "INTERNAL", Message: "an unexpected error occurred", Err: err}
}

// As returns the *Error in err's chain, if any.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// IsCode reports whether err's chain contains an *Error with the given code.
func IsCode(err error, code string) bool {
	e, ok := As(err)
	return ok && e.Code == code
}
