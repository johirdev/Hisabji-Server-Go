package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/core/contextx"
	"github.com/johirdev/Hisabji-Server/internal/core/logger"
)

// skipLogPaths are polled constantly by Docker, Kubernetes and load balancers.
// Logging them buries real traffic in noise and costs money in log storage.
var skipLogPaths = map[string]bool{
	"/health":       true,
	"/health/live":  true,
	"/health/ready": true,
	"/favicon.ico":  true,
	"/metrics":      true,
}

// Logger writes one structured line per request, after it completes.
//
// It logs the query string but never the request body: bodies here contain
// passwords, OTP codes and financial detail, none of which belongs in a log
// aggregator that a wider team can read.
func Logger(enabled bool, slowMS int64) gin.HandlerFunc {
	if !enabled {
		return func(c *gin.Context) { c.Next() }
	}
	if slowMS <= 0 {
		slowMS = 500
	}

	return func(c *gin.Context) {
		if skipLogPaths[c.Request.URL.Path] {
			c.Next()
			return
		}

		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		attrs := []any{
			"status", status,
			"method", c.Request.Method,
			"path", path,
			"latency_ms", latency.Milliseconds(),
			"ip", c.ClientIP(),
			"size", c.Writer.Size(),
		}
		if raw != "" {
			attrs = append(attrs, "query", raw)
		}
		if uid := contextx.UserID(c); uid != "" {
			attrs = append(attrs, "user_id", uid)
		}
		if ua := c.Request.UserAgent(); ua != "" {
			attrs = append(attrs, "user_agent", truncate(ua, 120))
		}
		// gin collects handler errors here; surface the first one.
		if len(c.Errors) > 0 {
			attrs = append(attrs, "error", c.Errors.String())
		}

		log := logger.FromGin(c)
		switch {
		case status >= 500:
			log.Error("request", attrs...)
		case status >= 400:
			log.Warn("request", attrs...)
		case latency.Milliseconds() >= slowMS:
			// A slow 200 is a future 500. Flag it while it is still cheap to fix.
			log.Warn("slow request", append(attrs, "threshold_ms", slowMS)...)
		default:
			log.Info("request", attrs...)
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
