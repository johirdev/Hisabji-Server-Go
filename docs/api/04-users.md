# Users — `/api/v1/users`

The account itself: profile, preferences, username, the onboarding wizard, and
account closure.

**Files:** `internal/module/user/` — `routes.go` · `handler.go` · `service.go` ·
`repository.go` · `dto.go`

Everything under `/users/me` requires a Bearer token. `/users/username-available`
is the one exception — it is used during registration, before a token exists.

---

## `GET /api/v1/users/username-available`

The "is this taken?" check a signup form makes as the user types.

**Auth:** optional · **Rate limit:** 60 / min / IP (`user:username_check`)

### Query parameters

| Param | Required | Notes |
|---|:---:|---|
| `username` | ✔ | Compared lower-cased and trimmed. |

### Response `200` — available

```json
{
  "success": true,
  "message": "That username is available.",
  "data": { "username": "rasel", "available": true }
}
```

### Response `200` — not available

```json
{
  "success": true,
  "message": "That username is already taken.",
  "data": {
    "username": "admin",
    "available": false,
    "reason": "That username is reserved.",
    "suggestions": ["admin1", "admin01", "admin_bd"]
  }
}
```

Note it is **200 either way** — "taken" is an answer, not an error. Branch on
`data.available`. `message` mirrors `data.reason` so it can be shown directly
under the input.

### Errors

| Status | Code | When |
|---|---|---|
| 422 | `VALIDATION_ERROR` | `?username=` missing entirely |

### Why optional auth

Signed **out** (registration) it just checks the table. Signed **in** (changing
a username) the caller's own current username is excluded from the check, so the
form does not tell a user their own name is taken (`service.go:CheckUsername`).

### Rejection reasons, in order

1. shorter than 3 characters
2. longer than 30 characters
3. reserved — `admin`, `support`, `hisabji`, `api`, `billing`, `me`, `settings`, … (the full list is `reservedUsernames` in `service.go:52`). These would let someone impersonate the product or collide with a future route.
4. already taken

Suggestions (`base1`, `base01`, `base_bd`, `base247`, `thebase`) are each
checked against the table before being offered, so a user is never handed a
suggestion that is also taken.

---

## `GET /api/v1/users/me`

The profile screen in one request.

**Auth:** Bearer · **Rate limit:** global only

### Query parameters

| Param | Default | Notes |
|---|---|---|
| `stats` | `false` | `true` or `1` adds the lifetime counters. |

### Response `200`

```json
{
  "success": true,
  "message": "Profile fetched successfully.",
  "data": {
    "user": {
      "id": "0193c7…",
      "name": "Rasel Ahmed",
      "username": "rasel",
      "email": "rasel@example.com",
      "phone": "01712345678",
      "avatar_path": null,
      "user_type": "personal",
      "role": "user",
      "status": "active",
      "locale": "bn",
      "currency": "BDT",
      "timezone": "Asia/Dhaka",
      "monthly_income": 45000.00,
      "month_start_day": 1,
      "phone_verified": true,
      "email_verified": false,
      "onboarding_step": "done",
      "onboarded_at": "2026-09-11T12:20:00Z",
      "plan_code": "free",
      "last_login_at": "2026-09-11T12:00:00Z",
      "created_at": "2026-09-01T09:00:00Z",
      "updated_at": "2026-09-11T12:20:00Z"
    },
    "entitlement": {
      "plan_code": "free",
      "tier": "free",
      "features": ["expense_tracking", "basic_reports"],
      "subscription_status": "none",
      "days_remaining": 0,
      "allowance_credits": 0,
      "purchased_credits": 3,
      "total_credits": 3
    },
    "onboarding_complete": true,
    "onboarding_step": "done",
    "stats": {
      "expense_count": 128,
      "income_count": 12,
      "category_count": 9,
      "goal_count": 2,
      "total_spent": 84250.00,
      "total_income": 540000.00,
      "first_entry_on": "2026-03-04",
      "active_days": 96
    }
  }
}
```

### Fields the client never receives

`password_hash`, `failed_login_count`, `locked_until`, `password_changed_at`,
`last_login_ip`, `deleted_at`, `metadata` — all carry `json:"-"` in
`internal/domain/user.go`. Some are credentials; the rest are security
telemetry that is useful to an admin and slightly alarming to a normal user.

### Why `stats` is opt-in

The counters cost several aggregate scans. A token refresh that only needs the
plan should not pay for them (`handler.go:19`). When you do ask, they arrive as
one query with eight scalar sub-selects rather than eight round trips — this is
the first screen after login, and its latency is the app's perceived speed
(`repository.go:163`).

### Degradation, on purpose

If billing or the stats query fails, the profile **still returns 200** without
that section, and a warning is logged. A billing hiccup must not make the home
screen fail — the app can render without the credit badge (`service.go:76`).

### Path through the code

```
handler.go:23   Me                  handler.UserID(c) ← the sub claim
service.go:62   Profile
  ├ repository.go:31  Get              SELECT … FROM users WHERE id = $1 AND deleted_at IS NULL
  ├ billing.Entitlement(userID)        plan + features + credits   (failure → warn, continue)
  ├ if ?stats=true → repository Stats  one query, eight sub-selects (failure → warn, continue)
  └ go repository.TouchLastSeen        fire-and-forget UPDATE last_seen_at
```

`TouchLastSeen` runs on a `context.WithoutCancel` goroutine, so the response is
not held open for a telemetry write.

---

## `PATCH /api/v1/users/me`

Partial profile update. `PUT` is registered as an alias to the same handler for
clients that prefer it — it is **still a partial update**, not a replace.

**Auth:** Bearer · **Rate limit:** 60 / min / user (`write:profile`)

### Request — send only what changes

```json
{ "name": "Rasel Ahmed", "monthly_income": 50000.00 }
```

| Field | Rules |
|---|---|
| `name` | 2–100, not blank, `safetext` |
| `email` | valid email, ≤ 255, lower-cased |
| `user_type` | `personal` \| `student` \| `job_holder` \| `business` \| `freelancer` \| `family` |
| `avatar_path` | ≤ 500 |
| `monthly_income` | money; not negative; ≤ 100,000,000.00 |
| `month_start_day` | 1–28 |

**Every field is a pointer.** Omitting `email` leaves it alone; sending
`"email": ""` clears it. Without that distinction, a client that only wants to
change a name would send a zero-valued email and silently erase the real one —
the single most common way a PATCH endpoint destroys user data (`dto.go:10`).

The 100 million taka ceiling is not pedantry: a billion taka a month is a typo,
and catching it here stops every downstream projection from being nonsense.

`month_start_day` stops at 28 so the salary cycle exists in February.

### Response `200`

```json
{ "success": true, "message": "Profile updated successfully.",
  "data": { "id": "0193c7…", "name": "Rasel Ahmed", "monthly_income": 50000.00, "…": "…" } }
```

`data` is the full updated `user` object (not wrapped in `{"user": …}` — that
shape belongs to `GET /me` only).

### Errors

| Status | Code | When |
|---|---|---|
| 400 | `BAD_REQUEST` | the body would change nothing — *"No changes were provided."* |
| 422 | `VALIDATION_ERROR` | a field rule failed |
| 409 | `DUPLICATE_ENTRY` | that email belongs to another account |
| 429 | `RATE_LIMIT_EXCEEDED` | > 60 writes / min |

---

## `PATCH /api/v1/users/me/preferences`

Display and cycle settings. Same partial semantics as above.

**Auth:** Bearer · **Rate limit:** 60 / min / user (`write:preferences`)

### Request

```json
{ "locale": "en", "timezone": "Asia/Dhaka", "month_start_day": 5 }
```

| Field | Rules |
|---|---|
| `locale` | `bn` \| `en` |
| `currency` | 3 letters — **only `BDT` is accepted today** |
| `timezone` | ≤ 64 characters |
| `month_start_day` | 1–28 |

### Response `200`

The full updated `user` object, message *"Preferences updated successfully."*

### Errors

| Status | Code | When |
|---|---|---|
| 400 | `BAD_REQUEST` | nothing to change |
| 422 | `INVALID_FIELD` | `currency` other than `BDT` — *"Only BDT is supported at the moment."* |

Saying so plainly beats accepting the value and then rendering every amount
with the wrong symbol (`service.go:113`).

---

## `PUT /api/v1/users/me/username`

Claim or change a username.

**Auth:** Bearer · **Rate limit:** 5 / hour / user (`strict:username`)

### Request

```json
{ "username": "rasel" }
```

Lower-cased and trimmed before anything else, so `Rasel` and `rasel` are one
name and cannot both be claimed.

### Response `200`

The full updated `user` object, message *"Username updated successfully."*

### Errors

| Status | Code | When |
|---|---|---|
| 409 | `DUPLICATE_ENTRY` | taken (with `details.suggestions`) or reserved |
| 422 | `VALIDATION_ERROR` | fails the `username` rule |
| 429 | `RATE_LIMIT_EXCEEDED` | more than 5 changes in an hour |

The hourly limit exists because a username is a public identifier: rapid
churn is how somebody squats a name someone else just released.

### Two layers of protection

The service checks reserved names and availability **and** the database holds a
unique index. The service check produces a friendly message with suggestions;
the index is what makes it correct when two people claim the same name in the
same millisecond. A constraint violation is still translated into the same
409 (`internal/app/app.go:registerSharedConstraints`).

---

## `POST /api/v1/users/me/onboarding`

Saves setup-wizard progress. One request per step; the step can carry the data
that belongs to it, so the wizard saves progress in a single round trip.

**Auth:** Bearer · **Rate limit:** 60 / min / user (`write:onboarding`)

### Request

```json
{ "step": "income", "monthly_income": 45000.00, "month_start_day": 1 }
```

| Field | Required | Rules |
|---|:---:|---|
| `step` | ✔ | `profile` \| `income` \| `categories` \| `budget` \| `done` |
| `user_type` | ✖ | the six user types |
| `monthly_income` | ✖ | money, not negative |
| `month_start_day` | ✖ | 1–28 |
| `currency` | ✖ | 3 letters |
| `locale` | ✖ | `bn` \| `en` |

Every optional field is applied with `COALESCE`, so omitting one leaves the
existing value untouched.

### Response `200`

The **same shape as `GET /users/me`** (without stats) — user + entitlement +
onboarding state — so the client can re-render the whole screen from one
response.

| `step` sent | Message |
|---|---|
| `done` | "Setup complete. Welcome to Hisabji." |
| anything else | "Setup progress saved." |

Sending `done` also stamps `onboarded_at` — via `COALESCE(onboarded_at, now())`,
so re-sending `done` never rewrites the original completion time.

### The wizard order

```
profile → income → categories → budget → done
```

`user.onboarding_step` holds where the user is, and `next_step` on every auth
response echoes it as `onboarding:<step>`. That is how a user who closes the app
mid-wizard resumes in the right place.

---

## `DELETE /api/v1/users/me`

Close the account.

**Auth:** Bearer · **Rate limit:** 3 / 24 hours / user (`strict:delete_account`)

### Request

```json
{ "password": "hisab1234", "confirm": "DELETE", "reason": "Not using it any more" }
```

| Field | Required | Rules |
|---|:---:|---|
| `password` | ✔ | the account password, re-entered |
| `confirm` | ✔ | must be the literal string `DELETE` |
| `reason` | ✖ | ≤ 500, `safetext` |

The password is required **even though the caller is already authenticated**: a
stolen phone with an unlocked app must not be able to erase somebody's whole
financial history in two taps. `confirm: "DELETE"` is the second deliberate
action (`dto.go:129`).

### Response `200`

```json
{
  "success": true,
  "message": "Your account has been closed.",
  "data": {
    "deleted": true,
    "purge_after": "30 days",
    "can_reregister": true,
    "message": "Your account has been closed. Your data is permanently removed after 30 days, and 017*****678 can be used to register again immediately."
  }
}
```

Every token dies with this call — the next request returns `401 TOKEN_INVALID`.

### Errors

| Status | Code | When |
|---|---|---|
| 401 | `UNAUTHORIZED` | wrong password (field-tagged `password`) |
| 422 | `VALIDATION_ERROR` | `confirm` is not exactly `DELETE` |
| 429 | `RATE_LIMIT_EXCEEDED` | more than 3 attempts in 24 hours |

### What "deleted" actually means

**Soft delete, in one transaction** (`repository.go:132`):

| Column | Becomes | Why |
|---|---|---|
| `status` | `deleted` | |
| `deleted_at` | `now()` | starts the 30-day purge clock |
| `phone` | `01712345678.deleted.1757592000` | frees the unique index **immediately**, so the person can sign up again the same day |
| `email`, `username` | `NULL` | same reason |
| `metadata` | gains `deleted_reason`, `original_phone`, `deleted_at` | recoverable during the grace period |

Then, still inside the same transaction, every session is revoked and a
`cancel_reason` row is written to `feedback` when a reason was given. Finally
the Redis denylist is updated, so already-issued access tokens stop working at
once rather than living out their 30 minutes.

Soft rather than hard delete for three reasons: an accidental deletion can be
undone during the grace period, financial records may be needed for dispute
resolution, and a hard delete of a busy account would cascade across every
table at once.

### Path through the code

```
routes.go:44    rate limit strict:delete_account (3 / 24h / user)
handler.go:127  DeleteMe          validate.Body → DeleteAccountRequest
service.go:213  DeleteAccount
  ├ repository Get                 load the account (needs password_hash)
  ├ hash.Compare                   wrong → 401 with field "password"
  └ txn.Run ─────────────────────────────────────────────┐
      repository.go:132  SoftDelete                       │  all or nothing
      auth.RevokeAllForUser(tx)     revoke every session  │
      INSERT INTO feedback          when a reason was given│
    ───────────────────────────────────────────────────────┘
  └ Authenticator.RevokeAllForUser  Redis denylist, kills live access tokens
```
