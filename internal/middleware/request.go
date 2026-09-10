// Package middleware holds the cross-cutting HTTP concerns: correlation ids,
// logging, panic recovery, CORS, security headers, body limits, authentication
// and rate limiting.
//
// Order matters, and the order used in routes.go is:
//
//	RequestID -> SecurityHeaders -> CORS -> BodyLimit -> Recovery -> Logger
//	          -> RateLimit -> Auth -> (route)
//
// RequestID first so everything downstream can log the id. Recovery before
// Logger so a panic still produces one access-log line. Auth last so an
// unauthenticated flood is rejected by the rate limiter before it costs a
// signature verification.
package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/core/contextx"
)

// HeaderRequestID is the correlation header, both accepted and returned.
const HeaderRequestID = "X-Request-ID"

// RequestID assigns every request a correlation id and echoes it back.
//
// When a user reports "it failed at 3:14pm", the id in their error response is
// the one string that finds the exact request in the logs.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(HeaderRequestID)

		// Accept a client-supplied id (mobile apps use it to stitch their own
		// traces together), but only after sanitising it — it ends up in log
		// lines and response headers, so it must not carry newlines or be
		// unbounded.
		if !validRequestID(id) {
			id = newRequestID()
		}

		c.Set(contextx.KeyRequestID, id)
		c.Set(contextx.KeyStartTime, time.Now())
		c.Header(HeaderRequestID, id)

		// Also put it on the request context so services and repositories that
		// only receive a context.Context can log with the same id.
		c.Request = c.Request.WithContext(contextx.WithRequestID(c.Request.Context(), id))

		c.Next()
	}
}

func validRequestID(id string) bool {
	if len(id) < 8 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			return false
		}
	}
	return true
}

func newRequestID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// Randomness failing is fatal for tokens but not for a log id; a
		// timestamp still correlates well enough to debug with.
		return "req-" + time.Now().UTC().Format("20060102150405.000000")
	}
	return hex.EncodeToString(b)
}

// Locale reads Accept-Language and stores "bn" or "en" for the response layer.
// Hisabji ships bilingual messages, and the client should not have to ask twice.
func Locale(fallback string) gin.HandlerFunc {
	if fallback == "" {
		fallback = "en"
	}
	return func(c *gin.Context) {
		locale := fallback

		// An explicit query parameter wins, for testing and for a user whose
		// device language differs from their app language setting.
		if v := strings.ToLower(c.Query("lang")); v == "bn" || v == "en" {
			locale = v
		} else if header := c.GetHeader("Accept-Language"); header != "" {
			// "bn-BD,bn;q=0.9,en;q=0.8" — first match wins.
			for _, part := range strings.Split(header, ",") {
				tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
				if strings.HasPrefix(tag, "bn") {
					locale = "bn"
					break
				}
				if strings.HasPrefix(tag, "en") {
					locale = "en"
					break
				}
			}
		}

		c.Set(contextx.KeyLocale, locale)
		c.Next()
	}
}
