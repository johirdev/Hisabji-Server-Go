package billing

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/contextx"
	"github.com/johirdev/Hisabji-Server/internal/core/response"
)

// ContextKeyAccess is where the gate stores its decision, so the handler behind
// it does not repeat the lookup.
const ContextKeyAccess = "ctx.feature_access"

// Gate builds the middleware that protects premium endpoints.
//
// Using it makes a route's business rule visible in the route table:
//
//	ai.POST("/monthly-coach", gate.Require("ai_monthly_coach"), handler.H(h.MonthlyCoach))
//
// The gate CHECKS but does not SPEND. Charging before the work runs would bill
// users for answers they never received; the handler calls billing.Spend once
// it is about to do (or has done) the expensive part.
type Gate struct {
	Service *Service
}

// NewGate builds the gate.
func NewGate(s *Service) *Gate { return &Gate{Service: s} }

// Require blocks the request unless the caller may use the feature.
func (g *Gate) Require(featureCode string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := contextx.UserID(c)
		if userID == "" {
			response.Fail(c, apperr.Unauthorized(""))
			return
		}

		access, err := g.Service.CheckAccess(c.Request.Context(), userID, featureCode)
		if err != nil {
			response.Fail(c, err)
			return
		}

		if !access.Allowed {
			response.Fail(c, accessDenied(access))
			return
		}

		c.Set(ContextKeyAccess, access)
		c.Next()
	}
}

// RequireTier blocks anything below a minimum plan tier. Use it for whole
// sections (the business workspace) rather than individual metered actions.
func (g *Gate) RequireTier(minTier string) gin.HandlerFunc {
	rank := map[string]int{"free": 0, "plus": 1, "pro": 2, "business": 3}

	return func(c *gin.Context) {
		userID := contextx.UserID(c)
		if userID == "" {
			response.Fail(c, apperr.Unauthorized(""))
			return
		}

		ent, err := g.Service.Entitlement(c.Request.Context(), userID)
		if err != nil {
			response.Fail(c, err)
			return
		}

		if rank[ent.Tier] < rank[minTier] {
			response.Fail(c, apperr.PaymentRequired(apperr.CodeSubscriptionRequired,
				"This section is available on the "+titleCase(minTier)+" plan.").
				WithDetails(map[string]any{
					"required_tier": minTier,
					"current_tier":  ent.Tier,
					"current_plan":  ent.PlanCode,
				}).
				WithHint("Open the plans screen to upgrade."))
			return
		}
		c.Next()
	}
}

// AccessFrom reads the gate's decision inside a handler.
func AccessFrom(c *gin.Context) (Access, bool) {
	v, ok := c.Get(ContextKeyAccess)
	if !ok {
		return Access{}, false
	}
	a, ok := v.(Access)
	return a, ok
}

// accessDenied turns a refusal into an error the client can act on: it says
// which of the two ways forward applies — upgrade, or buy credits — rather than
// a bare 402.
func accessDenied(a Access) *apperr.Error {
	code := apperr.CodeSubscriptionRequired
	hint := "Upgrade your plan to unlock this feature."

	if a.NeedsCredits {
		code = apperr.CodeInsufficientCredit
		hint = "Buy a credit pack, or upgrade to a plan with a monthly allowance."
		if a.Entitlement.TotalCredits == 0 && a.Entitlement.PlanCode == FreePlanCode {
			code = apperr.CodeQuotaExceeded
			hint = "You have used your free AI credits. Buy a credit pack or subscribe to continue."
		}
	}

	return apperr.PaymentRequired(code, a.Reason).
		WithDetails(map[string]any{
			"feature":           a.Feature.Code,
			"feature_name":      a.Feature.Name,
			"credit_cost":       a.CreditCost,
			"available_credits": a.Entitlement.TotalCredits,
			"current_plan":      a.Entitlement.PlanCode,
			"required_tier":     a.Feature.MinTier,
			"needs_upgrade":     a.NeedsUpgrade,
			"needs_credits":     a.NeedsCredits,
		}).
		WithHint(hint)
}

// SpendFor is the helper an AI handler calls once the work is committed.
// Keeping it here means no other module has to know the ledger exists.
func (g *Gate) SpendFor(ctx context.Context, userID, featureCode string, referenceType, referenceID *string) (SpendResult, error) {
	return g.Service.Spend(ctx, userID, featureCode, referenceType, referenceID)
}
