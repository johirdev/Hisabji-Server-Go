package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/logger"
	"github.com/johirdev/Hisabji-Server/internal/core/money"
	"github.com/johirdev/Hisabji-Server/internal/core/repository"
	"github.com/johirdev/Hisabji-Server/internal/core/txn"
	"github.com/johirdev/Hisabji-Server/internal/domain"
)

// CheckoutResult is what the client needs to complete a purchase.
type CheckoutResult struct {
	Payment domain.Payment `json:"payment"`

	// PaymentURL is where to send the user for a hosted gateway. Empty for the
	// manual provider, where an operator confirms the payment instead.
	PaymentURL string `json:"payment_url,omitempty"`

	// Instructions explains what to do next, in plain language.
	Instructions string `json:"instructions"`

	// AlreadyProcessed is true when an idempotency key replayed an earlier
	// request, so the client can skip showing a second payment screen.
	AlreadyProcessed bool `json:"already_processed"`
}

// StartSubscriptionCheckout creates a pending payment for a plan.
func (s *Service) StartSubscriptionCheckout(ctx context.Context, userID, planCode string, idempotencyKey *string) (*CheckoutResult, error) {
	plan, err := s.Repo.GetPlan(ctx, nil, planCode)
	if err != nil {
		return nil, err
	}
	if !plan.IsActive {
		return nil, apperr.Conflict("That plan is no longer available.")
	}
	if plan.Price <= 0 {
		return nil, apperr.BadRequest("The free plan does not need to be purchased.").
			WithHint("Choose a paid plan, or simply keep using the free one.")
	}

	// Block a second purchase while one is still running. Without this, a user
	// who taps twice ends up paying for two overlapping subscriptions, and the
	// unique index on live subscriptions would fail the second activation
	// AFTER their money had already moved.
	if current, err := s.Repo.LiveSubscription(ctx, nil, userID); err == nil {
		if current.PlanCode == planCode {
			return nil, apperr.Conflict("You are already subscribed to this plan.").
				WithDetails(map[string]any{
					"current_plan": current.PlanCode,
					"expires_at":   current.EndsAt,
				}).
				WithHint("Your plan renews or can be changed after it ends.")
		}
		return nil, apperr.Conflict("You already have an active subscription.").
			WithDetails(map[string]any{
				"current_plan": current.PlanCode,
				"expires_at":   current.EndsAt,
			}).
			WithHint("Cancel the current plan first, or wait until it ends.")
	} else if !IsNotFound(err) {
		return nil, err
	}

	return s.startCheckout(ctx, userID, "subscription", plan.Code, plan.Price, plan.Currency,
		fmt.Sprintf("%s — %s", plan.Name, plan.Price.String()), idempotencyKey)
}

// StartPackCheckout creates a pending payment for a credit pack.
func (s *Service) StartPackCheckout(ctx context.Context, userID, packCode string, idempotencyKey *string) (*CheckoutResult, error) {
	pack, err := s.Repo.GetPack(ctx, nil, packCode)
	if err != nil {
		return nil, err
	}
	if !pack.IsActive {
		return nil, apperr.Conflict("That credit pack is no longer available.")
	}
	return s.startCheckout(ctx, userID, "credit_pack", pack.Code, pack.Price, pack.Currency,
		fmt.Sprintf("%s — %s", pack.Name, pack.Price.String()), idempotencyKey)
}

func (s *Service) startCheckout(ctx context.Context, userID, kind, code string, price money.Amount, currency, label string, idempotencyKey *string) (*CheckoutResult, error) {
	// Replay protection: a retried request returns the ORIGINAL payment rather
	// than creating a second one. This is the difference between a flaky mobile
	// network costing the user nothing and costing them twice.
	if idempotencyKey != nil && *idempotencyKey != "" {
		if existing, err := s.Repo.FindPaymentByIdempotencyKey(ctx, userID, *idempotencyKey); err == nil {
			if existing.ReferenceCode != code || existing.Kind != kind {
				return nil, apperr.New(409, apperr.CodeIdempotency,
					"That idempotency key was already used for a different purchase.").
					WithHint("Use a fresh key for a new purchase.")
			}
			return &CheckoutResult{
				Payment:          existing,
				Instructions:     s.instructions(existing),
				AlreadyProcessed: true,
			}, nil
		} else if !IsNotFound(err) {
			return nil, err
		}
	}

	var payment domain.Payment
	err := txn.Run(ctx, s.Pool, func(tx pgx.Tx) error {
		created, err := s.Repo.CreatePayment(ctx, tx, userID, kind, code,
			price.Minor(), currency, s.Cfg.Payment.Provider, idempotencyKey)
		if err != nil {
			return err
		}
		payment = created
		return nil
	})
	if err != nil {
		return nil, err
	}

	logger.FromContext(ctx).Info("checkout started",
		"user_id", userID, "kind", kind, "code", code,
		"amount", price.String(), "payment_id", payment.ID)

	return &CheckoutResult{
		Payment:      payment,
		PaymentURL:   s.paymentURL(payment),
		Instructions: s.instructions(payment),
	}, nil
}

// ConfirmPayment settles a payment and applies what it bought.
//
// With the manual provider this is called by an admin (or, in sandbox, by the
// client) after the money is seen. With a real gateway the webhook calls it.
// Either way it is IDEMPOTENT: confirming an already-paid payment returns the
// same result instead of granting the credits twice.
func (s *Service) ConfirmPayment(ctx context.Context, userID, paymentID, providerRef string) (map[string]any, error) {
	out := map[string]any{}

	err := txn.RunSerializable(ctx, s.Pool, func(tx pgx.Tx) error {
		payment, err := s.Repo.LockPayment(ctx, tx, paymentID)
		if err != nil {
			return err
		}
		if payment.UserID != userID {
			// Report as missing rather than forbidden, so this endpoint cannot
			// be used to discover which payment ids exist.
			return apperr.NotFound("Payment")
		}

		switch payment.Status {
		case domain.PaymentPaid:
			out["already_confirmed"] = true
			out["payment_id"] = payment.ID
			return nil
		case domain.PaymentRefunded, domain.PaymentCancelled:
			return apperr.Conflict("This payment can no longer be confirmed.")
		}

		if err := s.Repo.MarkPaymentPaid(ctx, tx, payment.ID, providerRef); err != nil {
			return err
		}

		applied, err := s.applyPayment(ctx, tx, payment)
		if err != nil {
			return err
		}
		for k, v := range applied {
			out[k] = v
		}
		out["payment_id"] = payment.ID
		out["already_confirmed"] = false
		return nil
	})

	return out, err
}

// applyPayment grants whatever the payment bought. It runs inside the same
// transaction that marked the payment paid, so money and value move together
// or not at all.
func (s *Service) applyPayment(ctx context.Context, tx repository.DB, payment domain.Payment) (map[string]any, error) {
	switch payment.Kind {
	case "subscription":
		return s.activateSubscription(ctx, tx, payment)
	case "credit_pack":
		return s.creditPack(ctx, tx, payment)
	default:
		return nil, apperr.Internal("Unknown payment kind.").
			WithCause(fmt.Errorf("payment %s has kind %q", payment.ID, payment.Kind))
	}
}

func (s *Service) activateSubscription(ctx context.Context, tx repository.DB, payment domain.Payment) (map[string]any, error) {
	plan, err := s.Repo.GetPlan(ctx, tx, payment.ReferenceCode)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	startsAt := now
	months := plan.PeriodMonths
	if months <= 0 {
		months = 1
	}

	// If the user still holds a live subscription, extend from its end date
	// rather than from today — they should not lose the days they paid for.
	if current, err := s.Repo.LiveSubscription(ctx, tx, payment.UserID); err == nil {
		if current.EndsAt.After(now) {
			startsAt = current.EndsAt
		}
		if err := s.Repo.ExpireSubscription(ctx, tx, current.ID, domain.SubExpired); err != nil {
			return nil, err
		}
	} else if !IsNotFound(err) {
		return nil, err
	}

	endsAt := startsAt.AddDate(0, months, 0)

	// The next monthly refill. A 12-month plan refills 11 more times.
	var nextGrant *time.Time
	if months > 1 && plan.MonthlyCredits > 0 {
		t := startsAt.AddDate(0, 1, 0)
		nextGrant = &t
	}

	sub, err := s.Repo.CreateSubscription(ctx, tx, payment.UserID, plan.Code, plan,
		&payment.ID, startsAt, endsAt, nextGrant, domain.SubActive)
	if err != nil {
		return nil, err
	}

	// Grant the first month's allowance plus any signup bonus.
	desc := fmt.Sprintf("%s: %d credits for the first month", plan.Name, plan.MonthlyCredits)
	refType, refID := "subscription", sub.ID
	if err := s.Grant(ctx, tx, payment.UserID,
		plan.MonthlyCredits, plan.SignupCredits,
		domain.CreditPlanGrant, desc, &refType, &refID); err != nil {
		return nil, err
	}

	if err := s.Repo.SyncUserPlan(ctx, tx, payment.UserID, plan.Code, &endsAt); err != nil {
		return nil, err
	}

	logger.FromContext(ctx).Info("subscription activated",
		"user_id", payment.UserID, "plan", plan.Code,
		"ends_at", endsAt.Format(time.RFC3339))

	return map[string]any{
		"subscription":    sub,
		"plan_code":       plan.Code,
		"expires_at":      endsAt,
		"credits_granted": plan.MonthlyCredits + plan.SignupCredits,
	}, nil
}

func (s *Service) creditPack(ctx context.Context, tx repository.DB, payment domain.Payment) (map[string]any, error) {
	pack, err := s.Repo.GetPack(ctx, tx, payment.ReferenceCode)
	if err != nil {
		return nil, err
	}

	total := (pack.Credits + pack.BonusCredits) * payment.Quantity
	desc := fmt.Sprintf("%s: %d credits", pack.Name, total)
	refType, refID := "payment", payment.ID

	if err := s.Grant(ctx, tx, payment.UserID, 0, total,
		domain.CreditPackPurchase, desc, &refType, &refID); err != nil {
		return nil, err
	}

	wallet, err := s.Repo.GetWallet(ctx, tx, payment.UserID)
	if err != nil {
		return nil, err
	}

	logger.FromContext(ctx).Info("credit pack purchased",
		"user_id", payment.UserID, "pack", pack.Code, "credits", total)

	wallet.Compute()
	return map[string]any{
		"pack_code":       pack.Code,
		"credits_granted": total,
		"wallet":          wallet,
	}, nil
}

// CancelSubscription turns off auto-renew. Access continues until the paid
// period ends: the user bought that time and cancelling should not take it away.
func (s *Service) CancelSubscription(ctx context.Context, userID, reason string) (domain.Subscription, error) {
	var out domain.Subscription

	err := txn.Run(ctx, s.Pool, func(tx pgx.Tx) error {
		sub, err := s.Repo.LiveSubscription(ctx, tx, userID)
		if err != nil {
			if IsNotFound(err) {
				return apperr.NotFound("Active subscription").
					WithHint("You are on the free plan; there is nothing to cancel.")
			}
			return err
		}
		if sub.CancelledAt != nil {
			return apperr.Conflict("This subscription is already cancelled.").
				WithDetails(map[string]any{"access_until": sub.EndsAt})
		}

		cancelled, err := s.Repo.CancelSubscription(ctx, tx, sub.ID, reason)
		if err != nil {
			return err
		}
		out = cancelled
		return nil
	})
	if err != nil {
		return out, err
	}

	// Record the reason for the product feedback loop, best effort.
	if reason != "" {
		if _, err := s.Pool.Exec(ctx, `
			INSERT INTO feedback (user_id, kind, reference_type, reference_id, message)
			VALUES ($1, 'cancel_reason', 'subscription', $2, $3)`,
			userID, out.ID, reason); err != nil {
			logger.FromContext(ctx).Warn("could not record the cancellation reason",
				"error", err.Error())
		}
	}

	out.DaysRemaining = daysUntil(out.EndsAt)
	return out, nil
}

// ---------------------------------------------------------------------------
// Background maintenance (driven by the jobs package)
// ---------------------------------------------------------------------------

// ExpireDueSubscriptions downgrades subscriptions whose paid period has ended.
func (s *Service) ExpireDueSubscriptions(ctx context.Context, limit int) (int, error) {
	due, err := s.Repo.DueForExpiry(ctx, limit)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, sub := range due {
		err := txn.Run(ctx, s.Pool, func(tx pgx.Tx) error {
			if err := s.Repo.ExpireSubscription(ctx, tx, sub.ID, domain.SubExpired); err != nil {
				return err
			}
			// The allowance dies with the plan; purchased credits do not. That
			// is the promise made when the user bought them.
			wallet, err := s.Repo.LockWallet(ctx, tx, sub.UserID)
			if err != nil {
				return err
			}
			if wallet.AllowanceCredits > 0 {
				desc := "Plan allowance expired with the subscription"
				refType, refID := "subscription", sub.ID
				if _, err := s.Repo.ApplyWallet(ctx, tx, LedgerWrite{
					UserID:         sub.UserID,
					AllowanceAfter: 0,
					PurchasedAfter: wallet.PurchasedCredits,
					Delta:          -wallet.AllowanceCredits,
					Reason:         domain.CreditExpiry,
					ReferenceType:  &refType,
					ReferenceID:    &refID,
					Description:    &desc,
				}); err != nil {
					return err
				}
			}
			return s.Repo.SyncUserPlan(ctx, tx, sub.UserID, FreePlanCode, nil)
		})
		if err != nil {
			logger.FromContext(ctx).Error("could not expire a subscription",
				"subscription_id", sub.ID, "error", err.Error())
			continue
		}
		count++
	}
	return count, nil
}

// GrantDueAllowances performs the monthly credit refill inside a multi-month
// plan.
func (s *Service) GrantDueAllowances(ctx context.Context, limit int) (int, error) {
	due, err := s.Repo.DueForGrant(ctx, limit)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, sub := range due {
		err := txn.RunSerializable(ctx, s.Pool, func(tx pgx.Tx) error {
			plan, err := s.Repo.GetPlan(ctx, tx, sub.PlanCode)
			if err != nil {
				return err
			}

			desc := fmt.Sprintf("%s: monthly credit refill", plan.Name)
			refType, refID := "subscription", sub.ID
			if err := s.Grant(ctx, tx, sub.UserID, plan.MonthlyCredits, 0,
				domain.CreditPlanGrant, desc, &refType, &refID); err != nil {
				return err
			}

			// Schedule the next refill, but never past the end of the plan.
			var next *time.Time
			candidate := sub.StartsAt.AddDate(0, sub.GrantsMade+2, 0)
			if candidate.Before(sub.EndsAt) {
				next = &candidate
			}
			return s.Repo.MarkGranted(ctx, tx, sub.ID, next)
		})
		if err != nil {
			logger.FromContext(ctx).Error("could not grant a monthly allowance",
				"subscription_id", sub.ID, "error", err.Error())
			continue
		}
		count++
	}
	return count, nil
}

// ---------------------------------------------------------------------------
// provider-specific presentation
// ---------------------------------------------------------------------------

func (s *Service) paymentURL(p domain.Payment) string {
	switch s.Cfg.Payment.Provider {
	case "manual":
		return ""
	default:
		// A real gateway integration fills this in from its create-session call.
		// Until one is wired up, returning "" keeps the client honest rather
		// than sending the user to a broken link.
		return ""
	}
}

func (s *Service) instructions(p domain.Payment) string {
	amount := money.Amount(p.Amount).String()
	switch s.Cfg.Payment.Provider {
	case "manual":
		return fmt.Sprintf(
			"Send %s %s and share the transaction id with support, quoting reference %s. "+
				"Your purchase is applied as soon as the payment is confirmed.",
			p.Currency, amount, p.ID)
	default:
		return fmt.Sprintf("Complete the %s payment of %s %s to finish your purchase.",
			p.Provider, p.Currency, amount)
	}
}
