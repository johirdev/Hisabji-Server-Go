# Conventions

Everything on this page is true for **every** endpoint. The per-endpoint pages
do not repeat it.

---

## 1. Base URL and versioning

```
/                 meta + health          (outside the version prefix, never rate limited)
/api/v1/...       everything else
```

The version prefix is set in one place: `internal/app/router.go:65`.
`/v2` would be a second `r.Group` mounted next to it — existing clients keep
working.

---

## 2. Request headers

| Header | When | Notes |
|---|---|---|
| `Authorization: Bearer <access_token>` | any authenticated endpoint | A bare token without the `Bearer ` prefix is also accepted — a very common client mistake that is not worth a confusing 401 (`internal/middleware/auth.go:233`). |
| `Content-Type: application/json` | any request with a body | Anything else returns `415 UNSUPPORTED_MEDIA_TYPE`. |
| `Accept-Language: bn` \| `en` | optional | Picks the language of server messages. `?lang=bn` on the URL overrides it; both fall back to `APP_LOCALE` (`internal/middleware/request.go:83`). |
| `X-Request-ID` | optional | Your own correlation id (8–64 safe characters). It is echoed back and appears in every log line for that request. An invalid one is silently replaced. |
| `X-Idempotency-Key` | purchase endpoints | See §8. |

**Body size limit:** 2 MiB (`SERVER_MAX_BODY_BYTES`). Larger returns
`413 PAYLOAD_TOO_LARGE`.

---

## 3. Response headers

| Header | Always? | Meaning |
|---|---|---|
| `X-Request-ID` | yes | The correlation id for this request. |
| `X-RateLimit-Limit` / `-Remaining` / `-Reset` | on rate-limited routes | Requests permitted, left in this window, seconds until reset. |
| `Retry-After` | on `429` | Seconds to wait. |
| `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Content-Security-Policy`, `Referrer-Policy`, `Permissions-Policy`, `Cache-Control: no-store…` | yes | Set by `internal/middleware/security.go`. Financial responses are never cacheable. |
| `Strict-Transport-Security` | production over TLS only | Deliberately not sent in development so it cannot pin `localhost` to https. |

---

## 4. The response envelope

**Success and failure share one shape.** Write one client interceptor and never
special-case anything. Defined in `internal/core/response/response.go:26`.

```jsonc
{
  "success":    true,
  "code":       "OK",
  "message":    "Profile fetched successfully.",
  "data":       { },        // object, array, or absent
  "meta":       { },        // pagination — list endpoints only
  "errors":     [ ],        // failures only
  "hint":       "…",        // failures only, when there is a useful next step
  "details":    { },        // failures only, machine-readable extras
  "request_id": "a1b2c3…",
  "timestamp":  "2026-09-11T12:00:00.123456789Z"
}
```

Rules that never change:

- `success` tells you which half you are in. Branch on it, not on the HTTP status.
- `code` is a **stable** machine identifier. Branch on this, never on `message`.
- `message` is human text, already localised. Show it; do not parse it.
- A list endpoint's `data` is always an array — never `null` — so `.map()` is safe.
- `errors[]` is per-field and maps straight onto form fields.

### Success codes

| `code` | HTTP | Emitted by |
|---|---|---|
| `OK` | 200 | `response.OK`, `response.List` |
| `CREATED` | 201 | `response.Created` |
| `ACCEPTED` | 202 | `response.Accepted` |
| `NO_CONTENT` | 204 | `response.NoContent` |

### `meta` — pagination

```jsonc
{
  "page": 1, "limit": 20,
  "total_items": 134, "total_pages": 7, "count": 20,
  "has_next": true, "has_prev": false,
  "next_page": 2, "prev_page": null,
  "sort": "-created_at", "search": "", "filters": {}, "extra": {}
}
```

`count` is how many items are in **this** page; `total_items` is the whole
result set. Both derived in one place: `response.NewMeta`.

---

## 5. Errors

```jsonc
{
  "success": false,
  "code": "VALIDATION_ERROR",
  "message": "Some of the information you sent is not valid.",
  "errors": [
    { "field": "phone", "rule": "bdphone",
      "message": "Enter a valid Bangladeshi mobile number.",
      "message_bn": "সঠিক বাংলাদেশি মোবাইল নম্বর দিন।" }
  ],
  "hint": "Check the highlighted fields and try again.",
  "request_id": "a1b2c3…",
  "timestamp": "2026-09-11T12:00:00Z"
}
```

`internal/core/response/writer.go:50` (`response.Fail`) is the **only** place in
the codebase that turns an error into an HTTP status. That is why handlers can
simply `return nil, err`.

- In non-production, a 500 also carries `details.debug_cause` with the real
  underlying error. In production it never does.
- The full code catalogue is in [06-errors.md](06-errors.md).

---

## 6. Money

Never a float. `internal/core/money` stores **minor units** (paisa) in an
`int64` and serialises with exactly two decimals:

```json
{ "monthly_income": 45000.00, "price": 499.00 }
```

- Send it as a JSON number or a string — both parse exactly, never via `float64`.
- `null`, `""` and a missing field all mean zero.
- Currency is a separate field. Only `BDT` is accepted today
  (`internal/module/user/service.go:113`).

**Client rule:** parse with a decimal library or keep the string. `0.1 + 0.2`
in JavaScript is how a finance app loses a paisa per transaction.

---

## 7. Dates and times

All timestamps are **UTC, RFC 3339**: `2026-09-11T12:00:00Z`. Calendar dates
(`first_entry_on`) are plain `YYYY-MM-DD`. Convert to the user's `timezone`
in the client, not on the server.

---

## 8. Idempotency

The purchase endpoints (`POST /billing/subscribe`, `POST /billing/credits/buy`)
accept `X-Idempotency-Key` (any string, ≤ 120 chars — a UUID is ideal).

- **Same key, same purchase** → the original payment comes back with
  `"already_processed": true`. Nothing new is created.
- **Same key, different purchase** → `409 IDEMPOTENCY_CONFLICT`.
- **No key** → every retry creates a new pending payment.

Send one. A flaky mobile network should cost the user nothing, not charge them
twice (`internal/module/billing/checkout.go:88`).

---

## 9. Rate limits

A sliding-window counter in Redis (`internal/middleware/rate_limit.go:26`) —
not a fixed window, so 10 requests at 11:59:59 plus 10 at 12:00:00 does **not**
sneak 20 through a "10 per minute" limit.

Three scopes:

| Scope | Counted per | Used for |
|---|---|---|
| `ScopeIP` | client IP | anything reachable before sign-in |
| `ScopeUser` | user id (IP when anonymous) | anything behind auth — one user on an office IP must not throttle colleagues |
| `ScopeGlobal` | one shared bucket | genuinely global resources (an AI provider quota) |

Exceeding one returns `429 RATE_LIMIT_EXCEEDED` with `Retry-After` and
`details.retry_after_seconds`.

**When Redis is down** the limiter fails **closed** by default
(`REDIS_FAIL_OPEN=false`) and returns `503 SERVICE_UNAVAILABLE` — the endpoints
that most need a limiter are exactly the ones attacked when infrastructure
wobbles. Note that the *authenticator* fails **open** in the same situation: a
signed, unexpired token is still honoured, because a cache outage must not
become a full outage.

Local development: `./scripts/reset-rate-limit.sh` clears the buckets.

---

## 10. Validation rules used in the DTOs

| Tag | Means |
|---|---|
| `bdphone` | `01[3-9]` + 8 digits, optionally `+880`/`880` prefixed. Normalised to `01XXXXXXXXX` before it reaches the database, so `+8801712345678` and `01712345678` are the same person. |
| `strongpass` | ≥ 8 characters with at least one letter and one digit. Deliberately not stricter — unusable rules push people to `Password1!`. |
| `username` | lowercase `a–z0–9_.`, 3–30 chars, must start and end alphanumeric, no `..` or `__`. |
| `safetext` | rejects control characters and markup-looking input. |
| `notblank` | present, and not only whitespace. |
| `oneof=…` | one of the listed values. |

Failures come back as `422 VALIDATION_ERROR` with one `errors[]` entry per
field, carrying both `message` and `message_bn`.

---

## 11. PATCH semantics

Every field in an update DTO is a **pointer**, so the server can tell
"not sent" from "sent as empty".

```jsonc
{ "name": "Rasel" }             // changes only the name
{ "name": "Rasel", "email": "" } // explicitly clears the email
```

Send only what you are changing. A body that would change nothing is rejected
with `400 BAD_REQUEST` — "No changes were provided." — rather than performing a
pointless write.
