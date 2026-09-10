// Package logger wraps log/slog with two things this app needs everywhere:
// JSON output in production (so Docker/Loki can parse it) and automatic
// request-id correlation.
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/core/contextx"
)

var base *slog.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

// Options configures the process-wide logger.
type Options struct {
	Level  string // "debug" | "info" | "warn" | "error"
	Format string // "json" | "text"
	Output io.Writer
}

// Init installs the process-wide logger. Call once from main().
func Init(opt Options) {
	out := opt.Output
	if out == nil {
		out = os.Stdout
	}
	var lvl slog.Level
	switch strings.ToLower(opt.Level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	handlerOpts := &slog.HandlerOptions{
		Level: lvl,
		// Redact anything that looks like a secret, defensively, in case a
		// caller ever logs a whole struct.
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			switch strings.ToLower(a.Key) {
			case "password", "password_hash", "token", "access_token", "refresh_token",
				"authorization", "secret", "jwt_secret", "otp", "code", "card", "pin":
				return slog.String(a.Key, "[REDACTED]")
			}
			return a
		},
	}

	var h slog.Handler
	if strings.ToLower(opt.Format) == "json" {
		h = slog.NewJSONHandler(out, handlerOpts)
	} else {
		h = slog.NewTextHandler(out, handlerOpts)
	}
	base = slog.New(h)
	slog.SetDefault(base)
}

// L returns the process logger.
func L() *slog.Logger { return base }

// FromGin returns a logger already tagged with this request's correlation id,
// user id, method and path — so every line you write is traceable.
func FromGin(c *gin.Context) *slog.Logger {
	if c == nil {
		return base
	}
	l := base
	if id := contextx.RequestID(c); id != "" {
		l = l.With("request_id", id)
	}
	if uid := contextx.UserID(c); uid != "" {
		l = l.With("user_id", uid)
	}
	return l.With("method", c.Request.Method, "path", c.FullPath())
}

// FromContext returns a logger tagged from a plain context (jobs, workers).
func FromContext(ctx context.Context) *slog.Logger {
	l := base
	if id := contextx.RequestIDFrom(ctx); id != "" {
		l = l.With("request_id", id)
	}
	if uid := contextx.UserIDFrom(ctx); uid != "" {
		l = l.With("user_id", uid)
	}
	return l
}

// Named returns a logger tagged with a subsystem name, e.g. logger.Named("jobs").
func Named(name string) *slog.Logger { return base.With("component", name) }
