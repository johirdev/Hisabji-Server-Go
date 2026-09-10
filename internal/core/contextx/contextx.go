// Package contextx centralises every key we stash on the gin context, so no
// handler ever has to guess a magic string like c.GetString("user_id").
package contextx

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

// Keys used with gin.Context.Set / Get.
const (
	KeyRequestID  = "ctx.request_id"
	KeyUserID     = "ctx.user_id"
	KeyUserRole   = "ctx.user_role"
	KeyUserPlan   = "ctx.user_plan"
	KeySessionID  = "ctx.session_id"
	KeyStartTime  = "ctx.start_time"
	KeyClientIP   = "ctx.client_ip"
	KeyUserAgent  = "ctx.user_agent"
	KeyIdempotent = "ctx.idempotency_key"
	KeyLocale     = "ctx.locale"
)

// Identity is the authenticated caller, resolved once by the auth middleware.
type Identity struct {
	UserID    string
	Role      string
	Plan      string
	SessionID string
}

// SetIdentity stores the authenticated caller on the request context.
func SetIdentity(c *gin.Context, id Identity) {
	c.Set(KeyUserID, id.UserID)
	c.Set(KeyUserRole, id.Role)
	c.Set(KeyUserPlan, id.Plan)
	c.Set(KeySessionID, id.SessionID)
}

// UserID returns the authenticated user id, or "" for anonymous callers.
func UserID(c *gin.Context) string { return c.GetString(KeyUserID) }

// Role returns the caller's role ("user", "admin", ...), or "" when anonymous.
func Role(c *gin.Context) string { return c.GetString(KeyUserRole) }

// Plan returns the caller's subscription plan code, or "" when anonymous.
func Plan(c *gin.Context) string { return c.GetString(KeyUserPlan) }

// SessionID returns the refresh-session id bound to the access token.
func SessionID(c *gin.Context) string { return c.GetString(KeySessionID) }

// RequestID returns the per-request correlation id set by middleware.
func RequestID(c *gin.Context) string { return c.GetString(KeyRequestID) }

// Locale returns the requested language ("en" | "bn"), defaulting to "en".
func Locale(c *gin.Context) string {
	if v := c.GetString(KeyLocale); v != "" {
		return v
	}
	return "en"
}

// StartTime returns when the request entered the server.
func StartTime(c *gin.Context) time.Time {
	if v, ok := c.Get(KeyStartTime); ok {
		if t, ok := v.(time.Time); ok {
			return t
		}
	}
	return time.Now()
}

// ---------------------------------------------------------------------------
// Plain context.Context variants — used by jobs and services that run outside
// an HTTP request (cron, workers) but still want correlated logs.
// ---------------------------------------------------------------------------

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyUserID
)

// WithRequestID returns a context carrying a correlation id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, id)
}

// RequestIDFrom reads a correlation id off a plain context.
func RequestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyRequestID).(string); ok {
		return v
	}
	return ""
}

// WithUserID returns a context carrying the acting user id.
func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyUserID, id)
}

// UserIDFrom reads the acting user id off a plain context.
func UserIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyUserID).(string); ok {
		return v
	}
	return ""
}
