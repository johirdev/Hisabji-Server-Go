package app

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/core/handler"
	"github.com/johirdev/Hisabji-Server/internal/core/logger"
	"github.com/johirdev/Hisabji-Server/internal/core/response"
	"github.com/johirdev/Hisabji-Server/internal/middleware"
	"github.com/johirdev/Hisabji-Server/internal/module/auth"
	"github.com/johirdev/Hisabji-Server/internal/module/billing"
	"github.com/johirdev/Hisabji-Server/internal/module/user"
)

// Router builds the HTTP handler.
//
// MIDDLEWARE ORDER IS A DESIGN DECISION, not an accident:
//
//	RequestID       first, so every later log line and error carries the id
//	SecurityHeaders before any body is written
//	CORS            before Recovery, so a preflight never needs a panic guard
//	BodyLimit       before anything reads the body
//	Recovery        before Logger, so a panic still produces one access log line
//	Logger          wraps the handlers it reports on
//	Locale          before handlers, which use it to pick a language
//	RateLimit       before Auth, so an unauthenticated flood is rejected
//	                *before* it costs a signature verification
func (a *App) Router() *gin.Engine {
	if a.Cfg.App.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()

	// gin trusts every proxy by default, which means any client can forge
	// X-Forwarded-For and defeat per-IP rate limiting. Trust only what we
	// configure, and nothing at all when the list is empty.
	if len(a.Cfg.Server.TrustedProxies) > 0 {
		if err := r.SetTrustedProxies(a.Cfg.Server.TrustedProxies); err != nil {
			logger.Named("router").Error("invalid TRUSTED_PROXIES", "error", err.Error())
		}
	} else {
		_ = r.SetTrustedProxies(nil)
	}

	r.RedirectTrailingSlash = true
	r.HandleMethodNotAllowed = true
	r.NoRoute(response.NotFoundHandler())
	r.NoMethod(response.MethodNotAllowedHandler())

	r.Use(
		middleware.RequestID(),
		middleware.SecurityHeaders(a.Cfg.Security, a.Cfg.App.IsProduction()),
		middleware.CORS(a.Cfg.Security),
		middleware.BodyLimit(a.Cfg.Server.MaxBodyBytes),
		middleware.Recovery(),
		middleware.Logger(a.Cfg.Log.LogRequests, a.Cfg.Log.SlowQueryMS),
		middleware.Locale(a.Cfg.App.Locale),
	)

	a.mountMeta(r)

	api := r.Group("/api/v1")
	// The blanket per-IP limit. Individual routes add their own tighter ones.
	api.Use(a.Limiter.Limit(middleware.GlobalRule(a.Cfg.RateLimit)))

	auth.Mount(api.Group("/auth"), auth.Deps{
		Handler: a.AuthHandler, Auth: a.Auth, Limiter: a.Limiter, Cfg: a.Cfg,
	})
	user.Mount(api.Group("/users"), user.Deps{
		Handler: a.UserHandler, Auth: a.Auth, Limiter: a.Limiter, Cfg: a.Cfg,
	})
	billing.Mount(api.Group("/billing"), billing.Deps{
		Handler: a.BillingHandler, Auth: a.Auth, Limiter: a.Limiter, Cfg: a.Cfg,
	})

	return r
}

// mountMeta registers the root, health and version endpoints.
//
// These sit OUTSIDE /api/v1 and outside the rate limiter: an orchestrator's
// liveness probe must never be throttled, or a traffic spike turns into a
// restart loop that makes the spike worse.
func (a *App) mountMeta(r *gin.Engine) {
	r.GET("/", handler.H(func(c *gin.Context) (*response.Result, error) {
		return response.OK(map[string]any{
			"service":     a.Cfg.App.Name,
			"version":     a.Cfg.App.Version,
			"environment": a.Cfg.App.Env,
			"api_base":    "/api/v1",
			"docs":        "/docs",
			"health":      "/health",
		}, "Hisabji API is running."), nil
	}))

	// Liveness: is the process up? Deliberately checks nothing else, so a
	// database blip cannot make Kubernetes kill a perfectly healthy container.
	r.GET("/health/live", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "alive"})
	})

	// Readiness: can this instance serve traffic? Checks the database, because
	// without it every request would fail.
	r.GET("/health/ready", func(c *gin.Context) {
		ctx, cancel := timeoutCtx(c, 3*time.Second)
		defer cancel()

		healthy, details := a.Health(ctx)
		status := http.StatusOK
		if !healthy {
			status = http.StatusServiceUnavailable
		}
		c.JSON(status, gin.H{
			"status":    map[bool]string{true: "ready", false: "not_ready"}[healthy],
			"checks":    details,
			"version":   a.Cfg.App.Version,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	})

	// The general health endpoint most tooling expects.
	r.GET("/health", func(c *gin.Context) {
		ctx, cancel := timeoutCtx(c, 3*time.Second)
		defer cancel()

		healthy, details := a.Health(ctx)
		status := http.StatusOK
		if !healthy {
			status = http.StatusServiceUnavailable
		}
		c.JSON(status, gin.H{
			"status":      map[bool]string{true: "ok", false: "unhealthy"}[healthy],
			"service":     a.Cfg.App.Name,
			"version":     a.Cfg.App.Version,
			"environment": a.Cfg.App.Env,
			"checks":      details,
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		})
	})
}

// timeoutCtx bounds a health check, so a hung dependency cannot hold the
// probe open until the server write timeout fires.
func timeoutCtx(c *gin.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), d)
}
