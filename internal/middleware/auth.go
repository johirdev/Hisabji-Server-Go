package middleware

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/contextx"
	"github.com/johirdev/Hisabji-Server/internal/core/logger"
	"github.com/johirdev/Hisabji-Server/internal/core/response"
	"github.com/johirdev/Hisabji-Server/internal/shared/jwt"
	"github.com/redis/go-redis/v9"
)

// Authenticator verifies access tokens.
//
// # THE REVOCATION PROBLEM, AND HOW THIS SOLVES IT
//
// A JWT is valid until it expires — that is what makes it fast (no database
// read per request) and also what makes "log out everywhere" hard. Checking a
// sessions row on every request would throw the benefit away.
//
// The compromise here: access tokens live 30 minutes, and Redis holds a small
// denylist written on logout, password change and theft detection. A denylist
// lookup is one sub-millisecond Redis GET, and entries expire on their own
// after the access-token TTL, so the set stays tiny.
//
// If Redis is unavailable the check fails OPEN: a signed, unexpired token is
// still honoured. Rejecting every request because a cache is down would turn a
// cache outage into a full outage, and the exposure window is bounded by the
// 30-minute token lifetime.
type Authenticator struct {
	issuer    *jwt.Issuer
	redis     *redis.Client
	prefix    string
	accessTTL time.Duration
}

// NewAuthenticator builds the shared authenticator.
func NewAuthenticator(issuer *jwt.Issuer, client *redis.Client, keyPrefix string) *Authenticator {
	return &Authenticator{
		issuer:    issuer,
		redis:     client,
		prefix:    keyPrefix + ":revoked",
		accessTTL: issuer.AccessTTL(),
	}
}

// Required rejects any request without a valid, unrevoked access token.
func (a *Authenticator) Required() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := a.authenticate(c)
		if err != nil {
			response.Fail(c, err)
			return
		}
		contextx.SetIdentity(c, contextx.Identity{
			UserID:    claims.UserID,
			Role:      claims.Role,
			Plan:      claims.Plan,
			SessionID: claims.SessionID,
		})
		c.Request = c.Request.WithContext(
			contextx.WithUserID(c.Request.Context(), claims.UserID))
		c.Next()
	}
}

// Optional attaches the identity when a valid token is present, and simply
// continues when it is not. Use it on endpoints that are public but richer for
// a signed-in user, such as the plan catalogue (which can mark the user's
// current plan).
func (a *Authenticator) Optional() gin.HandlerFunc {
	return func(c *gin.Context) {
		if extractToken(c) == "" {
			c.Next()
			return
		}
		claims, err := a.authenticate(c)
		if err != nil {
			// A bad token on an optional route is treated as anonymous rather
			// than as an error, so an expired token never breaks a public page.
			c.Next()
			return
		}
		contextx.SetIdentity(c, contextx.Identity{
			UserID:    claims.UserID,
			Role:      claims.Role,
			Plan:      claims.Plan,
			SessionID: claims.SessionID,
		})
		c.Next()
	}
}

// RequireRole allows only the listed roles through. Mount it AFTER Required().
func (a *Authenticator) RequireRole(roles ...string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[strings.ToLower(r)] = true
	}

	return func(c *gin.Context) {
		role := strings.ToLower(contextx.Role(c))
		if role == "" {
			response.Fail(c, apperr.Unauthorized(""))
			return
		}
		if !allowed[role] {
			// Log the attempt: a user probing admin routes is worth knowing about.
			logger.FromGin(c).Warn("role check failed",
				"required", roles, "actual", role)
			response.Fail(c, apperr.Forbidden(
				"This action requires a different account role."))
			return
		}
		c.Next()
	}
}

// RequireAdminKey adds a second factor to the admin surface: the caller must
// present a shared secret header as well as hold an admin role. It means a
// stolen admin password alone is not enough to reach destructive endpoints.
func (a *Authenticator) RequireAdminKey(expected string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if expected == "" {
			// Not configured: rely on the role check alone rather than locking
			// the operator out of their own admin API.
			c.Next()
			return
		}
		provided := c.GetHeader("X-Admin-Key")
		if len(provided) != len(expected) || subtleCompare(provided, expected) != 1 {
			logger.FromGin(c).Warn("admin key rejected", "ip", c.ClientIP())
			response.Fail(c, apperr.Forbidden("A valid admin key is required."))
			return
		}
		c.Next()
	}
}

func (a *Authenticator) authenticate(c *gin.Context) (*jwt.Claims, error) {
	raw := extractToken(c)
	if raw == "" {
		return nil, apperr.Unauthorized("You must be signed in to access this resource.").
			WithHint("Send the header: Authorization: Bearer <access_token>")
	}

	claims, err := a.issuer.ParseAccess(raw)
	if err != nil {
		return nil, err
	}

	revoked, err := a.isRevoked(c.Request.Context(), claims)
	if err != nil {
		// Fail open — see the type comment.
		logger.FromGin(c).Error("revocation check failed", "error", err.Error())
	} else if revoked {
		return nil, apperr.New(401, apperr.CodeTokenInvalid,
			"Your session has been signed out. Please sign in again.").
			WithHint("This happens after logging out, changing your password, or a security event.")
	}

	return claims, nil
}

// isRevoked checks both the per-session denylist and the per-user cutoff,
// which is what "sign out of all devices" and "password changed" write.
func (a *Authenticator) isRevoked(ctx context.Context, claims *jwt.Claims) (bool, error) {
	if a.redis == nil {
		return false, nil
	}

	keys := make([]string, 0, 2)
	if claims.SessionID != "" {
		keys = append(keys, a.prefix+":sid:"+claims.SessionID)
	}
	keys = append(keys, a.prefix+":user:"+claims.UserID)

	values, err := a.redis.MGet(ctx, keys...).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return false, err
	}

	for i, v := range values {
		if v == nil {
			continue
		}
		// The session key's mere presence revokes it.
		if strings.Contains(keys[i], ":sid:") {
			return true, nil
		}
		// The user key holds a unix timestamp: every token issued before it is
		// invalid. That is what makes one write revoke every device at once.
		cutoff, ok := v.(string)
		if !ok {
			continue
		}
		ts, convErr := strconv.ParseInt(cutoff, 10, 64)
		if convErr != nil {
			continue
		}
		if claims.IssuedAt != nil && claims.IssuedAt.Unix() < ts {
			return true, nil
		}
	}
	return false, nil
}

// RevokeSession denylists one session for the remaining access-token lifetime.
func (a *Authenticator) RevokeSession(ctx context.Context, sessionID string) error {
	if a.redis == nil || sessionID == "" {
		return nil
	}
	// The TTL only needs to outlive the longest access token that could still
	// carry this session id; after that the token is expired anyway.
	return a.redis.Set(ctx, a.prefix+":sid:"+sessionID, "1", a.accessTTL+time.Minute).Err()
}

// RevokeAllForUser invalidates every access token issued to a user before now.
func (a *Authenticator) RevokeAllForUser(ctx context.Context, userID string) error {
	if a.redis == nil || userID == "" {
		return nil
	}
	return a.redis.Set(ctx, a.prefix+":user:"+userID,
		strconv.FormatInt(time.Now().Unix(), 10), a.accessTTL+time.Minute).Err()
}

// extractToken reads the bearer token from the Authorization header, falling
// back to a query parameter for the download links a browser cannot add headers
// to. The query form is deliberately NOT accepted anywhere else.
func extractToken(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	if header != "" {
		const prefix = "bearer "
		if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
			return strings.TrimSpace(header[len(prefix):])
		}
		// Tolerate a bare token: a very common client mistake, and rejecting it
		// with a confusing 401 wastes an afternoon of somebody's integration.
		if !strings.Contains(header, " ") {
			return strings.TrimSpace(header)
		}
	}
	return ""
}

// subtleCompare is crypto/subtle.ConstantTimeCompare without importing it into
// every file that needs a header comparison.
func subtleCompare(a, b string) int {
	if len(a) != len(b) {
		return 0
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	if diff == 0 {
		return 1
	}
	return 0
}
