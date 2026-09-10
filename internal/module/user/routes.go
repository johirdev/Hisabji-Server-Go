package user

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

// Mount registers the user routes.
func Mount(rg *gin.RouterGroup, d Deps) {
	lim := d.Limiter

	// Availability is checked during registration, before there is a token, so
	// it uses optional auth and a per-IP limit.
	rg.GET("/username-available",
		d.Auth.Optional(),
		lim.Limit(middleware.Rule{Name: "user:username_check", Limit: 60, Window: time.Minute, Scope: middleware.ScopeIP}),
		handler.H(d.Handler.CheckUsername))

	me := rg.Group("/me")
	me.Use(d.Auth.Required())
	{
		me.GET("", handler.H(d.Handler.Me))
		me.PATCH("", lim.Limit(middleware.WriteRule("profile", d.Cfg.RateLimit)), handler.H(d.Handler.UpdateMe))
		me.PUT("", lim.Limit(middleware.WriteRule("profile", d.Cfg.RateLimit)), handler.H(d.Handler.UpdateMe))

		me.PATCH("/preferences", lim.Limit(middleware.WriteRule("preferences", d.Cfg.RateLimit)), handler.H(d.Handler.Preferences))
		me.PUT("/username", lim.Limit(middleware.StrictRule("username", 5, time.Hour)), handler.H(d.Handler.SetUsername))
		me.POST("/onboarding", lim.Limit(middleware.WriteRule("onboarding", d.Cfg.RateLimit)), handler.H(d.Handler.Onboarding))

		// Closing an account is irreversible from the user's point of view, so
		// it is limited far harder than anything else in this module.
		me.DELETE("", lim.Limit(middleware.StrictRule("delete_account", 3, 24*time.Hour)), handler.H(d.Handler.DeleteMe))
	}
}
