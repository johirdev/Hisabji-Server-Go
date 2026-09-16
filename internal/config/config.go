// Package config loads and validates every runtime setting from environment
// variables exactly once, at boot.
//
// Two rules make this safe in production:
//
//  1. Nothing anywhere else calls os.Getenv. If a setting is not in this file,
//     it does not exist — so there are no surprise env vars that only matter on
//     one code path and blow up at 3am.
//  2. Load() FAILS the process on a bad or missing production setting rather
//     than starting with a weak default. A server that refuses to boot with an
//     empty JWT secret is far better than one that boots and signs tokens
//     anybody can forge.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Environment names.
const (
	EnvDevelopment = "development"
	EnvStaging     = "staging"
	EnvProduction  = "production"
	EnvTest        = "test"
)

// Config is the whole application configuration.
type Config struct {
	App       App
	Server    Server
	Database  Database
	Redis     Redis
	Auth      Auth
	OTP       OTP
	Security  Security
	RateLimit RateLimit
	SMS       SMS
	AI        AI
	Payment   Payment
	Upload    Upload
	Jobs      Jobs
	Log       Log
}

// App holds identity and environment.
type App struct {
	Name     string
	Env      string
	Version  string
	BaseURL  string // public URL, used in emails and payment callbacks
	Timezone string // e.g. Asia/Dhaka — all analytics day boundaries use it
	Currency string // ISO code, e.g. BDT
	Locale   string // default language for messages: "en" | "bn"
	Location *time.Location
}

// IsProduction reports whether we are running for real users.
func (a App) IsProduction() bool { return a.Env == EnvProduction }

// IsDevelopment reports whether developer conveniences may be enabled.
func (a App) IsDevelopment() bool { return a.Env == EnvDevelopment || a.Env == EnvTest }

// Server holds HTTP listener settings.
type Server struct {
	Host              string
	Port              string
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxHeaderBytes    int
	MaxBodyBytes      int64 // request body cap, enforced by middleware
	TrustedProxies    []string
}

// Addr is the listen address.
func (s Server) Addr() string { return s.Host + ":" + s.Port }

// Database holds the Postgres DSN and pool tuning.
type Database struct {
	URL              string
	MaxConns         int32
	MinConns         int32
	MaxConnLifetime  time.Duration
	MaxConnIdleTime  time.Duration
	ConnectTimeout   time.Duration
	StatementTimeout time.Duration
	AutoMigrate      bool
}

// Redis holds cache/rate-limit connection settings.
type Redis struct {
	URL          string
	PoolSize     int
	MinIdleConns int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	KeyPrefix    string
	// FailOpen decides what happens when Redis is down. false (the default)
	// means rate limiting rejects requests it cannot count — the safe choice
	// for auth endpoints. Cache reads always fail open regardless.
	FailOpen bool
}

// Auth holds token settings. Access and refresh tokens are signed with
// DIFFERENT secrets, so a leaked access-token secret cannot mint refresh
// tokens (which are long-lived and would otherwise mean permanent takeover).
type Auth struct {
	JWTSecret          string
	RefreshSecret      string
	Issuer             string
	AccessTokenTTL     time.Duration
	RefreshTokenTTL    time.Duration
	MaxSessionsPerUser int
	BcryptCost         int
	MaxLoginAttempts   int
	LoginLockWindow    time.Duration
	PasswordResetTTL   time.Duration
	RequirePhoneVerify bool
}

// OTP holds one-time-code policy.
type OTP struct {
	Secret          string
	Length          int
	TTL             time.Duration
	MaxPerWindow    int
	Window          time.Duration
	MaxVerifyTries  int
	ResendCooldown  time.Duration
	ExposeInDevMode bool // return the code in the API response outside production
}

// Security holds CORS and hardening settings.
type Security struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	CORSMaxAge       time.Duration
	HSTSMaxAge       time.Duration
	EnableHSTS       bool
	EnableSwagger    bool
	AdminAPIKey      string // guards the /admin surface in addition to the role check
	EncryptionKey    string // 32 bytes, for at-rest encryption of sensitive columns
}

// RateLimit holds the default buckets. Individual routes override these.
type RateLimit struct {
	Enabled       bool
	GlobalPerMin  int
	AuthPerMin    int
	WritePerMin   int
	AIPerHour     int
	BurstMultiple int
}

// SMS holds the OTP delivery provider.
type SMS struct {
	Provider  string // "log" | "twilio" | "bulksms" | "ssl"
	APIKey    string
	APISecret string
	SenderID  string
	BaseURL   string
	Timeout   time.Duration
}

// AI holds the model provider used by the insight and forecast modules.
type AI struct {
	Provider       string // "anthropic" | "openai" | "local" | "disabled"
	APIKey         string
	Model          string
	FallbackModel  string
	MaxTokens      int
	Timeout        time.Duration
	FreeQuota      int // lifetime free predictions for a new account
	CacheTTL       time.Duration
	MonthlyCostCap int // safety valve, in credits, per user per month
}

// Payment holds gateway settings for subscriptions and credit packs.
type Payment struct {
	Provider      string // "manual" | "bkash" | "sslcommerz" | "stripe"
	MerchantID    string
	APIKey        string
	APISecret     string
	BaseURL       string
	WebhookSecret string
	SuccessURL    string
	CancelURL     string
	Timeout       time.Duration
	SandboxMode   bool
}

// Upload holds attachment/avatar storage settings.
type Upload struct {
	Driver       string // "local" | "s3"
	LocalPath    string
	PublicPrefix string
	MaxSizeBytes int64
	AllowedTypes []string
	S3Bucket     string
	S3Region     string
	S3Endpoint   string
	S3AccessKey  string
	S3SecretKey  string
}

// Jobs holds background worker scheduling.
type Jobs struct {
	Enabled           bool
	RecurringPostCron string
	DailyDigestCron   string
	MonthlyReportCron string
	CleanupCron       string
	SubscriptionCron  string
	MaxConcurrent     int
}

// Log holds logging settings.
type Log struct {
	Level       string
	Format      string
	SlowQueryMS int64
	LogRequests bool
}

// Load reads .env (when present) and the process environment, validates
// everything, and returns the config or a descriptive error listing every
// problem at once — so you fix all of them in one pass, not one per restart.
func Load() (Config, error) {
	_ = godotenv.Load() // absent .env is fine; real deployments use real env vars

	env := strings.ToLower(str("APP_ENV", EnvDevelopment))
	prod := env == EnvProduction

	tz := str("APP_TIMEZONE", "Asia/Dhaka")
	loc, locErr := time.LoadLocation(tz)
	if locErr != nil {
		loc = time.UTC
	}

	c := Config{
		App: App{
			Name:     str("APP_NAME", "Hisabji API TEST"),
			Env:      env,
			Version:  str("APP_VERSION", "1.0.0"),
			BaseURL:  strings.TrimSuffix(str("APP_BASE_URL", "http://localhost:8080"), "/"),
			Timezone: tz,
			Currency: strings.ToUpper(str("APP_CURRENCY", "BDT")),
			Locale:   strings.ToLower(str("APP_LOCALE", "en")),
			Location: loc,
		},
		Server: Server{
			Host:              str("HOST", "0.0.0.0"),
			Port:              str("PORT", "8080"),
			ReadTimeout:       dur("SERVER_READ_TIMEOUT", 15*time.Second),
			ReadHeaderTimeout: dur("SERVER_READ_HEADER_TIMEOUT", 5*time.Second),
			WriteTimeout:      dur("SERVER_WRITE_TIMEOUT", 30*time.Second),
			IdleTimeout:       dur("SERVER_IDLE_TIMEOUT", 90*time.Second),
			ShutdownTimeout:   dur("SERVER_SHUTDOWN_TIMEOUT", 20*time.Second),
			MaxHeaderBytes:    num("SERVER_MAX_HEADER_BYTES", 1<<20),
			MaxBodyBytes:      int64(num("SERVER_MAX_BODY_BYTES", 2<<20)), // 2 MiB
			TrustedProxies:    list("TRUSTED_PROXIES", ""),
		},
		Database: Database{
			URL:              os.Getenv("DATABASE_URL"),
			MaxConns:         int32(num("DB_MAX_CONNS", 20)),
			MinConns:         int32(num("DB_MIN_CONNS", 2)),
			MaxConnLifetime:  dur("DB_MAX_CONN_LIFETIME", time.Hour),
			MaxConnIdleTime:  dur("DB_MAX_CONN_IDLE_TIME", 30*time.Minute),
			ConnectTimeout:   dur("DB_CONNECT_TIMEOUT", 10*time.Second),
			StatementTimeout: dur("DB_STATEMENT_TIMEOUT", 15*time.Second),
			AutoMigrate:      boolean("DB_AUTO_MIGRATE", true),
		},
		Redis: Redis{
			URL:          str("REDIS_URL", "redis://localhost:6379/0"),
			PoolSize:     num("REDIS_POOL_SIZE", 20),
			MinIdleConns: num("REDIS_MIN_IDLE_CONNS", 2),
			DialTimeout:  dur("REDIS_DIAL_TIMEOUT", 5*time.Second),
			ReadTimeout:  dur("REDIS_READ_TIMEOUT", 3*time.Second),
			WriteTimeout: dur("REDIS_WRITE_TIMEOUT", 3*time.Second),
			KeyPrefix:    str("REDIS_KEY_PREFIX", "hisabji"),
			FailOpen:     boolean("REDIS_FAIL_OPEN", false),
		},
		Auth: Auth{
			JWTSecret:          os.Getenv("JWT_SECRET"),
			RefreshSecret:      os.Getenv("REFRESH_SECRET"),
			Issuer:             str("JWT_ISSUER", "hisabji.com"),
			AccessTokenTTL:     dur("ACCESS_TOKEN_TTL", 30*time.Minute),
			RefreshTokenTTL:    dur("REFRESH_TOKEN_TTL", 30*24*time.Hour),
			MaxSessionsPerUser: num("MAX_SESSIONS_PER_USER", 5),
			BcryptCost:         num("BCRYPT_COST", 12),
			MaxLoginAttempts:   num("MAX_LOGIN_ATTEMPTS", 5),
			LoginLockWindow:    dur("LOGIN_LOCK_WINDOW", 6*time.Hour),
			PasswordResetTTL:   dur("PASSWORD_RESET_TTL", 15*time.Minute),
			RequirePhoneVerify: boolean("REQUIRE_PHONE_VERIFY", true),
		},
		OTP: OTP{
			Secret:          os.Getenv("OTP_SECRET"),
			Length:          num("OTP_LENGTH", 6),
			TTL:             dur("OTP_TTL", 10*time.Minute),
			MaxPerWindow:    num("OTP_MAX_PER_WINDOW", 5),
			Window:          dur("OTP_WINDOW", 6*time.Hour),
			MaxVerifyTries:  num("OTP_MAX_VERIFY_TRIES", 5),
			ResendCooldown:  dur("OTP_RESEND_COOLDOWN", 60*time.Second),
			ExposeInDevMode: boolean("OTP_EXPOSE_IN_DEV", true),
		},
		Security: Security{
			AllowedOrigins:   list("CORS_ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:5173"),
			AllowedMethods:   list("CORS_ALLOWED_METHODS", "GET,POST,PUT,PATCH,DELETE,OPTIONS"),
			AllowedHeaders:   list("CORS_ALLOWED_HEADERS", "Authorization,Content-Type,X-Request-ID,X-Idempotency-Key,Accept-Language"),
			ExposedHeaders:   list("CORS_EXPOSED_HEADERS", "X-Request-ID,Retry-After,X-RateLimit-Remaining,X-RateLimit-Reset"),
			AllowCredentials: boolean("CORS_ALLOW_CREDENTIALS", true),
			CORSMaxAge:       dur("CORS_MAX_AGE", 12*time.Hour),
			HSTSMaxAge:       dur("HSTS_MAX_AGE", 365*24*time.Hour),
			EnableHSTS:       boolean("ENABLE_HSTS", prod),
			EnableSwagger:    boolean("ENABLE_SWAGGER", !prod),
			AdminAPIKey:      os.Getenv("ADMIN_API_KEY"),
			EncryptionKey:    os.Getenv("ENCRYPTION_KEY"),
		},
		RateLimit: RateLimit{
			Enabled:       boolean("RATE_LIMIT_ENABLED", true),
			GlobalPerMin:  num("RATE_LIMIT_GLOBAL_PER_MIN", 300),
			AuthPerMin:    num("RATE_LIMIT_AUTH_PER_MIN", 10),
			WritePerMin:   num("RATE_LIMIT_WRITE_PER_MIN", 60),
			AIPerHour:     num("RATE_LIMIT_AI_PER_HOUR", 30),
			BurstMultiple: num("RATE_LIMIT_BURST_MULTIPLE", 2),
		},
		SMS: SMS{
			Provider:  str("SMS_PROVIDER", "log"),
			APIKey:    os.Getenv("SMS_API_KEY"),
			APISecret: os.Getenv("SMS_API_SECRET"),
			SenderID:  str("SMS_SENDER_ID", "Hisabji"),
			BaseURL:   os.Getenv("SMS_BASE_URL"),
			Timeout:   dur("SMS_TIMEOUT", 10*time.Second),
		},
		AI: AI{
			Provider:       str("AI_PROVIDER", "disabled"),
			APIKey:         os.Getenv("AI_API_KEY"),
			Model:          str("AI_MODEL", "claude-sonnet-5"),
			FallbackModel:  str("AI_FALLBACK_MODEL", "claude-haiku-4-5-20251001"),
			MaxTokens:      num("AI_MAX_TOKENS", 2048),
			Timeout:        dur("AI_TIMEOUT", 60*time.Second),
			FreeQuota:      num("AI_FREE_QUOTA", 3),
			CacheTTL:       dur("AI_CACHE_TTL", 6*time.Hour),
			MonthlyCostCap: num("AI_MONTHLY_COST_CAP", 2000),
		},
		Payment: Payment{
			Provider:      str("PAYMENT_PROVIDER", "manual"),
			MerchantID:    os.Getenv("PAYMENT_MERCHANT_ID"),
			APIKey:        os.Getenv("PAYMENT_API_KEY"),
			APISecret:     os.Getenv("PAYMENT_API_SECRET"),
			BaseURL:       os.Getenv("PAYMENT_BASE_URL"),
			WebhookSecret: os.Getenv("PAYMENT_WEBHOOK_SECRET"),
			SuccessURL:    str("PAYMENT_SUCCESS_URL", "hisabji://payment/success"),
			CancelURL:     str("PAYMENT_CANCEL_URL", "hisabji://payment/cancel"),
			Timeout:       dur("PAYMENT_TIMEOUT", 30*time.Second),
			SandboxMode:   boolean("PAYMENT_SANDBOX", !prod),
		},
		Upload: Upload{
			Driver:       str("UPLOAD_DRIVER", "local"),
			LocalPath:    str("UPLOAD_LOCAL_PATH", "./storage/uploads"),
			PublicPrefix: str("UPLOAD_PUBLIC_PREFIX", "/static/uploads"),
			MaxSizeBytes: int64(num("UPLOAD_MAX_SIZE_BYTES", 5<<20)),
			AllowedTypes: list("UPLOAD_ALLOWED_TYPES", "image/jpeg,image/png,image/webp,application/pdf"),
			S3Bucket:     os.Getenv("S3_BUCKET"),
			S3Region:     os.Getenv("S3_REGION"),
			S3Endpoint:   os.Getenv("S3_ENDPOINT"),
			S3AccessKey:  os.Getenv("S3_ACCESS_KEY"),
			S3SecretKey:  os.Getenv("S3_SECRET_KEY"),
		},
		Jobs: Jobs{
			Enabled:           boolean("JOBS_ENABLED", true),
			RecurringPostCron: str("JOBS_RECURRING_CRON", "0 5 * * *"),
			DailyDigestCron:   str("JOBS_DAILY_DIGEST_CRON", "0 21 * * *"),
			MonthlyReportCron: str("JOBS_MONTHLY_REPORT_CRON", "0 6 1 * *"),
			CleanupCron:       str("JOBS_CLEANUP_CRON", "0 3 * * *"),
			SubscriptionCron:  str("JOBS_SUBSCRIPTION_CRON", "0 * * * *"),
			MaxConcurrent:     num("JOBS_MAX_CONCURRENT", 4),
		},
		Log: Log{
			Level:       str("LOG_LEVEL", ifElse(prod, "info", "debug")),
			Format:      str("LOG_FORMAT", ifElse(prod, "json", "text")),
			SlowQueryMS: int64(num("LOG_SLOW_QUERY_MS", 500)),
			LogRequests: boolean("LOG_REQUESTS", true),
		},
	}

	if err := c.validate(locErr); err != nil {
		return c, err
	}
	c.applyDevelopmentFallbacks()
	return c, nil
}

// validate collects every problem before returning, so one restart tells you
// everything that is wrong.
func (c *Config) validate(locErr error) error {
	var problems []string

	add := func(format string, args ...any) {
		problems = append(problems, "  - "+fmt.Sprintf(format, args...))
	}

	switch c.App.Env {
	case EnvDevelopment, EnvStaging, EnvProduction, EnvTest:
	default:
		add("APP_ENV=%q is not one of development, staging, production, test", c.App.Env)
	}

	if locErr != nil {
		add("APP_TIMEZONE=%q is not a valid IANA timezone (falling back to UTC would silently shift every daily total)", c.App.Timezone)
	}

	if c.Database.URL == "" {
		add("DATABASE_URL is required (copy .env.example to .env)")
	} else if _, err := url.Parse(c.Database.URL); err != nil {
		add("DATABASE_URL is not a valid connection string: %v", err)
	}

	if c.Redis.URL == "" {
		add("REDIS_URL is required")
	}

	if p, err := strconv.Atoi(c.Server.Port); err != nil || p < 1 || p > 65535 {
		add("PORT=%q is not a valid TCP port", c.Server.Port)
	}

	if c.Auth.BcryptCost < 10 || c.Auth.BcryptCost > 15 {
		add("BCRYPT_COST=%d is outside the safe range 10-15", c.Auth.BcryptCost)
	}

	if c.Auth.AccessTokenTTL >= c.Auth.RefreshTokenTTL {
		add("ACCESS_TOKEN_TTL must be shorter than REFRESH_TOKEN_TTL, otherwise refreshing is pointless")
	}

	if c.OTP.Length < 4 || c.OTP.Length > 8 {
		add("OTP_LENGTH=%d is outside the sensible range 4-8", c.OTP.Length)
	}

	if c.Database.MinConns > c.Database.MaxConns {
		add("DB_MIN_CONNS (%d) cannot exceed DB_MAX_CONNS (%d)", c.Database.MinConns, c.Database.MaxConns)
	}

	// Production-only hard requirements. These are the settings where a
	// default would be a security hole rather than a convenience.
	if c.App.IsProduction() {
		if len(c.Auth.JWTSecret) < 32 {
			add("JWT_SECRET must be set to at least 32 random characters in production")
		}
		if len(c.Auth.RefreshSecret) < 32 {
			add("REFRESH_SECRET must be set to at least 32 random characters in production, and must differ from JWT_SECRET")
		}
		if c.Auth.JWTSecret != "" && c.Auth.JWTSecret == c.Auth.RefreshSecret {
			add("JWT_SECRET and REFRESH_SECRET must be different, so a leaked access secret cannot mint refresh tokens")
		}
		if len(c.OTP.Secret) < 32 {
			add("OTP_SECRET must be set to at least 32 random characters in production")
		}
		if isWeakSecret(c.Auth.JWTSecret) {
			add("JWT_SECRET still contains a placeholder value; generate a real one with: openssl rand -base64 48")
		}
		for _, o := range c.Security.AllowedOrigins {
			if o == "*" {
				add("CORS_ALLOWED_ORIGINS must not be * in production; list your real frontend origins")
			}
		}
		if c.SMS.Provider == "log" {
			add("SMS_PROVIDER=log would print OTP codes to the log instead of sending them; configure a real provider")
		}
		if c.Security.EncryptionKey != "" && len(c.Security.EncryptionKey) != 32 {
			add("ENCRYPTION_KEY must be exactly 32 bytes when set")
		}
		if !strings.HasPrefix(c.App.BaseURL, "https://") {
			add("APP_BASE_URL should be an https:// URL in production")
		}
	}

	if len(problems) > 0 {
		return errors.New("configuration is invalid:\n" + strings.Join(problems, "\n"))
	}
	return nil
}

// applyDevelopmentFallbacks fills in throwaway secrets so a new developer can
// run the server immediately after cloning. validate() has already guaranteed
// this never happens in production.
func (c *Config) applyDevelopmentFallbacks() {
	if c.Auth.JWTSecret == "" {
		c.Auth.JWTSecret = "development-only-access-secret-change-me-please"
	}
	if c.Auth.RefreshSecret == "" {
		c.Auth.RefreshSecret = "development-only-refresh-secret-change-me-please"
	}
	if c.OTP.Secret == "" {
		c.OTP.Secret = "development-only-otp-secret-change-me-please"
	}
	if c.App.IsDevelopment() && c.Auth.BcryptCost > 10 {
		// A cost of 12 adds ~250ms to every login; painful while iterating.
		c.Auth.BcryptCost = 10
	}
}

func isWeakSecret(s string) bool {
	l := strings.ToLower(s)
	for _, bad := range []string{"replace", "change-me", "changeme", "secret", "password", "example", "your-"} {
		if strings.Contains(l, bad) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// typed env readers
// ---------------------------------------------------------------------------

func str(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func num(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func boolean(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

// dur accepts both a Go duration ("30m", "1h30m") and a plain number of
// seconds ("1800"), because both show up in real .env files.
func dur(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	return fallback
}

func list(key, fallback string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		v = fallback
	}
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func ifElse(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
