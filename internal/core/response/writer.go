package response

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/contextx"
	"github.com/johirdev/Hisabji-Server/internal/core/logger"
)

// exposeInternals is flipped on in non-production so developers can see the
// real cause of a 500 in the response body. It is ALWAYS false in production.
var exposeInternals bool

// Configure wires environment-dependent behaviour. Call once from main().
func Configure(env string) { exposeInternals = env != "production" }

// Write renders a successful Result into the response.
func Write(c *gin.Context, r *Result) {
	if r == nil {
		r = OK(nil, "")
	}
	for k, v := range r.Headers {
		c.Header(k, v)
	}
	if r.Status == http.StatusNoContent {
		c.Status(http.StatusNoContent)
		return
	}
	status := r.Status
	if status == 0 {
		status = http.StatusOK
	}
	c.JSON(status, Envelope{
		Success:   true,
		Code:      def(r.Code, "OK"),
		Message:   def(r.Message, "Request completed successfully."),
		Data:      r.Data,
		Meta:      r.Meta,
		RequestID: contextx.RequestID(c),
		Timestamp: now(),
	})
}

// Fail renders any error into the standard envelope. This is the ONE place in
// the codebase that decides an HTTP status for an error, which is why handlers
// can simply `return nil, err`.
func Fail(c *gin.Context, err error) {
	if err == nil {
		return
	}

	appErr, ok := apperr.As(err)
	if !ok {
		// Not one of ours: translate what we can, otherwise treat as internal.
		appErr = apperr.Translate(err, "")
	}

	env := Envelope{
		Success:   false,
		Code:      string(appErr.Code),
		Message:   appErr.Message,
		Errors:    appErr.Fields,
		Hint:      appErr.Hint,
		Details:   appErr.Details,
		RequestID: contextx.RequestID(c),
		Timestamp: now(),
	}

	log := logger.FromGin(c)
	attrs := []any{
		"code", string(appErr.Code),
		"status", appErr.Status,
		"error", appErr.Error(),
	}

	switch {
	case appErr.Status >= 500:
		log.Error("request failed", attrs...)
		if exposeInternals && appErr.Cause() != nil {
			env.Details = map[string]any{"debug_cause": appErr.Cause().Error()}
		}
	case appErr.Status == http.StatusTooManyRequests:
		log.Warn("request throttled", attrs...)
	case appErr.Status >= 400:
		log.Debug("request rejected", attrs...)
	}

	// Retry-After is a real HTTP header, not just a body field.
	if d, ok := appErr.Details.(map[string]any); ok {
		if secs, ok := d["retry_after_seconds"].(int); ok && secs > 0 {
			c.Header("Retry-After", strconv.Itoa(secs))
		}
	}

	status := appErr.Status
	if status <= 0 {
		status = http.StatusInternalServerError
	}
	c.AbortWithStatusJSON(status, env)
}

// FailWith is a shorthand for building and writing an error in one line.
func FailWith(c *gin.Context, status int, code apperr.Code, message string) {
	Fail(c, apperr.New(status, code, message))
}

// NotFoundHandler is installed as gin's 404 route so unknown paths return the
// same envelope as everything else instead of gin's plain-text default.
func NotFoundHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		Fail(c, apperr.New(http.StatusNotFound, apperr.CodeRouteNotFound,
			"The requested endpoint does not exist.").
			WithDetails(map[string]any{"method": c.Request.Method, "path": c.Request.URL.Path}).
			WithHint("Check the API documentation for the correct path and version prefix."))
	}
}

// MethodNotAllowedHandler mirrors NotFoundHandler for 405s.
func MethodNotAllowedHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		Fail(c, apperr.New(http.StatusMethodNotAllowed, apperr.CodeMethodNotAllowed,
			"This HTTP method is not supported on that endpoint.").
			WithDetails(map[string]any{"method": c.Request.Method, "path": c.Request.URL.Path}))
	}
}

// LogUnexpected records a non-fatal background failure with full context.
func LogUnexpected(c *gin.Context, msg string, err error) {
	if err == nil || errors.Is(err, http.ErrAbortHandler) {
		return
	}
	logger.FromGin(c).Error(msg, slog.String("error", err.Error()))
}
