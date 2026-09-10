package auth

import (
	"context"

	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/johirdev/Hisabji-Server/internal/config"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/logger"
	"github.com/johirdev/Hisabji-Server/internal/core/repository"
	"github.com/johirdev/Hisabji-Server/internal/core/txn"
	"github.com/johirdev/Hisabji-Server/internal/domain"
	"github.com/johirdev/Hisabji-Server/internal/infrastructure/sms"
	"github.com/johirdev/Hisabji-Server/internal/shared/hash"
	"github.com/johirdev/Hisabji-Server/internal/shared/jwt"
)

// Provisioner sets up the billing side of a brand-new account: a credit wallet
// and a free-plan subscription.
//
// It is an interface declared HERE, in the consumer, rather than an import of
// the billing package. That keeps auth -> billing a one-way dependency and lets
// either module be tested without the other.
type Provisioner interface {
	ProvisionNewUser(ctx context.Context, tx repository.DB, userID string) error
	// MaxDevices reports how many concurrent sessions the user's plan allows.
	MaxDevices(ctx context.Context, userID string) int
}

// TokenRevoker pushes a revocation into the fast denylist the auth middleware
// consults. Implemented by middleware.Authenticator.
type TokenRevoker interface {
	RevokeSession(ctx context.Context, sessionID string) error
	RevokeAllForUser(ctx context.Context, userID string) error
}

// RequestMeta is the ambient information about the caller that the service
// records for security auditing.
type RequestMeta struct {
	IP         string
	UserAgent  string
	DeviceName *string
	Locale     string
}

// Service holds the authentication business rules.
type Service struct {
	Repo        *Repository
	Pool        *pgxpool.Pool
	Tokens      *jwt.Issuer
	SMS         sms.Sender
	Provisioner Provisioner
	Revoker     TokenRevoker
	Cfg         config.Config
}

// NewService builds the service.
func NewService(repo *Repository, pool *pgxpool.Pool, tokens *jwt.Issuer, sender sms.Sender, prov Provisioner, revoker TokenRevoker, cfg config.Config) *Service {
	return &Service{
		Repo: repo, Pool: pool, Tokens: tokens, SMS: sender,
		Provisioner: prov, Revoker: revoker, Cfg: cfg,
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// Register creates an account and starts phone verification.
//
// No tokens are issued here. An unverified phone number is an unproven claim to
// an identity, and handing out a session before that is proven is how somebody
// registers with a number they do not own.
func (s *Service) Register(ctx context.Context, in RegisterRequest, meta RequestMeta) (*AuthResponse, error) {
	in.Normalize()

	passwordHash, err := hash.Password(in.Password, s.Cfg.Auth.BcryptCost)
	if err != nil {
		return nil, apperr.Internal("").WithCause(err)
	}

	userType := domain.UserTypePersonal
	if in.UserType != nil {
		userType = *in.UserType
	}
	locale := s.Cfg.App.Locale
	if in.Locale != nil {
		locale = *in.Locale
	} else if meta.Locale != "" {
		locale = meta.Locale
	}

	var user domain.User
	err = txn.Run(ctx, s.Pool, func(tx pgx.Tx) error {
		created, err := s.Repo.CreateUser(ctx, tx, in, passwordHash, userType, locale)
		if err != nil {
			return err
		}
		// The wallet and the free subscription are part of creating an account,
		// not a follow-up step. Inside the same transaction, so a failure here
		// leaves no half-created user with no wallet.
		if err := s.Provisioner.ProvisionNewUser(ctx, tx, created.ID); err != nil {
			return err
		}
		user = created
		return nil
	})
	if err != nil {
		return nil, err
	}

	code, _, err := s.issueOTP(ctx, user.Phone, domain.OTPPurposeRegister, &user.ID, meta)
	if err != nil {
		// The account exists and is usable once verified. Failing the whole
		// registration because an SMS gateway hiccuped would be worse than
		// telling the user to request a new code.
		logger.FromContext(ctx).Error("could not send the registration OTP",
			"user_id", user.ID, "error", err.Error())
		return &AuthResponse{
			User:                 &user,
			RequiresVerification: true,
			NextStep:             "verify_phone",
		}, nil
	}

	return &AuthResponse{
		User:                 &user,
		RequiresVerification: true,
		NextStep:             "verify_phone",
		DevOTP:               s.devOTP(code),
	}, nil
}

// VerifyPhone completes registration and issues the first token pair.
func (s *Service) VerifyPhone(ctx context.Context, in VerifyOTPRequest, meta RequestMeta) (*AuthResponse, error) {
	in.Normalize()

	if err := s.Repo.ConsumeOTP(ctx, in.Phone, domain.OTPPurposeRegister, hash.Token(in.OTP)); err != nil {
		return nil, err
	}

	var user domain.User
	err := txn.Run(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := s.Repo.MarkPhoneVerified(ctx, tx, in.Phone); err != nil {
			return err
		}
		u, err := s.Repo.FindByPhone(ctx, tx, in.Phone)
		if err != nil {
			return err
		}
		user = u
		return nil
	})
	if err != nil {
		return nil, err
	}

	tokens, err := s.startSession(ctx, user, "", meta)
	if err != nil {
		return nil, err
	}

	return &AuthResponse{User: &user, Tokens: tokens, NextStep: nextStep(user)}, nil
}

// ResendOTP issues a fresh code, subject to the cooldown and the window quota.
func (s *Service) ResendOTP(ctx context.Context, in ResendOTPRequest, meta RequestMeta) (*OTPResponse, error) {
	in.Normalize()

	user, err := s.Repo.FindByPhone(ctx, nil, in.Phone)
	var userID *string
	if err == nil {
		userID = &user.ID
	} else if !apperr.IsCode(err, apperr.CodeNotFound) {
		return nil, err
	}
	// A number that is not registered still gets a "code sent" response below.
	// Telling the caller "no such account" would turn this endpoint into a free
	// check of which phone numbers are registered.

	if userID != nil {
		code, expiresAt, err := s.issueOTP(ctx, in.Phone, in.Purpose, userID, meta)
		if err != nil {
			return nil, err
		}
		return &OTPResponse{
			Phone:        maskPhone(in.Phone),
			Purpose:      in.Purpose,
			ExpiresAt:    expiresAt,
			ResendAfter:  int(s.Cfg.OTP.ResendCooldown.Seconds()),
			AttemptsLeft: s.Cfg.OTP.MaxVerifyTries,
			DevOTP:       s.devOTP(code),
		}, nil
	}

	return &OTPResponse{
		Phone:        maskPhone(in.Phone),
		Purpose:      in.Purpose,
		ExpiresAt:    time.Now().Add(s.Cfg.OTP.TTL),
		ResendAfter:  int(s.Cfg.OTP.ResendCooldown.Seconds()),
		AttemptsLeft: s.Cfg.OTP.MaxVerifyTries,
	}, nil
}

// ---------------------------------------------------------------------------
// Login
// ---------------------------------------------------------------------------

// Login authenticates and starts a session.
func (s *Service) Login(ctx context.Context, in LoginRequest, meta RequestMeta) (*AuthResponse, error) {
	in.Normalize()

	user, err := s.Repo.FindByIdentifier(ctx, in.Identifier)
	if err != nil {
		if apperr.IsCode(err, apperr.CodeNotFound) {
			// Burn roughly the same time a real bcrypt comparison would, so an
			// attacker cannot tell "no such user" from "wrong password" by
			// timing the response. Without this, the login endpoint is a fast
			// oracle for which phone numbers have accounts.
			hash.Compare(dummyHash, in.Password)
			s.Repo.LogAttempt(ctx, in.Identifier, nil, false, "not_found", meta.IP, meta.UserAgent)
			return nil, invalidCredentials()
		}
		return nil, err
	}

	if user.Status == domain.StatusSuspended {
		s.Repo.LogAttempt(ctx, in.Identifier, &user.ID, false, "suspended", meta.IP, meta.UserAgent)
		return nil, apperr.New(403, apperr.CodeAccountSuspended,
			"This account has been suspended.").
			WithHint("Contact support if you believe this is a mistake.")
	}

	if user.IsLocked() {
		s.Repo.LogAttempt(ctx, in.Identifier, &user.ID, false, "locked", meta.IP, meta.UserAgent)
		return nil, s.lockedError(user)
	}

	if !hash.Compare(user.PasswordHash, in.Password) {
		locked, until, lockErr := s.Repo.RecordLoginFailure(ctx, user.ID,
			s.Cfg.Auth.MaxLoginAttempts, s.Cfg.Auth.LoginLockWindow)
		if lockErr != nil {
			logger.FromContext(ctx).Error("could not record the failed login", "error", lockErr.Error())
		}
		s.Repo.LogAttempt(ctx, in.Identifier, &user.ID, false, "bad_password", meta.IP, meta.UserAgent)

		if locked {
			user.LockedUntil = until
			return nil, s.lockedError(user)
		}
		remaining := s.Cfg.Auth.MaxLoginAttempts - user.FailedLoginCount - 1
		if remaining < 0 {
			remaining = 0
		}
		return nil, invalidCredentials().
			WithDetails(map[string]any{"attempts_remaining": remaining})
	}

	if s.Cfg.Auth.RequirePhoneVerify && !user.PhoneVerified {
		// Send a fresh code so the client can go straight to the OTP screen
		// rather than making the user tap "resend" first.
		code, _, otpErr := s.issueOTP(ctx, user.Phone, domain.OTPPurposeRegister, &user.ID, meta)
		if otpErr != nil {
			logger.FromContext(ctx).Warn("could not send a verification code at login",
				"error", otpErr.Error())
		}
		s.Repo.LogAttempt(ctx, in.Identifier, &user.ID, false, "unverified", meta.IP, meta.UserAgent)
		return &AuthResponse{
			User:                 &user,
			RequiresVerification: true,
			NextStep:             "verify_phone",
			DevOTP:               s.devOTP(code),
		}, nil
	}

	if in.DeviceName != nil {
		meta.DeviceName = in.DeviceName
	}
	tokens, err := s.startSession(ctx, user, "", meta)
	if err != nil {
		return nil, err
	}

	if err := s.Repo.RecordLoginSuccess(ctx, user.ID, meta.IP); err != nil {
		logger.FromContext(ctx).Error("could not stamp the successful login", "error", err.Error())
	}
	s.Repo.LogAttempt(ctx, in.Identifier, &user.ID, true, "", meta.IP, meta.UserAgent)

	// Transparently upgrade a hash made at an older bcrypt cost. The user never
	// notices, and the stored hashes keep pace with hardware.
	if hash.NeedsRehash(user.PasswordHash, s.Cfg.Auth.BcryptCost) {
		go s.rehashPassword(user.ID, in.Password)
	}

	return &AuthResponse{User: &user, Tokens: tokens, NextStep: nextStep(user)}, nil
}

// ---------------------------------------------------------------------------
// Refresh, with rotation and theft detection
// ---------------------------------------------------------------------------

// Refresh exchanges a refresh token for a new pair and retires the old one.
//
// # ROTATION WITH REUSE DETECTION
//
// Every refresh mints a new token and revokes the one presented. So a token can
// legitimately be used exactly once. If a REVOKED token is presented again, one
// of two things happened: an attacker stole it and is using it after the real
// user already refreshed, or the real user is replaying an old one after the
// attacker refreshed. We cannot tell which — so the safe move is to revoke the
// entire family and force a fresh sign-in on every device.
func (s *Service) Refresh(ctx context.Context, in RefreshRequest, meta RequestMeta) (*AuthResponse, error) {
	// There is nothing to "parse": the token is opaque, so the sessions row is
	// the single source of truth about whether it is valid.
	session, err := s.Repo.FindSessionByToken(ctx, s.Tokens.PepperRefresh(in.RefreshToken))
	if err != nil {
		if apperr.IsCode(err, apperr.CodeNotFound) {
			return nil, apperr.New(401, apperr.CodeTokenInvalid,
				"This session is no longer valid. Please sign in again.")
		}
		return nil, err
	}

	if session.RevokedAt != nil {
		// Theft, or a replay. Either way, burn the family.
		logger.FromContext(ctx).Warn("refresh token reuse detected",
			"user_id", session.UserID, "family_id", session.FamilyID, "ip", meta.IP)

		_ = txn.Run(ctx, s.Pool, func(tx pgx.Tx) error {
			_, revokeErr := s.Repo.RevokeFamily(ctx, tx, session.FamilyID, "reuse_detected")
			return revokeErr
		})
		if s.Revoker != nil {
			_ = s.Revoker.RevokeAllForUser(ctx, session.UserID)
		}

		return nil, apperr.New(401, apperr.CodeTokenInvalid,
			"For your security, all sessions have been signed out.").
			WithHint("Please sign in again. If you did not expect this, change your password.")
	}

	if session.ExpiresAt.Before(time.Now()) {
		return nil, apperr.New(401, apperr.CodeTokenExpired,
			"Your session has expired. Please sign in again.")
	}

	user, err := s.Repo.FindByID(ctx, session.UserID)
	if err != nil {
		return nil, err
	}
	if !user.IsActive() {
		return nil, apperr.New(403, apperr.CodeAccountSuspended,
			"This account is no longer active.")
	}

	// A password change after this session started invalidates it, even though
	// the refresh token itself has not expired.
	if user.PasswordChangedAt.After(session.CreatedAt) {
		return nil, apperr.New(401, apperr.CodeTokenInvalid,
			"Your password was changed. Please sign in again.")
	}

	if meta.DeviceName == nil {
		meta.DeviceName = session.DeviceName
	}
	tokens, err := s.rotateSession(ctx, user, session, meta)
	if err != nil {
		return nil, err
	}

	return &AuthResponse{User: &user, Tokens: tokens, NextStep: nextStep(user)}, nil
}

// ---------------------------------------------------------------------------
// Logout
// ---------------------------------------------------------------------------

// Logout ends the current session, or every session when AllDevices is set.
func (s *Service) Logout(ctx context.Context, userID, sessionID string, in LogoutRequest) (map[string]any, error) {
	if in.AllDevices {
		var count int64
		err := txn.Run(ctx, s.Pool, func(tx pgx.Tx) error {
			n, err := s.Repo.RevokeAllForUser(ctx, tx, userID, "logout", "")
			count = n
			return err
		})
		if err != nil {
			return nil, err
		}
		if s.Revoker != nil {
			_ = s.Revoker.RevokeAllForUser(ctx, userID)
		}
		return map[string]any{"sessions_ended": count, "all_devices": true}, nil
	}

	// Prefer the refresh token when the client sent it, because that identifies
	// the exact device. Fall back to the session id in the access token.
	target := sessionID
	if in.RefreshToken != "" {
		id, err := s.Repo.RevokeSessionByToken(ctx, s.Tokens.PepperRefresh(in.RefreshToken), "logout")
		if err != nil {
			return nil, err
		}
		if id != "" {
			target = id
		}
	} else if sessionID != "" {
		err := txn.Run(ctx, s.Pool, func(tx pgx.Tx) error {
			err := s.Repo.RevokeSession(ctx, tx, sessionID, "logout")
			if apperr.IsCode(err, apperr.CodeNotFound) {
				return nil // already logged out; not an error
			}
			return err
		})
		if err != nil {
			return nil, err
		}
	}

	if s.Revoker != nil && target != "" {
		_ = s.Revoker.RevokeSession(ctx, target)
	}
	return map[string]any{"sessions_ended": 1, "all_devices": false}, nil
}

// ListSessions returns the user's signed-in devices.
func (s *Service) ListSessions(ctx context.Context, userID, currentSessionID string) ([]SessionResponse, error) {
	sessions, err := s.Repo.ListSessions(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]SessionResponse, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, SessionResponse{
			ID:         sess.ID,
			DeviceName: sess.DeviceName,
			IP:         sess.IP,
			IsCurrent:  sess.ID == currentSessionID,
			LastUsedAt: sess.LastUsedAt,
			ExpiresAt:  sess.ExpiresAt,
			CreatedAt:  sess.CreatedAt,
		})
	}
	return out, nil
}

// RevokeSession ends one named device.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID string) error {
	sessions, err := s.Repo.ListSessions(ctx, userID)
	if err != nil {
		return err
	}
	// Verify ownership before revoking: without this check, any signed-in user
	// could sign out any other user by guessing a session id.
	owned := false
	for _, sess := range sessions {
		if sess.ID == sessionID {
			owned = true
			break
		}
	}
	if !owned {
		return apperr.NotFound("Session")
	}

	if err := txn.Run(ctx, s.Pool, func(tx pgx.Tx) error {
		return s.Repo.RevokeSession(ctx, tx, sessionID, "logout")
	}); err != nil {
		return err
	}
	if s.Revoker != nil {
		_ = s.Revoker.RevokeSession(ctx, sessionID)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Passwords
// ---------------------------------------------------------------------------

// ChangePassword updates the password of a signed-in user and signs every OTHER
// device out — the standard, expected response to a password change.
func (s *Service) ChangePassword(ctx context.Context, userID, currentSessionID string, in ChangePasswordRequest) (map[string]any, error) {
	user, err := s.Repo.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !hash.Compare(user.PasswordHash, in.CurrentPassword) {
		return nil, apperr.New(401, apperr.CodeUnauthorized,
			"Your current password is not correct.").
			WithField(apperr.FieldError{
				Field: "current_password", Rule: "incorrect",
				Message:   "Your current password is not correct.",
				MessageBN: "বর্তমান password সঠিক নয়।",
			})
	}

	newHash, err := hash.Password(in.NewPassword, s.Cfg.Auth.BcryptCost)
	if err != nil {
		return nil, apperr.Internal("").WithCause(err)
	}

	var ended int64
	err = txn.Run(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := s.Repo.UpdatePassword(ctx, tx, userID, newHash); err != nil {
			return err
		}
		n, err := s.Repo.RevokeAllForUser(ctx, tx, userID, "password_changed", currentSessionID)
		ended = n
		return err
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{"other_sessions_ended": ended}, nil
}

// ForgotPassword starts a reset by sending an OTP.
//
// It always reports success. A different response for an unregistered number
// would let anyone enumerate which phone numbers have accounts.
func (s *Service) ForgotPassword(ctx context.Context, in ForgotPasswordRequest, meta RequestMeta) (*OTPResponse, error) {
	in.Normalize()

	resp := &OTPResponse{
		Phone:        maskPhone(in.Phone),
		Purpose:      domain.OTPPurposeResetPassword,
		ExpiresAt:    time.Now().Add(s.Cfg.OTP.TTL),
		ResendAfter:  int(s.Cfg.OTP.ResendCooldown.Seconds()),
		AttemptsLeft: s.Cfg.OTP.MaxVerifyTries,
	}

	user, err := s.Repo.FindByPhone(ctx, nil, in.Phone)
	if err != nil {
		if apperr.IsCode(err, apperr.CodeNotFound) {
			return resp, nil
		}
		return nil, err
	}

	code, expiresAt, err := s.issueOTP(ctx, in.Phone, domain.OTPPurposeResetPassword, &user.ID, meta)
	if err != nil {
		// Rate-limit errors are real and must surface; anything else stays quiet.
		if apperr.IsCode(err, apperr.CodeOTPLimit) {
			return nil, err
		}
		logger.FromContext(ctx).Error("could not send the reset OTP", "error", err.Error())
		return resp, nil
	}

	resp.ExpiresAt = expiresAt
	resp.DevOTP = s.devOTP(code)
	return resp, nil
}

// ResetPassword completes a reset and signs every device out.
func (s *Service) ResetPassword(ctx context.Context, in ResetPasswordRequest, meta RequestMeta) (map[string]any, error) {
	in.Normalize()

	if err := s.Repo.ConsumeOTP(ctx, in.Phone, domain.OTPPurposeResetPassword, hash.Token(in.OTP)); err != nil {
		return nil, err
	}

	user, err := s.Repo.FindByPhone(ctx, nil, in.Phone)
	if err != nil {
		return nil, err
	}

	newHash, err := hash.Password(in.NewPassword, s.Cfg.Auth.BcryptCost)
	if err != nil {
		return nil, apperr.Internal("").WithCause(err)
	}

	var ended int64
	err = txn.Run(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := s.Repo.UpdatePassword(ctx, tx, user.ID, newHash); err != nil {
			return err
		}
		// A reset means the account may have been compromised. Every existing
		// session goes, with no exception for a "current" one.
		n, err := s.Repo.RevokeAllForUser(ctx, tx, user.ID, "password_changed", "")
		ended = n
		return err
	})
	if err != nil {
		return nil, err
	}

	if s.Revoker != nil {
		_ = s.Revoker.RevokeAllForUser(ctx, user.ID)
	}
	s.Repo.LogAttempt(ctx, in.Phone, &user.ID, true, "password_reset", meta.IP, meta.UserAgent)

	return map[string]any{"sessions_ended": ended}, nil
}

// ---------------------------------------------------------------------------
// internals
// ---------------------------------------------------------------------------

// startSession creates a session row and issues the token pair.
func (s *Service) startSession(ctx context.Context, user domain.User, familyID string, meta RequestMeta) (*jwt.TokenPair, error) {
	refreshToken, err := hash.RandomToken(32)
	if err != nil {
		return nil, apperr.Internal("").WithCause(err)
	}

	var session domain.Session
	err = txn.Run(ctx, s.Pool, func(tx pgx.Tx) error {
		created, err := s.Repo.CreateSession(ctx, tx,
			user.ID, familyID, s.Tokens.PepperRefresh(refreshToken),
			meta.DeviceName, nullIfEmpty(meta.UserAgent), nullIfEmpty(meta.IP),
			time.Now().Add(s.Cfg.Auth.RefreshTokenTTL))
		if err != nil {
			return err
		}
		session = created

		// Enforce the plan's device limit, oldest first.
		maxDevices := s.Cfg.Auth.MaxSessionsPerUser
		if s.Provisioner != nil {
			if n := s.Provisioner.MaxDevices(ctx, user.ID); n > 0 {
				maxDevices = n
			}
		}
		return s.Repo.TrimSessions(ctx, tx, user.ID, maxDevices)
	})
	if err != nil {
		return nil, err
	}

	// The access token is signed; the refresh token is the opaque random string
	// whose HMAC was just stored on the session row.
	pair, err := s.Tokens.Issue(jwt.Identity{
		UserID:    user.ID,
		Role:      user.Role,
		Plan:      user.PlanCode,
		SessionID: session.ID,
	}, refreshToken)
	if err != nil {
		return nil, apperr.Internal("").WithCause(err)
	}
	return &pair, nil
}

// rotateSession retires the presented refresh token and issues a new one in the
// same family.
func (s *Service) rotateSession(ctx context.Context, user domain.User, old domain.Session, meta RequestMeta) (*jwt.TokenPair, error) {
	newToken, err := hash.RandomToken(32)
	if err != nil {
		return nil, apperr.Internal("").WithCause(err)
	}

	var session domain.Session
	err = txn.Run(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := s.Repo.RevokeSession(ctx, tx, old.ID, "rotated"); err != nil {
			// Somebody else rotated it a millisecond ago: treat as reuse.
			if apperr.IsCode(err, apperr.CodeNotFound) {
				return apperr.New(401, apperr.CodeTokenInvalid,
					"This session was already refreshed. Please sign in again.")
			}
			return err
		}
		created, err := s.Repo.CreateSession(ctx, tx,
			user.ID, old.FamilyID, s.Tokens.PepperRefresh(newToken),
			meta.DeviceName, nullIfEmpty(meta.UserAgent), nullIfEmpty(meta.IP),
			time.Now().Add(s.Cfg.Auth.RefreshTokenTTL))
		if err != nil {
			return err
		}
		session = created
		return s.Repo.TouchSession(ctx, tx, created.ID)
	})
	if err != nil {
		return nil, err
	}

	pair, err := s.Tokens.Issue(jwt.Identity{
		UserID:    user.ID,
		Role:      user.Role,
		Plan:      user.PlanCode,
		SessionID: session.ID,
	}, newToken)
	if err != nil {
		return nil, apperr.Internal("").WithCause(err)
	}
	return &pair, nil
}

// issueOTP generates, stores and sends a verification code.
func (s *Service) issueOTP(ctx context.Context, phone, purpose string, userID *string, meta RequestMeta) (string, time.Time, error) {
	// Cooldown: stops a "resend" button from becoming an SMS bill.
	if last, err := s.Repo.LastOTPSentAt(ctx, phone, purpose); err == nil && last != nil {
		if wait := s.Cfg.OTP.ResendCooldown - time.Since(*last); wait > 0 {
			return "", time.Time{}, apperr.New(429, apperr.CodeOTPLimit,
				"Please wait before requesting another code.").
				WithDetails(map[string]any{"retry_after_seconds": int(wait.Seconds()) + 1}).
				WithHint("A code was sent recently. Check your SMS inbox.")
		}
	}

	count, err := s.Repo.CountRecentOTPs(ctx, phone, purpose, s.Cfg.OTP.Window)
	if err != nil {
		return "", time.Time{}, err
	}
	if count >= s.Cfg.OTP.MaxPerWindow {
		return "", time.Time{}, apperr.New(429, apperr.CodeOTPLimit,
			"Too many verification codes have been requested for this number.").
			WithDetails(map[string]any{
				"window_hours": int(s.Cfg.OTP.Window.Hours()),
				"max_requests": s.Cfg.OTP.MaxPerWindow,
			}).
			WithHint("Try again later, or contact support if you cannot receive the SMS.")
	}

	code, err := hash.NumericCode(s.Cfg.OTP.Length)
	if err != nil {
		return "", time.Time{}, apperr.Internal("").WithCause(err)
	}

	expiresAt := time.Now().Add(s.Cfg.OTP.TTL)
	if err := s.Repo.SaveOTP(ctx, phone, purpose, hash.Token(code), userID,
		expiresAt, s.Cfg.OTP.MaxVerifyTries, meta.IP); err != nil {
		return "", time.Time{}, err
	}

	locale := meta.Locale
	if locale == "" {
		locale = s.Cfg.App.Locale
	}
	body := sms.OTPMessage(code, s.Cfg.App.Name, locale, s.Cfg.OTP.TTL)
	if err := s.SMS.Send(ctx, phone, body); err != nil {
		return code, expiresAt, err
	}
	return code, expiresAt, nil
}

// devOTP returns the code only outside production, so a developer can finish a
// signup without an SMS gateway. In production it always returns "".
func (s *Service) devOTP(code string) string {
	if s.Cfg.App.IsProduction() || !s.Cfg.OTP.ExposeInDevMode {
		return ""
	}
	return code
}

func (s *Service) lockedError(user domain.User) *apperr.Error {
	minutes := int(user.LockRemaining().Minutes()) + 1
	return apperr.New(423, apperr.CodeAccountLocked,
		"This account is temporarily locked after too many failed sign-in attempts.").
		WithDetails(map[string]any{
			"locked_until":      user.LockedUntil,
			"minutes_remaining": minutes,
		}).
		WithHint("Wait for the lock to expire, or reset your password to unlock it immediately.")
}

// rehashPassword upgrades a stored hash in the background.
func (s *Service) rehashPassword(userID, plain string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	newHash, err := hash.Password(plain, s.Cfg.Auth.BcryptCost)
	if err != nil {
		return
	}
	if _, err := s.Pool.Exec(ctx,
		`UPDATE users SET password_hash = $2 WHERE id = $1`, userID, newHash); err != nil {
		logger.Named("auth").Warn("password rehash failed", "user_id", userID, "error", err.Error())
	}
}

// invalidCredentials is deliberately identical for a wrong password and an
// unknown account.
func invalidCredentials() *apperr.Error {
	return apperr.New(401, apperr.CodeUnauthorized,
		"The phone number, email or password is not correct.").
		WithHint("Check your details, or use Forgot Password to reset it.")
}

// nextStep tells the client which screen to show after authenticating.
func nextStep(u domain.User) string {
	if u.NeedsOnboarding() {
		return "onboarding:" + u.OnboardingStep
	}
	return "dashboard"
}

func maskPhone(phone string) string {
	if len(phone) < 7 {
		return "***"
	}
	return phone[:3] + "*****" + phone[len(phone)-3:]
}

// dummyHash is a real bcrypt hash of a random string, used to spend the same
// CPU time on an unknown account as on a real one. Cost 12 to match production.
const dummyHash = "$2a$12$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
