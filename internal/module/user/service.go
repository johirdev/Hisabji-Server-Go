package user

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/johirdev/Hisabji-Server/internal/config"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/logger"
	"github.com/johirdev/Hisabji-Server/internal/core/repository"
	"github.com/johirdev/Hisabji-Server/internal/core/txn"
	"github.com/johirdev/Hisabji-Server/internal/domain"
	"github.com/johirdev/Hisabji-Server/internal/shared/hash"
)

// Entitlements is the slice of billing this module needs, declared here so the
// user package does not import billing.
type Entitlements interface {
	Entitlement(ctx context.Context, userID string) (domain.Entitlement, error)
}

// SessionRevoker signs a deleted account out everywhere.
type SessionRevoker interface {
	RevokeAllForUser(ctx context.Context, tx repository.DB, userID, reason string, exceptSessionID string) (int64, error)
}

// TokenRevoker pushes the revocation into the fast denylist.
type TokenRevoker interface {
	RevokeAllForUser(ctx context.Context, userID string) error
}

// Service holds the user business rules.
type Service struct {
	Repo     *Repository
	Pool     *pgxpool.Pool
	Billing  Entitlements
	Sessions SessionRevoker
	Tokens   TokenRevoker
	Cfg      config.Config
}

// NewService builds the service.
func NewService(repo *Repository, pool *pgxpool.Pool, billing Entitlements, sessions SessionRevoker, tokens TokenRevoker, cfg config.Config) *Service {
	return &Service{Repo: repo, Pool: pool, Billing: billing, Sessions: sessions, Tokens: tokens, Cfg: cfg}
}

// reservedUsernames are names that must not belong to a person, because they
// would let one impersonate the product or collide with a future route.
var reservedUsernames = map[string]bool{
	"admin": true, "administrator": true, "root": true, "system": true,
	"support": true, "help": true, "hisabji": true, "official": true,
	"api": true, "www": true, "app": true, "billing": true, "payment": true,
	"security": true, "team": true, "staff": true, "moderator": true,
	"me": true, "user": true, "users": true, "account": true, "settings": true,
	"null": true, "undefined": true, "anonymous": true, "test": true,
}

// Profile returns the account plus the context the home screen needs.
func (s *Service) Profile(ctx context.Context, userID string, withStats bool) (*ProfileResponse, error) {
	user, err := s.Repo.Get(ctx, userID)
	if err != nil {
		return nil, err
	}

	out := &ProfileResponse{
		User:               &user,
		OnboardingStep:     user.OnboardingStep,
		OnboardingComplete: !user.NeedsOnboarding(),
	}

	if s.Billing != nil {
		if ent, err := s.Billing.Entitlement(ctx, userID); err == nil {
			out.Entitlement = &ent
		} else {
			// A billing hiccup must not make the profile screen fail; the app
			// can render without the credit badge.
			logger.FromContext(ctx).Warn("could not load the entitlement for the profile",
				"user_id", userID, "error", err.Error())
		}
	}

	if withStats {
		if stats, err := s.Repo.Stats(ctx, userID); err == nil {
			out.Stats = &stats
		} else {
			logger.FromContext(ctx).Warn("could not load profile stats",
				"user_id", userID, "error", err.Error())
		}
	}

	go s.Repo.TouchLastSeen(context.WithoutCancel(ctx), userID)

	return out, nil
}

// UpdateProfile applies a partial profile change.
func (s *Service) UpdateProfile(ctx context.Context, userID string, in UpdateProfileRequest) (domain.User, error) {
	in.Normalize()
	if in.IsEmpty() {
		return domain.User{}, apperr.BadRequest("No changes were provided.").
			WithHint("Send at least one field, such as name or monthly_income.")
	}
	return s.Repo.Update(ctx, userID, in)
}

// UpdatePreferences applies a partial preferences change.
func (s *Service) UpdatePreferences(ctx context.Context, userID string, in UpdatePreferencesRequest) (domain.User, error) {
	in.Normalize()
	if in.IsEmpty() {
		return domain.User{}, apperr.BadRequest("No changes were provided.")
	}
	if in.Currency != nil && *in.Currency != "BDT" {
		// One currency for now. Saying so plainly beats accepting the value and
		// then rendering every amount with the wrong symbol.
		return domain.User{}, apperr.InvalidField("currency",
			"Only BDT is supported at the moment.")
	}
	return s.Repo.UpdatePreferences(ctx, userID, in)
}

// SetUsername claims a username after checking availability and reserved names.
func (s *Service) SetUsername(ctx context.Context, userID string, in UsernameRequest) (domain.User, error) {
	in.Normalize()

	if reservedUsernames[in.Username] {
		return domain.User{}, apperr.Duplicate("username",
			"That username is reserved and cannot be used.")
	}

	taken, err := s.Repo.UsernameTaken(ctx, in.Username, userID)
	if err != nil {
		return domain.User{}, err
	}
	if taken {
		return domain.User{}, apperr.Duplicate("username",
			"That username is already taken.").
			WithDetails(map[string]any{"suggestions": s.suggest(ctx, in.Username)})
	}

	return s.Repo.SetUsername(ctx, userID, in.Username)
}

// CheckUsername answers the availability check the signup form makes as the
// user types.
func (s *Service) CheckUsername(ctx context.Context, userID, username string) (UsernameAvailability, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	out := UsernameAvailability{Username: username}

	switch {
	case len(username) < 3:
		out.Reason = "Username must be at least 3 characters."
		return out, nil
	case len(username) > 30:
		out.Reason = "Username must be 30 characters or fewer."
		return out, nil
	case reservedUsernames[username]:
		out.Reason = "That username is reserved."
		out.Suggestions = s.suggest(ctx, username)
		return out, nil
	}

	taken, err := s.Repo.UsernameTaken(ctx, username, userID)
	if err != nil {
		return out, err
	}
	if taken {
		out.Reason = "That username is already taken."
		out.Suggestions = s.suggest(ctx, username)
		return out, nil
	}

	out.Available = true
	return out, nil
}

// suggest offers alternatives when a username is unavailable, checking each so
// the user is never handed a suggestion that is also taken.
func (s *Service) suggest(ctx context.Context, base string) []string {
	if len(base) > 24 {
		base = base[:24]
	}
	candidates := []string{
		base + "1", base + "01", base + "_bd", base + "247", "the" + base,
	}

	out := make([]string, 0, 3)
	for _, c := range candidates {
		if len(out) == 3 {
			break
		}
		if reservedUsernames[c] {
			continue
		}
		if taken, err := s.Repo.UsernameTaken(ctx, c, ""); err == nil && !taken {
			out = append(out, c)
		}
	}
	return out
}

// Onboarding saves wizard progress.
func (s *Service) Onboarding(ctx context.Context, userID string, in OnboardingRequest) (*ProfileResponse, error) {
	if _, err := s.Repo.AdvanceOnboarding(ctx, userID, in); err != nil {
		return nil, err
	}
	return s.Profile(ctx, userID, false)
}

// DeleteAccount soft-deletes the account and signs every device out.
func (s *Service) DeleteAccount(ctx context.Context, userID string, in DeleteAccountRequest) (map[string]any, error) {
	user, err := s.Repo.Get(ctx, userID)
	if err != nil {
		return nil, err
	}

	if !hash.Compare(user.PasswordHash, in.Password) {
		return nil, apperr.Unauthorized("Your password is not correct.").
			WithField(apperr.FieldError{
				Field: "password", Rule: "incorrect",
				Message:   "Your password is not correct.",
				MessageBN: "Password সঠিক নয়।",
			})
	}

	reason := ""
	if in.Reason != nil {
		reason = *in.Reason
	}

	err = txn.Run(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := s.Repo.SoftDelete(ctx, tx, userID, reason); err != nil {
			return err
		}
		if s.Sessions != nil {
			if _, err := s.Sessions.RevokeAllForUser(ctx, tx, userID, "admin", ""); err != nil {
				return err
			}
		}
		if reason != "" {
			if _, err := tx.Exec(ctx, `
				INSERT INTO feedback (user_id, kind, message)
				VALUES ($1, 'cancel_reason', $2)`, userID, reason); err != nil {
				return apperr.FromPostgres(err, "Feedback")
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if s.Tokens != nil {
		_ = s.Tokens.RevokeAllForUser(ctx, userID)
	}

	logger.FromContext(ctx).Info("account deleted", "user_id", userID)

	return map[string]any{
		"deleted":        true,
		"purge_after":    "30 days",
		"can_reregister": true,
		"message": fmt.Sprintf(
			"Your account has been closed. Your data is permanently removed after 30 days, and %s can be used to register again immediately.",
			maskPhone(user.Phone)),
	}, nil
}

func maskPhone(phone string) string {
	if len(phone) < 7 {
		return "your phone number"
	}
	return phone[:3] + "*****" + phone[len(phone)-3:]
}
