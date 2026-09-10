package auth

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/config"
	"github.com/johirdev/Hisabji-Server/internal/core/handler"
	"github.com/johirdev/Hisabji-Server/internal/middleware"
)

// Deps is everything the route group needs, passed in rather than constructed
// here so main() owns the wiring and this file stays a readable route table.
type Deps struct {
	Handler *Handler
	Auth    *middleware.Authenticator
	Limiter *middleware.Limiter
	Cfg     config.Config
}

// Mount registers the auth routes on the group.
//
// The rate limits below are policy, not decoration. Login and OTP endpoints are
// the ones an attacker actually targets, so each gets its own bucket — a flood
// of login attempts must not also lock legitimate users out of registration.
func Mount(rg *gin.RouterGroup, d Deps) {
	cfg := d.Cfg.RateLimit
	lim := d.Limiter

	// ---- public -----------------------------------------------------------
	rg.POST("/register",
		lim.Limit(middleware.Rule{Name: "auth:register", Limit: 5, Window: time.Hour, Scope: middleware.ScopeIP}),
		handler.H(d.Handler.Register))

	rg.POST("/login",
		lim.Limit(middleware.AuthRule("login", cfg)),
		handler.H(d.Handler.Login))

	rg.POST("/verify-otp",
		lim.Limit(middleware.Rule{Name: "auth:verify", Limit: 15, Window: time.Minute, Scope: middleware.ScopeIP}),
		handler.H(d.Handler.VerifyOTP))

	// Tighter than the others: every call here costs money in SMS.
	rg.POST("/resend-otp",
		lim.Limit(middleware.Rule{Name: "auth:resend", Limit: 5, Window: 10 * time.Minute, Scope: middleware.ScopeIP}),
		handler.H(d.Handler.ResendOTP))

	rg.POST("/refresh",
		lim.Limit(middleware.Rule{Name: "auth:refresh", Limit: 60, Window: time.Hour, Scope: middleware.ScopeIP}),
		handler.H(d.Handler.Refresh))

	rg.POST("/forgot-password",
		lim.Limit(middleware.Rule{Name: "auth:forgot", Limit: 5, Window: time.Hour, Scope: middleware.ScopeIP}),
		handler.H(d.Handler.ForgotPassword))

	rg.POST("/reset-password",
		lim.Limit(middleware.Rule{Name: "auth:reset", Limit: 10, Window: time.Hour, Scope: middleware.ScopeIP}),
		handler.H(d.Handler.ResetPassword))

	// ---- authenticated ----------------------------------------------------
	authed := rg.Group("")
	authed.Use(d.Auth.Required())
	{
		authed.POST("/logout", handler.H(d.Handler.Logout))
		authed.GET("/sessions", handler.H(d.Handler.Sessions))
		authed.DELETE("/sessions/:id", handler.H(d.Handler.RevokeSession))

		authed.POST("/change-password",
			lim.Limit(middleware.StrictRule("change_password", 5, time.Hour)),
			handler.H(d.Handler.ChangePassword))
	}
}
