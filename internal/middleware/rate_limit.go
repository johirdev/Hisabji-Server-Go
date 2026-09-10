package middleware

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/config"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/contextx"
	"github.com/johirdev/Hisabji-Server/internal/core/logger"
	"github.com/johirdev/Hisabji-Server/internal/core/response"
	"github.com/redis/go-redis/v9"
)

// slidingWindow implements a sliding-window counter in Lua, so the whole
// check-and-increment is one atomic round trip.
//
// Why not a plain INCR with EXPIRE (the usual first attempt)? That is a FIXED
// window, and it allows double the intended rate at a window boundary: 10
// requests at 11:59:59 and 10 more at 12:00:00 is 20 in one second against a
// "10 per minute" limit. The sliding window weights the previous window by how
// much of the current one has elapsed, which removes that burst without the
// memory cost of storing every request timestamp.
var slidingWindow = redis.NewScript(`
local base    = KEYS[1]
local now     = tonumber(ARGV[1])
local window  = tonumber(ARGV[2])
local limit   = tonumber(ARGV[3])

local slot     = math.floor(now / window)
local key_cur  = base .. ':' .. slot
local key_prev = base .. ':' .. (slot - 1)

local prev = tonumber(redis.call('GET', key_prev) or '0')
local cur  = tonumber(redis.call('GET', key_cur)  or '0')

local elapsed = now % window
local weight  = (window - elapsed) / window
local used    = prev * weight + cur
local retry   = math.ceil((window - elapsed) / 1000)

if used >= limit then
  return {0, 0, retry}
end

cur = redis.call('INCR', key_cur)
if cur == 1 then
  redis.call('PEXPIRE', key_cur, window * 2)
end

local remaining = math.floor(limit - (prev * weight + cur))
if remaining < 0 then remaining = 0 end
return {1, remaining, retry}
`)

// Limiter applies rate limits. One instance is shared by every route.
type Limiter struct {
	redis    *redis.Client
	prefix   string
	failOpen bool
	enabled  bool
}

// NewLimiter builds the shared limiter.
func NewLimiter(client *redis.Client, cfg config.Redis, enabled bool) *Limiter {
	return &Limiter{
		redis:    client,
		prefix:   cfg.KeyPrefix + ":rl",
		failOpen: cfg.FailOpen,
		enabled:  enabled,
	}
}

// Scope decides what a limit is counted against.
type Scope int

const (
	// ScopeIP counts per client IP. Correct for endpoints reachable before
	// sign-in (register, login, OTP) — there is no user to count yet.
	ScopeIP Scope = iota
	// ScopeUser counts per authenticated user, falling back to IP for
	// anonymous callers. Correct for everything behind auth: one user on a
	// shared office IP should not throttle their colleagues.
	ScopeUser
	// ScopeGlobal counts every caller against one shared bucket. Use it only
	// to protect a genuinely global resource, such as the AI provider quota.
	ScopeGlobal
)

// Rule describes one limit.
type Rule struct {
	Name   string        // appears in the Redis key and the error message
	Limit  int           // permitted requests per window
	Window time.Duration // the window length
	Scope  Scope
}

// Limit returns middleware enforcing one rule.
//
//	auth.POST("/login", limiter.Limit(middleware.Rule{
//	    Name: "auth:login", Limit: 10, Window: time.Minute, Scope: middleware.ScopeIP,
//	}), handler.H(h.Login))
func (l *Limiter) Limit(rule Rule) gin.HandlerFunc {
	if rule.Limit <= 0 {
		rule.Limit = 60
	}
	if rule.Window <= 0 {
		rule.Window = time.Minute
	}

	return func(c *gin.Context) {
		if !l.enabled || l.redis == nil {
			c.Next()
			return
		}

		key := l.key(c, rule)
		allowed, remaining, retryAfter, err := l.check(c.Request.Context(), key, rule)

		if err != nil {
			// Redis is down. Now we must choose between letting traffic through
			// unmetered and rejecting everything. The configured default is
			// fail-CLOSED, because the endpoints that most need a limiter are
			// exactly the ones an attacker targets when infrastructure wobbles.
			logger.FromGin(c).Error("rate limiter unavailable",
				"rule", rule.Name, "error", err.Error())
			if l.failOpen {
				c.Next()
				return
			}
			response.Fail(c, apperr.Unavailable(
				"The service is temporarily unable to process requests. Please try again shortly.").
				WithCause(err))
			return
		}

		c.Header("X-RateLimit-Limit", strconv.Itoa(rule.Limit))
		c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))
		c.Header("X-RateLimit-Reset", strconv.Itoa(retryAfter))

		if !allowed {
			response.Fail(c, apperr.RateLimited(
				fmt.Sprintf("Too many requests. Please wait %s and try again.", humanSeconds(retryAfter)),
				retryAfter).
				WithDetails(map[string]any{
					"retry_after_seconds": retryAfter,
					"limit":               rule.Limit,
					"window_seconds":      int(rule.Window.Seconds()),
				}).
				WithHint("Slow down the request rate; the limit resets automatically."))
			return
		}

		c.Next()
	}
}

func (l *Limiter) check(ctx context.Context, key string, rule Rule) (allowed bool, remaining, retryAfter int, err error) {
	res, err := slidingWindow.Run(ctx, l.redis, []string{key},
		time.Now().UnixMilli(), rule.Window.Milliseconds(), rule.Limit).Slice()
	if err != nil {
		return false, 0, 0, err
	}
	if len(res) < 3 {
		return false, 0, 0, fmt.Errorf("rate limiter returned %d values, expected 3", len(res))
	}

	toInt := func(v any) int {
		if n, ok := v.(int64); ok {
			return int(n)
		}
		return 0
	}
	return toInt(res[0]) == 1, toInt(res[1]), toInt(res[2]), nil
}

func (l *Limiter) key(c *gin.Context, rule Rule) string {
	switch rule.Scope {
	case ScopeGlobal:
		return l.prefix + ":" + rule.Name + ":global"
	case ScopeUser:
		if uid := contextx.UserID(c); uid != "" {
			return l.prefix + ":" + rule.Name + ":u:" + uid
		}
		return l.prefix + ":" + rule.Name + ":ip:" + c.ClientIP()
	default:
		return l.prefix + ":" + rule.Name + ":ip:" + c.ClientIP()
	}
}

// Reset clears one bucket. Used by tests and by the admin "unblock this user"
// action.
func (l *Limiter) Reset(ctx context.Context, name, identifier string) error {
	if l.redis == nil {
		return nil
	}
	pattern := l.prefix + ":" + name + ":" + identifier + ":*"
	iter := l.redis.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		if err := l.redis.Del(ctx, iter.Val()).Err(); err != nil {
			return err
		}
	}
	return iter.Err()
}

// ---------------------------------------------------------------------------
// Ready-made rules, so route files read as policy rather than arithmetic.
// ---------------------------------------------------------------------------

// GlobalRule is the blanket per-IP limit applied to the whole API.
func GlobalRule(cfg config.RateLimit) Rule {
	return Rule{Name: "global", Limit: cfg.GlobalPerMin, Window: time.Minute, Scope: ScopeIP}
}

// AuthRule protects an unauthenticated endpoint (login, register, OTP).
func AuthRule(name string, cfg config.RateLimit) Rule {
	return Rule{Name: "auth:" + name, Limit: cfg.AuthPerMin, Window: time.Minute, Scope: ScopeIP}
}

// WriteRule protects an authenticated mutation.
func WriteRule(name string, cfg config.RateLimit) Rule {
	return Rule{Name: "write:" + name, Limit: cfg.WritePerMin, Window: time.Minute, Scope: ScopeUser}
}

// AIRule protects the expensive model-backed endpoints. Hourly, per user,
// because the cost we are protecting is money rather than CPU.
func AIRule(name string, cfg config.RateLimit) Rule {
	return Rule{Name: "ai:" + name, Limit: cfg.AIPerHour, Window: time.Hour, Scope: ScopeUser}
}

// StrictRule is for the few actions that must be nearly impossible to abuse:
// password reset, phone change, account deletion.
func StrictRule(name string, limit int, window time.Duration) Rule {
	return Rule{Name: "strict:" + name, Limit: limit, Window: window, Scope: ScopeUser}
}

func humanSeconds(s int) string {
	switch {
	case s <= 1:
		return "a moment"
	case s < 60:
		return strconv.Itoa(s) + " seconds"
	case s < 3600:
		return strconv.Itoa((s+59)/60) + " minutes"
	default:
		return strconv.Itoa((s+3599)/3600) + " hours"
	}
}
