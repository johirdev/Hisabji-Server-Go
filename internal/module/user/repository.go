package user

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/repository"
	"github.com/johirdev/Hisabji-Server/internal/domain"
)

// userColumns is the full projection for a user row.
const userColumns = `
	id, name, username, email, phone, password_hash, avatar_path,
	user_type, role, status, locale, currency, timezone,
	monthly_income, month_start_day,
	phone_verified, phone_verified_at, email_verified, email_verified_at,
	onboarding_step, onboarded_at, plan_code, plan_expires_at,
	failed_login_count, locked_until, password_changed_at,
	last_login_at, last_seen_at, deleted_at, metadata, created_at, updated_at`

// Repository is the user module's data access.
type Repository struct {
	DB *pgxpool.Pool
}

// NewRepository builds the repository.
func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{DB: db} }

// Get loads one account.
func (r *Repository) Get(ctx context.Context, id string) (domain.User, error) {
	return repository.QueryOne[domain.User](ctx, r.DB, "User",
		`SELECT `+userColumns+` FROM users WHERE id = $1 AND deleted_at IS NULL`, id)
}

// Update applies a partial profile change.
//
// COALESCE is the SQL idiom that makes a PATCH safe:
// a parameter that is NULL (field absent) leaves the column untouched, so one
// statement handles every combination of present and absent fields without
// building the SET clause by hand.
func (r *Repository) Update(ctx context.Context, id string, in UpdateProfileRequest) (domain.User, error) {
	var monthlyIncome *int64
	if in.MonthlyIncome != nil {
		v := in.MonthlyIncome.Minor()
		monthlyIncome = &v
	}

	return repository.QueryOne[domain.User](ctx, r.DB, "User", `
		UPDATE users SET
		    name            = COALESCE($2, name),
		    email           = COALESCE($3::citext, email),
		    user_type       = COALESCE($4, user_type),
		    avatar_path     = COALESCE($5, avatar_path),
		    monthly_income  = COALESCE($6, monthly_income),
		    month_start_day = COALESCE($7, month_start_day),
		    updated_at      = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING `+userColumns,
		id, in.Name, in.Email, in.UserType, in.AvatarPath, monthlyIncome, in.MonthStartDay)
}

// UpdatePreferences applies a partial preferences change.
func (r *Repository) UpdatePreferences(ctx context.Context, id string, in UpdatePreferencesRequest) (domain.User, error) {
	return repository.QueryOne[domain.User](ctx, r.DB, "User", `
		UPDATE users SET
		    locale          = COALESCE($2, locale),
		    currency        = COALESCE($3, currency),
		    timezone        = COALESCE($4, timezone),
		    month_start_day = COALESCE($5, month_start_day),
		    updated_at      = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING `+userColumns,
		id, in.Locale, in.Currency, in.Timezone, in.MonthStartDay)
}

// SetUsername claims a username.
func (r *Repository) SetUsername(ctx context.Context, id, username string) (domain.User, error) {
	return repository.QueryOne[domain.User](ctx, r.DB, "User", `
		UPDATE users SET username = $2::citext, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING `+userColumns, id, username)
}

// UsernameTaken reports whether a username is already in use by somebody else.
func (r *Repository) UsernameTaken(ctx context.Context, username, exceptUserID string) (bool, error) {
	var taken bool
	err := r.DB.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM users
		    WHERE username = $1::citext AND deleted_at IS NULL AND id <> COALESCE($2::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
		)`, username, nullIfEmpty(exceptUserID)).Scan(&taken)
	if err != nil {
		return false, apperr.FromPostgres(err, "User")
	}
	return taken, nil
}

// AdvanceOnboarding saves wizard progress and any profile data collected.
func (r *Repository) AdvanceOnboarding(ctx context.Context, id string, in OnboardingRequest) (domain.User, error) {
	var monthlyIncome *int64
	if in.MonthlyIncome != nil {
		v := in.MonthlyIncome.Minor()
		monthlyIncome = &v
	}

	// $2 is cast explicitly because it appears in two positions with different
	// inferred types: an assignment to a varchar column and a comparison
	// against a literal. Without the cast Postgres refuses the statement with
	// "inconsistent types deduced for parameter $2".
	return repository.QueryOne[domain.User](ctx, r.DB, "User", `
		UPDATE users SET
		    onboarding_step = $2::text,
		    onboarded_at    = CASE WHEN $2::text = 'done' THEN COALESCE(onboarded_at, now()) ELSE onboarded_at END,
		    user_type       = COALESCE($3, user_type),
		    monthly_income  = COALESCE($4, monthly_income),
		    month_start_day = COALESCE($5, month_start_day),
		    currency        = COALESCE($6, currency),
		    locale          = COALESCE($7, locale),
		    updated_at      = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING `+userColumns,
		id, in.Step, in.UserType, monthlyIncome, in.MonthStartDay, in.Currency, in.Locale)
}

// SoftDelete marks the account deleted without destroying the rows.
//
// Soft delete, not DELETE, for three reasons: an accidental deletion can be
// undone during the grace period, financial records may need to be retained for
// dispute resolution, and a hard delete of a busy user would cascade across
// every table at once. The cleanup job purges them properly later.
func (r *Repository) SoftDelete(ctx context.Context, tx repository.DB, id, reason string) error {
	tag, err := tx.Exec(ctx, `
		UPDATE users SET
		    status     = 'deleted',
		    deleted_at = now(),
		    -- Free the unique identifiers immediately so the person can sign up
		    -- again with the same phone or email without waiting for the purge.
		    phone      = phone || '.deleted.' || extract(epoch from now())::bigint,
		    email      = NULL,
		    username   = NULL,
		    metadata   = metadata || jsonb_build_object(
		        'deleted_reason', $2::text,
		        'original_phone', phone,
		        'deleted_at', now()
		    ),
		    updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL`, id, reason)
	if err != nil {
		return apperr.FromPostgres(err, "Account")
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("Account")
	}
	return nil
}

// Stats returns the lifetime counters shown on the profile screen.
//
// One query with scalar sub-selects rather than six round trips: the profile
// screen is the first thing loaded after login, and its latency is the app's
// perceived speed.
func (r *Repository) Stats(ctx context.Context, userID string) (ProfileStats, error) {
	return repository.QueryOne[ProfileStats](ctx, r.DB, "Profile", `
		SELECT
		    (SELECT count(*)  FROM expenses   WHERE user_id = $1 AND deleted_at IS NULL) AS expense_count,
		    (SELECT count(*)  FROM incomes    WHERE user_id = $1 AND deleted_at IS NULL) AS income_count,
		    (SELECT count(*)  FROM categories WHERE user_id = $1 AND deleted_at IS NULL) AS category_count,
		    (SELECT count(*)  FROM goals      WHERE user_id = $1 AND deleted_at IS NULL) AS goal_count,
		    (SELECT COALESCE(sum(amount), 0)::bigint FROM expenses WHERE user_id = $1 AND deleted_at IS NULL) AS total_spent,
		    (SELECT COALESCE(sum(amount), 0)::bigint FROM incomes  WHERE user_id = $1 AND deleted_at IS NULL) AS total_income,
		    (SELECT to_char(min(spent_at), 'YYYY-MM-DD') FROM expenses WHERE user_id = $1 AND deleted_at IS NULL) AS first_entry_on,
		    (SELECT count(DISTINCT spent_at) FROM expenses WHERE user_id = $1 AND deleted_at IS NULL) AS active_days`,
		userID)
}

// TouchLastSeen records activity, best effort.
func (r *Repository) TouchLastSeen(ctx context.Context, id string) {
	_, _ = r.DB.Exec(ctx, `UPDATE users SET last_seen_at = now() WHERE id = $1`, id)
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
