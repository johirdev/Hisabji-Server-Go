package auth

import (
	"strings"
	"time"

	"github.com/johirdev/Hisabji-Server/internal/core/validate"
	"github.com/johirdev/Hisabji-Server/internal/domain"
	"github.com/johirdev/Hisabji-Server/internal/shared/jwt"
)

// RegisterRequest creates a new account.
//
// Note what is NOT here: role, plan, status, credits. A client that could set
// those would be able to make itself an admin on a free plan with a million
// credits. Privileged fields are only ever set server-side.
type RegisterRequest struct {
	Name     string  `json:"name"     binding:"required,notblank,min=2,max=100,safetext"`
	Phone    string  `json:"phone"    binding:"required,bdphone"`
	Password string  `json:"password" binding:"required,strongpass,max=72"`
	Email    *string `json:"email"    binding:"omitempty,email,max=255"`
	Username *string `json:"username" binding:"omitempty,username"`

	// Optional profile hints collected on the signup screen.
	UserType *string `json:"user_type" binding:"omitempty,oneof=personal student job_holder business freelancer family"`
	Locale   *string `json:"locale"    binding:"omitempty,oneof=bn en"`

	// DeviceName labels the session in the "signed-in devices" list.
	DeviceName *string `json:"device_name" binding:"omitempty,max=120"`
}

// Normalize canonicalises input before it reaches the database, so the same
// person cannot register twice as "+8801712345678" and "01712345678", or as
// "Rasel@Example.COM" and "rasel@example.com".
func (r *RegisterRequest) Normalize() {
	r.Name = strings.TrimSpace(r.Name)
	r.Phone = validate.NormalizePhone(r.Phone)
	if r.Email != nil {
		e := strings.ToLower(strings.TrimSpace(*r.Email))
		if e == "" {
			r.Email = nil
		} else {
			r.Email = &e
		}
	}
	if r.Username != nil {
		u := strings.ToLower(strings.TrimSpace(*r.Username))
		if u == "" {
			r.Username = nil
		} else {
			r.Username = &u
		}
	}
}

// Validate adds the cross-field rule struct tags cannot express.
func (r RegisterRequest) Validate() error {
	e := validate.NewErrors()
	e.AddIf(strings.EqualFold(r.Password, r.Name),
		"password", "weak",
		"Password must not be the same as your name.",
		"Password আপনার নামের মতো হতে পারবে না।")
	e.AddIf(strings.Contains(r.Password, r.Phone) || strings.Contains(r.Phone, r.Password),
		"password", "weak",
		"Password must not contain your phone number.",
		"Password-এ আপনার মোবাইল নম্বর থাকতে পারবে না।")
	return e.Err()
}

// LoginRequest signs in with a phone number, email or username.
type LoginRequest struct {
	Identifier string  `json:"identifier" binding:"required,notblank,max=255"`
	Password   string  `json:"password"   binding:"required,max=72"`
	DeviceName *string `json:"device_name" binding:"omitempty,max=120"`
}

// Normalize lower-cases the identifier and canonicalises it when it looks like
// a phone number, so login accepts every form registration accepted.
func (r *LoginRequest) Normalize() {
	id := strings.TrimSpace(r.Identifier)
	if looksLikePhone(id) {
		r.Identifier = validate.NormalizePhone(id)
		return
	}
	r.Identifier = strings.ToLower(id)
}

func looksLikePhone(s string) bool {
	if len(s) < 6 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || r == '+' || r == '-' || r == ' ') {
			return false
		}
	}
	return true
}

// VerifyOTPRequest completes phone verification.
type VerifyOTPRequest struct {
	Phone string `json:"phone" binding:"required,bdphone"`
	OTP   string `json:"otp"   binding:"required,len=6,numeric"`
}

// Normalize canonicalises the phone number.
func (r *VerifyOTPRequest) Normalize() { r.Phone = validate.NormalizePhone(r.Phone) }

// ResendOTPRequest asks for a fresh code.
type ResendOTPRequest struct {
	Phone   string `json:"phone"   binding:"required,bdphone"`
	Purpose string `json:"purpose" binding:"omitempty,oneof=register login reset_password change_phone"`
}

// Normalize canonicalises the phone number and defaults the purpose.
func (r *ResendOTPRequest) Normalize() {
	r.Phone = validate.NormalizePhone(r.Phone)
	if r.Purpose == "" {
		r.Purpose = domain.OTPPurposeRegister
	}
}

// RefreshRequest exchanges a refresh token for a new pair.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required,min=20"`
}

// LogoutRequest ends one session. An empty token means "the current session".
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"omitempty"`
	AllDevices   bool   `json:"all_devices"`
}

// ForgotPasswordRequest starts a password reset by phone.
type ForgotPasswordRequest struct {
	Phone string `json:"phone" binding:"required,bdphone"`
}

// Normalize canonicalises the phone number.
func (r *ForgotPasswordRequest) Normalize() { r.Phone = validate.NormalizePhone(r.Phone) }

// ResetPasswordRequest completes a password reset with the OTP.
type ResetPasswordRequest struct {
	Phone       string `json:"phone"        binding:"required,bdphone"`
	OTP         string `json:"otp"          binding:"required,len=6,numeric"`
	NewPassword string `json:"new_password" binding:"required,strongpass,max=72"`
}

// Normalize canonicalises the phone number.
func (r *ResetPasswordRequest) Normalize() { r.Phone = validate.NormalizePhone(r.Phone) }

// ChangePasswordRequest changes the password of a signed-in user.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required,max=72"`
	NewPassword     string `json:"new_password"     binding:"required,strongpass,max=72"`
}

// Validate rejects a "change" that changes nothing.
func (r ChangePasswordRequest) Validate() error {
	e := validate.NewErrors()
	e.AddIf(r.CurrentPassword == r.NewPassword,
		"new_password", "same",
		"The new password must be different from your current one.",
		"নতুন password আগেরটির থেকে আলাদা হতে হবে।")
	return e.Err()
}

// ---------------------------------------------------------------------------
// Responses
// ---------------------------------------------------------------------------

// AuthResponse is returned by register, login and refresh.
type AuthResponse struct {
	User   *domain.User   `json:"user,omitempty"`
	Tokens *jwt.TokenPair `json:"tokens,omitempty"`

	// RequiresVerification tells the client to route to the OTP screen instead
	// of the dashboard. Explicit beats making the app infer it from a missing
	// token — one boolean removes a whole class of navigation bug.
	RequiresVerification bool `json:"requires_verification"`

	// DevOTP is populated only outside production, so a developer can complete
	// a signup without an SMS gateway. It is impossible to leak in production:
	// the service only ever sets it when the environment is not production.
	DevOTP string `json:"dev_otp,omitempty"`

	// NextStep tells the client which onboarding screen to show.
	NextStep string `json:"next_step,omitempty"`
}

// SessionResponse is one entry in the "signed-in devices" list.
type SessionResponse struct {
	ID         string     `json:"id"`
	DeviceName *string    `json:"device_name"`
	IP         *string    `json:"ip,omitempty"`
	IsCurrent  bool       `json:"is_current"`
	LastUsedAt time.Time  `json:"last_used_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// OTPResponse acknowledges that a code was sent.
type OTPResponse struct {
	Phone        string    `json:"phone"`
	Purpose      string    `json:"purpose"`
	ExpiresAt    time.Time `json:"expires_at"`
	ResendAfter  int       `json:"resend_after_seconds"`
	AttemptsLeft int       `json:"attempts_left"`
	DevOTP       string    `json:"dev_otp,omitempty"`
}
