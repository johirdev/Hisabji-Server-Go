package domain

import (
	"time"

	"github.com/johirdev/Hisabji-Server/internal/core/money"
)

// Insight severities.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Forecast kinds — the prediction modules from the product spec.
const (
	ForecastMonthEndSpend   = "month_end_spend"
	ForecastMonthEndBalance = "month_end_balance"
	ForecastCategorySpend   = "category_spend"
	ForecastBudgetRisk      = "budget_risk"
	ForecastCashRunway      = "cash_runway"
	ForecastSavings         = "savings"
	ForecastGoal            = "goal"
	ForecastYearly          = "yearly"
)

// Insight is one persisted AI answer.
type Insight struct {
	ID          string `db:"id"           json:"id"`
	UserID      string `db:"user_id"      json:"-"`
	FeatureCode string `db:"feature_code" json:"feature_code"`

	PeriodType  string    `db:"period_type"  json:"period_type"`
	PeriodStart time.Time `db:"period_start" json:"period_start"`
	PeriodEnd   time.Time `db:"period_end"   json:"period_end"`

	Title     string  `db:"title"      json:"title"`
	TitleBN   *string `db:"title_bn"   json:"title_bn,omitempty"`
	Summary   string  `db:"summary"    json:"summary"`
	SummaryBN *string `db:"summary_bn" json:"summary_bn,omitempty"`

	// Payload is the structured half of the answer that the client renders as
	// cards: metrics, per-category findings, ranked actions, projections.
	Payload map[string]any `db:"payload" json:"payload"`

	Severity string `db:"severity" json:"severity"`

	Model         *string `db:"model"          json:"model,omitempty"`
	PromptVersion *string `db:"prompt_version" json:"-"`
	CreditsSpent  int     `db:"credits_spent"  json:"credits_spent"`
	InputTokens   int     `db:"input_tokens"   json:"-"`
	OutputTokens  int     `db:"output_tokens"  json:"-"`
	LatencyMS     int     `db:"latency_ms"     json:"-"`

	CacheKey  *string    `db:"cache_key"  json:"-"`
	ExpiresAt *time.Time `db:"expires_at" json:"-"`
	IsCurrent bool       `db:"is_current" json:"is_current"`

	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// Forecast is one projection. Most are arithmetic rather than model output,
// which is why they cost nothing and stay available on the free plan.
type Forecast struct {
	ID     string `db:"id"      json:"id"`
	UserID string `db:"user_id" json:"-"`

	Kind       string  `db:"kind"        json:"kind"`
	CategoryID *string `db:"category_id" json:"category_id,omitempty"`
	GoalID     *string `db:"goal_id"     json:"goal_id,omitempty"`

	PeriodStart time.Time `db:"period_start" json:"period_start"`
	PeriodEnd   time.Time `db:"period_end"   json:"period_end"`

	ProjectedAmount money.Amount  `db:"projected_amount" json:"projected_amount"`
	ActualAmount    *money.Amount `db:"actual_amount"    json:"actual_amount,omitempty"`
	Confidence      int           `db:"confidence"       json:"confidence"`
	ProjectedDays   *int          `db:"projected_days"   json:"projected_days,omitempty"`

	Basis map[string]any `db:"basis" json:"basis"`

	GeneratedAt time.Time `db:"generated_at" json:"generated_at"`
	ExpiresAt   time.Time `db:"expires_at"   json:"-"`
}

// Notification is one in-app or push message.
type Notification struct {
	ID     string `db:"id"      json:"id"`
	UserID string `db:"user_id" json:"-"`

	Type    string  `db:"type"     json:"type"`
	Title   string  `db:"title"    json:"title"`
	TitleBN *string `db:"title_bn" json:"title_bn,omitempty"`
	Body    string  `db:"body"     json:"body"`
	BodyBN  *string `db:"body_bn"  json:"body_bn,omitempty"`

	Data     map[string]any `db:"data"     json:"data"`
	Channel  string         `db:"channel"  json:"channel"`
	Priority string         `db:"priority" json:"priority"`

	ReadAt *time.Time `db:"read_at" json:"read_at,omitempty"`
	SentAt *time.Time `db:"sent_at" json:"sent_at,omitempty"`

	// When the cleanup job may remove it. Internal housekeeping, not something
	// the notification list needs to show.
	ExpiresAt time.Time `db:"expires_at" json:"-"`

	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// IsRead reports whether the user has already seen it.
func (n Notification) IsRead() bool { return n.ReadAt != nil }

// Notification types.
const (
	NotifyBudgetWarning   = "budget_warning"
	NotifyBudgetCritical  = "budget_critical"
	NotifyRecurringDue    = "recurring_due"
	NotifyRecurringPosted = "recurring_posted"
	NotifyGoalBehind      = "goal_behind"
	NotifyGoalAchieved    = "goal_achieved"
	NotifyInsightReady    = "insight_ready"
	NotifyLowCredits      = "low_credits"
	NotifySubExpiring     = "subscription_expiring"
	NotifySubExpired      = "subscription_expired"
	NotifyPaymentFailed   = "payment_failed"
	NotifySecurityAlert   = "security_alert"
)

// Device is one push target.
type Device struct {
	ID     string `db:"id"      json:"id"`
	UserID string `db:"user_id" json:"-"`

	PushToken  string  `db:"push_token"  json:"-"`
	Platform   string  `db:"platform"    json:"platform"`
	DeviceName *string `db:"device_name" json:"device_name"`
	AppVersion *string `db:"app_version" json:"app_version"`
	Locale     *string `db:"locale"      json:"locale"`

	IsActive   bool      `db:"is_active"    json:"is_active"`
	LastSeenAt time.Time `db:"last_seen_at" json:"last_seen_at"`
	CreatedAt  time.Time `db:"created_at"   json:"created_at"`
}

// Feedback kinds.
const (
	FeedbackInsight        = "insight"
	FeedbackRecommendation = "recommendation"
	FeedbackFeatureRequest = "feature_request"
	FeedbackCancelReason   = "cancel_reason"
	FeedbackBug            = "bug"
	FeedbackCategoryFix    = "category_correction"
	FeedbackGeneral        = "general"
)

// Feedback is one piece of user input about the product or its advice.
type Feedback struct {
	ID     string `db:"id"      json:"id"`
	UserID string `db:"user_id" json:"-"`

	Kind          string  `db:"kind"           json:"kind"`
	ReferenceType *string `db:"reference_type" json:"reference_type,omitempty"`
	ReferenceID   *string `db:"reference_id"   json:"reference_id,omitempty"`

	Helpful *bool   `db:"helpful" json:"helpful,omitempty"`
	Rating  *int    `db:"rating"  json:"rating,omitempty"`
	Message *string `db:"message" json:"message,omitempty"`

	SuggestedValue *string `db:"suggested_value" json:"suggested_value,omitempty"`
	CorrectedValue *string `db:"corrected_value" json:"corrected_value,omitempty"`

	Followed *bool   `db:"followed" json:"followed,omitempty"`
	Outcome  *string `db:"outcome"  json:"outcome,omitempty"`
	Status   string  `db:"status"   json:"status"`

	CreatedAt time.Time `db:"created_at" json:"created_at"`
}
