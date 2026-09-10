// Package domain holds the structs that map one-to-one onto database rows and
// onto the JSON the API returns.
//
// Two tag sets, two audiences:
//
//	db:"..."   consumed by pgx.RowToStructByNameLax when scanning a row
//	json:"..." the public contract the frontend codes against
//
// A field the client must never see (password_hash) carries `json:"-"`. That
// single character is the difference between a safe response and a credential
// leak, so it is worth checking on every new field.
package domain

import (
	"net/netip"
	"time"

	"github.com/johirdev/Hisabji-Server/internal/core/money"
)

// User account roles.
const (
	RoleUser    = "user"
	RoleAdmin   = "admin"
	RoleSupport = "support"
)

// Account statuses.
const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
	StatusDeleted   = "deleted"
)

// User types, which decide the dashboard layout and the AI prompt used.
const (
	UserTypePersonal   = "personal"
	UserTypeStudent    = "student"
	UserTypeJobHolder  = "job_holder"
	UserTypeBusiness   = "business"
	UserTypeFreelancer = "freelancer"
	UserTypeFamily     = "family"
)

// Onboarding steps, in order.
const (
	OnboardingProfile    = "profile"
	OnboardingIncome     = "income"
	OnboardingCategories = "categories"
	OnboardingBudget     = "budget"
	OnboardingDone       = "done"
)

// User is one account.
type User struct {
	ID       string  `db:"id"       json:"id"`
	Name     string  `db:"name"     json:"name"`
	Username *string `db:"username" json:"username"`
	Email    *string `db:"email"    json:"email"`
	Phone    string  `db:"phone"    json:"phone"`

	// PasswordHash is loaded by the auth repository and must never be
	// serialised. The json:"-" tag is the only thing standing between a bcrypt
	// hash and every client that calls GET /users/me.
	PasswordHash string `db:"password_hash" json:"-"`

	AvatarPath *string `db:"avatar_path" json:"avatar_path"`

	UserType string `db:"user_type" json:"user_type"`
	Role     string `db:"role"      json:"role"`
	Status   string `db:"status"    json:"status"`

	Locale   string `db:"locale"   json:"locale"`
	Currency string `db:"currency" json:"currency"`
	Timezone string `db:"timezone" json:"timezone"`

	MonthlyIncome money.Amount `db:"monthly_income"   json:"monthly_income"`
	MonthStartDay int          `db:"month_start_day"  json:"month_start_day"`

	PhoneVerified   bool       `db:"phone_verified"     json:"phone_verified"`
	PhoneVerifiedAt *time.Time `db:"phone_verified_at"  json:"phone_verified_at,omitempty"`
	EmailVerified   bool       `db:"email_verified"     json:"email_verified"`
	EmailVerifiedAt *time.Time `db:"email_verified_at"  json:"email_verified_at,omitempty"`

	OnboardingStep string     `db:"onboarding_step" json:"onboarding_step"`
	OnboardedAt    *time.Time `db:"onboarded_at"    json:"onboarded_at,omitempty"`

	PlanCode      string     `db:"plan_code"       json:"plan_code"`
	PlanExpiresAt *time.Time `db:"plan_expires_at" json:"plan_expires_at,omitempty"`

	// Security counters: useful to an admin, meaningless and slightly alarming
	// to a normal user, so they stay out of the JSON.
	FailedLoginCount  int        `db:"failed_login_count"  json:"-"`
	LockedUntil       *time.Time `db:"locked_until"        json:"-"`
	PasswordChangedAt time.Time  `db:"password_changed_at" json:"-"`

	LastLoginAt *time.Time `db:"last_login_at" json:"last_login_at,omitempty"`
	LastSeenAt  *time.Time `db:"last_seen_at"  json:"last_seen_at,omitempty"`

	// The column is Postgres `inet`, which pgx maps to netip.Addr — a plain
	// string would fail to scan. It is security telemetry, not profile data, so
	// it is never serialised to a client.
	LastLoginIP *netip.Addr `db:"last_login_ip" json:"-"`

	DeletedAt *time.Time     `db:"deleted_at" json:"-"`
	Metadata  map[string]any `db:"metadata"   json:"-"`

	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// IsActive reports whether the account may sign in and use the API.
func (u User) IsActive() bool { return u.Status == StatusActive && u.DeletedAt == nil }

// IsAdmin reports whether the account holds an administrative role.
func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// IsLocked reports whether a failed-login lock is still in force.
func (u User) IsLocked() bool {
	return u.LockedUntil != nil && u.LockedUntil.After(time.Now())
}

// LockRemaining is how much longer the account stays locked.
func (u User) LockRemaining() time.Duration {
	if !u.IsLocked() {
		return 0
	}
	return time.Until(*u.LockedUntil)
}

// NeedsOnboarding reports whether the setup wizard should still be shown.
func (u User) NeedsOnboarding() bool { return u.OnboardingStep != OnboardingDone }

// Session is one signed-in device, backing a refresh token.
type Session struct {
	ID       string `db:"id"        json:"id"`
	UserID   string `db:"user_id"   json:"-"`
	FamilyID string `db:"family_id" json:"-"`
	// TokenHash never leaves the server.
	TokenHash string `db:"token_hash" json:"-"`

	DeviceName *string `db:"device_name" json:"device_name"`
	UserAgent  *string `db:"user_agent"  json:"user_agent,omitempty"`
	IP         *string `db:"ip"          json:"ip,omitempty"`

	ExpiresAt     time.Time  `db:"expires_at"     json:"expires_at"`
	LastUsedAt    time.Time  `db:"last_used_at"   json:"last_used_at"`
	RevokedAt     *time.Time `db:"revoked_at"     json:"revoked_at,omitempty"`
	RevokedReason *string    `db:"revoked_reason" json:"revoked_reason,omitempty"`

	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// IsLive reports whether the session may still be refreshed.
func (s Session) IsLive() bool {
	return s.RevokedAt == nil && s.ExpiresAt.After(time.Now())
}

// OTP purposes.
const (
	OTPPurposeRegister      = "register"
	OTPPurposeLogin         = "login"
	OTPPurposeResetPassword = "reset_password"
	OTPPurposeChangePhone   = "change_phone"
	OTPPurposeChangeEmail   = "change_email"
)
