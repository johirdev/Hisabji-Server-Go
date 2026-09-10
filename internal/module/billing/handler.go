package billing

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/core/contextx"
	"github.com/johirdev/Hisabji-Server/internal/core/handler"
	"github.com/johirdev/Hisabji-Server/internal/core/response"
	"github.com/johirdev/Hisabji-Server/internal/core/validate"
	"github.com/johirdev/Hisabji-Server/internal/domain"
)

// Handler is the billing module's HTTP layer.
type Handler struct {
	Service *Service
}

// NewHandler builds the handler.
func NewHandler(s *Service) *Handler { return &Handler{Service: s} }

// ---------------------------------------------------------------------------
// Catalogue (public, richer when signed in)
// ---------------------------------------------------------------------------

// Plans handles GET /billing/plans.
//
// It works signed out (for a pricing page) and signed in (marking the current
// plan), which is why the route uses optional auth rather than requiring it.
func (h *Handler) Plans(c *gin.Context) (*response.Result, error) {
	plans, err := h.Service.Repo.ListPlans(c.Request.Context(), false)
	if err != nil {
		return nil, err
	}

	currentPlan := ""
	if userID := contextx.UserID(c); userID != "" {
		if ent, err := h.Service.Entitlement(c.Request.Context(), userID); err == nil {
			currentPlan = ent.PlanCode
		}
	}

	for i := range plans {
		plans[i].Compute()
		plans[i].IsCurrentPlan = plans[i].Code == currentPlan
	}
	return response.OK(plans, "Subscription plans fetched successfully."), nil
}

// Packs handles GET /billing/credit-packs.
func (h *Handler) Packs(c *gin.Context) (*response.Result, error) {
	packs, err := h.Service.Repo.ListPacks(c.Request.Context(), false)
	if err != nil {
		return nil, err
	}
	for i := range packs {
		packs[i].Compute()
	}
	return response.OK(packs, "Credit packs fetched successfully."), nil
}

// Features handles GET /billing/features.
//
// For a signed-in user each feature is annotated with whether their plan
// includes it and whether they can afford it, so the client can render the
// right call to action without a request per feature.
func (h *Handler) Features(c *gin.Context) (*response.Result, error) {
	features, err := h.Service.Repo.ListFeatures(c.Request.Context(), false)
	if err != nil {
		return nil, err
	}

	if userID := contextx.UserID(c); userID != "" {
		if ent, err := h.Service.Entitlement(c.Request.Context(), userID); err == nil {
			for i := range features {
				features[i].IncludedInPlan = ent.Has(features[i].Code)
				features[i].Affordable = ent.TotalCredits >= features[i].CreditCost
			}
		}
	}

	// Group by category so the client can render sections without hardcoding
	// which feature belongs where.
	grouped := map[string][]domain.Feature{}
	for _, f := range features {
		grouped[f.Category] = append(grouped[f.Category], f)
	}

	return response.OK(map[string]any{
		"features":    features,
		"by_category": grouped,
	}, "Features fetched successfully."), nil
}

// ---------------------------------------------------------------------------
// The signed-in user's own billing state
// ---------------------------------------------------------------------------

// Me handles GET /billing/me — everything the account screen needs in one call.
func (h *Handler) Me(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	ctx := c.Request.Context()

	ent, err := h.Service.Entitlement(ctx, userID)
	if err != nil {
		return nil, err
	}
	wallet, err := h.Service.Repo.GetWallet(ctx, nil, userID)
	if err != nil && !IsNotFound(err) {
		return nil, err
	}
	wallet.Compute()

	out := map[string]any{
		"entitlement": ent,
		"wallet":      wallet,
	}

	if sub, err := h.Service.Repo.LiveSubscription(ctx, nil, userID); err == nil {
		sub.DaysRemaining = daysUntil(sub.EndsAt)
		out["subscription"] = sub
	} else if !IsNotFound(err) {
		return nil, err
	}

	return response.OK(out, "Billing details fetched successfully."), nil
}

// Wallet handles GET /billing/wallet.
func (h *Handler) Wallet(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	wallet, err := h.Service.Repo.GetWallet(c.Request.Context(), nil, userID)
	if err != nil {
		return nil, err
	}
	// Returned as the same Wallet object /billing/me nests, so a client has one
	// wallet type rather than two subtly different ones.
	wallet.Compute()
	return response.OK(wallet, "Credit balance fetched successfully."), nil
}

// Ledger handles GET /billing/ledger — where the credits went.
func (h *Handler) Ledger(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	page, limit := pageParams(c)

	entries, total, err := h.Service.Repo.ListLedger(c.Request.Context(), userID, limit, (page-1)*limit)
	if err != nil {
		return nil, err
	}
	meta := response.NewMeta(page, limit, total, len(entries))
	return response.List(entries, meta, "Credit history fetched successfully."), nil
}

// Payments handles GET /billing/payments.
func (h *Handler) Payments(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	page, limit := pageParams(c)

	items, total, err := h.Service.Repo.ListPayments(c.Request.Context(), userID, limit, (page-1)*limit)
	if err != nil {
		return nil, err
	}
	meta := response.NewMeta(page, limit, total, len(items))
	return response.List(items, meta, "Payment history fetched successfully."), nil
}

// Access handles GET /billing/features/:code/access — "can I run this, and
// what will it cost me?".
func (h *Handler) Access(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	code, err := validate.SlugParam(c, "code")
	if err != nil {
		return nil, err
	}
	out, err := h.Service.CheckAccess(c.Request.Context(), userID, code)
	if err != nil {
		return nil, err
	}
	return response.OK(out, "Feature access checked successfully."), nil
}

// ---------------------------------------------------------------------------
// Purchasing
// ---------------------------------------------------------------------------

// SubscribeRequest starts a plan purchase.
type SubscribeRequest struct {
	PlanCode string `json:"plan_code" binding:"required,max=40"`
}

// Subscribe handles POST /billing/subscribe.
func (h *Handler) Subscribe(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	var in SubscribeRequest
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}

	out, err := h.Service.StartSubscriptionCheckout(c.Request.Context(), userID,
		strings.ToLower(strings.TrimSpace(in.PlanCode)), idempotencyKey(c))
	if err != nil {
		return nil, err
	}
	if out.AlreadyProcessed {
		return response.OK(out, "This purchase was already started."), nil
	}
	return response.Created(out, "Checkout started. Complete the payment to activate your plan."), nil
}

// BuyCreditsRequest starts a credit-pack purchase.
type BuyCreditsRequest struct {
	PackCode string `json:"pack_code" binding:"required,max=40"`
}

// BuyCredits handles POST /billing/credits/buy.
func (h *Handler) BuyCredits(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	var in BuyCreditsRequest
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}

	out, err := h.Service.StartPackCheckout(c.Request.Context(), userID,
		strings.ToLower(strings.TrimSpace(in.PackCode)), idempotencyKey(c))
	if err != nil {
		return nil, err
	}
	if out.AlreadyProcessed {
		return response.OK(out, "This purchase was already started."), nil
	}
	return response.Created(out, "Checkout started. Complete the payment to receive your credits."), nil
}

// ConfirmRequest settles a payment.
type ConfirmRequest struct {
	PaymentID   string `json:"payment_id"   binding:"required,uuid"`
	ProviderRef string `json:"provider_ref" binding:"omitempty,max=120"`
}

// Confirm handles POST /billing/payments/confirm.
//
// With a real gateway this is driven by the provider's webhook. It is exposed
// to the client only in sandbox mode, and the route file enforces that.
func (h *Handler) Confirm(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	var in ConfirmRequest
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}

	out, err := h.Service.ConfirmPayment(c.Request.Context(), userID, in.PaymentID, in.ProviderRef)
	if err != nil {
		return nil, err
	}
	if already, _ := out["already_confirmed"].(bool); already {
		return response.OK(out, "This payment was already confirmed."), nil
	}
	return response.OK(out, "Payment confirmed. Your purchase is active."), nil
}

// CancelRequest ends a subscription's auto-renewal.
type CancelRequest struct {
	Reason string `json:"reason" binding:"omitempty,max=300,safetext"`
}

// Cancel handles POST /billing/subscription/cancel.
func (h *Handler) Cancel(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	var in CancelRequest
	_ = c.ShouldBindJSON(&in) // an empty body is a valid cancellation

	sub, err := h.Service.CancelSubscription(c.Request.Context(), userID, strings.TrimSpace(in.Reason))
	if err != nil {
		return nil, err
	}
	return response.OK(sub,
		"Subscription cancelled. You keep access until it expires."), nil
}

// ---------------------------------------------------------------------------

func idempotencyKey(c *gin.Context) *string {
	key := strings.TrimSpace(c.GetHeader("X-Idempotency-Key"))
	if key == "" || len(key) > 120 {
		return nil
	}
	return &key
}

// pageParams reads ?page and ?limit for the simple history listings. The
// generic query engine is overkill here: these lists have no filters, and a bad
// value should quietly fall back rather than fail a billing screen.
func pageParams(c *gin.Context) (page, limit int) {
	page, limit = 1, 20
	if n, err := strconv.Atoi(c.Query("page")); err == nil && n > 0 {
		page = n
	}
	if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 {
		limit = n
		if limit > 100 {
			limit = 100
		}
	}
	return page, limit
}
