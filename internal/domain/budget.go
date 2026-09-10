package domain

import (
	"time"

	"github.com/johirdev/Hisabji-Server/internal/core/money"
)

// Budget statuses.
const (
	BudgetDraft  = "draft"
	BudgetActive = "active"
	BudgetClosed = "closed"
)

// Budget health levels, shown on the dashboard as a traffic light.
const (
	HealthSafe     = "safe"     // under the warn threshold
	HealthWarning  = "warning"  // past warn_percent, still inside the limit
	HealthCritical = "critical" // at or past the limit
)

// Budget is one planning cycle.
type Budget struct {
	ID     string `db:"id"      json:"id"`
	UserID string `db:"user_id" json:"-"`

	PeriodStart time.Time `db:"period_start" json:"period_start"`
	PeriodEnd   time.Time `db:"period_end"   json:"period_end"`
	PeriodType  string    `db:"period_type"  json:"period_type"`

	PlannedIncome money.Amount `db:"planned_income" json:"planned_income"`
	SavingsTarget money.Amount `db:"savings_target" json:"savings_target"`

	Title  *string `db:"title"  json:"title"`
	Note   *string `db:"note"   json:"note"`
	Status string  `db:"status" json:"status"`

	WarnPercent int  `db:"warn_percent" json:"warn_percent"`
	Rollover    bool `db:"rollover"     json:"rollover"`

	ClosedAt  *time.Time `db:"closed_at"  json:"closed_at,omitempty"`
	DeletedAt *time.Time `db:"deleted_at" json:"-"`
	CreatedAt time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt time.Time  `db:"updated_at" json:"updated_at"`
}

// Covers reports whether a date falls inside this budget cycle.
func (b Budget) Covers(day time.Time) bool {
	d := day.Truncate(24 * time.Hour)
	return !d.Before(b.PeriodStart) && !d.After(b.PeriodEnd)
}

// TotalDays is the length of the cycle in days, inclusive of both ends.
func (b Budget) TotalDays() int {
	return int(b.PeriodEnd.Sub(b.PeriodStart).Hours()/24) + 1
}

// DaysElapsed counts days from the start up to and including `day`, clamped to
// the cycle. Used to work out how far through the month the user is.
func (b Budget) DaysElapsed(day time.Time) int {
	switch {
	case day.Before(b.PeriodStart):
		return 0
	case day.After(b.PeriodEnd):
		return b.TotalDays()
	default:
		return int(day.Sub(b.PeriodStart).Hours()/24) + 1
	}
}

// DaysRemaining counts days left including today, never below zero. It is the
// denominator of the "safe to spend today" calculation, so it must never be 0.
func (b Budget) DaysRemaining(day time.Time) int {
	if day.After(b.PeriodEnd) {
		return 0
	}
	if day.Before(b.PeriodStart) {
		return b.TotalDays()
	}
	return int(b.PeriodEnd.Sub(day).Hours()/24) + 1
}

// BudgetLimit is one category's plan inside a budget.
type BudgetLimit struct {
	ID         string `db:"id"          json:"id"`
	BudgetID   string `db:"budget_id"   json:"budget_id"`
	UserID     string `db:"user_id"     json:"-"`
	CategoryID string `db:"category_id" json:"category_id"`

	LimitAmount money.Amount `db:"limit_amount" json:"limit_amount"`
	IsFixed     bool         `db:"is_fixed"     json:"is_fixed"`
	WarnPercent *int         `db:"warn_percent" json:"warn_percent"`
	Note        *string      `db:"note"         json:"note"`

	CategoryName  *string `db:"category_name"  json:"category_name,omitempty"`
	CategoryIcon  *string `db:"category_icon"  json:"category_icon,omitempty"`
	CategoryColor *string `db:"category_color" json:"category_color,omitempty"`

	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// CategorySpend is one row of a "budget vs actual" breakdown. It is a
// computed view, not a table.
type CategorySpend struct {
	CategoryID     *string `db:"category_id"   json:"category_id"`
	CategoryName   string  `db:"category_name" json:"category_name"`
	CategoryNameBN *string `db:"category_name_bn" json:"category_name_bn,omitempty"`
	Icon           string  `db:"icon"          json:"icon"`
	Color          string  `db:"color"         json:"color"`
	IsFixed        bool    `db:"is_fixed"      json:"is_fixed"`

	Limit   money.Amount `db:"limit_amount" json:"limit_amount"`
	Spent   money.Amount `db:"spent"        json:"spent"`
	TxCount int          `db:"tx_count"     json:"transaction_count"`

	// Derived in Go rather than SQL, so the rounding rules live in one place.
	Remaining    money.Amount `db:"-" json:"remaining"`
	UsedPercent  float64      `db:"-" json:"used_percent"`
	SharePercent float64      `db:"-" json:"share_percent"`
	Health       string       `db:"-" json:"health"`
	IsOverspent  bool         `db:"-" json:"is_overspent"`
}

// Goal statuses.
const (
	GoalActive    = "active"
	GoalAchieved  = "achieved"
	GoalPaused    = "paused"
	GoalCancelled = "cancelled"
)

// Goal is a savings target.
type Goal struct {
	ID     string `db:"id"      json:"id"`
	UserID string `db:"user_id" json:"-"`

	Title       string  `db:"title"       json:"title"`
	Description *string `db:"description" json:"description"`
	Icon        string  `db:"icon"        json:"icon"`
	Color       string  `db:"color"       json:"color"`

	TargetAmount money.Amount `db:"target_amount" json:"target_amount"`
	SavedAmount  money.Amount `db:"saved_amount"  json:"saved_amount"`

	StartDate  time.Time  `db:"start_date"  json:"start_date"`
	TargetDate *time.Time `db:"target_date" json:"target_date"`
	AchievedAt *time.Time `db:"achieved_at" json:"achieved_at,omitempty"`

	Kind     string `db:"kind"     json:"kind"`
	Priority int    `db:"priority" json:"priority"`
	Status   string `db:"status"   json:"status"`

	RequiredMonthly money.Amount `db:"required_monthly" json:"required_monthly"`

	DeletedAt *time.Time `db:"deleted_at" json:"-"`
	CreatedAt time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt time.Time  `db:"updated_at" json:"updated_at"`
}

// Remaining is how much is still needed, never negative.
func (g Goal) Remaining() money.Amount {
	if g.SavedAmount >= g.TargetAmount {
		return 0
	}
	return g.TargetAmount.Sub(g.SavedAmount)
}

// ProgressPercent is 0-100, capped so an over-funded goal reads as 100 rather
// than 137 in a progress bar.
func (g Goal) ProgressPercent() float64 {
	p := g.SavedAmount.RatioPercent(g.TargetAmount)
	if p > 100 {
		return 100
	}
	return p
}

// MonthsRemaining is how many whole months are left before the target date.
func (g Goal) MonthsRemaining(now time.Time) int {
	if g.TargetDate == nil {
		return 0
	}
	months := int(g.TargetDate.Sub(now).Hours() / 24 / 30.44)
	if months < 0 {
		return 0
	}
	return months
}

// GoalContribution is one deposit into (or withdrawal from) a goal.
type GoalContribution struct {
	ID     string `db:"id"      json:"id"`
	GoalID string `db:"goal_id" json:"goal_id"`
	UserID string `db:"user_id" json:"-"`

	Amount        money.Amount `db:"amount"         json:"amount"`
	ContributedAt time.Time    `db:"contributed_at" json:"contributed_at"`
	Note          *string      `db:"note"           json:"note"`
	Origin        string       `db:"origin"         json:"origin"`

	CreatedAt time.Time `db:"created_at" json:"created_at"`
}
