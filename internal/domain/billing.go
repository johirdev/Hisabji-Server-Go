package domain

import (
	"time"

	"github.com/johirdev/Hisabji-Server/internal/core/money"
)

// Plan tiers, ordered from least to most capable.
const (
	TierFree     = "free"
	TierPlus     = "plus"
	TierPro      = "pro"
	TierBusiness = "business"
)

// Subscription statuses.
const (
	SubPending   = "pending"  // checkout started, payment not settled
	SubTrialing  = "trialing" // inside a free trial
	SubActive    = "active"
	SubGrace     = "grace" // past ends_at, renewal payment still settling
	SubExpired   = "expired"
	SubCancelled = "cancelled"
	SubRefunded  = "refunded"
)

// Feature kinds.
const (
	FeatureModule   = "module"    // access gate, free to use once unlocked
	FeatureAIAction = "ai_action" // costs credits per invocation
)

// Credit ledger reasons.
const (
	CreditSignupBonus     = "signup_bonus"
	CreditPlanGrant       = "plan_grant"
	CreditPlanSignupBonus = "plan_signup_bonus"
	CreditPackPurchase    = "pack_purchase"
	CreditFeatureUse      = "feature_use"
	CreditRefund          = "refund"
	CreditExpiry          = "expiry"
	CreditAdminAdjust     = "admin_adjust"
	CreditPromo           = "promo"
)

// Payment statuses.
const (
	PaymentPending    = "pending"
	PaymentProcessing = "processing"
	PaymentPaid       = "paid"
	PaymentFailed     = "failed"
	PaymentCancelled  = "cancelled"
	PaymentRefunded   = "refunded"
)

// Feature is one gated or metered capability.
type Feature struct {
	Code          string  `db:"code"           json:"code"`
	Name          string  `db:"name"           json:"name"`
	NameBN        *string `db:"name_bn"        json:"name_bn,omitempty"`
	Description   *string `db:"description"    json:"description"`
	DescriptionBN *string `db:"description_bn" json:"description_bn,omitempty"`

	Kind        string `db:"kind"         json:"kind"`
	CreditCost  int    `db:"credit_cost"  json:"credit_cost"`
	MinTier     string `db:"min_tier"     json:"min_tier"`
	PAYGAllowed bool   `db:"payg_allowed" json:"payg_allowed"`

	Category  string  `db:"category"   json:"category"`
	Icon      *string `db:"icon"       json:"icon"`
	SortOrder int     `db:"sort_order" json:"sort_order"`
	IsActive  bool    `db:"is_active"  json:"is_active"`

	// Filled in per request by the billing service so the client can render an
	// accurate "Unlock" or "Run (5 credits)" button without extra calls.
	IncludedInPlan bool `db:"-" json:"included_in_plan"`
	Affordable     bool `db:"-" json:"affordable"`

	CreatedAt time.Time `db:"created_at" json:"-"`
	UpdatedAt time.Time `db:"updated_at" json:"-"`
}

// Plan is one purchasable subscription.
type Plan struct {
	Code      string  `db:"code"       json:"code"`
	Name      string  `db:"name"       json:"name"`
	NameBN    *string `db:"name_bn"    json:"name_bn,omitempty"`
	Tagline   *string `db:"tagline"    json:"tagline"`
	TaglineBN *string `db:"tagline_bn" json:"tagline_bn,omitempty"`

	Tier         string `db:"tier"          json:"tier"`
	PeriodMonths int    `db:"period_months" json:"period_months"`

	Price     money.Amount `db:"price"      json:"price"`
	ListPrice money.Amount `db:"list_price" json:"list_price"`
	Currency  string       `db:"currency"   json:"currency"`

	MonthlyCredits     int  `db:"monthly_credits"      json:"monthly_credits"`
	SignupCredits      int  `db:"signup_credits"       json:"signup_credits"`
	AllowanceRollsOver bool `db:"allowance_rolls_over" json:"allowance_rolls_over"`

	FeatureCodes []string `db:"feature_codes" json:"feature_codes"`
	MaxDevices   int      `db:"max_devices"   json:"max_devices"`
	TrialDays    int      `db:"trial_days"    json:"trial_days"`

	IsActive  bool `db:"is_active"  json:"is_active"`
	IsPopular bool `db:"is_popular" json:"is_popular"`
	SortOrder int  `db:"sort_order" json:"sort_order"`

	// Computed for the pricing screen.
	MonthlyPrice   money.Amount `db:"-" json:"monthly_price"`
	SavingsPercent int          `db:"-" json:"savings_percent"`
	IsCurrentPlan  bool         `db:"-" json:"is_current_plan"`
	TotalCredits   int          `db:"-" json:"total_credits"`

	CreatedAt time.Time `db:"created_at" json:"-"`
	UpdatedAt time.Time `db:"updated_at" json:"-"`
}

// Includes reports whether this plan unlocks a feature.
func (p Plan) Includes(featureCode string) bool {
	for _, c := range p.FeatureCodes {
		if c == featureCode {
			return true
		}
	}
	return false
}

// Compute fills the derived pricing fields shown on the plan cards.
func (p *Plan) Compute() {
	if p.PeriodMonths > 0 {
		p.MonthlyPrice = p.Price.DivInt(int64(p.PeriodMonths))
		p.TotalCredits = p.MonthlyCredits*p.PeriodMonths + p.SignupCredits
	} else {
		p.MonthlyPrice = p.Price
		p.TotalCredits = p.SignupCredits
	}
	if p.ListPrice > p.Price && p.ListPrice > 0 {
		saved := p.ListPrice.Sub(p.Price)
		p.SavingsPercent = int(saved.RatioPercent(p.ListPrice) + 0.5)
	}
}

// CreditPack is one purchasable bundle of tokens.
type CreditPack struct {
	Code   string  `db:"code"    json:"code"`
	Name   string  `db:"name"    json:"name"`
	NameBN *string `db:"name_bn" json:"name_bn,omitempty"`

	Credits      int          `db:"credits"       json:"credits"`
	BonusCredits int          `db:"bonus_credits" json:"bonus_credits"`
	Price        money.Amount `db:"price"         json:"price"`
	ListPrice    money.Amount `db:"list_price"    json:"list_price"`
	Currency     string       `db:"currency"      json:"currency"`
	ValidityDays int          `db:"validity_days" json:"validity_days"`

	IsActive  bool `db:"is_active"  json:"is_active"`
	IsPopular bool `db:"is_popular" json:"is_popular"`
	SortOrder int  `db:"sort_order" json:"sort_order"`

	// Computed for the store screen.
	TotalCredits   int          `db:"-" json:"total_credits"`
	PricePerCredit money.Amount `db:"-" json:"price_per_credit"`
	SavingsPercent int          `db:"-" json:"savings_percent"`

	CreatedAt time.Time `db:"created_at" json:"-"`
	UpdatedAt time.Time `db:"updated_at" json:"-"`
}

// Compute fills the derived fields shown on the pack cards.
func (p *CreditPack) Compute() {
	p.TotalCredits = p.Credits + p.BonusCredits
	if p.TotalCredits > 0 {
		p.PricePerCredit = p.Price.DivInt(int64(p.TotalCredits))
	}
	if p.ListPrice > p.Price && p.ListPrice > 0 {
		p.SavingsPercent = int(p.ListPrice.Sub(p.Price).RatioPercent(p.ListPrice) + 0.5)
	}
}

// Subscription is one purchase of a plan.
type Subscription struct {
	ID       string `db:"id"        json:"id"`
	UserID   string `db:"user_id"   json:"-"`
	PlanCode string `db:"plan_code" json:"plan_code"`

	Status      string     `db:"status"        json:"status"`
	StartsAt    time.Time  `db:"starts_at"     json:"starts_at"`
	EndsAt      time.Time  `db:"ends_at"       json:"ends_at"`
	GraceUntil  *time.Time `db:"grace_until"   json:"grace_until,omitempty"`
	NextGrantAt *time.Time `db:"next_grant_at" json:"next_grant_at,omitempty"`
	GrantsMade  int        `db:"grants_made"   json:"grants_made"`

	AutoRenew    bool       `db:"auto_renew"    json:"auto_renew"`
	CancelledAt  *time.Time `db:"cancelled_at"  json:"cancelled_at,omitempty"`
	CancelReason *string    `db:"cancel_reason" json:"cancel_reason,omitempty"`

	PreviousID *string `db:"previous_id" json:"-"`
	PaymentID  *string `db:"payment_id"  json:"payment_id,omitempty"`

	PricePaid money.Amount `db:"price_paid" json:"price_paid"`
	Currency  string       `db:"currency"   json:"currency"`

	// Joined from subscription_plans for display.
	PlanName *string `db:"plan_name" json:"plan_name,omitempty"`
	PlanTier *string `db:"plan_tier" json:"plan_tier,omitempty"`

	// Computed.
	DaysRemaining int `db:"-" json:"days_remaining"`

	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// IsLive reports whether the subscription currently grants access.
func (s Subscription) IsLive(now time.Time) bool {
	switch s.Status {
	case SubActive, SubTrialing:
		return now.Before(s.EndsAt)
	case SubGrace:
		return s.GraceUntil != nil && now.Before(*s.GraceUntil)
	default:
		return false
	}
}

// Wallet is a user's credit balance.
type Wallet struct {
	UserID string `db:"user_id" json:"-"`

	AllowanceCredits int        `db:"allowance_credits"  json:"allowance_credits"`
	PurchasedCredits int        `db:"purchased_credits"  json:"purchased_credits"`
	AllowanceResetAt *time.Time `db:"allowance_reset_at" json:"allowance_reset_at,omitempty"`

	LifetimeGranted   int `db:"lifetime_granted"   json:"lifetime_granted"`
	LifetimePurchased int `db:"lifetime_purchased" json:"lifetime_purchased"`
	LifetimeUsed      int `db:"lifetime_used"      json:"lifetime_used"`

	MonthlySpendCap int       `db:"monthly_spend_cap" json:"monthly_spend_cap"`
	MonthSpent      int       `db:"month_spent"       json:"month_spent"`
	MonthStartedAt  time.Time `db:"month_started_at"  json:"-"`

	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`

	// TotalCredits is derived, not stored. It is serialised so the client never
	// has to add two numbers to answer "how many credits do I have?" — and so
	// that every endpoint returning a wallet returns the SAME shape.
	TotalCredits int `db:"-" json:"total_credits"`
}

// Total is the spendable balance.
func (w Wallet) Total() int { return w.AllowanceCredits + w.PurchasedCredits }

// Compute fills the derived fields. Call it on every wallet before returning it
// from a handler.
func (w *Wallet) Compute() { w.TotalCredits = w.AllowanceCredits + w.PurchasedCredits }

// CanAfford reports whether the wallet covers a cost without breaching the
// monthly safety cap.
func (w Wallet) CanAfford(cost int) bool {
	return w.Total() >= cost && w.MonthSpent+cost <= w.MonthlySpendCap
}

// LedgerEntry is one immutable credit movement.
type LedgerEntry struct {
	ID     int64  `db:"id"      json:"id"`
	UserID string `db:"user_id" json:"-"`

	Delta          int    `db:"delta"           json:"delta"`
	AllowanceAfter int    `db:"allowance_after" json:"allowance_after"`
	PurchasedAfter int    `db:"purchased_after" json:"purchased_after"`
	Reason         string `db:"reason"          json:"reason"`

	FeatureCode   *string `db:"feature_code"   json:"feature_code,omitempty"`
	ReferenceType *string `db:"reference_type" json:"reference_type,omitempty"`
	ReferenceID   *string `db:"reference_id"   json:"reference_id,omitempty"`
	Description   *string `db:"description"    json:"description"`

	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// Payment is one money movement with a gateway.
type Payment struct {
	ID     string `db:"id"      json:"id"`
	UserID string `db:"user_id" json:"-"`

	Kind          string `db:"kind"           json:"kind"`
	ReferenceCode string `db:"reference_code" json:"reference_code"`
	Quantity      int    `db:"quantity"       json:"quantity"`

	Amount   money.Amount `db:"amount"   json:"amount"`
	Currency string       `db:"currency" json:"currency"`

	Provider    string  `db:"provider"     json:"provider"`
	ProviderRef *string `db:"provider_ref" json:"provider_ref,omitempty"`

	// The raw gateway response, kept for reconciliation and dispute handling.
	// It can contain provider-side account identifiers, so it never leaves the
	// server — but it MUST be declared here, because pgx's struct scanner fails
	// on a column the destination struct has no field for.
	ProviderPayload map[string]any `db:"provider_payload" json:"-"`
	IdempotencyKey  *string        `db:"idempotency_key"  json:"-"`

	Status        string  `db:"status"         json:"status"`
	FailureReason *string `db:"failure_reason" json:"failure_reason,omitempty"`

	PaidAt       *time.Time   `db:"paid_at"       json:"paid_at,omitempty"`
	RefundedAt   *time.Time   `db:"refunded_at"   json:"refunded_at,omitempty"`
	RefundAmount money.Amount `db:"refund_amount" json:"refund_amount"`

	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// Entitlement is the answer to "what can this user do right now?", assembled
// by the billing service and cached briefly in Redis.
type Entitlement struct {
	UserID   string   `json:"-"`
	PlanCode string   `json:"plan_code"`
	Tier     string   `json:"tier"`
	Features []string `json:"features"`

	SubscriptionStatus string     `json:"subscription_status"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	DaysRemaining      int        `json:"days_remaining"`

	AllowanceCredits int `json:"allowance_credits"`
	PurchasedCredits int `json:"purchased_credits"`
	TotalCredits     int `json:"total_credits"`
}

// Has reports whether the plan unlocks a feature.
func (e Entitlement) Has(featureCode string) bool {
	for _, f := range e.Features {
		if f == featureCode {
			return true
		}
	}
	return false
}
