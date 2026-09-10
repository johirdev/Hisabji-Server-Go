package billing

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

// Repository is the billing module's data access.
type Repository struct {
	DB *pgxpool.Pool
}

// NewRepository builds the repository.
func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{DB: db} }

// ---------------------------------------------------------------------------
// Catalogue
// ---------------------------------------------------------------------------

// ListPlans returns the purchasable plans, cheapest first.
func (r *Repository) ListPlans(ctx context.Context, includeInactive bool) ([]domain.Plan, error) {
	return repository.Query[domain.Plan](ctx, r.DB, "Plan", `
		SELECT * FROM subscription_plans
		WHERE ($1 OR is_active)
		ORDER BY sort_order, price`, includeInactive)
}

// GetPlan loads one plan by code.
func (r *Repository) GetPlan(ctx context.Context, db repository.DB, code string) (domain.Plan, error) {
	if db == nil {
		db = r.DB
	}
	return repository.QueryOne[domain.Plan](ctx, db, "Plan",
		`SELECT * FROM subscription_plans WHERE code = $1`, code)
}

// ListPacks returns the purchasable credit packs.
func (r *Repository) ListPacks(ctx context.Context, includeInactive bool) ([]domain.CreditPack, error) {
	return repository.Query[domain.CreditPack](ctx, r.DB, "Credit pack", `
		SELECT * FROM credit_packs
		WHERE ($1 OR is_active)
		ORDER BY sort_order, price`, includeInactive)
}

// GetPack loads one pack by code.
func (r *Repository) GetPack(ctx context.Context, db repository.DB, code string) (domain.CreditPack, error) {
	if db == nil {
		db = r.DB
	}
	return repository.QueryOne[domain.CreditPack](ctx, db, "Credit pack",
		`SELECT * FROM credit_packs WHERE code = $1`, code)
}

// ListFeatures returns the feature catalogue.
func (r *Repository) ListFeatures(ctx context.Context, includeInactive bool) ([]domain.Feature, error) {
	return repository.Query[domain.Feature](ctx, r.DB, "Feature", `
		SELECT * FROM features
		WHERE ($1 OR is_active)
		ORDER BY sort_order, code`, includeInactive)
}

// GetFeature loads one feature by code.
func (r *Repository) GetFeature(ctx context.Context, db repository.DB, code string) (domain.Feature, error) {
	if db == nil {
		db = r.DB
	}
	return repository.QueryOne[domain.Feature](ctx, db, "Feature",
		`SELECT * FROM features WHERE code = $1`, code)
}

// ---------------------------------------------------------------------------
// Subscriptions
// ---------------------------------------------------------------------------

// LiveSubscription returns the user's currently effective subscription, or a
// NOT_FOUND error when they are on the free plan.
//
// Free is the ABSENCE of a subscription row, not a row with a fake 100-year
// expiry. That means "is this user paying?" has exactly one answer and there is
// no synthetic row to keep in step.
func (r *Repository) LiveSubscription(ctx context.Context, db repository.DB, userID string) (domain.Subscription, error) {
	if db == nil {
		db = r.DB
	}
	return repository.QueryOne[domain.Subscription](ctx, db, "Subscription", `
		SELECT s.*, p.name AS plan_name, p.tier AS plan_tier
		FROM subscriptions s
		JOIN subscription_plans p ON p.code = s.plan_code
		WHERE s.user_id = $1
		  AND s.status IN ('active','trialing','grace')
		  AND (s.ends_at > now() OR (s.status = 'grace' AND s.grace_until > now()))
		ORDER BY s.ends_at DESC
		LIMIT 1`, userID)
}

// ListSubscriptions returns the user's billing history.
func (r *Repository) ListSubscriptions(ctx context.Context, userID string, limit int) ([]domain.Subscription, error) {
	return repository.Query[domain.Subscription](ctx, r.DB, "Subscription", `
		SELECT s.*, p.name AS plan_name, p.tier AS plan_tier
		FROM subscriptions s
		JOIN subscription_plans p ON p.code = s.plan_code
		WHERE s.user_id = $1
		ORDER BY s.created_at DESC
		LIMIT $2`, userID, limit)
}

// CreateSubscription inserts an active subscription.
func (r *Repository) CreateSubscription(ctx context.Context, tx repository.DB, userID, planCode string, plan domain.Plan, paymentID *string, startsAt, endsAt time.Time, nextGrantAt *time.Time, status string) (domain.Subscription, error) {
	return repository.QueryOne[domain.Subscription](ctx, tx, "Subscription", `
		INSERT INTO subscriptions
		    (user_id, plan_code, status, starts_at, ends_at, next_grant_at,
		     auto_renew, payment_id, price_paid, currency)
		VALUES ($1, $2, $3, $4, $5, $6, false, $7, $8, $9)
		RETURNING *`,
		userID, planCode, status, startsAt, endsAt, nextGrantAt,
		paymentID, plan.Price.Minor(), plan.Currency)
}

// ExpireSubscription closes out a live subscription, used when upgrading.
func (r *Repository) ExpireSubscription(ctx context.Context, tx repository.DB, id, status string) error {
	_, err := tx.Exec(ctx, `
		UPDATE subscriptions SET status = $2, updated_at = now()
		WHERE id = $1`, id, status)
	return apperr.FromPostgres(err, "Subscription")
}

// CancelSubscription marks auto-renew off and records the reason. The user
// keeps access until ends_at — they paid for that time.
func (r *Repository) CancelSubscription(ctx context.Context, tx repository.DB, id, reason string) (domain.Subscription, error) {
	return repository.QueryOne[domain.Subscription](ctx, tx, "Subscription", `
		UPDATE subscriptions
		SET auto_renew = false, cancelled_at = now(),
		    cancel_reason = NULLIF($2, ''), updated_at = now()
		WHERE id = $1
		RETURNING *`, id, reason)
}

// SyncUserPlan refreshes the denormalised plan snapshot on the users row, which
// is what the JWT claim is built from.
func (r *Repository) SyncUserPlan(ctx context.Context, tx repository.DB, userID, planCode string, expiresAt *time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE users SET plan_code = $2, plan_expires_at = $3, updated_at = now()
		WHERE id = $1`, userID, planCode, expiresAt)
	return apperr.FromPostgres(err, "User")
}

// DueForExpiry returns subscriptions whose paid period has ended, for the
// background job that downgrades them.
func (r *Repository) DueForExpiry(ctx context.Context, limit int) ([]domain.Subscription, error) {
	return repository.Query[domain.Subscription](ctx, r.DB, "Subscription", `
		SELECT * FROM subscriptions
		WHERE status IN ('active','trialing','grace')
		  AND ends_at <= now()
		  AND (grace_until IS NULL OR grace_until <= now())
		ORDER BY ends_at
		LIMIT $1`, limit)
}

// DueForGrant returns active subscriptions whose next monthly credit refill is
// due.
func (r *Repository) DueForGrant(ctx context.Context, limit int) ([]domain.Subscription, error) {
	return repository.Query[domain.Subscription](ctx, r.DB, "Subscription", `
		SELECT * FROM subscriptions
		WHERE status = 'active'
		  AND next_grant_at IS NOT NULL
		  AND next_grant_at <= now()
		  AND ends_at > now()
		ORDER BY next_grant_at
		LIMIT $1`, limit)
}

// MarkGranted advances the refill schedule after a successful grant.
func (r *Repository) MarkGranted(ctx context.Context, tx repository.DB, id string, next *time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE subscriptions
		SET grants_made = grants_made + 1, next_grant_at = $2, updated_at = now()
		WHERE id = $1`, id, next)
	return apperr.FromPostgres(err, "Subscription")
}

// ---------------------------------------------------------------------------
// Wallet and ledger
// ---------------------------------------------------------------------------

// EnsureWallet creates the wallet row if it does not exist and returns it.
func (r *Repository) EnsureWallet(ctx context.Context, tx repository.DB, userID string, monthlyCap int) (domain.Wallet, error) {
	return repository.QueryOne[domain.Wallet](ctx, tx, "Wallet", `
		INSERT INTO credit_wallets (user_id, monthly_spend_cap)
		VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET user_id = EXCLUDED.user_id
		RETURNING *`, userID, monthlyCap)
}

// GetWallet reads the balance without locking.
func (r *Repository) GetWallet(ctx context.Context, db repository.DB, userID string) (domain.Wallet, error) {
	if db == nil {
		db = r.DB
	}
	return repository.QueryOne[domain.Wallet](ctx, db, "Wallet",
		`SELECT * FROM credit_wallets WHERE user_id = $1`, userID)
}

// LockWallet reads the balance FOR UPDATE, which is mandatory before any debit.
//
// Without the row lock, two concurrent AI requests both read "5 credits",
// both decide 5 >= 3, and both debit — leaving -1 credits (or a CHECK
// violation). The lock serialises them so the second one correctly fails.
func (r *Repository) LockWallet(ctx context.Context, tx repository.DB, userID string) (domain.Wallet, error) {
	return repository.QueryOne[domain.Wallet](ctx, tx, "Wallet",
		`SELECT * FROM credit_wallets WHERE user_id = $1 FOR UPDATE`, userID)
}

// ApplyWallet writes new balances and appends the matching ledger row.
//
// The two writes are one operation on purpose: a balance change with no ledger
// entry is money that vanished with no explanation, which is exactly the bug a
// user will notice and never forgive.
func (r *Repository) ApplyWallet(ctx context.Context, tx repository.DB, e LedgerWrite) (domain.Wallet, error) {
	wallet, err := repository.QueryOne[domain.Wallet](ctx, tx, "Wallet", `
		UPDATE credit_wallets
		SET allowance_credits  = $2,
		    purchased_credits  = $3,
		    allowance_reset_at = COALESCE($4, allowance_reset_at),
		    lifetime_granted   = lifetime_granted   + $5,
		    lifetime_purchased = lifetime_purchased + $6,
		    lifetime_used      = lifetime_used      + $7,
		    month_spent        = CASE
		        WHEN month_started_at < date_trunc('month', now()) THEN $7
		        ELSE month_spent + $7
		    END,
		    month_started_at   = CASE
		        WHEN month_started_at < date_trunc('month', now()) THEN date_trunc('month', now())
		        ELSE month_started_at
		    END,
		    updated_at = now()
		WHERE user_id = $1
		RETURNING *`,
		e.UserID, e.AllowanceAfter, e.PurchasedAfter, e.AllowanceResetAt,
		e.GrantedDelta, e.PurchasedDelta, e.UsedDelta)
	if err != nil {
		return domain.Wallet{}, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO credit_ledger
		    (user_id, delta, allowance_after, purchased_after, reason,
		     feature_code, reference_type, reference_id, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		e.UserID, e.Delta, e.AllowanceAfter, e.PurchasedAfter, e.Reason,
		e.FeatureCode, e.ReferenceType, e.ReferenceID, e.Description,
	); err != nil {
		return domain.Wallet{}, apperr.FromPostgres(err, "Credit ledger")
	}

	return wallet, nil
}

// LedgerWrite describes one balance change plus its audit row.
type LedgerWrite struct {
	UserID string

	// Final balances after the change. Computed by the service so all the
	// spend-order rules live in one readable place.
	AllowanceAfter int
	PurchasedAfter int

	// Signed total change, for the ledger row.
	Delta int

	// Lifetime counter increments (all non-negative).
	GrantedDelta   int
	PurchasedDelta int
	UsedDelta      int

	AllowanceResetAt *time.Time

	Reason        string
	FeatureCode   *string
	ReferenceType *string
	ReferenceID   *string
	Description   *string
}

// ListLedger returns the user's credit history, newest first.
func (r *Repository) ListLedger(ctx context.Context, userID string, limit, offset int) ([]domain.LedgerEntry, int64, error) {
	entries, err := repository.Query[domain.LedgerEntry](ctx, r.DB, "Credit history", `
		SELECT * FROM credit_ledger
		WHERE user_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}

	var total int64
	if err := r.DB.QueryRow(ctx,
		`SELECT count(*) FROM credit_ledger WHERE user_id = $1`, userID).Scan(&total); err != nil {
		return nil, 0, apperr.FromPostgres(err, "Credit history")
	}
	return entries, total, nil
}

// ---------------------------------------------------------------------------
// Payments
// ---------------------------------------------------------------------------

// CreatePayment records a pending charge.
func (r *Repository) CreatePayment(ctx context.Context, tx repository.DB, userID, kind, referenceCode string, amount int64, currency, provider string, idempotencyKey *string) (domain.Payment, error) {
	return repository.QueryOne[domain.Payment](ctx, tx, "Payment", `
		INSERT INTO payments (user_id, kind, reference_code, amount, currency, provider, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING *`,
		userID, kind, referenceCode, amount, currency, provider, idempotencyKey)
}

// GetPayment loads one payment owned by the user.
func (r *Repository) GetPayment(ctx context.Context, db repository.DB, userID, id string) (domain.Payment, error) {
	if db == nil {
		db = r.DB
	}
	return repository.QueryOne[domain.Payment](ctx, db, "Payment",
		`SELECT * FROM payments WHERE id = $1 AND user_id = $2`, id, userID)
}

// LockPayment reads a payment FOR UPDATE, so a webhook and a client callback
// racing to confirm the same payment cannot both credit the account.
func (r *Repository) LockPayment(ctx context.Context, tx repository.DB, id string) (domain.Payment, error) {
	return repository.QueryOne[domain.Payment](ctx, tx, "Payment",
		`SELECT * FROM payments WHERE id = $1 FOR UPDATE`, id)
}

// FindPaymentByIdempotencyKey supports safe client retries.
func (r *Repository) FindPaymentByIdempotencyKey(ctx context.Context, userID, key string) (domain.Payment, error) {
	return repository.QueryOne[domain.Payment](ctx, r.DB, "Payment",
		`SELECT * FROM payments WHERE user_id = $1 AND idempotency_key = $2`, userID, key)
}

// MarkPaymentPaid settles a payment.
func (r *Repository) MarkPaymentPaid(ctx context.Context, tx repository.DB, id, providerRef string) error {
	_, err := tx.Exec(ctx, `
		UPDATE payments
		SET status = 'paid', paid_at = now(),
		    provider_ref = COALESCE(NULLIF($2, ''), provider_ref), updated_at = now()
		WHERE id = $1`, id, providerRef)
	return apperr.FromPostgres(err, "Payment")
}

// MarkPaymentFailed records a decline.
func (r *Repository) MarkPaymentFailed(ctx context.Context, tx repository.DB, id, reason string) error {
	_, err := tx.Exec(ctx, `
		UPDATE payments
		SET status = 'failed', failure_reason = NULLIF($2, ''), updated_at = now()
		WHERE id = $1`, id, reason)
	return apperr.FromPostgres(err, "Payment")
}

// ListPayments returns the user's payment history.
func (r *Repository) ListPayments(ctx context.Context, userID string, limit, offset int) ([]domain.Payment, int64, error) {
	items, err := repository.Query[domain.Payment](ctx, r.DB, "Payment", `
		SELECT * FROM payments
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if err := r.DB.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE user_id = $1`, userID).Scan(&total); err != nil {
		return nil, 0, apperr.FromPostgres(err, "Payment")
	}
	return items, total, nil
}

// IsNotFound reports whether an error is a plain "no such row".
func IsNotFound(err error) bool {
	return apperr.IsCode(err, apperr.CodeNotFound) || errors.Is(err, pgx.ErrNoRows)
}
