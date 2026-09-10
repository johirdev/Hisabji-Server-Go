package billing

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/config"
	"github.com/johirdev/Hisabji-Server/internal/core/handler"
	"github.com/johirdev/Hisabji-Server/internal/middleware"
)

// Deps is everything the route group needs.
type Deps struct {
	Handler *Handler
	Auth    *middleware.Authenticator
	Limiter *middleware.Limiter
	Cfg     config.Config
}

// Mount registers the billing routes.
func Mount(rg *gin.RouterGroup, d Deps) {
	lim := d.Limiter

	// ---- catalogue: readable signed out, richer signed in ------------------
	catalogue := rg.Group("")
	catalogue.Use(d.Auth.Optional())
	{
		catalogue.GET("/plans", handler.H(d.Handler.Plans))
		catalogue.GET("/credit-packs", handler.H(d.Handler.Packs))
		catalogue.GET("/features", handler.H(d.Handler.Features))
	}

	// ---- the signed-in user's own billing ---------------------------------
	me := rg.Group("")
	me.Use(d.Auth.Required())
	{
		me.GET("/me", handler.H(d.Handler.Me))
		me.GET("/wallet", handler.H(d.Handler.Wallet))
		me.GET("/ledger", handler.H(d.Handler.Ledger))
		me.GET("/payments", handler.H(d.Handler.Payments))
		me.GET("/features/:code/access", handler.H(d.Handler.Access))

		// Purchase endpoints are rate limited hard. They create rows that a
		// support person may later have to reconcile by hand, so a runaway
		// client must not be able to make a thousand of them.
		me.POST("/subscribe",
			lim.Limit(middleware.StrictRule("subscribe", 10, time.Hour)),
			handler.H(d.Handler.Subscribe))

		me.POST("/credits/buy",
			lim.Limit(middleware.StrictRule("buy_credits", 20, time.Hour)),
			handler.H(d.Handler.BuyCredits))

		me.POST("/subscription/cancel",
			lim.Limit(middleware.StrictRule("cancel", 5, time.Hour)),
			handler.H(d.Handler.Cancel))

		// Client-side confirmation only exists in sandbox mode. In production a
		// client that could confirm its own payment could grant itself a plan
		// for free, so the route is not registered at all — its absence is the
		// security control, not a runtime check that might be misconfigured.
		if d.Cfg.Payment.SandboxMode {
			me.POST("/payments/confirm",
				lim.Limit(middleware.StrictRule("confirm_payment", 30, time.Hour)),
				handler.H(d.Handler.Confirm))
		}
	}
}
