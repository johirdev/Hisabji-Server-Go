package apperr

import (
	"errors"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestFromPostgresNilIsATrueNil guards against the typed-nil trap.
//
// If FromPostgres ever goes back to returning *Error, this idiom silently
// breaks and every "success" return from a repository becomes a non-nil error
// interface wrapping a nil pointer — which panics on the caller's next
// err.Error(). It cost a production 500 once; it must not do so twice.
func TestFromPostgresNilIsATrueNil(t *testing.T) {
	// The exact shape that broke: a repository method ending in a bare return.
	save := func() error { return FromPostgres(nil, "Verification code") }

	err := save()
	if err != nil {
		t.Fatalf("FromPostgres(nil) must produce a nil error interface, got %#v", err)
	}

	// And the same through a second hop, which is where it usually bites.
	wrap := func() error { return save() }
	if wrap() != nil {
		t.Fatal("a nil error must stay nil through another return")
	}
}

func TestFromPostgresTranslatesUniqueViolation(t *testing.T) {
	RegisterConstraint("users_phone_key", ConstraintMessage{
		Field:   "phone",
		Message: "This phone number is already registered.",
	})

	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "users_phone_key"}
	err := FromPostgres(pgErr, "User")

	e, ok := As(err)
	if !ok {
		t.Fatalf("expected an *Error, got %#v", err)
	}
	if e.Status != http.StatusConflict || e.Code != CodeDuplicate {
		t.Errorf("got %d %s, want 409 DUPLICATE_ENTRY", e.Status, e.Code)
	}
	if len(e.Fields) != 1 || e.Fields[0].Field != "phone" {
		t.Errorf("the offending field should be named: %#v", e.Fields)
	}
}

func TestUnknownConstraintStillGuessesTheField(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "goals_title_key"}
	e, _ := As(FromPostgres(pgErr, "Goal"))

	if len(e.Fields) == 0 || e.Fields[0].Field != "title" {
		t.Errorf("expected the field to be guessed as \"title\", got %#v", e.Fields)
	}
}

func TestNoRowsBecomesNotFound(t *testing.T) {
	e, ok := As(FromPostgres(pgx.ErrNoRows, "Expense"))
	if !ok || e.Status != http.StatusNotFound {
		t.Fatalf("pgx.ErrNoRows should become a 404, got %#v", e)
	}
	if e.Message != "Expense was not found." {
		t.Errorf("message should name the resource, got %q", e.Message)
	}
}

func TestNotNullViolationNamesTheColumn(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23502", ColumnName: "monthly_income"}
	e, _ := As(FromPostgres(pgErr, "Budget"))

	if e.Code != CodeMissingField {
		t.Errorf("got %s, want MISSING_FIELD", e.Code)
	}
	if e.Fields[0].Message != "Monthly Income is required." {
		t.Errorf("column name should be humanised, got %q", e.Fields[0].Message)
	}
}

func TestCauseIsNeverSerialised(t *testing.T) {
	// A 500 must not leak SQL text or host names to a client. The cause is
	// available to the logger through Cause(), and to errors.Is/As through
	// Unwrap, but it carries no json tag.
	secret := errors.New("connection to db-primary.internal:5432 failed: password authentication failed for user \"postgres\"")
	e := Database(secret)

	if e.Cause() == nil {
		t.Fatal("the cause must be retained for logging")
	}
	if !errors.Is(e, secret) {
		t.Error("errors.Is must see through to the cause")
	}
	if e.Message == secret.Error() {
		t.Error("the internal error must not become the user-facing message")
	}
}

func TestIsCodeAndStatusWorkThroughWrapping(t *testing.T) {
	base := NotFound("Expense")
	wrapped := errors.Join(errors.New("in service layer"), base)

	if !IsCode(wrapped, CodeNotFound) {
		t.Error("IsCode must see through wrapping")
	}
	if Status(wrapped) != http.StatusNotFound {
		t.Errorf("Status through wrapping = %d, want 404", Status(wrapped))
	}
	if Status(errors.New("plain")) != http.StatusInternalServerError {
		t.Error("an unknown error must default to 500")
	}
}
