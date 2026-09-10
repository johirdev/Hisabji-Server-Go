package user

import (
	"strings"

	"github.com/johirdev/Hisabji-Server/internal/core/money"
	"github.com/johirdev/Hisabji-Server/internal/core/validate"
	"github.com/johirdev/Hisabji-Server/internal/domain"
)

// UpdateProfileRequest is a PARTIAL update: every field is a pointer, so the
// service can tell "not sent" from "sent as empty".
//
// Without pointers, a client that only wants to change its name would send a
// zero-valued email and silently erase the real one. This is the single most
// common way a PATCH endpoint destroys user data.
type UpdateProfileRequest struct {
	Name     *string `json:"name"      binding:"omitempty,notblank,min=2,max=100,safetext"`
	Email    *string `json:"email"     binding:"omitempty,email,max=255"`
	UserType *string `json:"user_type" binding:"omitempty,oneof=personal student job_holder business freelancer family"`

	AvatarPath *string `json:"avatar_path" binding:"omitempty,max=500"`

	MonthlyIncome *money.Amount `json:"monthly_income"`
	MonthStartDay *int          `json:"month_start_day" binding:"omitempty,min=1,max=28"`
}

// Normalize trims and lower-cases where appropriate.
func (r *UpdateProfileRequest) Normalize() {
	if r.Name != nil {
		trimmed := strings.TrimSpace(*r.Name)
		r.Name = &trimmed
	}
	if r.Email != nil {
		e := strings.ToLower(strings.TrimSpace(*r.Email))
		r.Email = &e
	}
}

// Validate checks what the struct tags cannot.
func (r UpdateProfileRequest) Validate() error {
	e := validate.NewErrors()
	if r.MonthlyIncome != nil {
		e.AddIf(r.MonthlyIncome.IsNegative(),
			"monthly_income", "min",
			"Monthly income cannot be negative.",
			"মাসিক আয় ঋণাত্মক হতে পারে না।")
		// A billion taka a month is a typo, not an income. Catching it here
		// stops every downstream projection from being nonsense.
		e.AddIf(*r.MonthlyIncome > money.FromMajor(100_000_000),
			"monthly_income", "max",
			"That monthly income looks too large. Please check the amount.",
			"মাসিক আয়ের পরিমাণটি অস্বাভাবিক বড় মনে হচ্ছে। অনুগ্রহ করে যাচাই করুন।")
	}
	return e.Err()
}

// IsEmpty reports whether the request would change nothing.
func (r UpdateProfileRequest) IsEmpty() bool {
	return r.Name == nil && r.Email == nil && r.UserType == nil &&
		r.AvatarPath == nil && r.MonthlyIncome == nil && r.MonthStartDay == nil
}

// UpdatePreferencesRequest changes display and cycle settings.
type UpdatePreferencesRequest struct {
	Locale        *string `json:"locale"          binding:"omitempty,oneof=bn en"`
	Currency      *string `json:"currency"        binding:"omitempty,len=3,alpha"`
	Timezone      *string `json:"timezone"        binding:"omitempty,max=64"`
	MonthStartDay *int    `json:"month_start_day" binding:"omitempty,min=1,max=28"`
}

// Normalize upper-cases the currency code.
func (r *UpdatePreferencesRequest) Normalize() {
	if r.Currency != nil {
		c := strings.ToUpper(strings.TrimSpace(*r.Currency))
		r.Currency = &c
	}
	if r.Locale != nil {
		l := strings.ToLower(strings.TrimSpace(*r.Locale))
		r.Locale = &l
	}
}

// IsEmpty reports whether the request would change nothing.
func (r UpdatePreferencesRequest) IsEmpty() bool {
	return r.Locale == nil && r.Currency == nil && r.Timezone == nil && r.MonthStartDay == nil
}

// UsernameRequest claims or changes a username.
type UsernameRequest struct {
	Username string `json:"username" binding:"required,username"`
}

// Normalize lower-cases the username, so "Rasel" and "rasel" are one name.
func (r *UsernameRequest) Normalize() {
	r.Username = strings.ToLower(strings.TrimSpace(r.Username))
}

// OnboardingRequest advances the setup wizard.
type OnboardingRequest struct {
	Step string `json:"step" binding:"required,oneof=profile income categories budget done"`

	// Everything below is optional and depends on the step. Collecting them in
	// one request means the wizard can save progress in a single round trip.
	UserType      *string       `json:"user_type"       binding:"omitempty,oneof=personal student job_holder business freelancer family"`
	MonthlyIncome *money.Amount `json:"monthly_income"`
	MonthStartDay *int          `json:"month_start_day" binding:"omitempty,min=1,max=28"`
	Currency      *string       `json:"currency"        binding:"omitempty,len=3,alpha"`
	Locale        *string       `json:"locale"          binding:"omitempty,oneof=bn en"`
}

// Validate rejects impossible income figures early.
func (r OnboardingRequest) Validate() error {
	e := validate.NewErrors()
	if r.MonthlyIncome != nil {
		e.AddIf(r.MonthlyIncome.IsNegative(),
			"monthly_income", "min",
			"Monthly income cannot be negative.",
			"মাসিক আয় ঋণাত্মক হতে পারে না।")
	}
	return e.Err()
}

// DeleteAccountRequest confirms an account deletion.
//
// The password is required even though the caller is already authenticated: a
// stolen phone with an unlocked app must not be able to erase somebody's whole
// financial history in two taps.
type DeleteAccountRequest struct {
	Password string  `json:"password" binding:"required,max=72"`
	Reason   *string `json:"reason"   binding:"omitempty,max=500,safetext"`
	// Confirm must be the literal word DELETE, as a second deliberate action.
	Confirm string `json:"confirm" binding:"required,eq=DELETE"`
}

// ProfileResponse is what GET /users/me returns: the account plus everything
// the app needs to render its home screen without four more requests.
type ProfileResponse struct {
	User        *domain.User        `json:"user"`
	Entitlement *domain.Entitlement `json:"entitlement,omitempty"`

	// Onboarding progress, so the client can resume the wizard.
	OnboardingComplete bool   `json:"onboarding_complete"`
	OnboardingStep     string `json:"onboarding_step"`

	// Counters that turn an empty dashboard into a useful first-run experience.
	Stats *ProfileStats `json:"stats,omitempty"`
}

// ProfileStats are lifetime counters for the account.
type ProfileStats struct {
	ExpenseCount  int64        `db:"expense_count"  json:"expense_count"`
	IncomeCount   int64        `db:"income_count"   json:"income_count"`
	CategoryCount int64        `db:"category_count" json:"category_count"`
	GoalCount     int64        `db:"goal_count"     json:"goal_count"`
	TotalSpent    money.Amount `db:"total_spent"    json:"total_spent"`
	TotalIncome   money.Amount `db:"total_income"   json:"total_income"`
	FirstEntryOn  *string      `db:"first_entry_on" json:"first_entry_on"`
	ActiveDays    int64        `db:"active_days"    json:"active_days"`
}

// UsernameAvailability answers the "is this taken?" check as the user types.
type UsernameAvailability struct {
	Username    string   `json:"username"`
	Available   bool     `json:"available"`
	Reason      string   `json:"reason,omitempty"`
	Suggestions []string `json:"suggestions,omitempty"`
}
