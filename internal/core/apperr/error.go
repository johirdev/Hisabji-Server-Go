package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// FieldError is one validation problem tied to one request field. The frontend
// can map `Field` straight onto its form input name.
type FieldError struct {
	Field   string `json:"field"`             // json name, e.g. "amount" or "items[0].price"
	Rule    string `json:"rule,omitempty"`    // "required" | "min" | "email" | ...
	Message string `json:"message"`           // human readable, English
	MessageBN string `json:"message_bn,omitempty"` // Bangla, when we have one
	Value   any    `json:"value,omitempty"`   // what was received (never for password fields)
}

// Error is the one error type that travels through the app. Everything the
// HTTP layer needs to render a response lives on it.
type Error struct {
	Status  int          `json:"-"`                 // HTTP status to write
	Code    Code         `json:"code"`              // stable machine code
	Message string       `json:"message"`           // safe to show a user
	Fields  []FieldError `json:"fields,omitempty"`  // per-field validation detail
	Details any          `json:"details,omitempty"` // extra structured context
	Hint    string       `json:"hint,omitempty"`    // "what should I do now?"
	// cause is the underlying error. It is logged but NEVER serialised, so
	// internal details (SQL text, host names) cannot leak to a client.
	cause error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap lets errors.Is / errors.As see through to the cause.
func (e *Error) Unwrap() error { return e.cause }

// WithCause attaches the internal error for logging. Returns e for chaining.
func (e *Error) WithCause(err error) *Error { e.cause = err; return e }

// WithHint adds a "next action" string for the client.
func (e *Error) WithHint(h string) *Error { e.Hint = h; return e }

// WithDetails attaches arbitrary structured context (must be JSON-safe).
func (e *Error) WithDetails(d any) *Error { e.Details = d; return e }

// WithField appends one field-level problem.
func (e *Error) WithField(f FieldError) *Error { e.Fields = append(e.Fields, f); return e }

// Cause exposes the wrapped error to the logger.
func (e *Error) Cause() error { return e.cause }

// New builds an Error explicitly.
func New(status int, code Code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// ---------------------------------------------------------------------------
// Constructors — use these instead of errors.New anywhere in service code.
// ---------------------------------------------------------------------------

func BadRequest(msg string) *Error { return New(http.StatusBadRequest, CodeBadRequest, msg) }

func Validation(msg string, fields ...FieldError) *Error {
	e := New(http.StatusUnprocessableEntity, CodeValidation, msg)
	e.Fields = fields
	return e
}

func MissingField(field, msg string) *Error {
	return New(http.StatusUnprocessableEntity, CodeMissingField, msg).
		WithField(FieldError{Field: field, Rule: "required", Message: msg})
}

func InvalidField(field, msg string) *Error {
	return New(http.StatusUnprocessableEntity, CodeInvalidField, msg).
		WithField(FieldError{Field: field, Rule: "invalid", Message: msg})
}

func Unauthorized(msg string) *Error {
	if msg == "" {
		msg = "Authentication is required to access this resource."
	}
	return New(http.StatusUnauthorized, CodeUnauthorized, msg)
}

func Forbidden(msg string) *Error {
	if msg == "" {
		msg = "You do not have permission to perform this action."
	}
	return New(http.StatusForbidden, CodeForbidden, msg)
}

func NotFound(resource string) *Error {
	return New(http.StatusNotFound, CodeNotFound, resource+" was not found.")
}

func Conflict(msg string) *Error { return New(http.StatusConflict, CodeConflict, msg) }

func Duplicate(field, msg string) *Error {
	e := New(http.StatusConflict, CodeDuplicate, msg)
	if field != "" {
		e.WithField(FieldError{Field: field, Rule: "unique", Message: msg})
	}
	return e
}

func RateLimited(msg string, retryAfterSeconds int) *Error {
	return New(http.StatusTooManyRequests, CodeRateLimited, msg).
		WithDetails(map[string]any{"retry_after_seconds": retryAfterSeconds})
}

func PaymentRequired(code Code, msg string) *Error {
	return New(http.StatusPaymentRequired, code, msg)
}

func Internal(msg string) *Error {
	if msg == "" {
		msg = "Something went wrong on our side. Please try again."
	}
	return New(http.StatusInternalServerError, CodeInternal, msg)
}

func Database(err error) *Error {
	return New(http.StatusInternalServerError, CodeDatabase,
		"A database error occurred. Please try again.").WithCause(err)
}

func Upstream(service string, err error) *Error {
	return New(http.StatusBadGateway, CodeUpstream,
		service+" is currently unavailable. Please try again shortly.").WithCause(err)
}

func Unavailable(msg string) *Error {
	return New(http.StatusServiceUnavailable, CodeUnavailable, msg)
}

func Timeout(msg string) *Error {
	if msg == "" {
		msg = "The request took too long to complete."
	}
	return New(http.StatusGatewayTimeout, CodeTimeout, msg)
}

// ---------------------------------------------------------------------------
// Sentinels — repositories return these; services translate them.
// ---------------------------------------------------------------------------

var (
	// ErrNoRows is what a repository returns when a lookup found nothing. The
	// service decides whether that is a 404, an empty result, or fine.
	ErrNoRows = errors.New("apperr: no rows")
)

// As extracts an *Error from any error chain, or nil if there is none.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// IsCode reports whether err (or anything it wraps) carries the given code.
func IsCode(err error, code Code) bool {
	if e, ok := As(err); ok {
		return e.Code == code
	}
	return false
}

// Status returns the HTTP status an error should produce (500 when unknown).
func Status(err error) int {
	if e, ok := As(err); ok {
		return e.Status
	}
	return http.StatusInternalServerError
}
