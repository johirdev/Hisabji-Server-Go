# Error catalogue

Every failure the API can return, and what a client should do about it.

**Branch on `code`, never on `message` or on the HTTP status.** Codes are
stable and never change once shipped, precisely because mobile and web clients
branch on them (`internal/core/apperr/code.go:11`). Messages are human text and
are localised.

---

## The failure envelope

```jsonc
{
  "success": false,
  "code":    "VALIDATION_ERROR",   // ← branch on this
  "message": "…",                  // ← show this
  "errors":  [                     // ← map onto form fields
    { "field": "phone", "rule": "bdphone",
      "message": "Enter a valid Bangladeshi mobile number.",
      "message_bn": "সঠিক বাংলাদেশি মোবাইল নম্বর দিন।" }
  ],
  "hint":       "…",               // ← optional next step, safe to show
  "details":    { },               // ← machine-readable extras
  "request_id": "a1b2c3…",         // ← quote this in a bug report
  "timestamp":  "2026-09-11T12:00:00Z"
}
```

`errors[]` entries carry `field` (the **JSON** name, including nested paths like
`items[0].price`), `rule`, `message` and — where a translation exists —
`message_bn`.

---

## 4xx — the caller's fault

| Code | HTTP | Meaning | What the client should do |
|---|:---:|---|---|
| `BAD_REQUEST` | 400 | Malformed JSON, a bad parameter, or a request that would change nothing. | Show `message`. |
| `OTP_INVALID` | 400 | Wrong, expired or already-used verification code. | Clear the OTP input. `details.attempts_left` says how many tries remain. |
| `UNAUTHORIZED` | 401 | Missing, wrong or absent credentials. | On login: show the message. Elsewhere: sign the user out. |
| `TOKEN_EXPIRED` | 401 | The access token is past its 30 minutes. | Call `POST /auth/refresh`, then retry **once**. |
| `TOKEN_INVALID` | 401 | Broken signature, revoked session, reused refresh token, or a password change since the session started. | **Do not retry.** Clear the tokens and send the user to sign-in. |
| `FORBIDDEN` | 403 | Authenticated, but not allowed. | Show the message. Do not sign out. |
| `PHONE_NOT_VERIFIED` | 403 | Finish OTP verification first. | Route to the OTP screen. |
| `ACCOUNT_SUSPENDED` | 403 | Disabled by an admin. | Sign out; show a support contact. |
| `NOT_FOUND` | 404 | The resource does not exist — **or is not yours**. | Show an empty state. |
| `ROUTE_NOT_FOUND` | 404 | No such endpoint. | A client bug. `details` carries the method and path. |
| `METHOD_NOT_ALLOWED` | 405 | Wrong verb on a real path. | A client bug. |
| `CONFLICT` | 409 | The current state forbids this (already subscribed, already cancelled). | Show the message; `details` usually carries the current state. |
| `DUPLICATE_ENTRY` | 409 | A unique constraint was hit — phone, email, username. | Highlight the field named in `errors[0].field`. |
| `IDEMPOTENCY_CONFLICT` | 409 | `X-Idempotency-Key` reused for a **different** purchase. | Generate a fresh key. |
| `GONE` | 410 | The resource is permanently gone. | Show an empty state. |
| `PAYLOAD_TOO_LARGE` | 413 | Body over 2 MiB. | Shrink the payload. |
| `UNSUPPORTED_MEDIA_TYPE` | 415 | Wrong `Content-Type`. | Send `application/json`. |
| `ACCOUNT_LOCKED` | 423 | Too many failed sign-ins. | Show `details.minutes_remaining`; offer "Forgot password", which unlocks immediately. |
| `VALIDATION_ERROR` | 422 | One or more fields failed a rule. | Map `errors[]` onto the form. |
| `MISSING_FIELD` | 422 | A required field was absent. | Same. |
| `INVALID_FIELD` | 422 | A field held an illegal value. | Same. |
| `RATE_LIMIT_EXCEEDED` | 429 | Too many requests. | Wait `details.retry_after_seconds` (also the `Retry-After` header). Back off; do not hammer. |
| `OTP_LIMIT_REACHED` | 429 | Inside the resend cooldown, over the window quota, or too many wrong guesses on one code. | Disable the resend button for `details.retry_after_seconds`. |

---

## 402 — billing

These five are the commercial surface. They are the only errors that should
open a purchase screen.

| Code | Meaning | Offer |
|---|---|---|
| `PAYMENT_REQUIRED` | A premium feature on a free plan. | Upgrade |
| `SUBSCRIPTION_REQUIRED` | The module is not in this plan and is not pay-as-you-go. | Upgrade to `details.required_tier` |
| `INSUFFICIENT_CREDITS` | Not enough credits for this action. | Buy a credit pack |
| `QUOTA_EXCEEDED` | Free credits used up, or the monthly safety cap hit. | Buy credits, or contact support for a higher cap |
| `PAYMENT_FAILED` | The provider declined. | Retry with another method |

### The `details` payload on a 402

The gate always tells you which way forward applies, so the UI never has to
guess (`internal/module/billing/gate.go:accessDenied`):

```json
{
  "code": "INSUFFICIENT_CREDITS",
  "message": "This uses 5 credits and you have 2.",
  "hint": "Buy a credit pack, or upgrade to a plan with a monthly allowance.",
  "details": {
    "feature": "ai_monthly_coach",
    "feature_name": "Monthly Coach",
    "credit_cost": 5,
    "available_credits": 2,
    "current_plan": "free",
    "required_tier": "plus",
    "needs_upgrade": false,
    "needs_credits": true
  }
}
```

Branch on `needs_upgrade` / `needs_credits`, not on the message.

`QUOTA_EXCEEDED` is used for two different situations — a free account that
has spent its signup credits, and any account that has hit
`monthly_spend_cap`. `details` distinguishes them: the second carries
`month_spent` and `monthly_cap`.

---

## 5xx — our fault

| Code | HTTP | Meaning | Client |
|---|:---:|---|---|
| `INTERNAL_ERROR` | 500 | Unexpected. | "Something went wrong." Offer retry. Log `request_id`. |
| `DATABASE_ERROR` | 500 | A query or transaction failed. | Same. |
| `CACHE_ERROR` | 500 | Redis failed. | Same. |
| `UPSTREAM_ERROR` | 502 | A third party (SMS, AI, payment) failed. | "Please try again in a moment." |
| `SERVICE_UNAVAILABLE` | 503 | Shutting down, or a dependency is down. Also what the rate limiter returns when Redis is unreachable and it fails closed. | Retry with backoff. |
| `TIMEOUT` | 504 | A deadline was exceeded. | Retry once, then give up. |

Outside production a 5xx also carries `details.debug_cause` with the real
underlying error. In production it never does — the cause is logged against the
`request_id` instead.

---

## Suggested client handling

```ts
switch (err.code) {
  case 'TOKEN_EXPIRED':
    await refreshOnce();      // serialise this — see below
    return retry();

  case 'TOKEN_INVALID':
  case 'ACCOUNT_SUSPENDED':
    return signOut();

  case 'PHONE_NOT_VERIFIED':
    return goTo('/verify-otp');

  case 'VALIDATION_ERROR':
  case 'MISSING_FIELD':
  case 'INVALID_FIELD':
  case 'DUPLICATE_ENTRY':
    return applyFieldErrors(err.errors);   // field → message

  case 'PAYMENT_REQUIRED':
  case 'SUBSCRIPTION_REQUIRED':
  case 'INSUFFICIENT_CREDITS':
  case 'QUOTA_EXCEEDED':
    return openPaywall(err.details);       // needs_upgrade / needs_credits

  case 'RATE_LIMIT_EXCEEDED':
  case 'OTP_LIMIT_REACHED':
    return backOff(err.details.retry_after_seconds);

  default:
    return toast(err.message);             // always safe to show
}
```

**Never retry a refresh in parallel.** Two concurrent refreshes with the same
token trip the reuse detector and sign the user out of every device. Put the
refresh behind a single in-flight promise. See
[03-auth.md](03-auth.md#post-apiv1authrefresh).

---

## Where these come from

```
internal/core/apperr/code.go       the catalogue — codes never change once shipped
internal/core/apperr/error.go      the error type + constructors
                                     BadRequest, Validation, MissingField, InvalidField,
                                     Unauthorized, Forbidden, NotFound, Conflict,
                                     Duplicate, RateLimited, PaymentRequired,
                                     Internal, Unavailable, Timeout
internal/core/apperr/postgres.go   driver errors → codes, using the constraint-name
                                   catalogue so "budgets_user_period_uniq" becomes
                                   "You already have a budget starting on that date."
internal/core/response/writer.go   the ONE place a status code is chosen
```

Constraint messages are registered in `internal/app/app.go`
(`registerSharedConstraints`) and `internal/module/auth` (`RegisterConstraints`).
Adding a new unique index without adding its message there means a user sees a
raw constraint name — so add both together.
