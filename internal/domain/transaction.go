package domain

import (
	"time"

	"github.com/johirdev/Hisabji-Server/internal/core/money"
)

// Transaction kinds.
const (
	KindExpense = "expense"
	KindIncome  = "income"
)

// Row origins, so the client can badge auto-generated entries.
const (
	SourceManual    = "manual"
	SourceRecurring = "recurring"
	SourceImport    = "import"
	SourceAI        = "ai"
)

// Payment methods accepted by the API. Mirrors the CHECK constraint in
// migration 002 — keep the two in step.
var PaymentMethods = []string{"cash", "card", "bkash", "nagad", "rocket", "bank", "due", "other"}

// Category is a spending or income bucket. A row with a nil UserID is a system
// category shared by every user.
type Category struct {
	ID       string  `db:"id"        json:"id"`
	UserID   *string `db:"user_id"   json:"-"`
	ParentID *string `db:"parent_id" json:"parent_id,omitempty"`

	Slug   string  `db:"slug"    json:"slug"`
	Name   string  `db:"name"    json:"name"`
	NameBN *string `db:"name_bn" json:"name_bn,omitempty"`
	Icon   string  `db:"icon"    json:"icon"`
	Color  string  `db:"color"   json:"color"`

	Kind      string `db:"kind"       json:"kind"`
	IsFixed   bool   `db:"is_fixed"   json:"is_fixed"`
	IsSystem  bool   `db:"is_system"  json:"is_system"`
	IsActive  bool   `db:"is_active"  json:"is_active"`
	SortOrder int    `db:"sort_order" json:"sort_order"`

	DefaultLimit *money.Amount `db:"default_limit" json:"default_limit,omitempty"`

	DeletedAt *time.Time `db:"deleted_at" json:"-"`
	CreatedAt time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt time.Time  `db:"updated_at" json:"updated_at"`
}

// Label returns the name in the requested locale, falling back to English.
func (c Category) Label(locale string) string {
	if locale == "bn" && c.NameBN != nil && *c.NameBN != "" {
		return *c.NameBN
	}
	return c.Name
}

// Expense is one outgoing transaction.
type Expense struct {
	ID              string  `db:"id"                json:"id"`
	UserID          string  `db:"user_id"           json:"-"`
	CategoryID      *string `db:"category_id"       json:"category_id"`
	RecurringRuleID *string `db:"recurring_rule_id" json:"recurring_rule_id,omitempty"`

	Amount   money.Amount `db:"amount"   json:"amount"`
	Currency string       `db:"currency" json:"currency"`
	SpentAt  time.Time    `db:"spent_at" json:"spent_at"`

	Note           *string  `db:"note"            json:"note"`
	Merchant       *string  `db:"merchant"        json:"merchant"`
	PaymentMethod  string   `db:"payment_method"  json:"payment_method"`
	Tags           []string `db:"tags"            json:"tags"`
	AttachmentPath *string  `db:"attachment_path" json:"attachment_path,omitempty"`

	Source    string `db:"source"     json:"source"`
	IsAnomaly bool   `db:"is_anomaly" json:"is_anomaly"`

	// Populated by the LEFT JOIN in the list query so the client can render a
	// row without a second request. RowToStructByNameLax leaves them zero when
	// a query does not select them.
	CategoryName  *string `db:"category_name"  json:"category_name,omitempty"`
	CategoryIcon  *string `db:"category_icon"  json:"category_icon,omitempty"`
	CategoryColor *string `db:"category_color" json:"category_color,omitempty"`

	DeletedAt *time.Time `db:"deleted_at" json:"-"`
	CreatedAt time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt time.Time  `db:"updated_at" json:"updated_at"`
}

// Income is one incoming transaction.
type Income struct {
	ID              string  `db:"id"                json:"id"`
	UserID          string  `db:"user_id"           json:"-"`
	CategoryID      *string `db:"category_id"       json:"category_id"`
	RecurringRuleID *string `db:"recurring_rule_id" json:"recurring_rule_id,omitempty"`

	Amount     money.Amount `db:"amount"      json:"amount"`
	Currency   string       `db:"currency"    json:"currency"`
	ReceivedAt time.Time    `db:"received_at" json:"received_at"`

	Source        *string `db:"source"         json:"source"`
	Note          *string `db:"note"           json:"note"`
	PaymentMethod string  `db:"payment_method" json:"payment_method"`
	Origin        string  `db:"origin"         json:"origin"`

	CategoryName  *string `db:"category_name"  json:"category_name,omitempty"`
	CategoryIcon  *string `db:"category_icon"  json:"category_icon,omitempty"`
	CategoryColor *string `db:"category_color" json:"category_color,omitempty"`

	DeletedAt *time.Time `db:"deleted_at" json:"-"`
	CreatedAt time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt time.Time  `db:"updated_at" json:"updated_at"`
}

// Recurring frequencies.
const (
	FreqDaily   = "daily"
	FreqWeekly  = "weekly"
	FreqMonthly = "monthly"
	FreqYearly  = "yearly"
)

// RecurringRule is the template that generates repeated expenses or incomes.
type RecurringRule struct {
	ID         string  `db:"id"          json:"id"`
	UserID     string  `db:"user_id"     json:"-"`
	CategoryID *string `db:"category_id" json:"category_id"`

	Kind          string       `db:"kind"           json:"kind"`
	Title         string       `db:"title"          json:"title"`
	Amount        money.Amount `db:"amount"         json:"amount"`
	Note          *string      `db:"note"           json:"note"`
	PaymentMethod *string      `db:"payment_method" json:"payment_method"`

	Frequency     string `db:"frequency"      json:"frequency"`
	IntervalCount int    `db:"interval_count" json:"interval_count"`
	DayOfMonth    *int   `db:"day_of_month"   json:"day_of_month"`
	Weekday       *int   `db:"weekday"        json:"weekday"`

	StartDate   time.Time  `db:"start_date"   json:"start_date"`
	EndDate     *time.Time `db:"end_date"     json:"end_date"`
	NextRunOn   time.Time  `db:"next_run_on"  json:"next_run_on"`
	LastRunOn   *time.Time `db:"last_run_on"  json:"last_run_on"`
	RunsCreated int        `db:"runs_created" json:"runs_created"`

	AutoPost bool `db:"auto_post" json:"auto_post"`
	IsActive bool `db:"is_active" json:"is_active"`

	CategoryName *string `db:"category_name" json:"category_name,omitempty"`

	DeletedAt *time.Time `db:"deleted_at" json:"-"`
	CreatedAt time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt time.Time  `db:"updated_at" json:"updated_at"`
}
