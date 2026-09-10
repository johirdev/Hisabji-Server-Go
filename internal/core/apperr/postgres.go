package apperr

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ConstraintMessage describes how one database constraint should be reported
// to a human. Modules register their own so that a raw
// `users_email_key violation` becomes "This email is already registered."
type ConstraintMessage struct {
	Field     string // json field name the client should highlight
	Message   string
	MessageBN string
	Code      Code // defaults to CodeDuplicate for unique violations
}

var (
	constraintMu sync.RWMutex
	constraints  = map[string]ConstraintMessage{}
)

// RegisterConstraint teaches the translator about one database constraint.
// Call it from a module's init() or from RegisterConstraints() at boot.
func RegisterConstraint(name string, m ConstraintMessage) {
	constraintMu.Lock()
	defer constraintMu.Unlock()
	constraints[name] = m
}

// RegisterConstraints registers many at once.
func RegisterConstraints(m map[string]ConstraintMessage) {
	constraintMu.Lock()
	defer constraintMu.Unlock()
	for k, v := range m {
		constraints[k] = v
	}
}

func lookupConstraint(name string) (ConstraintMessage, bool) {
	constraintMu.RLock()
	defer constraintMu.RUnlock()
	m, ok := constraints[name]
	return m, ok
}

// FromPostgres converts a driver error into a user-facing error. Anything it
// does not recognise becomes a generic 500 with the original error preserved
// as the (never-serialised) cause.
//
// It returns the `error` interface, NOT *Error, and that is deliberate. With a
// *Error return type, this idiom silently breaks:
//
//	func Save(...) error { return apperr.FromPostgres(tx.Commit(ctx), "X") }
//
// A nil *Error assigned to an `error` interface produces a non-nil interface
// holding a nil pointer, so the caller's `if err != nil` is true and the next
// `err.Error()` panics. Returning `error` makes nil mean nil everywhere.
//
// Callers that need the concrete type use Translate instead.
func FromPostgres(err error, resource string) error {
	if err == nil {
		return nil
	}
	return Translate(err, resource)
}

// Translate is FromPostgres for callers that need the concrete *Error, such as
// the HTTP writer. It never returns nil for a non-nil input.
func Translate(err error, resource string) *Error {
	if err == nil {
		return nil
	}
	if e, ok := As(err); ok { // already translated upstream
		return e
	}

	switch {
	case errors.Is(err, pgx.ErrNoRows), errors.Is(err, ErrNoRows):
		if resource == "" {
			resource = "Resource"
		}
		return NotFound(resource).WithCause(err)
	case errors.Is(err, context.DeadlineExceeded):
		return Timeout("").WithCause(err)
	case errors.Is(err, context.Canceled):
		return New(499, CodeBadRequest, "The request was cancelled.").WithCause(err)
	case errors.Is(err, pgx.ErrTxClosed), errors.Is(err, pgx.ErrTxCommitRollback):
		return Database(err)
	}

	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return Database(err)
	}

	switch pg.Code {
	case "23505": // unique_violation
		if m, ok := lookupConstraint(pg.ConstraintName); ok {
			code := m.Code
			if code == "" {
				code = CodeDuplicate
			}
			return New(http.StatusConflict, code, m.Message).
				WithField(FieldError{Field: m.Field, Rule: "unique", Message: m.Message, MessageBN: m.MessageBN}).
				WithCause(err)
		}
		field := guessFieldFromConstraint(pg.ConstraintName)
		msg := "This value is already in use."
		if field != "" {
			msg = "This " + strings.ReplaceAll(field, "_", " ") + " is already in use."
		}
		return Duplicate(field, msg).WithCause(err)

	case "23503": // foreign_key_violation
		if m, ok := lookupConstraint(pg.ConstraintName); ok {
			return New(http.StatusUnprocessableEntity, CodeInvalidField, m.Message).
				WithField(FieldError{Field: m.Field, Rule: "exists", Message: m.Message, MessageBN: m.MessageBN}).
				WithCause(err)
		}
		field := guessFieldFromConstraint(pg.ConstraintName)
		return New(http.StatusUnprocessableEntity, CodeInvalidField,
			"A referenced record does not exist or is still in use.").
			WithField(FieldError{Field: field, Rule: "exists", Message: "Referenced record was not found."}).
			WithCause(err)

	case "23502": // not_null_violation
		return New(http.StatusUnprocessableEntity, CodeMissingField,
			"A required field is missing.").
			WithField(FieldError{Field: pg.ColumnName, Rule: "required",
				Message: humanise(pg.ColumnName) + " is required."}).
			WithCause(err)

	case "23514": // check_violation
		if m, ok := lookupConstraint(pg.ConstraintName); ok {
			return New(http.StatusUnprocessableEntity, CodeInvalidField, m.Message).
				WithField(FieldError{Field: m.Field, Rule: "invalid", Message: m.Message, MessageBN: m.MessageBN}).
				WithCause(err)
		}
		return New(http.StatusUnprocessableEntity, CodeInvalidField,
			"One of the submitted values is not allowed.").WithCause(err)

	case "22001": // string too long
		return New(http.StatusUnprocessableEntity, CodeInvalidField,
			"One of the submitted values is too long.").WithCause(err)

	case "22003": // numeric out of range
		return New(http.StatusUnprocessableEntity, CodeInvalidField,
			"A numeric value is out of the allowed range.").WithCause(err)

	case "22P02", "22007", "22008": // bad uuid / bad timestamp
		return New(http.StatusBadRequest, CodeBadRequest,
			"One of the submitted values has an invalid format.").WithCause(err)

	case "40001", "40P01": // serialization failure / deadlock
		return New(http.StatusConflict, CodeConflict,
			"The request conflicted with another operation. Please retry.").
			WithHint("Retry the request.").WithCause(err)

	case "57014": // query canceled (statement_timeout)
		return Timeout("The database query took too long.").WithCause(err)

	case "53300", "53400": // too many connections / config limit
		return Unavailable("The service is busy. Please try again in a moment.").WithCause(err)

	case "42501": // insufficient privilege
		return Internal("").WithCause(err)
	}

	return Database(err)
}

// guessFieldFromConstraint turns "users_email_key" / "expenses_user_id_fkey"
// into "email" / "user_id" on a best-effort basis.
func guessFieldFromConstraint(name string) string {
	if name == "" {
		return ""
	}
	s := name
	for _, suffix := range []string{"_key", "_fkey", "_check", "_unique", "_idx", "_pkey"} {
		s = strings.TrimSuffix(s, suffix)
	}
	// drop the leading table name segment when there is more than one left
	if i := strings.Index(s, "_"); i > 0 && i < len(s)-1 {
		return s[i+1:]
	}
	return s
}

func humanise(col string) string {
	if col == "" {
		return "A field"
	}
	parts := strings.Split(col, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}
