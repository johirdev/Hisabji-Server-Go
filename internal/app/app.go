// Package app wires the whole application together.
//
// Every dependency is constructed exactly once here and passed down explicitly.
// There is no global state, no service locator and no init() magic: if you want
// to know what a module can reach, you read this file.
//
// That also makes the dependency direction visible. billing is built before
// auth because auth needs a Provisioner; auth is built before user because user
// needs a SessionRevoker. A cycle would show up as a compile error rather than
// as a mysterious nil pointer at runtime.
package app

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/johirdev/Hisabji-Server/internal/config"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/logger"
	"github.com/johirdev/Hisabji-Server/internal/core/response"
	"github.com/johirdev/Hisabji-Server/internal/core/validate"
	"github.com/johirdev/Hisabji-Server/internal/infrastructure/database"
	"github.com/johirdev/Hisabji-Server/internal/infrastructure/sms"
	"github.com/johirdev/Hisabji-Server/internal/middleware"
	"github.com/johirdev/Hisabji-Server/internal/module/auth"
	"github.com/johirdev/Hisabji-Server/internal/module/billing"
	"github.com/johirdev/Hisabji-Server/internal/module/user"
	"github.com/johirdev/Hisabji-Server/internal/shared/jwt"
	"github.com/redis/go-redis/v9"
)

// App holds every long-lived dependency.
type App struct {
	Cfg   config.Config
	DB    *pgxpool.Pool
	Redis *redis.Client

	Tokens  *jwt.Issuer
	Auth    *middleware.Authenticator
	Limiter *middleware.Limiter
	SMS     sms.Sender

	Billing *billing.Service
	Gate    *billing.Gate

	// Handlers, kept so the router can mount them.
	AuthHandler    *auth.Handler
	UserHandler    *user.Handler
	BillingHandler *billing.Handler
}

// New builds the application graph. It connects to Postgres and Redis, runs
// migrations when enabled, and returns a ready App.
func New(ctx context.Context, cfg config.Config) (*App, error) {
	logger.Init(logger.Options{Level: cfg.Log.Level, Format: cfg.Log.Format})
	response.Configure(cfg.App.Env)
	validate.Init()

	// Teach the error translator how to phrase each module's constraint
	// violations, so a unique-index error becomes a sentence a user understands.
	auth.RegisterConstraints()
	registerSharedConstraints()

	db, err := database.Open(ctx, cfg.Database, cfg.App)
	if err != nil {
		return nil, err
	}

	if cfg.Database.AutoMigrate {
		if err := database.Migrate(ctx, db); err != nil {
			db.Close()
			return nil, err
		}
	}

	rdb, err := database.OpenRedis(ctx, cfg.Redis)
	if err != nil {
		db.Close()
		return nil, err
	}

	tokens, err := jwt.New(jwt.Config{
		AccessSecret:  cfg.Auth.JWTSecret,
		RefreshSecret: cfg.Auth.RefreshSecret,
		Issuer:        cfg.Auth.Issuer,
		AccessTTL:     cfg.Auth.AccessTokenTTL,
		RefreshTTL:    cfg.Auth.RefreshTokenTTL,
	})
	if err != nil {
		db.Close()
		_ = rdb.Close()
		return nil, fmt.Errorf("token issuer could not be created: %w", err)
	}

	app := &App{
		Cfg:     cfg,
		DB:      db,
		Redis:   rdb,
		Tokens:  tokens,
		Auth:    middleware.NewAuthenticator(tokens, rdb, cfg.Redis.KeyPrefix),
		Limiter: middleware.NewLimiter(rdb, cfg.Redis, cfg.RateLimit.Enabled),
		SMS:     sms.New(cfg.SMS, cfg.App),
	}

	// ---- billing: no dependencies on other modules ------------------------
	billingRepo := billing.NewRepository(db)
	app.Billing = billing.NewService(billingRepo, db, cfg)
	app.Gate = billing.NewGate(app.Billing)
	app.BillingHandler = billing.NewHandler(app.Billing)

	// ---- auth: needs billing (to provision a new account) ------------------
	authRepo := auth.NewRepository(db)
	authService := auth.NewService(authRepo, db, tokens, app.SMS, app.Billing, app.Auth, cfg)
	app.AuthHandler = auth.NewHandler(authService)

	// ---- user: needs billing (entitlement) and auth (session revocation) ---
	userRepo := user.NewRepository(db)
	userService := user.NewService(userRepo, db, app.Billing, authRepo, app.Auth, cfg)
	app.UserHandler = user.NewHandler(userService)

	logger.Named("app").Info("application ready",
		"env", cfg.App.Env,
		"version", cfg.App.Version,
		"sms_provider", app.SMS.Name(),
		"payment_provider", cfg.Payment.Provider,
		"ai_provider", cfg.AI.Provider)

	return app, nil
}

// Close releases every resource, in reverse order of construction.
func (a *App) Close() {
	if a.Redis != nil {
		if err := a.Redis.Close(); err != nil {
			logger.Named("app").Warn("redis did not close cleanly", "error", err.Error())
		}
	}
	if a.DB != nil {
		a.DB.Close()
	}
}

// Health reports the state of every dependency, for the readiness probe.
func (a *App) Health(ctx context.Context) (bool, map[string]any) {
	healthy := true
	out := map[string]any{}

	if err := database.Healthy(ctx, a.DB); err != nil {
		healthy = false
		out["database"] = map[string]any{"status": "down", "error": err.Error()}
	} else {
		out["database"] = map[string]any{"status": "up", "pool": database.Stats(a.DB)}
	}

	if err := database.RedisHealthy(ctx, a.Redis); err != nil {
		// Redis being down degrades rate limiting and caching but does not make
		// the service unable to serve requests, so it is reported and not fatal.
		out["redis"] = map[string]any{"status": "down", "error": err.Error()}
	} else {
		out["redis"] = map[string]any{"status": "up", "pool": database.RedisStats(a.Redis)}
	}

	return healthy, out
}

// registerSharedConstraints covers the constraints that belong to no single
// module, or that several modules can hit.
func registerSharedConstraints() {
	apperr.RegisterConstraints(map[string]apperr.ConstraintMessage{
		"categories_user_slug_uniq": {
			Field:     "name",
			Message:   "You already have a category with that name.",
			MessageBN: "এই নামে আপনার একটি ক্যাটাগরি ইতিমধ্যে আছে।",
		},
		"budgets_user_period_uniq": {
			Field:     "period_start",
			Message:   "You already have a budget starting on that date.",
			MessageBN: "ওই তারিখ থেকে শুরু হওয়া একটি বাজেট ইতিমধ্যে আছে।",
		},
		"budget_limits_unique_category": {
			Field:     "category_id",
			Message:   "That category already has a limit in this budget.",
			MessageBN: "এই বাজেটে ওই ক্যাটাগরির একটি সীমা ইতিমধ্যে আছে।",
		},
		"subscriptions_one_live_per_user": {
			Field:     "plan_code",
			Message:   "You already have an active subscription.",
			MessageBN: "আপনার একটি সক্রিয় সাবস্ক্রিপশন ইতিমধ্যে আছে।",
		},
		"payments_provider_ref_uniq": {
			Field:     "provider_ref",
			Message:   "This payment has already been recorded.",
			MessageBN: "এই পেমেন্টটি ইতিমধ্যে রেকর্ড করা হয়েছে।",
		},
		"payments_idempotency_uniq": {
			Field:     "idempotency_key",
			Code:      apperr.CodeIdempotency,
			Message:   "This request was already submitted.",
			MessageBN: "এই অনুরোধটি ইতিমধ্যে জমা দেওয়া হয়েছে।",
		},
		"user_devices_token_uniq": {
			Field:   "push_token",
			Message: "This device is already registered.",
		},
		"expenses_amount_check": {
			Field:     "amount",
			Message:   "The amount must be greater than zero.",
			MessageBN: "পরিমাণ শূন্যের চেয়ে বেশি হতে হবে।",
		},
		"incomes_amount_check": {
			Field:     "amount",
			Message:   "The amount must be greater than zero.",
			MessageBN: "পরিমাণ শূন্যের চেয়ে বেশি হতে হবে।",
		},
		"expenses_date_sane": {
			Field:     "spent_at",
			Message:   "That date looks wrong. Please check the year.",
			MessageBN: "তারিখটি সঠিক মনে হচ্ছে না। বছরটি যাচাই করুন।",
		},
		"goals_target_after_start": {
			Field:     "target_date",
			Message:   "The target date must be on or after the start date.",
			MessageBN: "লক্ষ্যের তারিখ শুরুর তারিখের সমান বা পরে হতে হবে।",
		},
		"budgets_period_ordered": {
			Field:     "period_end",
			Message:   "The end date must be on or after the start date.",
			MessageBN: "শেষ তারিখ শুরুর তারিখের সমান বা পরে হতে হবে।",
		},
	})
}
