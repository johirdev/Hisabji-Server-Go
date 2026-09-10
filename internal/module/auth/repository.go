package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/repository"
	"github.com/johirdev/Hisabji-Server/internal/domain"
)

// userColumns is the full projection for a user row. Listing columns explicitly
// rather than using SELECT * means adding a column to the table cannot silently
// change what this query returns.
const userColumns = `
	id, name, username, email, phone, password_hash, avatar_path,
	user_type, role, status, locale, currency, timezone,
	monthly_income, month_start_day,
	phone_verified, phone_verified_at, email_verified, email_verified_at,
	onboarding_step, onboarded_at, plan_code, plan_expires_at,
	failed_login_count, locked_until, password_changed_at,
	last_login_at, last_seen_at, deleted_at, metadata, created_at, updated_at`

// Repository is the auth module's data access.
type Repository struct {
	DB *pgxpool.Pool
}

// NewRepository builds the repository.
func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{DB: db} }

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

// CreateUser inserts a new account. Uniqueness violations come back already
// translated into a friendly 409 by the constraint registry.
func (r *Repository) CreateUser(ctx context.Context, tx repository.DB, in RegisterRequest, passwordHash, userType, locale string) (domain.User, error) {
	return repository.QueryOne[domain.User](ctx, tx, "User", `
		INSERT INTO users (name, phone, email, username, password_hash, user_type, locale)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+userColumns,
		in.Name, in.Phone, in.Email, in.Username, passwordHash, userType, locale)
}

// FindByIdentifier loads a user by phone, email or username in one query.
//
// It deliberately does NOT filter on status: the service must be able to tell a
// suspended user apart from a non-existent one, so it can return the right
// message instead of a confusing "invalid credentials".
func (r *Repository) FindByIdentifier(ctx context.Context, identifier string) (domain.User, error) {
	return repository.QueryOne[domain.User](ctx, r.DB, "Account", `
		SELECT `+userColumns+`
		FROM users
		WHERE deleted_at IS NULL
		  AND (phone = $1 OR email = $1::citext OR username = $1::citext)
		LIMIT 1`, identifier)
}

// FindByID loads a user by primary key.
func (r *Repository) FindByID(ctx context.Context, id string) (domain.User, error) {
	return repository.QueryOne[domain.User](ctx, r.DB, "User", `
		SELECT `+userColumns+`
		FROM users WHERE id = $1 AND deleted_at IS NULL`, id)
}

// FindByPhone loads a user by phone number.
//
// Pass the transaction when reading back a row the same transaction just wrote:
// a read on the pool cannot see uncommitted changes, so verifying a phone and
// then re-reading on the pool returns the OLD row with phone_verified still
// false — and the client shows "unverified" one second after verifying.
func (r *Repository) FindByPhone(ctx context.Context, db repository.DB, phone string) (domain.User, error) {
	if db == nil {
		db = r.DB
	}
	return repository.QueryOne[domain.User](ctx, db, "Account", `
		SELECT `+userColumns+`
		FROM users WHERE phone = $1 AND deleted_at IS NULL`, phone)
}

// PhoneExists reports whether a phone number is already registered.
func (r *Repository) PhoneExists(ctx context.Context, phone string) (bool, error) {
	var exists bool
	err := r.DB.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE phone = $1 AND deleted_at IS NULL)`,
		phone).Scan(&exists)
	if err != nil {
		return false, apperr.FromPostgres(err, "Account")
	}
	return exists, nil
}

// MarkPhoneVerified completes phone verification.
func (r *Repository) MarkPhoneVerified(ctx context.Context, tx repository.DB, phone string) error {
	tag, err := tx.Exec(ctx, `
		UPDATE users
		SET phone_verified = true, phone_verified_at = now(), updated_at = now()
		WHERE phone = $1 AND deleted_at IS NULL`, phone)
	if err != nil {
		return apperr.FromPostgres(err, "Account")
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("Account")
	}
	return nil
}

// RecordLoginSuccess clears the failure counter and stamps the login.
func (r *Repository) RecordLoginSuccess(ctx context.Context, userID, ip string) error {
	_, err := r.DB.Exec(ctx, `
		UPDATE users
		SET failed_login_count = 0,
		    locked_until       = NULL,
		    last_login_at      = now(),
		    last_login_ip      = NULLIF($2, '')::inet,
		    last_seen_at       = now(),
		    updated_at         = now()
		WHERE id = $1`, userID, ip)
	if err != nil {
		return apperr.FromPostgres(err, "Account")
	}
	return nil
}

// RecordLoginFailure increments the counter and locks the account once it
// crosses the threshold.
//
// The increment and the lock decision happen in ONE statement. Doing it as a
// read-then-write would let a burst of parallel guesses each read "4 failures"
// and none of them trigger the lock.
func (r *Repository) RecordLoginFailure(ctx context.Context, userID string, maxAttempts int, lockFor time.Duration) (locked bool, until *time.Time, err error) {
	row := r.DB.QueryRow(ctx, `
		UPDATE users
		SET failed_login_count = failed_login_count + 1,
		    locked_until = CASE
		        WHEN failed_login_count + 1 >= $2 THEN now() + $3::interval
		        ELSE locked_until
		    END,
		    updated_at = now()
		WHERE id = $1
		RETURNING failed_login_count, locked_until`,
		userID, maxAttempts, lockFor.String())

	var count int
	var lockedUntil *time.Time
	if err := row.Scan(&count, &lockedUntil); err != nil {
		return false, nil, apperr.FromPostgres(err, "Account")
	}
	return lockedUntil != nil && lockedUntil.After(time.Now()), lockedUntil, nil
}

// LogAttempt appends to the append-only security audit.
func (r *Repository) LogAttempt(ctx context.Context, identifier string, userID *string, success bool, reason, ip, userAgent string) {
	// Best effort: a failed audit insert must never block a login response.
	_, _ = r.DB.Exec(ctx, `
		INSERT INTO login_attempts (identifier, user_id, success, reason, ip, user_agent)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, '')::inet, NULLIF($6, ''))`,
		identifier, userID, success, reason, ip, userAgent)
}

// UpdatePassword sets a new hash and stamps the change time, which is what
// invalidates every existing session.
func (r *Repository) UpdatePassword(ctx context.Context, tx repository.DB, userID, passwordHash string) error {
	tag, err := tx.Exec(ctx, `
		UPDATE users
		SET password_hash      = $2,
		    password_changed_at = now(),
		    failed_login_count  = 0,
		    locked_until        = NULL,
		    updated_at          = now()
		WHERE id = $1 AND deleted_at IS NULL`, userID, passwordHash)
	if err != nil {
		return apperr.FromPostgres(err, "Account")
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("Account")
	}
	return nil
}

// ---------------------------------------------------------------------------
// OTP codes
// ---------------------------------------------------------------------------

// CountRecentOTPs is the abuse check: how many codes went to this number
// recently. It counts SMS we paid to send, not verification attempts.
func (r *Repository) CountRecentOTPs(ctx context.Context, phone, purpose string, window time.Duration) (int, error) {
	var n int
	err := r.DB.QueryRow(ctx, `
		SELECT count(*) FROM otp_codes
		WHERE phone = $1 AND purpose = $2 AND created_at > now() - $3::interval`,
		phone, purpose, window.String()).Scan(&n)
	if err != nil {
		return 0, apperr.FromPostgres(err, "Verification code")
	}
	return n, nil
}

// LastOTPSentAt supports the resend cooldown.
func (r *Repository) LastOTPSentAt(ctx context.Context, phone, purpose string) (*time.Time, error) {
	var t *time.Time
	err := r.DB.QueryRow(ctx, `
		SELECT max(created_at) FROM otp_codes WHERE phone = $1 AND purpose = $2`,
		phone, purpose).Scan(&t)
	if err != nil {
		return nil, apperr.FromPostgres(err, "Verification code")
	}
	return t, nil
}

// SaveOTP stores a code hash and invalidates any earlier live code for the
// same phone and purpose — so an old SMS the user scrolls back to cannot be
// used after a resend.
func (r *Repository) SaveOTP(ctx context.Context, phone, purpose, codeHash string, userID *string, expiresAt time.Time, maxAttempts int, ip string) error {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return apperr.FromPostgres(err, "Verification code")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE otp_codes SET consumed_at = now()
		WHERE phone = $1 AND purpose = $2 AND consumed_at IS NULL AND expires_at > now()`,
		phone, purpose); err != nil {
		return apperr.FromPostgres(err, "Verification code")
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO otp_codes (user_id, phone, purpose, code_hash, expires_at, max_attempts, request_ip)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, '')::inet)`,
		userID, phone, purpose, codeHash, expiresAt, maxAttempts, ip); err != nil {
		return apperr.FromPostgres(err, "Verification code")
	}

	return apperr.FromPostgres(tx.Commit(ctx), "Verification code")
}

// otpRow is the scan target for ConsumeOTP.
type otpRow struct {
	ID          string `db:"id"`
	Attempts    int    `db:"attempts"`
	MaxAttempts int    `db:"max_attempts"`
}

// ConsumeOTP verifies a code and marks it used, atomically.
//
// The whole check runs inside one transaction with SELECT ... FOR UPDATE. Two
// requests racing with the same code cannot both succeed, and a wrong guess
// increments the attempt counter even though the transaction "failed" — which
// is exactly what stops a brute-force loop.
func (r *Repository) ConsumeOTP(ctx context.Context, phone, purpose, codeHash string) error {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return apperr.FromPostgres(err, "Verification code")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT id, attempts, max_attempts
		FROM otp_codes
		WHERE phone = $1 AND purpose = $2 AND consumed_at IS NULL AND expires_at > now()
		ORDER BY created_at DESC
		LIMIT 1
		FOR UPDATE`, phone, purpose)
	if err != nil {
		return apperr.FromPostgres(err, "Verification code")
	}

	live, err := pgx.CollectOneRow(rows, pgx.RowToStructByNameLax[otpRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.New(400, apperr.CodeOTPInvalid,
				"This verification code has expired or was already used.").
				WithHint("Request a new code and try again.")
		}
		return apperr.FromPostgres(err, "Verification code")
	}

	if live.Attempts >= live.MaxAttempts {
		// Burn the code so the attacker cannot keep guessing this one.
		_, _ = tx.Exec(ctx, `UPDATE otp_codes SET consumed_at = now() WHERE id = $1`, live.ID)
		_ = tx.Commit(ctx)
		return apperr.New(429, apperr.CodeOTPLimit,
			"Too many incorrect attempts for this code.").
			WithHint("Request a new verification code.")
	}

	var matched bool
	if err := tx.QueryRow(ctx, `
		UPDATE otp_codes
		SET attempts    = attempts + 1,
		    consumed_at = CASE WHEN code_hash = $2 THEN now() ELSE NULL END
		WHERE id = $1
		RETURNING (code_hash = $2)`, live.ID, codeHash).Scan(&matched); err != nil {
		return apperr.FromPostgres(err, "Verification code")
	}

	// Commit either way: the attempt counter must persist even for a wrong code.
	if err := tx.Commit(ctx); err != nil {
		return apperr.FromPostgres(err, "Verification code")
	}

	if !matched {
		left := live.MaxAttempts - live.Attempts - 1
		return apperr.New(400, apperr.CodeOTPInvalid,
			"That verification code is not correct.").
			WithDetails(map[string]any{"attempts_left": left}).
			WithHint("Check the SMS and enter the code again.")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

// CreateSession stores a refresh-token hash for one device.
func (r *Repository) CreateSession(ctx context.Context, tx repository.DB, userID, familyID, tokenHash string, deviceName, userAgent, ip *string, expiresAt time.Time) (domain.Session, error) {
	return repository.QueryOne[domain.Session](ctx, tx, "Session", `
		INSERT INTO sessions (user_id, family_id, token_hash, device_name, user_agent, ip, expires_at)
		VALUES ($1, COALESCE($2::uuid, gen_random_uuid()), $3, $4, $5, $6::inet, $7)
		RETURNING id, user_id, family_id, token_hash, device_name, user_agent,
		          host(ip) AS ip, expires_at, last_used_at, revoked_at, revoked_reason, created_at`,
		userID, nullIfEmpty(familyID), tokenHash, deviceName, userAgent, ip, expiresAt)
}

// FindSessionByToken looks a session up by its token hash, including revoked
// ones — reuse detection depends on being able to see a revoked row.
func (r *Repository) FindSessionByToken(ctx context.Context, tokenHash string) (domain.Session, error) {
	return repository.QueryOne[domain.Session](ctx, r.DB, "Session", `
		SELECT id, user_id, family_id, token_hash, device_name, user_agent,
		       host(ip) AS ip, expires_at, last_used_at, revoked_at, revoked_reason, created_at
		FROM sessions WHERE token_hash = $1`, tokenHash)
}

// ListSessions returns a user's devices, newest first.
func (r *Repository) ListSessions(ctx context.Context, userID string) ([]domain.Session, error) {
	return repository.Query[domain.Session](ctx, r.DB, "Session", `
		SELECT id, user_id, family_id, token_hash, device_name, user_agent,
		       host(ip) AS ip, expires_at, last_used_at, revoked_at, revoked_reason, created_at
		FROM sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()
		ORDER BY last_used_at DESC`, userID)
}

// RevokeSession ends one session.
func (r *Repository) RevokeSession(ctx context.Context, tx repository.DB, sessionID, reason string) error {
	tag, err := tx.Exec(ctx, `
		UPDATE sessions SET revoked_at = now(), revoked_reason = $2
		WHERE id = $1 AND revoked_at IS NULL`, sessionID, reason)
	if err != nil {
		return apperr.FromPostgres(err, "Session")
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("Session")
	}
	return nil
}

// RevokeSessionByToken ends the session holding a specific refresh token.
func (r *Repository) RevokeSessionByToken(ctx context.Context, tokenHash, reason string) (string, error) {
	var sessionID string
	err := r.DB.QueryRow(ctx, `
		UPDATE sessions SET revoked_at = now(), revoked_reason = $2
		WHERE token_hash = $1 AND revoked_at IS NULL
		RETURNING id`, tokenHash, reason).Scan(&sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil // already gone: logging out twice is not an error
	}
	if err != nil {
		return "", apperr.FromPostgres(err, "Session")
	}
	return sessionID, nil
}

// RevokeFamily ends every session in a token family. This is the response to
// detected refresh-token theft: the thief and the victim are both signed out,
// which is the only safe outcome when we cannot tell them apart.
func (r *Repository) RevokeFamily(ctx context.Context, tx repository.DB, familyID, reason string) (int64, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE sessions SET revoked_at = now(), revoked_reason = $2
		WHERE family_id = $1 AND revoked_at IS NULL`, familyID, reason)
	if err != nil {
		return 0, apperr.FromPostgres(err, "Session")
	}
	return tag.RowsAffected(), nil
}

// RevokeAllForUser signs a user out everywhere.
func (r *Repository) RevokeAllForUser(ctx context.Context, tx repository.DB, userID, reason string, exceptSessionID string) (int64, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE sessions SET revoked_at = now(), revoked_reason = $2
		WHERE user_id = $1 AND revoked_at IS NULL AND ($3 = '' OR id <> $3::uuid)`,
		userID, reason, exceptSessionID)
	if err != nil {
		return 0, apperr.FromPostgres(err, "Session")
	}
	return tag.RowsAffected(), nil
}

// TouchSession records that a refresh token was just used.
func (r *Repository) TouchSession(ctx context.Context, tx repository.DB, sessionID string) error {
	_, err := tx.Exec(ctx, `UPDATE sessions SET last_used_at = now() WHERE id = $1`, sessionID)
	return apperr.FromPostgres(err, "Session")
}

// TrimSessions enforces the per-plan device limit by revoking the oldest
// sessions beyond it, so signing in on a sixth phone signs out the first.
func (r *Repository) TrimSessions(ctx context.Context, tx repository.DB, userID string, keep int) error {
	if keep <= 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE sessions SET revoked_at = now(), revoked_reason = 'device_limit'
		WHERE id IN (
		    SELECT id FROM sessions
		    WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()
		    ORDER BY last_used_at DESC
		    OFFSET $2
		)`, userID, keep)
	return apperr.FromPostgres(err, "Session")
}

// CountActiveSessions counts a user's live devices.
func (r *Repository) CountActiveSessions(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.DB.QueryRow(ctx, `
		SELECT count(*) FROM sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()`, userID).Scan(&n)
	if err != nil {
		return 0, apperr.FromPostgres(err, "Session")
	}
	return n, nil
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// RegisterConstraints teaches the error translator how to phrase this module's
// unique-constraint violations. Called once at boot.
func RegisterConstraints() {
	apperr.RegisterConstraints(map[string]apperr.ConstraintMessage{
		"users_phone_key": {
			Field:     "phone",
			Message:   "This phone number is already registered.",
			MessageBN: "এই মোবাইল নম্বরটি ইতিমধ্যে ব্যবহৃত হয়েছে।",
		},
		"users_email_key": {
			Field:     "email",
			Message:   "This email address is already registered.",
			MessageBN: "এই email ঠিকানাটি ইতিমধ্যে ব্যবহৃত হয়েছে।",
		},
		"users_username_key": {
			Field:     "username",
			Message:   "This username is already taken.",
			MessageBN: "এই username টি ইতিমধ্যে নেওয়া হয়েছে।",
		},
		"users_username_format": {
			Field:     "username",
			Message:   "Username may use 3-30 lowercase letters, numbers, dots and underscores.",
			MessageBN: "Username-এ ছোট হাতের অক্ষর, সংখ্যা, dot ও underscore ব্যবহার করা যাবে।",
		},
		"sessions_token_hash_key": {
			Field:   "refresh_token",
			Message: "This session already exists.",
		},
	})
}
