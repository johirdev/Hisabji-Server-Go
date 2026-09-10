package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/config"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/response"
)

// SecurityHeaders sets the response headers that harden any browser client.
//
// This is a JSON API, so several of these look redundant — until somebody
// opens an API URL directly in a browser, or an error page renders reflected
// input. They cost nothing and remove whole classes of attack.
func SecurityHeaders(cfg config.Security, isProduction bool) gin.HandlerFunc {
	hstsValue := "max-age=" + strconv.Itoa(int(cfg.HSTSMaxAge.Seconds())) + "; includeSubDomains"

	return func(c *gin.Context) {
		h := c.Writer.Header()

		// Never let a browser second-guess our Content-Type. Without this a
		// JSON response containing attacker-controlled text can be sniffed as
		// HTML and executed.
		h.Set("X-Content-Type-Options", "nosniff")

		// This API is never meant to be framed.
		h.Set("X-Frame-Options", "DENY")

		// A JSON API needs no scripts, styles, images or frames of its own.
		// Denying everything means an HTML error page could not execute script
		// even if one were somehow returned.
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")

		// Do not leak our URLs (which contain resource ids) to third parties.
		h.Set("Referrer-Policy", "no-referrer")

		// Deny device APIs we never use.
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=(), usb=()")

		// Financial data must not sit in a shared or browser cache.
		h.Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
		h.Set("Pragma", "no-cache")

		// Do not advertise the stack we run.
		h.Set("X-Powered-By", "")
		h.Del("X-Powered-By")

		// HSTS is only meaningful over TLS, and setting it in development would
		// pin localhost to https in the developer's browser — a genuinely
		// annoying thing to undo.
		if cfg.EnableHSTS && isProduction {
			h.Set("Strict-Transport-Security", hstsValue)
		}

		c.Next()
	}
}

// CORS answers preflights and adds the cross-origin headers.
//
// The origin allowlist is exact-match, never a wildcard, because
// AllowCredentials with "*" is both forbidden by the spec and a real
// vulnerability: it would let any website read a signed-in user's data.
func CORS(cfg config.Security) gin.HandlerFunc {
	allowed := make(map[string]bool, len(cfg.AllowedOrigins))
	allowAny := false
	for _, o := range cfg.AllowedOrigins {
		o = strings.TrimSuffix(strings.TrimSpace(o), "/")
		if o == "*" {
			allowAny = true
			continue
		}
		allowed[strings.ToLower(o)] = true
	}

	methods := strings.Join(cfg.AllowedMethods, ", ")
	headers := strings.Join(cfg.AllowedHeaders, ", ")
	exposed := strings.Join(cfg.ExposedHeaders, ", ")
	maxAge := strconv.Itoa(int(cfg.CORSMaxAge.Seconds()))

	return func(c *gin.Context) {
		origin := strings.TrimSuffix(c.GetHeader("Origin"), "/")

		// No Origin header means a native app, curl or a server-to-server call.
		// CORS is a browser mechanism; there is nothing to add.
		if origin == "" {
			c.Next()
			return
		}

		permitted := allowAny || allowed[strings.ToLower(origin)]

		if permitted {
			h := c.Writer.Header()
			if allowAny && !cfg.AllowCredentials {
				h.Set("Access-Control-Allow-Origin", "*")
			} else {
				h.Set("Access-Control-Allow-Origin", origin)
				// Caches must not serve one origin's response to another.
				h.Add("Vary", "Origin")
			}
			if cfg.AllowCredentials {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
			if exposed != "" {
				h.Set("Access-Control-Expose-Headers", exposed)
			}
		}

		if c.Request.Method == http.MethodOptions {
			if !permitted {
				// Fail the preflight explicitly rather than letting the browser
				// report a confusing generic CORS error.
				response.Fail(c, apperr.Forbidden(
					"This origin is not allowed to call the API.").
					WithDetails(map[string]any{"origin": origin}).
					WithHint("Add the origin to CORS_ALLOWED_ORIGINS."))
				return
			}
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Methods", methods)
			h.Set("Access-Control-Allow-Headers", headers)
			h.Set("Access-Control-Max-Age", maxAge)
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// BodyLimit rejects oversized request bodies before they are read into memory.
//
// Without it, one client streaming a 2 GB body can exhaust the server's memory.
// MaxBytesReader also protects against a lying Content-Length.
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	if maxBytes <= 0 {
		maxBytes = 2 << 20
	}
	return func(c *gin.Context) {
		// Cheap rejection when the client declares the size honestly.
		if c.Request.ContentLength > maxBytes {
			response.Fail(c, apperr.New(http.StatusRequestEntityTooLarge,
				apperr.CodePayloadTooLarge,
				"The request body is too large.").
				WithDetails(map[string]any{
					"max_bytes":      maxBytes,
					"received_bytes": c.Request.ContentLength,
				}))
			return
		}
		// And the real enforcement, for a body that lies about its length.
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}

// NoCacheStatic is the opposite of SecurityHeaders' no-store rule, applied to
// the uploads route where cacheable, immutable files are served.
func NoCacheStatic(maxAgeSeconds int) gin.HandlerFunc {
	value := "public, max-age=" + strconv.Itoa(maxAgeSeconds)
	return func(c *gin.Context) {
		c.Writer.Header().Set("Cache-Control", value)
		c.Next()
	}
}
