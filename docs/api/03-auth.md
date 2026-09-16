# Auth — `/api/v1/auth`

Registration, phone verification, sign-in, token rotation, device management
and passwords.

**Files:** `internal/module/auth/` — `routes.go` · `handler.go` · `service.go` ·
`repository.go` · `dto.go`

---

## The flows, before the endpoints

### Registration

```
POST /auth/register
   → account created, wallet + free plan provisioned in the SAME transaction
   → OTP sent by SMS
   → NO TOKENS ISSUED           ← an unverified phone is an unproven identity claim
   ↓ next_step: "verify_phone"
POST /auth/verify-otp
   → phone marked verified, session created, tokens issued
   ↓ next_step: "onboarding:profile"  (or "dashboard")
POST /users/me/onboarding …
```

### Sign-in

```
POST /auth/login
   ├─ unknown identifier   → 401, after burning bcrypt-equivalent time
   ├─ suspended            → 403 ACCOUNT_SUSPENDED
   ├─ locked               → 423 ACCOUNT_LOCKED
   ├─ wrong password       → 401 + attempts_remaining, failure counted
   ├─ phone unverified     → 200 with requires_verification:true, fresh OTP sent
   └─ ok                   → 200 with tokens
```

### Session lifetime

```
access token   30 min, signed JWT      ── verified with no DB read
refresh token  30 days, opaque random  ── checked against the sessions row on every use

access token expires → POST /auth/refresh → new pair, old refresh token revoked
```

### Password reset

```
POST /auth/forgot-password   → always 200 (never reveals whether the number exists)
POST /auth/reset-password    → password changed, EVERY session revoked
```

---

## `POST /api/v1/auth/register`

Creates an account and starts phone verification.

**Auth:** none · **Rate limit:** 5 / hour / IP (`auth:register`)

### Request

```json
{
  "name": "Rasel Ahmed",
  "phone": "01712345678",
  "password": "hisab1234",
  "email": "rasel@example.com",
  "username": "rasel",
  "user_type": "personal",
  "locale": "bn",
  "device_name": "Rasel's Pixel"
}
```

| Field | Type | Required | Rules |
|---|---|:---:|---|
| `name` | string | ✔ | 2–100, not blank, `safetext` |
| `phone` | string | ✔ | `bdphone` — normalised to `01XXXXXXXXX` |
| `password` | string | ✔ | `strongpass`, ≤ 72 |
| `email` | string | ✖ | valid email, ≤ 255, lower-cased |
| `username` | string | ✖ | `username` rule, lower-cased |
| `user_type` | string | ✖ | `personal` \| `student` \| `job_holder` \| `business` \| `freelancer` \| `family` — defaults to `personal` |
| `locale` | string | ✖ | `bn` \| `en` — defaults to `Accept-Language`, then `APP_LOCALE` |
| `device_name` | string | ✖ | ≤ 120. Falls back to the `User-Agent`, so the devices list is never a column of "Unknown device". |

**Not accepted, ever:** `role`, `plan`, `status`, `credits`. A client that could
set those could make itself an admin on a free plan with a million credits.
Privileged fields are only ever set server-side (`dto.go:12`).

**Cross-field rules** (`RegisterRequest.Validate`):
- the password must not equal the name
- the password must not contain the phone number

### Response `201`

```json
{
  "success": true,
  "code": "CREATED",
  "message": "Account created. Enter the verification code we sent to your phone.",
  "data": {
    "user": { "id": "0193…", "name": "Rasel Ahmed", "phone": "01712345678",
              "phone_verified": false, "plan_code": "free",
              "onboarding_step": "profile", "…": "…" },
    "requires_verification": true,
    "next_step": "verify_phone",
    "dev_otp": "483920"
  }
}
```

- **No tokens.** `tokens` is absent by design — see the flow above.
- `dev_otp` appears **only** outside production (`OTP_EXPOSE_IN_DEV=true`) so you
  can finish a signup without an SMS gateway. The service literally cannot set it
  in production (`service.go:devOTP`).

### Errors

| Status | Code | When |
|---|---|---|
| 422 | `VALIDATION_ERROR` | any field rule failed |
| 409 | `DUPLICATE_ENTRY` | phone, email or username already registered |
| 429 | `RATE_LIMIT_EXCEEDED` | more than 5 registrations from one IP in an hour |

### Path through the code

```
routes.go:31        rate limit auth:register (5/h/IP)
handler.go:34       Register            validate.Body → RegisterRequest.Normalize + Validate
                                        deviceName(c, in.DeviceName)  ← User-Agent fallback
service.go:77       Register
  ├ hash.Password(password, BCRYPT_COST)
  ├ txn.Run ───────────────────────────────────────────────┐
  │   repository.go:41  CreateUser        INSERT INTO users │  one transaction:
  │   billing.ProvisionNewUser(tx)        wallet + free plan│  no user without a wallet
  └───────────────────────────────────────────────────────┘
  └ issueOTP(phone, "register")
      ├ repository LastOTPSentAt      → cooldown check
      ├ repository CountRecentOTPs    → window quota check
      ├ hash.NumericCode(6)
      ├ repository SaveOTP            INSERT INTO otp_codes (code stored HASHED)
      └ sms.Send
handler.go:47       response.Created
```

If the SMS fails, registration still succeeds with `requires_verification: true`
— the account is usable once verified, and failing the whole signup because a
gateway hiccuped would be worse (`service.go:115`).

---

## `POST /api/v1/auth/verify-otp`

Completes phone verification and issues the first token pair.

**Auth:** none · **Rate limit:** 15 / min / IP (`auth:verify`)

### Request

```json
{ "phone": "01712345678", "otp": "483920" }
```

| Field | Rules |
|---|---|
| `phone` | `bdphone`, normalised |
| `otp` | exactly 6 digits, numeric |

### Response `200`

```json
{
  "success": true,
  "code": "OK",
  "message": "Phone number verified. You are now signed in.",
  "data": {
    "user": { "id": "0193…", "phone_verified": true, "…": "…" },
    "tokens": {
      "access_token": "eyJhbGciOi…",
      "refresh_token": "9f2c1a…",
      "token_type": "Bearer",
      "expires_in": 1800,
      "expires_at": "2026-09-11T12:30:00Z",
      "refresh_expires_at": "2026-10-11T12:00:00Z"
    },
    "requires_verification": false,
    "next_step": "onboarding:profile"
  }
}
```

`next_step` is `onboarding:<step>` while the wizard is unfinished, otherwise
`dashboard`. Route on this rather than making the app infer it from a missing
field — one string removes a whole class of navigation bug.

### Errors

| Status | Code | When |
|---|---|---|
| 400 | `OTP_INVALID` | wrong code (`details.attempts_left`), or expired / already used |
| 429 | `OTP_LIMIT_REACHED` | too many wrong attempts on this code — the code is burned, request a new one |
| 422 | `VALIDATION_ERROR` | malformed phone or a non-6-digit code |

### Path through the code

```
handler.go:53   VerifyOTP
service.go:138  VerifyPhone
  ├ repository.go:255  ConsumeOTP(phone, "register", hash.Token(otp))
  │     own transaction, SELECT … FOR UPDATE on the newest live code
  │     increments attempts and COMMITS EVEN ON A WRONG CODE
  │     ← that is what stops unlimited guessing
  ├ txn.Run
  │     MarkPhoneVerified  →  FindByPhone
  └ startSession(user)
        hash.RandomToken(32)                    ← the refresh token itself
        repository CreateSession                stores its PEPPERED HASH, never the token
        repository TrimSessions(maxDevices)     oldest sessions dropped past the plan limit
        jwt.Issue                               signs the access token
```

**The OTP is stored hashed.** A database dump does not hand an attacker live
verification codes.

---

## `POST /api/v1/auth/resend-otp`

Issues a fresh code.

**Auth:** none · **Rate limit:** 5 / 10 min / IP (`auth:resend`) — tighter than
the others, because every call here costs money in SMS.

### Request

```json
{ "phone": "01712345678", "purpose": "register" }
```

`purpose`: `register` (default) · `login` · `reset_password` · `change_phone`

### Response `200`

```json
{
  "success": true,
  "code": "OK",
  "message": "A new verification code has been sent.",
  "data": {
    "phone": "017*****678",
    "purpose": "register",
    "expires_at": "2026-09-11T12:10:00Z",
    "resend_after_seconds": 60,
    "attempts_left": 5,
    "dev_otp": "112233"
  }
}
```

The phone is **masked**. An unregistered number gets this identical response
with no SMS sent — otherwise the endpoint becomes a free check of which numbers
have accounts (`service.go:170`).

### Errors

| Status | Code | When |
|---|---|---|
| 429 | `OTP_LIMIT_REACHED` | inside the 60 s cooldown (`details.retry_after_seconds`), or more than 5 codes for this number in 6 hours (`details.window_hours`, `details.max_requests`) |

### Two limits, doing different jobs

| Guard | Default | Env | Stops |
|---|---|---|---|
| resend cooldown | 60 s | `OTP_RESEND_COOLDOWN` | a "resend" button becoming an SMS bill |
| window quota | 5 per 6 h | `OTP_MAX_PER_WINDOW` / `OTP_WINDOW` | slow, sustained abuse of one number |
| verify attempts | 5 per code | `OTP_MAX_VERIFY_TRIES` | guessing a code |
| code lifetime | 10 min | `OTP_TTL` | a leaked code staying useful |

---

## `POST /api/v1/auth/login`

**Auth:** none · **Rate limit:** 10 / min / IP (`auth:login`, `RATE_LIMIT_AUTH_PER_MIN`)

### Request

```json
{ "identifier": "01712345678", "password": "hisab1234", "device_name": "Chrome / Windows" }
```

`identifier` accepts a **phone, an email or a username**. Input that looks like
a phone number is normalised the same way registration normalised it, so every
form that registration accepted also logs in (`dto.go:LoginRequest.Normalize`).

### Response `200` — signed in

Same `data` shape as verify-otp: `user` + `tokens` + `next_step`.

### Response `200` — phone not verified yet

```json
{
  "success": true,
  "message": "Please verify your phone number to continue.",
  "data": {
    "user": { "…": "…" },
    "requires_verification": true,
    "next_step": "verify_phone",
    "dev_otp": "554433"
  }
}
```

Note this is **200, not an error**, and a fresh code has already been sent — so
the client goes straight to the OTP screen instead of making the user tap
"resend" first. Branch on `requires_verification`.

### Errors

| Status | Code | When | Extra |
|---|---|---|---|
| 401 | `UNAUTHORIZED` | wrong password, or no such account | `details.attempts_remaining` on a wrong password |
| 423 | `ACCOUNT_LOCKED` | too many failed attempts | `details.locked_until`, `details.minutes_remaining` |
| 403 | `ACCOUNT_SUSPENDED` | disabled by an admin | |
| 429 | `RATE_LIMIT_EXCEEDED` | > 10 attempts / min from this IP | |

### Two security properties worth knowing

**1. Unknown account and wrong password are indistinguishable.** Same message,
same code — and for an unknown identifier the server still runs a bcrypt
comparison against a dummy hash, burning the same CPU time. Without it, the
login endpoint is a fast oracle for which phone numbers have accounts
(`service.go:213`).

**2. Lockout.** `MAX_LOGIN_ATTEMPTS` (5) failures locks the account for
`LOGIN_LOCK_WINDOW` (6 h). A successful password reset clears it immediately —
which is why the 423 hint says so.

### Path through the code

```
handler.go:79   Login
service.go:213  Login
  ├ repository FindByIdentifier      phone OR email OR username, one query
  ├ not found      → dummy bcrypt + LogAttempt("not_found") + 401
  ├ suspended      → LogAttempt("suspended") + 403
  ├ locked         → LogAttempt("locked")    + 423
  ├ hash.Compare fails → RecordLoginFailure (counts, may lock) + 401
  ├ phone unverified && REQUIRE_PHONE_VERIFY → issueOTP + 200 requires_verification
  ├ startSession                     session row + token pair
  ├ RecordLoginSuccess               last_login_at / last_login_ip, counter reset
  ├ LogAttempt(success)              login_attempts audit row
  └ if hash.NeedsRehash → go rehashPassword   ← silent bcrypt-cost upgrade
```

Every attempt, successful or not, is written to `login_attempts`.

---

## `POST /api/v1/auth/refresh`

Exchanges a refresh token for a new pair and retires the old one.

**Auth:** none (the refresh token *is* the credential) · **Rate limit:** 60 / hour / IP

### Request

```json
{ "refresh_token": "9f2c1a7e…" }
```

### Response `200`

`user` + a **new** `tokens` pair + `next_step`. The presented refresh token is
dead the moment this returns — store the new one.

### Errors

| Status | Code | When |
|---|---|---|
| 401 | `TOKEN_INVALID` | unknown token · **reused** token (see below) · password changed since the session started |
| 401 | `TOKEN_EXPIRED` | the refresh token is past its 30 days |
| 403 | `ACCOUNT_SUSPENDED` | the account is no longer active |

### Rotation with reuse detection — read this

Every refresh mints a new token and revokes the presented one, so a refresh
token is legitimately usable **exactly once**.

If a **revoked** token is presented again, one of two things happened: an
attacker stole it and is using it after the real user already refreshed, or the
real user is replaying an old one after the attacker refreshed. The server
cannot tell which — so the safe move is to revoke the **entire session family**
and force a fresh sign-in on every device (`service.go:303`).

```
401 TOKEN_INVALID
message: "For your security, all sessions have been signed out."
hint:    "Please sign in again. If you did not expect this, change your password."
```

**Client implication:** never retry a refresh in parallel. Two concurrent
refreshes with the same token will sign the user out of everything. Serialise
them behind a single in-flight promise.

### Path through the code

```
handler.go:101  Refresh
service.go:315  Refresh
  ├ FindSessionByToken(PepperRefresh(token))   ← the sessions row is the source of truth
  ├ session.RevokedAt != nil  → RevokeFamily + Revoker.RevokeAllForUser + 401
  ├ session expired           → 401 TOKEN_EXPIRED
  ├ FindByID → !IsActive      → 403
  ├ PasswordChangedAt > session.CreatedAt → 401
  └ rotateSession
        RevokeSession(old, "rotated")   ← a NOT_FOUND here means someone
                                          rotated a millisecond ago: treat as reuse
        CreateSession(same family_id)
        jwt.Issue
```

---

## `POST /api/v1/auth/logout`

**Auth:** Bearer · **Rate limit:** global only

### Request — all three are valid

```json
{}                                    // end the current session (from the access token's sid)
{ "refresh_token": "9f2c…" }          // end exactly that device
{ "all_devices": true }               // end every session
```

An empty or absent body is a valid logout of the current session — binding
failures are ignored on purpose (`handler.go:117`).

### Response `200`

```json
{ "success": true, "message": "Signed out successfully.",
  "data": { "sessions_ended": 1, "all_devices": false } }
```

### What actually happens

Two stores are updated, and both matter:

| Store | Write | Effect |
|---|---|---|
| Postgres `sessions` | `revoked_at`, `revoked_reason` | the refresh token can no longer be exchanged |
| Redis denylist | `…:revoked:sid:<id>` or `…:revoked:user:<id>` | the **already-issued access token** stops working immediately, instead of living out its 30 minutes |

Without the second one, "log out" would leave a valid access token in an
attacker's hands for up to half an hour.

---

## `GET /api/v1/auth/sessions`

The "signed-in devices" list.

**Auth:** Bearer · **Rate limit:** global only

### Response `200`

```json
{
  "success": true,
  "message": "Signed-in devices fetched successfully.",
  "data": [
    {
      "id": "0193c7…",
      "device_name": "Rasel's Pixel",
      "ip": "103.108.…",
      "is_current": true,
      "last_used_at": "2026-09-11T11:58:00Z",
      "expires_at": "2026-10-11T12:00:00Z",
      "created_at": "2026-09-11T12:00:00Z"
    }
  ]
}
```

`is_current` is computed by comparing each row against the `sid` claim in the
caller's own access token — so the UI can label "This device" and refuse to
offer a sign-out button that would log the user out of the screen they are on.

Token hashes, the family id and the user id are never serialised (`json:"-"`).

---

## `DELETE /api/v1/auth/sessions/:id`

Sign one other device out.

**Auth:** Bearer · **Rate limit:** global only

`:id` must be a UUID; anything else is a clean `422`, not a Postgres 500.

### Response `200`

```json
{ "success": true, "message": "That device has been signed out.",
  "data": { "id": "0193c7…" } }
```

### Errors

| Status | Code | When |
|---|---|---|
| 404 | `NOT_FOUND` | no such session, **or it belongs to somebody else** |
| 422 | `VALIDATION_ERROR` | `:id` is not a UUID |

Ownership is verified before revoking. Without that check any signed-in user
could sign out any other user by guessing a session id — and reporting
somebody else's session as `404` rather than `403` means this endpoint cannot
be used to discover which session ids exist (`service.go:451`).

---

## `POST /api/v1/auth/change-password`

**Auth:** Bearer · **Rate limit:** 5 / hour / user (`strict:change_password`)

### Request

```json
{ "current_password": "hisab1234", "new_password": "notunPass99" }
```

`new_password` must satisfy `strongpass` and must differ from the current one.

### Response `200`

```json
{ "success": true,
  "message": "Password changed. Your other devices have been signed out.",
  "data": { "other_sessions_ended": 3 } }
```

The **current** session survives — the user stays signed in on the device they
just used. Every other device is signed out, which is the standard, expected
response to a password change.

### Errors

| Status | Code | When |
|---|---|---|
| 401 | `UNAUTHORIZED` | `current_password` is wrong — with `errors[0].field = "current_password"` so it lands on the right input |
| 422 | `VALIDATION_ERROR` | weak new password, or identical to the current one |
| 429 | `RATE_LIMIT_EXCEEDED` | more than 5 changes in an hour |

---

## `POST /api/v1/auth/forgot-password`

Starts a reset by sending an OTP.

**Auth:** none · **Rate limit:** 5 / hour / IP

### Request

```json
{ "phone": "01712345678" }
```

### Response `200` — always

```json
{
  "success": true,
  "message": "If that phone number is registered, a reset code has been sent to it.",
  "data": {
    "phone": "017*****678",
    "purpose": "reset_password",
    "expires_at": "2026-09-11T12:10:00Z",
    "resend_after_seconds": 60,
    "attempts_left": 5
  }
}
```

**Always 200, always the same message**, registered or not. A different response
for an unknown number would let anyone enumerate which phone numbers have
accounts (`service.go:526`). The one exception that *does* surface as an error
is `429 OTP_LIMIT_REACHED` — that is a real limit the user must see.

---

## `POST /api/v1/auth/reset-password`

**Auth:** none · **Rate limit:** 10 / hour / IP

### Request

```json
{ "phone": "01712345678", "otp": "483920", "new_password": "notunPass99" }
```

### Response `200`

```json
{ "success": true,
  "message": "Password reset. Please sign in with your new password.",
  "data": { "sessions_ended": 4 } }
```

### Errors

| Status | Code | When |
|---|---|---|
| 400 | `OTP_INVALID` | wrong, expired or already-used code |
| 429 | `OTP_LIMIT_REACHED` | too many wrong attempts on this code |
| 422 | `VALIDATION_ERROR` | weak new password |

**Every session is revoked, with no exception for a "current" one** — unlike
change-password. A reset means the account may have been compromised, so
nothing that existed before it survives (`service.go:561`).

---

## Where the auth data lives

| Table | Holds | Notes |
|---|---|---|
| `users` | the account | `password_hash` is bcrypt and carries `json:"-"` |
| `sessions` | one row per signed-in device | stores the **peppered HMAC** of the refresh token, never the token |
| `otp_codes` | live and spent codes | stores the **hash** of the code, plus `attempts` / `max_attempts` |
| `login_attempts` | every attempt, good or bad | the audit trail behind lockouts |
| Redis `…:revoked:*` | the access-token denylist | short TTL, self-expiring |

## Related config

| Env | Default | Effect |
|---|---|---|
| `ACCESS_TOKEN_TTL` | 30m | access-token lifetime, and the denylist TTL |
| `REFRESH_TOKEN_TTL` | 720h (30d) | session lifetime |
| `MAX_SESSIONS_PER_USER` | 5 | fallback device cap; the plan's `max_devices` wins when higher |
| `MAX_LOGIN_ATTEMPTS` | 5 | failures before a lock |
| `LOGIN_LOCK_WINDOW` | 6h | how long the lock lasts |
| `REQUIRE_PHONE_VERIFY` | true | whether login demands a verified phone |
| `BCRYPT_COST` | — | hashes below it are silently upgraded on next login |
| `OTP_LENGTH` / `OTP_TTL` / `OTP_MAX_PER_WINDOW` / `OTP_WINDOW` / `OTP_MAX_VERIFY_TRIES` / `OTP_RESEND_COOLDOWN` | 6 / 10m / 5 / 6h / 5 / 60s | OTP policy |
| `OTP_EXPOSE_IN_DEV` | true | returns `dev_otp` outside production only |
