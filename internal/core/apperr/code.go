// Package apperr defines the single error type used across every layer of the
// application (repository -> service -> handler) plus a stable machine-readable
// error code catalogue that the frontend can switch on.
//
// Rule of thumb for the whole codebase:
//
//	repository  -> returns raw driver errors, or apperr.ErrNotFound
//	service     -> returns apperr.*  (business meaning lives here)
//	handler     -> never formats errors itself; it just `return nil, err`
//	              and the response writer turns it into JSON.
package apperr

// Code is a stable, machine-readable identifier for a failure. It never
// changes once shipped, because mobile/web clients branch on it.
type Code string

const (
	// ---- 4xx : caller's fault -------------------------------------------
	CodeValidation       Code = "VALIDATION_ERROR"        // 422 - body/query failed validation
	CodeBadRequest       Code = "BAD_REQUEST"             // 400 - malformed json, bad param
	CodeMissingField     Code = "MISSING_FIELD"           // 422 - a required field was absent
	CodeInvalidField     Code = "INVALID_FIELD"           // 422 - a field had an illegal value
	CodeUnauthorized     Code = "UNAUTHORIZED"            // 401 - no/invalid credentials
	CodeTokenExpired     Code = "TOKEN_EXPIRED"           // 401 - access token expired, refresh it
	CodeTokenInvalid     Code = "TOKEN_INVALID"           // 401 - signature/format broken
	CodeForbidden        Code = "FORBIDDEN"               // 403 - authenticated but not allowed
	CodePhoneUnverified  Code = "PHONE_NOT_VERIFIED"      // 403 - finish OTP first
	CodeAccountLocked    Code = "ACCOUNT_LOCKED"          // 423 - too many failed logins
	CodeAccountSuspended Code = "ACCOUNT_SUSPENDED"       // 403 - disabled by admin
	CodeNotFound         Code = "NOT_FOUND"               // 404 - resource does not exist
	CodeRouteNotFound    Code = "ROUTE_NOT_FOUND"         // 404 - no such endpoint
	CodeMethodNotAllowed Code = "METHOD_NOT_ALLOWED"      // 405
	CodeConflict         Code = "CONFLICT"                // 409 - state conflict
	CodeDuplicate        Code = "DUPLICATE_ENTRY"         // 409 - unique constraint hit
	CodeGone             Code = "GONE"                    // 410
	CodePayloadTooLarge  Code = "PAYLOAD_TOO_LARGE"       // 413
	CodeUnsupportedMedia Code = "UNSUPPORTED_MEDIA_TYPE"  // 415
	CodeRateLimited      Code = "RATE_LIMIT_EXCEEDED"     // 429
	CodeOTPInvalid       Code = "OTP_INVALID"             // 400 - wrong/expired otp
	CodeOTPLimit         Code = "OTP_LIMIT_REACHED"       // 429 - too many otp requests
	CodeIdempotency      Code = "IDEMPOTENCY_CONFLICT"    // 409 - key reused with new body

	// ---- 402 / billing ---------------------------------------------------
	CodePaymentRequired   Code = "PAYMENT_REQUIRED"        // 402 - premium feature on free plan
	CodeInsufficientCredit Code = "INSUFFICIENT_CREDITS"   // 402 - not enough AI credits
	CodeQuotaExceeded     Code = "QUOTA_EXCEEDED"          // 402 - free AI predictions used up
	CodeSubscriptionRequired Code = "SUBSCRIPTION_REQUIRED" // 402 - module not in plan
	CodePaymentFailed     Code = "PAYMENT_FAILED"          // 402 - provider declined

	// ---- 5xx : our fault --------------------------------------------------
	CodeInternal    Code = "INTERNAL_ERROR"      // 500 - unexpected
	CodeDatabase    Code = "DATABASE_ERROR"      // 500 - query/tx failed
	CodeCache       Code = "CACHE_ERROR"         // 500 - redis failed
	CodeUpstream    Code = "UPSTREAM_ERROR"      // 502 - third party (SMS, AI, payment) failed
	CodeUnavailable Code = "SERVICE_UNAVAILABLE" // 503 - shutting down / dependency down
	CodeTimeout     Code = "TIMEOUT"             // 504 - context deadline exceeded
)
