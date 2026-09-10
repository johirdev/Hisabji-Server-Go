// Package billing owns subscriptions, credits (tokens), the feature catalogue
// and payments.
//
// # THE ONE RULE THAT MAKES THIS SAFE
//
// Nothing outside this package ever writes credit_wallets or credit_ledger.
// Every balance change goes through Grant or Spend, which run inside a
// SERIALIZABLE transaction that locks the wallet row and writes the ledger in
// the same commit. That is why the balance and its history can never disagree,
// and why two concurrent AI requests cannot spend the same credit twice.
package billing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/johirdev/Hisabji-Server/internal/config"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/logger"
	"github.com/johirdev/Hisabji-Server/internal/core/repository"
	"github.com/johirdev/Hisabji-Server/internal/core/txn"
	"github.com/johirdev/Hisabji-Server/internal/domain"
)

// FreePlanCode is the plan every account starts on.
const FreePlanCode = "free"

// Service holds the billing business rules.
type Service struct {
	Repo *Repository
	Pool *pgxpool.Pool
	Cfg  config.Config
}

// NewService builds the service.
func NewService(repo *Repository, pool *pgxpool.Pool, cfg config.Config) *Service {
	return &Service{Repo: repo, Pool: pool, Cfg: cfg}
}

// ---------------------------------------------------------------------------
// Provisioning (called by the auth module on registration)
// ---------------------------------------------------------------------------

// ProvisionNewUser creates the wallet and grants the free signup credits.
//
// It runs inside the caller's registration transaction — no new transaction is
// started here — so a user is never created without a wallet.
func (s *Service) ProvisionNewUser(ctx context.Context, tx repository.DB, userID string) error {
	if _, err := s.Repo.EnsureWallet(ctx, tx, userID, s.Cfg.AI.MonthlyCostCap); err != nil {
		return err
	}

	free, err := s.Repo.GetPlan(ctx, tx, FreePlanCode)
	if err != nil {
		// A missing free plan means the seed migration did not run. Fail loudly:
		// silently giving zero credits would look like a product bug forever.
		return apperr.Internal("Billing is not configured correctly.").
			WithCause(fmt.Errorf("the %q plan is missing from subscription_plans: %w", FreePlanCode, err))
	}

	credits := free.SignupCredits
	if credits <= 0 {
		credits = s.Cfg.AI.FreeQuota
	}
	if credits <= 0 {
		return nil
	}

	desc := fmt.Sprintf("Welcome bonus: %d free AI credits", credits)
	_, err = s.Repo.ApplyWallet(ctx, tx, LedgerWrite{
		UserID: userID,
		// Signup credits go into the PURCHASED bucket, not the allowance,
		// because they must not be wiped by the first monthly refill. They are
		// the user's three free tries, and they keep them until they use them.
		AllowanceAfter: 0,
		PurchasedAfter: credits,
		Delta:          credits,
		PurchasedDelta: credits,
		Reason:         domain.CreditSignupBonus,
		Description:    &desc,
	})
	return err
}

// MaxDevices reports the concurrent-session limit for the user's plan.
func (s *Service) MaxDevices(ctx context.Context, userID string) int {
	ent, err := s.Entitlement(ctx, userID)
	if err != nil {
		return s.Cfg.Auth.MaxSessionsPerUser
	}
	plan, err := s.Repo.GetPlan(ctx, nil, ent.PlanCode)
	if err != nil || plan.MaxDevices <= 0 {
		return s.Cfg.Auth.MaxSessionsPerUser
	}
	return plan.MaxDevices
}

// ---------------------------------------------------------------------------
// Entitlement
// ---------------------------------------------------------------------------

// Entitlement answers "what can this user do, and what can they afford?".
//
// It self-heals a missing wallet, which means it may open its own transaction.
// Never call it while holding a lock — use entitlementOn(tx, ...) there.
func (s *Service) Entitlement(ctx context.Context, userID string) (domain.Entitlement, error) {
	ent, err := s.entitlementOn(ctx, nil, userID)
	if err == nil || !IsNotFound(err) {
		return ent, err
	}

	// The wallet row is missing: an account created before provisioning
	// existed, or a half-failed registration. Create it and retry once.
	if err := txn.Run(ctx, s.Pool, func(tx pgx.Tx) error {
		return s.ProvisionNewUser(ctx, tx, userID)
	}); err != nil {
		return ent, err
	}
	return s.entitlementOn(ctx, nil, userID)
}

// entitlementOn resolves the entitlement against a specific handle. Passing a
// transaction makes it safe to call while that transaction holds the wallet
// lock: it starts no transaction of its own, so it cannot deadlock against the
// caller.
func (s *Service) entitlementOn(ctx context.Context, db repository.DB, userID string) (domain.Entitlement, error) {
	ent := domain.Entitlement{
		UserID:             userID,
		PlanCode:           FreePlanCode,
		Tier:               domain.TierFree,
		SubscriptionStatus: "none",
	}

	sub, err := s.Repo.LiveSubscription(ctx, db, userID)
	switch {
	case err == nil:
		ent.PlanCode = sub.PlanCode
		ent.SubscriptionStatus = sub.Status
		ent.ExpiresAt = &sub.EndsAt
		ent.DaysRemaining = daysUntil(sub.EndsAt)
	case IsNotFound(err):
		// Free plan: no subscription row, which is the normal case.
	default:
		return ent, err
	}

	plan, err := s.Repo.GetPlan(ctx, db, ent.PlanCode)
	if err != nil {
		if !IsNotFound(err) {
			return ent, err
		}
		// The plan was deleted from the catalogue while a user still held it.
		// Fall back to free rather than failing every request they make.
		logger.FromContext(ctx).Warn("user holds an unknown plan; falling back to free",
			"user_id", userID, "plan_code", ent.PlanCode)
		plan, err = s.Repo.GetPlan(ctx, db, FreePlanCode)
		if err != nil {
			return ent, err
		}
		ent.PlanCode = FreePlanCode
	}
	ent.Tier = plan.Tier
	ent.Features = plan.FeatureCodes

	// A missing wallet surfaces as NOT_FOUND for the caller to heal; healing it
	// here would mean starting a transaction, which is unsafe under a lock.
	wallet, err := s.Repo.GetWallet(ctx, db, userID)
	if err != nil {
		return ent, err
	}

	ent.AllowanceCredits = wallet.AllowanceCredits
	ent.PurchasedCredits = wallet.PurchasedCredits
	ent.TotalCredits = wallet.Total()
	return ent, nil
}

// Access is the decision about one feature for one user.
type Access struct {
	Feature      domain.Feature     `json:"feature"`
	Allowed      bool               `json:"allowed"`
	Reason       string             `json:"reason"`
	CreditCost   int                `json:"credit_cost"`
	Entitlement  domain.Entitlement `json:"entitlement"`
	NeedsUpgrade bool               `json:"needs_upgrade"`
	NeedsCredits bool               `json:"needs_credits"`
}

// CheckAccess decides whether a user may run a feature, WITHOUT spending
// anything. Handlers use it to render the right button; Spend re-checks
// everything before it debits, so a stale check can never grant free usage.
//
// The decision order is the whole hybrid model in one function:
//
//  1. inactive feature                     -> no
//  2. included in the plan                 -> yes (still costs credits if metered)
//  3. not included but pay-as-you-go       -> yes, if they can afford it
//  4. otherwise                            -> upgrade required
func (s *Service) CheckAccess(ctx context.Context, userID, featureCode string) (Access, error) {
	feature, err := s.Repo.GetFeature(ctx, nil, featureCode)
	if err != nil {
		return Access{}, err
	}
	ent, err := s.Entitlement(ctx, userID)
	if err != nil {
		return Access{}, err
	}

	access := Access{
		Feature:     feature,
		CreditCost:  feature.CreditCost,
		Entitlement: ent,
	}

	if !feature.IsActive {
		access.Reason = "This feature is not available right now."
		return access, nil
	}

	included := ent.Has(feature.Code)

	if !included && !feature.PAYGAllowed {
		access.Reason = fmt.Sprintf("%s is part of the %s plan.", feature.Name, titleCase(feature.MinTier))
		access.NeedsUpgrade = true
		return access, nil
	}

	if feature.CreditCost > 0 && ent.TotalCredits < feature.CreditCost {
		access.Reason = fmt.Sprintf(
			"This uses %d credits and you have %d.", feature.CreditCost, ent.TotalCredits)
		access.NeedsCredits = true
		access.NeedsUpgrade = !included
		return access, nil
	}

	access.Allowed = true
	if included {
		access.Reason = "Included in your plan."
	} else {
		access.Reason = fmt.Sprintf("Pay as you go: %d credits.", feature.CreditCost)
	}
	return access, nil
}

// ---------------------------------------------------------------------------
// Spending credits
// ---------------------------------------------------------------------------

// SpendResult is what a successful debit returns.
type SpendResult struct {
	Spent            int    `json:"credits_spent"`
	AllowanceCredits int    `json:"allowance_credits"`
	PurchasedCredits int    `json:"purchased_credits"`
	TotalCredits     int    `json:"total_credits"`
	FeatureCode      string `json:"feature_code"`
}

// Spend debits the cost of a feature and records it in the ledger.
//
// Call this AFTER the work succeeds where the work is cheap to redo, and
// BEFORE where it is not — for a paid model call, spend first, and refund with
// Refund() if the provider fails. Charging for an answer the user never got is
// the worse failure of the two.
func (s *Service) Spend(ctx context.Context, userID, featureCode string, referenceType, referenceID *string) (SpendResult, error) {
	var out SpendResult

	err := txn.RunSerializable(ctx, s.Pool, func(tx pgx.Tx) error {
		feature, err := s.Repo.GetFeature(ctx, tx, featureCode)
		if err != nil {
			return err
		}
		if !feature.IsActive {
			return apperr.New(403, apperr.CodeForbidden,
				"This feature is not available right now.")
		}

		// Re-check entitlement inside the transaction. The plan may have expired
		// between the client's CheckAccess call and this request.
		ent, err := s.entitlementOn(ctx, tx, userID)
		if err != nil && !IsNotFound(err) {
			return err
		}
		included := ent.Has(feature.Code)
		if !included && !feature.PAYGAllowed {
			return apperr.PaymentRequired(apperr.CodeSubscriptionRequired,
				fmt.Sprintf("%s is part of the %s plan.", feature.Name, titleCase(feature.MinTier))).
				WithDetails(map[string]any{
					"feature":       feature.Code,
					"required_tier": feature.MinTier,
					"current_plan":  ent.PlanCode,
				}).
				WithHint("Upgrade your plan to unlock this feature.")
		}

		cost := feature.CreditCost
		out.FeatureCode = feature.Code
		out.Spent = cost

		wallet, err := s.Repo.LockWallet(ctx, tx, userID)
		if err != nil {
			if IsNotFound(err) {
				if err := s.ProvisionNewUser(ctx, tx, userID); err != nil {
					return err
				}
				wallet, err = s.Repo.LockWallet(ctx, tx, userID)
				if err != nil {
					return err
				}
			} else {
				return err
			}
		}

		if cost == 0 {
			out.AllowanceCredits = wallet.AllowanceCredits
			out.PurchasedCredits = wallet.PurchasedCredits
			out.TotalCredits = wallet.Total()
			return nil
		}

		if wallet.Total() < cost {
			return apperr.PaymentRequired(apperr.CodeInsufficientCredit,
				fmt.Sprintf("This uses %d credits and you have %d.", cost, wallet.Total())).
				WithDetails(map[string]any{
					"required":  cost,
					"available": wallet.Total(),
					"shortfall": cost - wallet.Total(),
					"feature":   feature.Code,
				}).
				WithHint("Buy a credit pack, or upgrade to a plan that includes a monthly allowance.")
		}

		// The safety cap is not about the user's balance; it is about a runaway
		// loop or a compromised account draining a large purchased balance in
		// minutes. Refuse and let a human look.
		if wallet.MonthSpent+cost > wallet.MonthlySpendCap {
			return apperr.PaymentRequired(apperr.CodeQuotaExceeded,
				"You have reached this month's usage limit for AI features.").
				WithDetails(map[string]any{
					"month_spent": wallet.MonthSpent,
					"monthly_cap": wallet.MonthlySpendCap,
				}).
				WithHint("Contact support if you need a higher limit.")
		}

		// Spend the allowance first so purchased credits, which never expire,
		// survive as long as possible. That is what the user would choose.
		fromAllowance := cost
		if fromAllowance > wallet.AllowanceCredits {
			fromAllowance = wallet.AllowanceCredits
		}
		fromPurchased := cost - fromAllowance

		desc := fmt.Sprintf("%s (%d credits)", feature.Name, cost)
		updated, err := s.Repo.ApplyWallet(ctx, tx, LedgerWrite{
			UserID:         userID,
			AllowanceAfter: wallet.AllowanceCredits - fromAllowance,
			PurchasedAfter: wallet.PurchasedCredits - fromPurchased,
			Delta:          -cost,
			UsedDelta:      cost,
			Reason:         domain.CreditFeatureUse,
			FeatureCode:    &feature.Code,
			ReferenceType:  referenceType,
			ReferenceID:    referenceID,
			Description:    &desc,
		})
		if err != nil {
			return err
		}

		out.AllowanceCredits = updated.AllowanceCredits
		out.PurchasedCredits = updated.PurchasedCredits
		out.TotalCredits = updated.Total()
		return nil
	})

	return out, err
}

// Refund returns credits after a feature failed to deliver. The ledger keeps
// both the charge and the refund, so the history reads as what actually
// happened rather than as if the charge never occurred.
func (s *Service) Refund(ctx context.Context, userID, featureCode string, credits int, reason string, referenceType, referenceID *string) error {
	if credits <= 0 {
		return nil
	}
	return txn.RunSerializable(ctx, s.Pool, func(tx pgx.Tx) error {
		wallet, err := s.Repo.LockWallet(ctx, tx, userID)
		if err != nil {
			return err
		}
		// Refund into the purchased bucket: it does not expire, so the user is
		// never worse off than before the failed call.
		desc := reason
		if desc == "" {
			desc = "Refund for a feature that did not complete"
		}
		_, err = s.Repo.ApplyWallet(ctx, tx, LedgerWrite{
			UserID:         userID,
			AllowanceAfter: wallet.AllowanceCredits,
			PurchasedAfter: wallet.PurchasedCredits + credits,
			Delta:          credits,
			PurchasedDelta: credits,
			Reason:         domain.CreditRefund,
			FeatureCode:    &featureCode,
			ReferenceType:  referenceType,
			ReferenceID:    referenceID,
			Description:    &desc,
		})
		return err
	})
}

// Grant adds credits from a plan refill, a pack purchase or an admin action.
func (s *Service) Grant(ctx context.Context, tx repository.DB, userID string, allowance, purchased int, reason, description string, referenceType, referenceID *string) error {
	if allowance == 0 && purchased == 0 {
		return nil
	}

	wallet, err := s.Repo.LockWallet(ctx, tx, userID)
	if err != nil {
		if !IsNotFound(err) {
			return err
		}
		if err := s.ProvisionNewUser(ctx, tx, userID); err != nil {
			return err
		}
		wallet, err = s.Repo.LockWallet(ctx, tx, userID)
		if err != nil {
			return err
		}
	}

	newAllowance := wallet.AllowanceCredits + allowance
	if allowance > 0 && !s.allowanceRollsOver(ctx, tx, userID) {
		// An allowance is a monthly permission, not an asset. Replacing rather
		// than adding is what stops a dormant user from banking a year of
		// unused allowance and then costing us a year of model calls at once.
		newAllowance = allowance
	}

	var resetAt *time.Time
	if allowance > 0 {
		t := time.Now().AddDate(0, 1, 0)
		resetAt = &t
	}

	_, err = s.Repo.ApplyWallet(ctx, tx, LedgerWrite{
		UserID:           userID,
		AllowanceAfter:   newAllowance,
		PurchasedAfter:   wallet.PurchasedCredits + purchased,
		Delta:            allowance + purchased,
		GrantedDelta:     allowance,
		PurchasedDelta:   purchased,
		AllowanceResetAt: resetAt,
		Reason:           reason,
		ReferenceType:    referenceType,
		ReferenceID:      referenceID,
		Description:      &description,
	})
	return err
}

func (s *Service) allowanceRollsOver(ctx context.Context, tx repository.DB, userID string) bool {
	sub, err := s.Repo.LiveSubscription(ctx, tx, userID)
	if err != nil {
		return false
	}
	plan, err := s.Repo.GetPlan(ctx, tx, sub.PlanCode)
	if err != nil {
		return false
	}
	return plan.AllowanceRollsOver
}

func daysUntil(t time.Time) int {
	d := int(time.Until(t).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

// titleCase capitalises a plan tier for display: "pro" -> "Pro".
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
