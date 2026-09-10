# Hisabji — Frontend Integration Contract

**Single source of truth for building the Next.js client against the Hisabji Go API.**

Version: 1.0 · API: `v1` · Last verified against a live server: all shapes in this
document were captured from real responses, not written from memory.

---

## 0. How to use this file

If you are an AI assistant or a developer generating the frontend, read this file
first and treat it as binding. Specifically:

1. **Never invent an endpoint.** Section 5 lists every endpoint that exists today.
   Section 6 lists endpoints that are planned but **not yet implemented** — you may
   build UI and typed stubs against them, but they will return `404 ROUTE_NOT_FOUND`
   until the backend ships them. Mark such screens as gated behind a feature flag.
2. **Never invent a response shape.** Every response is the envelope in §3.2. The
   `data` payloads are typed in §7.
3. **Never parse error strings.** Branch on `code` (§3.4). The `message` is for
   display only and may be reworded at any time.
4. **Money is never a float you compute with.** See §3.6.
5. **Follow the design system in §10 exactly** — tokens, not ad-hoc values.

Prompt to use when handing this to an assistant:

> Read `docs/FRONTEND_INTEGRATION.md`. Build the Hisabji Next.js frontend
> following it exactly: the stack in §1, the API client in §8, the design tokens
> in §10, and the screens in §11. Do not invent endpoints or response shapes. For
> anything in §6, generate the screen but gate it behind `flags.<name>` defaulting
> to off.

---

## 1. Tech stack (decided — do not substitute)

| Concern | Choice | Why this one |
|---|---|---|
| Framework | **Next.js 15+ (App Router)** | Server Components let the dashboard render with data already fetched; Route Handlers give us the BFF token layer in §4. |
| Language | **TypeScript**, `strict: true` | The API contract is large; types are what stop it drifting. |
| Styling | **Tailwind CSS v4** | Design tokens in §10 map 1:1 onto `@theme` CSS variables. |
| Components | **shadcn/ui** (Radix primitives) | Copy-in, not a dependency — so the tokens in §10 apply cleanly. Accessible by default. |
| Server state | **TanStack Query v5** | The API is REST with a pagination envelope; Query handles caching, refetch and optimistic updates. **Do not use it for auth tokens.** |
| Client state | **Zustand** | Tiny. Only for UI state (sheet open, active filter, locale). |
| Forms | **react-hook-form + zod** | The API returns per-field errors (§3.5) that map straight onto `setError`. |
| Charts | **Recharts** | Composable, works with the category colors the API already returns. |
| Icons | **lucide-react** | **Required** — the API returns lucide icon names (§3.9). |
| Dates | **date-fns** + `date-fns-tz` | The API works in `Asia/Dhaka` dates, not UTC timestamps. |
| i18n | **next-intl** | Bangla-first with English fallback; see §10.8. |
| Toasts | **sonner** | |
| Fonts | `next/font/google` | See §10.3. |

```bash
npx create-next-app@latest hisabji-web --typescript --tailwind --app --src-dir --import-alias "@/*"
cd hisabji-web
npx shadcn@latest init
npm i @tanstack/react-query zustand react-hook-form zod @hookform/resolvers \
      recharts lucide-react date-fns date-fns-tz next-intl sonner
```

**Do not install:** axios (native `fetch` is enough and works in RSC), moment,
any CSS-in-JS library, any state library beyond the two above.

---

## 2. Environment

```bash
# .env.local  — server-only. There is deliberately NO NEXT_PUBLIC_API_URL:
# the browser never talks to the Go API directly. See §4.
HISABJI_API_URL=http://localhost:8080
AUTH_COOKIE_SECRET=<32+ random chars>
NODE_ENV=development
```

Run the backend first:

```bash
cd Hisabji-Server
docker compose up -d postgres redis
go run ./cmd/api            # http://localhost:8080
bash scripts/smoke.sh       # 80 checks, should all pass
```

> **Note on ports:** the Docker containers publish Postgres on **5433** and Redis on
> **6380** (not the defaults), because a native Windows PostgreSQL service was
> already bound to 5432. The API itself is on **8080**.

---

## 3. API contract fundamentals

### 3.1 Base URL and headers

```
Base:    {HISABJI_API_URL}/api/v1
```

| Header | When | Value |
|---|---|---|
| `Content-Type` | every request with a body | `application/json` |
| `Authorization` | authenticated endpoints | `Bearer <access_token>` |
| `Accept-Language` | always | `bn` or `en` — drives `message_bn` selection |
| `X-Request-ID` | optional | your own correlation id; echoed back |
| `X-Idempotency-Key` | **required on purchases** | see §3.8 |

Non-versioned endpoints (outside `/api/v1`): `GET /`, `GET /health`,
`GET /health/live`, `GET /health/ready`.

### 3.2 The response envelope

**Every** response — success or failure — has this shape. Write one parser.

```jsonc
// Success (single resource)
{
  "success": true,
  "code": "OK",                        // "OK" | "CREATED" | "ACCEPTED" | "NO_CONTENT"
  "message": "Profile fetched successfully.",   // safe to show in a toast
  "data": { /* the resource */ },
  "request_id": "7a736ea77cb2ca17c82a33d3",
  "timestamp": "2026-09-10T17:22:39.517Z"
}

// Success (list) — `data` is ALWAYS an array, never null
{
  "success": true,
  "code": "OK",
  "message": "Credit history fetched successfully.",
  "data": [ /* items */ ],
  "meta": {
    "page": 1, "limit": 20, "total_items": 134, "total_pages": 7,
    "count": 20, "has_next": true, "has_prev": false,
    "next_page": 2,
    "sort": "-spent_at", "search": "coffee",
    "filters": { "amount[gte]": "500" },   // echo of what was applied
    "extra": { "total_spent": 12450.00 }   // aggregates, when the endpoint has them
  },
  "request_id": "...", "timestamp": "..."
}

// Failure
{
  "success": false,
  "code": "VALIDATION_ERROR",           // branch on THIS, never on message
  "message": "Some fields are missing or invalid.",
  "errors": [
    {
      "field": "phone",
      "rule": "bdphone",
      "message": "Enter a valid Bangladeshi mobile number, e.g. 01712345678.",
      "message_bn": "সঠিক মোবাইল নম্বর দিন, যেমন ০১৭১২৩৪৫৬৭৮।",
      "value": "12345"                   // never present for password/otp/token fields
    }
  ],
  "hint": "Fix the listed fields and submit again.",   // optional, show as helper text
  "details": { "retry_after_seconds": 3202 },          // optional, structured
  "request_id": "...", "timestamp": "..."
}
```

Rules that hold everywhere:

- `data` for a list is an empty array `[]` when there are no results — never `null`.
- `meta` is present only on paginated list endpoints.
- `errors[]` is present only for `VALIDATION_ERROR`, `MISSING_FIELD`,
  `INVALID_FIELD` and `DUPLICATE_ENTRY`.
- `request_id` is on every response. **Show it in your error UI** — it is the one
  string that finds the request in the server logs.

### 3.3 Error codes → what the UI must do

Branch on `code`. This is the complete catalogue.

| `code` | HTTP | What the UI should do |
|---|---|---|
| `VALIDATION_ERROR` | 422 | Map `errors[]` onto form fields (§3.5). Do not toast. |
| `MISSING_FIELD` | 422 | Same as above. |
| `INVALID_FIELD` | 422 | Same as above. |
| `BAD_REQUEST` | 400 | Toast `message`. Usually a client bug — log it. |
| `UNAUTHORIZED` | 401 | Redirect to `/login`. Clear session. |
| `TOKEN_EXPIRED` | 401 | **Refresh once, retry** (§4.3). Only redirect if refresh fails. |
| `TOKEN_INVALID` | 401 | Clear session, redirect to `/login`. Do not retry. |
| `FORBIDDEN` | 403 | Toast. Do not redirect. |
| `PHONE_NOT_VERIFIED` | 403 | Route to `/verify-otp`. |
| `ACCOUNT_LOCKED` | 423 | Show `details.minutes_remaining` and a "Reset password" CTA. |
| `ACCOUNT_SUSPENDED` | 403 | Full-screen state with a support link. |
| `NOT_FOUND` | 404 | Empty state on the screen, not a toast. |
| `ROUTE_NOT_FOUND` | 404 | Client bug — the endpoint does not exist. Log loudly. |
| `METHOD_NOT_ALLOWED` | 405 | Client bug. |
| `CONFLICT` | 409 | Toast `message` + `hint`. Refetch the affected query. |
| `DUPLICATE_ENTRY` | 409 | Map `errors[0].field` onto the form field. |
| `IDEMPOTENCY_CONFLICT` | 409 | Generate a fresh idempotency key and let the user retry. |
| `PAYLOAD_TOO_LARGE` | 413 | Toast with the size limit from `details`. |
| `UNSUPPORTED_MEDIA_TYPE` | 415 | Client bug. |
| `RATE_LIMIT_EXCEEDED` | 429 | Disable the action for `details.retry_after_seconds`, show a countdown. |
| `OTP_INVALID` | 400 | Inline error under the OTP input + `details.attempts_left`. |
| `OTP_LIMIT_REACHED` | 429 | Disable resend, show a countdown from `details.retry_after_seconds`. |
| `PAYMENT_REQUIRED` | 402 | Open the paywall sheet (§11.7). |
| `INSUFFICIENT_CREDITS` | 402 | Paywall, **Buy credits** tab preselected. `details.shortfall` tells you how many are missing. |
| `QUOTA_EXCEEDED` | 402 | Paywall — free credits are used up. |
| `SUBSCRIPTION_REQUIRED` | 402 | Paywall, **Plans** tab preselected, highlight `details.required_tier`. |
| `PAYMENT_FAILED` | 402 | Toast + retry CTA. |
| `INTERNAL_ERROR` | 500 | Generic error screen with `request_id`. |
| `DATABASE_ERROR` | 500 | Same. |
| `UPSTREAM_ERROR` | 502 | "Service temporarily unavailable", retry button. |
| `SERVICE_UNAVAILABLE` | 503 | Same, with backoff. |
| `TIMEOUT` | 504 | "That took too long", retry button. |

The four `402` codes are the whole monetisation surface. Handle them in **one**
shared interceptor that opens the paywall — never per-screen.

### 3.4 Validation errors → form fields

`errors[].field` is the **JSON field name**, and nested/indexed paths use
`items[0].amount` notation. This maps directly onto react-hook-form:

```ts
// See §9 for the full helper.
for (const e of err.errors ?? []) {
  form.setError(e.field as never, {
    type: e.rule ?? "server",
    message: locale === "bn" && e.message_bn ? e.message_bn : e.message,
  });
}
```

`rule` values you will see: `required`, `min`, `max`, `len`, `email`, `bdphone`,
`username`, `strongpass`, `oneof`, `uuid`, `unique`, `type`, `numeric`, `date`,
`enum`, `not_filterable`, `unknown_filter`, `operator`, `incorrect`, `same`, `weak`.

### 3.5 Money format — read this carefully

Amounts are stored server-side as **integer paisa** and serialised as a **JSON
number with exactly two decimals**:

```jsonc
{ "amount": 1500.50, "monthly_income": 30000.00, "price": 199.00 }
```

**Reading:** `JSON.parse` gives you a JS number (`1500.5`). Safe for display and
comparison at Hisabji's scale. **Never** sum many amounts in floating point and
show the result as authoritative — read a server-provided total instead
(`meta.extra`, or a `/summary` endpoint).

**Writing:** send the raw string from the input field. The API parses decimal text
exactly and rounds half-up at the second decimal:

```ts
// ✅ exact — no float ever involved
await api.post("/expenses", { amount: "1500.50" });

// ✅ also accepted
await api.post("/expenses", { amount: 1500.5 });

// ❌ never do arithmetic in the client and send the result
await api.post("/expenses", { amount: 0.1 + 0.2 });  // 0.30000000000000004
```

Accepted input forms: `1500`, `1500.5`, `1500.50`, `"1500.50"`, `.5`, `-250.25`.
Rejected: `"1,500"`, `"1.2.3"`, `"1e5"`, `"--5"`, `"abc"`.

**Display:** always `৳` prefix, two decimals, thousands separators, **tabular
numerals** (§10.3):

```ts
export const formatBDT = (n: number, opts?: { compact?: boolean }) =>
  new Intl.NumberFormat("en-BD", {
    style: "currency", currency: "BDT",
    minimumFractionDigits: opts?.compact ? 0 : 2,
    maximumFractionDigits: opts?.compact ? 0 : 2,
    notation: opts?.compact ? "compact" : "standard",
  }).format(n);
// formatBDT(1500.5)               -> "৳1,500.50"
// formatBDT(1500.5, {compact:1})  -> "৳2K"   (use only in tight chart labels)
```

### 3.6 Dates

Transaction dates (`spent_at`, `received_at`, `period_start`, `target_date`, …)
are **calendar dates** in the user's timezone, serialised as
`"2026-01-31T00:00:00Z"`. Treat them as dates, never as instants:

```ts
import { parseISO, format } from "date-fns";
format(parseISO(expense.spent_at), "d MMM yyyy");   // ✅
new Date(expense.spent_at).toLocaleDateString();     // ❌ shifts by timezone
```

Audit timestamps (`created_at`, `updated_at`, `last_used_at`, …) are real
instants with an offset (`"2026-09-10T23:22:39.176725+06:00"`) — format those in
local time normally.

### 3.7 Query conventions (list endpoints)

Every list endpoint accepts the same parameters. The backend validates them
against a per-resource whitelist and returns `VALIDATION_ERROR` naming the exact
offending parameter, so a typo is a clear 422, never a silent empty list.

| Parameter | Example | Notes |
|---|---|---|
| `page` | `?page=2` | 1-based. Default 1. |
| `limit` | `?limit=20` | Default 20, max 100 (per resource). |
| `search` | `?search=coffee` | Case-insensitive contains, across that resource's searchable fields. |
| `sort` | `?sort=-spent_at,amount` | `-` prefix = descending. Max 4 keys. |
| `date_from` / `date_to` | `?date_from=2026-01-01&date_to=2026-01-31` | Inclusive; `date_to` covers the whole day. |
| `with_total` | `?with_total=false` | Skips the COUNT query — use for infinite scroll. |
| `include` | `?include=category` | Relation expansion, where supported. |
| `fields` | `?fields=id,amount,spent_at` | Sparse fieldsets, where supported. |

**Filters** use `field` or `field[operator]`:

```
?category_id=<uuid>                 eq
?amount[gte]=500                    >=
?amount[between]=100,500            inclusive range
?payment_method[in]=cash,bkash      IN
?note[contains]=lunch               ILIKE %…%
?merchant[starts]=Sha               ILIKE …%
?note[null]=true                    IS NULL
?tags[has]=work                     array contains
```

Operators: `eq` `ne` `gt` `gte` `lt` `lte` `in` `nin` `like` `contains` `starts`
`ends` `between` `null` `has`. Which ones a field accepts depends on its type;
asking for an unsupported one returns a 422 that **lists the supported operators**.

Build these with a helper, never by string concatenation:

```ts
export function toQuery(params: Record<string, unknown>): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === "") continue;
    sp.set(k, Array.isArray(v) ? v.join(",") : String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
}
```

### 3.8 Rate limits and idempotency

Rate-limited responses carry `X-RateLimit-Limit`, `X-RateLimit-Remaining`,
`X-RateLimit-Reset` and, on 429, a `Retry-After` header.

Current limits (per IP unless stated):

| Endpoint | Limit |
|---|---|
| everything under `/api/v1` | 300 / min |
| `POST /auth/register` | 5 / hour |
| `POST /auth/login` | 10 / min |
| `POST /auth/verify-otp` | 15 / min |
| `POST /auth/resend-otp` | 5 / 10 min |
| `POST /auth/forgot-password` | 5 / hour |
| `POST /auth/refresh` | 60 / hour |
| `POST /billing/subscribe` | 10 / hour (per user) |
| `POST /billing/credits/buy` | 20 / hour (per user) |
| `PUT /users/me/username` | 5 / hour (per user) |
| `DELETE /users/me` | 3 / day (per user) |

**Idempotency.** Send `X-Idempotency-Key: <uuid>` on `POST /billing/subscribe` and
`POST /billing/credits/buy`. Generate it once when the user opens the checkout
sheet, reuse it for every retry of that same purchase, and discard it on success.
Replaying a key returns the original payment with `already_processed: true` — that
is what stops a flaky network from charging twice.

### 3.9 Icons and colors come from the API

`categories.icon` and `features.icon` are **lucide-react icon names**
(`utensils`, `bus`, `home`, `credit-card`, `piggy-bank`, `sparkles`, …). Render
them dynamically:

```tsx
import * as Lucide from "lucide-react";

export function DynamicIcon({ name, ...props }: { name: string } & Lucide.LucideProps) {
  const key = name.split("-").map(p => p[0].toUpperCase() + p.slice(1)).join("");
  const Icon = (Lucide as Record<string, unknown>)[key] as Lucide.LucideIcon | undefined;
  return <(Icon ?? Lucide.Circle) {...props} />;
}
```

`categories.color` is a `#RRGGBB` hex. **Use it for that category everywhere** —
chips, chart series, list rows. Do not assign your own category colors; the data
already carries them, so the pie chart and the list always agree.

---

## 4. Auth architecture — BFF with httpOnly cookies

### 4.1 The decision

The API returns tokens as JSON. If the browser stored them, they would live in
`localStorage` and any XSS would hand an attacker a 30-day refresh token.

So: **the browser never sees a token.** Next.js Route Handlers act as a
Backend-For-Frontend. They hold the tokens in `httpOnly`, `secure`, `sameSite=lax`
cookies and proxy every API call.

```
Browser ──(cookie)──▶ Next Route Handler ──(Bearer)──▶ Go API
```

Consequences you must respect:

- Client components call **`/api/hisabji/...`**, never `HISABJI_API_URL` directly.
- Server Components call the Go API directly using `lib/api/server.ts`.
- CORS is irrelevant in production (same origin). Keep `http://localhost:3000` in
  `CORS_ALLOWED_ORIGINS` only for debugging.

### 4.2 Cookies

| Cookie | Contents | Flags | Max-Age |
|---|---|---|---|
| `hisabji_at` | access token | httpOnly, secure, sameSite=lax, path=/ | 1800 |
| `hisabji_rt` | refresh token | httpOnly, secure, sameSite=lax, path=/api | 2592000 |

### 4.3 Token lifecycle

- Access token: **30 minutes**, a signed JWT carrying `sub`, `role`, `plan`, `sid`.
- Refresh token: **30 days**, an opaque 256-bit random string checked against the
  server's `sessions` table on every use.
- **Rotation with reuse detection:** every refresh issues a new refresh token and
  revokes the old one. Presenting an already-used token means it was stolen, so the
  server revokes **the entire device family** and returns
  `TOKEN_INVALID` with *"all sessions have been signed out"*.

  → Your client must therefore **never refresh concurrently.** The proxy in §8
  serialises refreshes with a single-flight promise. Firing two refreshes in
  parallel will log the user out of every device.

### 4.4 The full auth flow

```
POST /auth/register        → 201, requires_verification: true, next_step: "verify_phone"
                             (no tokens — an unverified phone is an unproven identity)
POST /auth/verify-otp      → 200, tokens issued, user signed in
POST /auth/login           → 200 + tokens
                             ...unless phone is unverified: 200 with
                             requires_verification: true and a fresh OTP already sent
POST /auth/refresh         → 200 + a NEW pair (old refresh token is now dead)
POST /auth/logout          → 200  ({ "all_devices": true } to end every session)
```

`next_step` tells you where to navigate — do not infer it:

| `next_step` | Route to |
|---|---|
| `verify_phone` | `/verify-otp` |
| `onboarding:profile` | `/onboarding/profile` |
| `onboarding:income` | `/onboarding/income` |
| `onboarding:categories` | `/onboarding/categories` |
| `onboarding:budget` | `/onboarding/budget` |
| `dashboard` | `/` |

**Development helper:** outside production the register/verify responses include
`dev_otp` so you can complete signup without an SMS gateway. It is never present
when `APP_ENV=production`. Show it in a dev-only banner.

---

## 5. Endpoint reference — implemented

`✔` = live and covered by the backend smoke suite. `🔒` = requires `Authorization`.

### 5.1 Meta

| Method | Path | Notes |
|---|---|---|
| `GET` | `/` | ✔ service info |
| `GET` | `/health` | ✔ full check (db + redis) |
| `GET` | `/health/live` | ✔ liveness only — never rate limited |
| `GET` | `/health/ready` | ✔ readiness |

### 5.2 Auth — `/api/v1/auth`

| Method | Path | Body | Returns |
|---|---|---|---|
| `POST` | `/register` | `{name, phone, password, email?, username?, user_type?, locale?, device_name?}` | `AuthResponse` (201) |
| `POST` | `/verify-otp` | `{phone, otp}` | `AuthResponse` with tokens |
| `POST` | `/resend-otp` | `{phone, purpose?}` | `OTPResponse` |
| `POST` | `/login` | `{identifier, password, device_name?}` | `AuthResponse` |
| `POST` | `/refresh` | `{refresh_token}` | `AuthResponse` with a new pair |
| `POST` | `/forgot-password` | `{phone}` | `OTPResponse` — always 200, even for an unknown number |
| `POST` | `/reset-password` | `{phone, otp, new_password}` | `{sessions_ended}` |
| `POST` | `/logout` 🔒 | `{refresh_token?, all_devices?}` | `{sessions_ended, all_devices}` |
| `GET` | `/sessions` 🔒 | — | `SessionResponse[]` |
| `DELETE` | `/sessions/:id` 🔒 | — | `{id}` |
| `POST` | `/change-password` 🔒 | `{current_password, new_password}` | `{other_sessions_ended}` |

`identifier` accepts **phone, email or username**. Phone numbers are normalised, so
`+8801712345678`, `8801712345678` and `01712345678` are the same account.

### 5.3 Users — `/api/v1/users`

| Method | Path | Body / Query | Returns |
|---|---|---|---|
| `GET` | `/username-available` | `?username=rasel` | `UsernameAvailability` |
| `GET` | `/me` 🔒 | `?stats=true` | `ProfileResponse` |
| `PATCH` | `/me` 🔒 | `{name?, email?, user_type?, avatar_path?, monthly_income?, month_start_day?}` | `User` |
| `PATCH` | `/me/preferences` 🔒 | `{locale?, currency?, timezone?, month_start_day?}` | `User` |
| `PUT` | `/me/username` 🔒 | `{username}` | `User` |
| `POST` | `/me/onboarding` 🔒 | `{step, user_type?, monthly_income?, month_start_day?, currency?, locale?}` | `ProfileResponse` |
| `DELETE` | `/me` 🔒 | `{password, confirm:"DELETE", reason?}` | `{deleted, purge_after, can_reregister, message}` |

`PATCH /me` is a **true partial update** — omitted fields are left untouched.
Sending `{}` returns `BAD_REQUEST`, so a "save" with no changes is caught.

`onboarding.step` ∈ `profile | income | categories | budget | done`.

### 5.4 Billing — `/api/v1/billing`

| Method | Path | Auth | Returns |
|---|---|---|---|
| `GET` | `/plans` | optional | `Plan[]` — `is_current_plan` set when signed in |
| `GET` | `/credit-packs` | optional | `CreditPack[]` |
| `GET` | `/features` | optional | `{features: Feature[], by_category: Record<string, Feature[]>}` |
| `GET` | `/me` | 🔒 | `{entitlement, wallet, subscription?}` |
| `GET` | `/wallet` | 🔒 | `Wallet` |
| `GET` | `/ledger` | 🔒 | `LedgerEntry[]` + `meta` |
| `GET` | `/payments` | 🔒 | `Payment[]` + `meta` |
| `GET` | `/features/:code/access` | 🔒 | `Access` |
| `POST` | `/subscribe` | 🔒 | `CheckoutResult` (201) |
| `POST` | `/credits/buy` | 🔒 | `CheckoutResult` (201) |
| `POST` | `/subscription/cancel` | 🔒 | `Subscription` |
| `POST` | `/payments/confirm` | 🔒 | **sandbox only** — settles a payment |

> `POST /payments/confirm` is registered **only** when `PAYMENT_SANDBOX=true`. In
> production a gateway webhook settles payments; the route does not exist, so a
> client cannot grant itself a plan. Build the success screen to poll
> `GET /billing/me` rather than to call confirm.

### 5.5 The billing model (what the UI must communicate)

Everything premium is metered in one currency: **credits** (the "tokens"). Two ways
to get them:

1. **Subscribe** — a plan grants `monthly_credits` every billing month and unlocks
   a set of `feature_codes`.
2. **Buy a pack** — one-off credits that never expire.

`GET /billing/features/:code/access` answers "can this user run this feature?" and
returns the reason, so **the button label is data-driven**:

| Response | Button |
|---|---|
| `allowed: true`, `included_in_plan: true`, `credit_cost: 0` | **Run** |
| `allowed: true`, `credit_cost: 5` | **Run · 5 credits** |
| `allowed: false`, `needs_credits: true` | **Buy credits** |
| `allowed: false`, `needs_upgrade: true`, `needs_credits: false` | **Upgrade to Pro** |

Spend order is allowance first, purchased second — so a subscriber's bought credits
survive as long as possible. Show both numbers separately in the wallet.

---

## 6. Endpoint reference — planned (NOT yet implemented)

These will return `404 ROUTE_NOT_FOUND` today. Build the screens and typed stubs,
but **gate them behind a flag defaulting to off**. The shapes below are the agreed
contract and will not change.

```ts
export const flags = {
  expenses: false, income: false, categories: false, recurring: false,
  budgets: false, goals: false, dashboard: false, analytics: false,
  ai: false, notifications: false,
} as const;
```

| Group | Endpoints |
|---|---|
| Categories | `GET/POST /categories`, `GET/PATCH/DELETE /categories/:id` |
| Expenses | `GET/POST /expenses`, `GET/PATCH/DELETE /expenses/:id`, `GET /expenses/summary`, `DELETE /expenses/bulk` |
| Income | `GET/POST /incomes`, `GET/PATCH/DELETE /incomes/:id`, `GET /incomes/summary` |
| Recurring | `GET/POST /recurring`, `GET/PATCH/DELETE /recurring/:id`, `POST /recurring/:id/post` |
| Budgets | `GET/POST /budgets`, `GET /budgets/current`, `PATCH/DELETE /budgets/:id`, `PUT /budgets/:id/limits` |
| Goals | `GET/POST /goals`, `GET/PATCH/DELETE /goals/:id`, `POST /goals/:id/contribute` |
| Dashboard | `GET /dashboard` |
| Analytics | `GET /analytics/weekly`, `/monthly`, `/yearly`, `/categories` |
| AI | `POST /ai/:feature_code`, `GET /ai/insights`, `GET /ai/insights/:id`, `POST /ai/insights/:id/feedback` |
| Notifications | `GET /notifications`, `POST /notifications/:id/read`, `POST /notifications/read-all` |

All list endpoints will follow §3.7 exactly. `GET /dashboard` is the important one
— it will return the whole home screen in a single call:

```ts
export interface DashboardResponse {
  period: { start: string; end: string; days_total: number; days_remaining: number };
  income: { planned: number; actual: number };
  expense: { total: number; fixed: number; variable: number; today: number; this_week: number };
  remaining: number;
  budget_used_percent: number;
  safe_to_spend_today: number;             // the headline metric
  health: "safe" | "warning" | "critical";
  top_category: CategorySpend | null;
  categories: CategorySpend[];
  projection: { month_end_spend: number; month_end_balance: number; confidence: number };
  recent: Expense[];
  alerts: { type: string; severity: "info" | "warning" | "critical"; message: string }[];
}
```

---

## 7. TypeScript types

Create `src/types/api.ts` with exactly this. These are transcribed from the Go
structs — do not "improve" them.

```ts
/* ─── envelope ─────────────────────────────────────────────────────────── */
export interface FieldError {
  field: string;
  rule?: string;
  message: string;
  message_bn?: string;
  value?: unknown;
}

export interface Meta {
  page: number; limit: number; total_items: number; total_pages: number;
  count: number; has_next: boolean; has_prev: boolean;
  next_page?: number; prev_page?: number;
  sort?: string; search?: string;
  filters?: Record<string, unknown>;
  extra?: Record<string, unknown>;
}

export interface ApiSuccess<T> {
  success: true; code: string; message: string;
  data: T; meta?: Meta; request_id: string; timestamp: string;
}

export interface ApiFailure {
  success: false; code: ApiErrorCode; message: string;
  errors?: FieldError[]; hint?: string; details?: Record<string, unknown>;
  request_id: string; timestamp: string;
}

export type ApiErrorCode =
  | "OK" | "VALIDATION_ERROR" | "MISSING_FIELD" | "INVALID_FIELD" | "BAD_REQUEST"
  | "UNAUTHORIZED" | "TOKEN_EXPIRED" | "TOKEN_INVALID" | "FORBIDDEN"
  | "PHONE_NOT_VERIFIED" | "ACCOUNT_LOCKED" | "ACCOUNT_SUSPENDED"
  | "NOT_FOUND" | "ROUTE_NOT_FOUND" | "METHOD_NOT_ALLOWED"
  | "CONFLICT" | "DUPLICATE_ENTRY" | "GONE" | "IDEMPOTENCY_CONFLICT"
  | "PAYLOAD_TOO_LARGE" | "UNSUPPORTED_MEDIA_TYPE" | "RATE_LIMIT_EXCEEDED"
  | "OTP_INVALID" | "OTP_LIMIT_REACHED"
  | "PAYMENT_REQUIRED" | "INSUFFICIENT_CREDITS" | "QUOTA_EXCEEDED"
  | "SUBSCRIPTION_REQUIRED" | "PAYMENT_FAILED"
  | "INTERNAL_ERROR" | "DATABASE_ERROR" | "CACHE_ERROR"
  | "UPSTREAM_ERROR" | "SERVICE_UNAVAILABLE" | "TIMEOUT";

/* ─── domain ───────────────────────────────────────────────────────────── */
export type UserType = "personal" | "student" | "job_holder" | "business" | "freelancer" | "family";
export type OnboardingStep = "profile" | "income" | "categories" | "budget" | "done";
export type Tier = "free" | "plus" | "pro" | "business";
export type Health = "safe" | "warning" | "critical";
export type PaymentMethod = "cash" | "card" | "bkash" | "nagad" | "rocket" | "bank" | "due" | "other";

export interface User {
  id: string; name: string;
  username: string | null; email: string | null; phone: string;
  avatar_path: string | null;
  user_type: UserType; role: "user" | "admin" | "support";
  status: "active" | "suspended" | "deleted";
  locale: "bn" | "en"; currency: string; timezone: string;
  monthly_income: number; month_start_day: number;
  phone_verified: boolean; phone_verified_at?: string;
  email_verified: boolean; email_verified_at?: string;
  onboarding_step: OnboardingStep; onboarded_at?: string;
  plan_code: string; plan_expires_at?: string;
  last_login_at?: string; last_seen_at?: string;
  created_at: string; updated_at: string;
}

export interface Tokens {
  access_token: string; refresh_token: string; token_type: "Bearer";
  expires_in: number; expires_at: string; refresh_expires_at: string;
}

export interface AuthResponse {
  user?: User; tokens?: Tokens;
  requires_verification: boolean;
  dev_otp?: string;          // development only
  next_step?: string;
}

export interface OTPResponse {
  phone: string; purpose: string; expires_at: string;
  resend_after_seconds: number; attempts_left: number; dev_otp?: string;
}

export interface SessionInfo {
  id: string; device_name: string | null; ip?: string;
  is_current: boolean; last_used_at: string; expires_at: string;
  created_at: string; revoked_at?: string;
}

export interface Entitlement {
  plan_code: string; tier: Tier; features: string[];
  subscription_status: "none" | "pending" | "trialing" | "active" | "grace" | "expired" | "cancelled" | "refunded";
  expires_at?: string; days_remaining: number;
  allowance_credits: number; purchased_credits: number; total_credits: number;
}

export interface ProfileStats {
  expense_count: number; income_count: number; category_count: number; goal_count: number;
  total_spent: number; total_income: number;
  first_entry_on: string | null; active_days: number;
}

export interface ProfileResponse {
  user: User; entitlement?: Entitlement;
  onboarding_complete: boolean; onboarding_step: OnboardingStep;
  stats?: ProfileStats;
}

export interface UsernameAvailability {
  username: string; available: boolean; reason?: string; suggestions?: string[];
}

export interface Wallet {
  allowance_credits: number; purchased_credits: number; total_credits: number;
  allowance_reset_at?: string;
  lifetime_granted: number; lifetime_purchased: number; lifetime_used: number;
  monthly_spend_cap: number; month_spent: number;
  updated_at: string;
}

export interface Plan {
  code: string; name: string; name_bn?: string;
  tagline: string | null; tagline_bn?: string;
  tier: Tier; period_months: number;
  price: number; list_price: number; currency: string;
  monthly_credits: number; signup_credits: number; allowance_rolls_over: boolean;
  feature_codes: string[]; max_devices: number; trial_days: number;
  is_active: boolean; is_popular: boolean; sort_order: number;
  monthly_price: number; savings_percent: number;
  is_current_plan: boolean; total_credits: number;
}

export interface CreditPack {
  code: string; name: string; name_bn?: string;
  credits: number; bonus_credits: number;
  price: number; list_price: number; currency: string; validity_days: number;
  is_active: boolean; is_popular: boolean; sort_order: number;
  total_credits: number; price_per_credit: number; savings_percent: number;
}

export interface Feature {
  code: string; name: string; name_bn?: string;
  description: string | null; description_bn?: string;
  kind: "module" | "ai_action";
  credit_cost: number; min_tier: Tier; payg_allowed: boolean;
  category: string; icon: string | null; sort_order: number; is_active: boolean;
  included_in_plan: boolean; affordable: boolean;
}

export interface Access {
  feature: Feature; allowed: boolean; reason: string; credit_cost: number;
  entitlement: Entitlement; needs_upgrade: boolean; needs_credits: boolean;
}

export interface Subscription {
  id: string; plan_code: string; status: Entitlement["subscription_status"];
  starts_at: string; ends_at: string; grace_until?: string; next_grant_at?: string;
  grants_made: number; auto_renew: boolean;
  cancelled_at?: string; cancel_reason?: string; payment_id?: string;
  price_paid: number; currency: string;
  plan_name?: string; plan_tier?: string; days_remaining: number;
  created_at: string; updated_at: string;
}

export interface Payment {
  id: string; kind: "subscription" | "credit_pack"; reference_code: string;
  quantity: number; amount: number; currency: string;
  provider: string; provider_ref?: string;
  status: "pending" | "processing" | "paid" | "failed" | "cancelled" | "refunded";
  failure_reason?: string; paid_at?: string; refunded_at?: string; refund_amount: number;
  created_at: string; updated_at: string;
}

export interface CheckoutResult {
  payment: Payment; payment_url?: string;
  instructions: string; already_processed: boolean;
}

export interface LedgerEntry {
  id: number; delta: number;
  allowance_after: number; purchased_after: number;
  reason: "signup_bonus" | "plan_grant" | "plan_signup_bonus" | "pack_purchase"
        | "feature_use" | "refund" | "expiry" | "admin_adjust" | "promo";
  feature_code?: string; reference_type?: string; reference_id?: string;
  description: string | null; created_at: string;
}

/* ─── planned (§6) ─────────────────────────────────────────────────────── */
export interface Category {
  id: string; parent_id?: string; slug: string;
  name: string; name_bn?: string; icon: string; color: string;
  kind: "expense" | "income";
  is_fixed: boolean; is_system: boolean; is_active: boolean; sort_order: number;
  default_limit?: number; created_at: string; updated_at: string;
}

export interface Expense {
  id: string; category_id: string | null; recurring_rule_id?: string;
  amount: number; currency: string; spent_at: string;
  note: string | null; merchant: string | null;
  payment_method: PaymentMethod; tags: string[]; attachment_path?: string;
  source: "manual" | "recurring" | "import" | "ai"; is_anomaly: boolean;
  category_name?: string; category_icon?: string; category_color?: string;
  created_at: string; updated_at: string;
}

export interface CategorySpend {
  category_id: string | null; category_name: string; category_name_bn?: string;
  icon: string; color: string; is_fixed: boolean;
  limit_amount: number; spent: number; transaction_count: number;
  remaining: number; used_percent: number; share_percent: number;
  health: Health; is_overspent: boolean;
}
```

---

## 8. The API client

### 8.1 `src/lib/api/errors.ts`

```ts
import type { ApiFailure, FieldError, ApiErrorCode } from "@/types/api";

export class ApiError extends Error {
  readonly code: ApiErrorCode;
  readonly status: number;
  readonly errors: FieldError[];
  readonly hint?: string;
  readonly details?: Record<string, unknown>;
  readonly requestId: string;

  constructor(status: number, body: ApiFailure) {
    super(body.message);
    this.name = "ApiError";
    this.status = status;
    this.code = body.code;
    this.errors = body.errors ?? [];
    this.hint = body.hint;
    this.details = body.details;
    this.requestId = body.request_id;
  }

  /** Should the paywall open? */
  get isPaywall() {
    return this.status === 402;
  }
  /** Should the form show inline field errors rather than a toast? */
  get isValidation() {
    return this.errors.length > 0;
  }
  get retryAfterSeconds(): number | undefined {
    const v = this.details?.retry_after_seconds;
    return typeof v === "number" ? v : undefined;
  }
}
```

### 8.2 `src/lib/api/server.ts` — for Server Components and Route Handlers

```ts
import "server-only";
import { cookies } from "next/headers";
import { ApiError } from "./errors";
import type { ApiSuccess, ApiFailure, Meta } from "@/types/api";

const BASE = `${process.env.HISABJI_API_URL}/api/v1`;

export interface Result<T> { data: T; meta?: Meta; message: string }

export async function apiFetch<T>(
  path: string,
  init: RequestInit & { token?: string | null } = {},
): Promise<Result<T>> {
  const jar = await cookies();
  const token = init.token !== undefined ? init.token : jar.get("hisabji_at")?.value;

  const res = await fetch(`${BASE}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      "Accept-Language": jar.get("NEXT_LOCALE")?.value ?? "bn",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...init.headers,
    },
    // Financial data is per-user and changes constantly. Opt into caching
    // explicitly per call; never cache by default.
    cache: init.cache ?? "no-store",
  });

  const body = (await res.json()) as ApiSuccess<T> | ApiFailure;
  if (!res.ok || body.success === false) {
    throw new ApiError(res.status, body as ApiFailure);
  }
  return { data: body.data, meta: body.meta, message: body.message };
}
```

### 8.3 `src/app/api/hisabji/[...path]/route.ts` — the proxy

This is the only place a token is attached for browser-originated calls, and the
only place a refresh happens. **The single-flight lock is not optional** — see §4.3.

```ts
import { NextRequest, NextResponse } from "next/server";
import { cookies } from "next/headers";

const BASE = `${process.env.HISABJI_API_URL}/api/v1`;

// One in-flight refresh per server instance. Two parallel refreshes would trip
// the backend's reuse detection and sign the user out of every device.
let refreshing: Promise<string | null> | null = null;

async function refreshAccessToken(refreshToken: string): Promise<string | null> {
  if (refreshing) return refreshing;
  refreshing = (async () => {
    try {
      const res = await fetch(`${BASE}/auth/refresh`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: refreshToken }),
        cache: "no-store",
      });
      const body = await res.json();
      if (!res.ok || !body.success) return null;

      const t = body.data.tokens;
      const jar = await cookies();
      jar.set("hisabji_at", t.access_token, {
        httpOnly: true, secure: process.env.NODE_ENV === "production",
        sameSite: "lax", path: "/", maxAge: t.expires_in,
      });
      jar.set("hisabji_rt", t.refresh_token, {
        httpOnly: true, secure: process.env.NODE_ENV === "production",
        sameSite: "lax", path: "/api", maxAge: 60 * 60 * 24 * 30,
      });
      return t.access_token as string;
    } finally {
      refreshing = null;
    }
  })();
  return refreshing;
}

async function proxy(req: NextRequest, ctx: { params: Promise<{ path: string[] }> }) {
  const { path } = await ctx.params;
  const url = `${BASE}/${path.join("/")}${req.nextUrl.search}`;
  const jar = await cookies();
  const body = ["GET", "HEAD"].includes(req.method) ? undefined : await req.text();

  const call = (token?: string) =>
    fetch(url, {
      method: req.method,
      headers: {
        "Content-Type": "application/json",
        "Accept-Language": jar.get("NEXT_LOCALE")?.value ?? "bn",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
        ...(req.headers.get("x-idempotency-key")
          ? { "X-Idempotency-Key": req.headers.get("x-idempotency-key")! }
          : {}),
      },
      body,
      cache: "no-store",
    });

  let res = await call(jar.get("hisabji_at")?.value);

  // One transparent retry, and only for an EXPIRED token. TOKEN_INVALID means
  // the session is genuinely gone — retrying would just fail again.
  if (res.status === 401) {
    const cloned = await res.clone().json().catch(() => null);
    const rt = jar.get("hisabji_rt")?.value;
    if (cloned?.code === "TOKEN_EXPIRED" && rt) {
      const fresh = await refreshAccessToken(rt);
      if (fresh) res = await call(fresh);
    }
  }

  return new NextResponse(await res.text(), {
    status: res.status,
    headers: { "Content-Type": "application/json" },
  });
}

export const GET = proxy;
export const POST = proxy;
export const PATCH = proxy;
export const PUT = proxy;
export const DELETE = proxy;
```

### 8.4 `src/lib/api/client.ts` — for Client Components

```ts
"use client";
import { ApiError } from "./errors";
import type { ApiSuccess, ApiFailure, Meta } from "@/types/api";

export interface Result<T> { data: T; meta?: Meta; message: string }

async function request<T>(method: string, path: string, opts: {
  body?: unknown; idempotencyKey?: string; signal?: AbortSignal;
} = {}): Promise<Result<T>> {
  const res = await fetch(`/api/hisabji${path}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(opts.idempotencyKey ? { "X-Idempotency-Key": opts.idempotencyKey } : {}),
    },
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
    signal: opts.signal,
  });

  const body = (await res.json()) as ApiSuccess<T> | ApiFailure;
  if (!res.ok || body.success === false) throw new ApiError(res.status, body as ApiFailure);
  return { data: body.data, meta: body.meta, message: body.message };
}

export const api = {
  get:    <T>(p: string, o?: { signal?: AbortSignal }) => request<T>("GET", p, o),
  post:   <T>(p: string, body?: unknown, o?: { idempotencyKey?: string }) => request<T>("POST", p, { body, ...o }),
  patch:  <T>(p: string, body?: unknown) => request<T>("PATCH", p, { body }),
  put:    <T>(p: string, body?: unknown) => request<T>("PUT", p, { body }),
  delete: <T>(p: string, body?: unknown) => request<T>("DELETE", p, { body }),
};
```

### 8.5 Auth route handlers

`src/app/api/auth/login/route.ts` (mirror for `verify-otp`, and a `logout` that
clears both cookies):

```ts
import { NextRequest, NextResponse } from "next/server";
import { cookies } from "next/headers";

export async function POST(req: NextRequest) {
  const res = await fetch(`${process.env.HISABJI_API_URL}/api/v1/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: await req.text(),
    cache: "no-store",
  });
  const body = await res.json();

  if (res.ok && body.success && body.data?.tokens) {
    const t = body.data.tokens;
    const jar = await cookies();
    const secure = process.env.NODE_ENV === "production";
    jar.set("hisabji_at", t.access_token, { httpOnly: true, secure, sameSite: "lax", path: "/", maxAge: t.expires_in });
    jar.set("hisabji_rt", t.refresh_token, { httpOnly: true, secure, sameSite: "lax", path: "/api", maxAge: 60 * 60 * 24 * 30 });
    // Strip the tokens: the browser must never receive them.
    delete body.data.tokens;
  }

  return NextResponse.json(body, { status: res.status });
}
```

---

## 9. TanStack Query + forms

### 9.1 Query keys

```ts
export const qk = {
  profile: (stats = false) => ["profile", { stats }] as const,
  sessions: () => ["sessions"] as const,
  billing:  () => ["billing", "me"] as const,
  wallet:   () => ["billing", "wallet"] as const,
  ledger:   (page: number) => ["billing", "ledger", page] as const,
  plans:    () => ["billing", "plans"] as const,
  packs:    () => ["billing", "packs"] as const,
  features: () => ["billing", "features"] as const,
  access:   (code: string) => ["billing", "access", code] as const,
  // planned
  expenses: (params: Record<string, unknown>) => ["expenses", params] as const,
  dashboard: (period?: string) => ["dashboard", period] as const,
} as const;
```

### 9.2 Global error handling

Do the 401 and 402 handling **once**, in the QueryClient, not per screen:

```tsx
"use client";
import { QueryClient, QueryCache, MutationCache } from "@tanstack/react-query";
import { ApiError } from "@/lib/api/errors";
import { toast } from "sonner";

export function makeQueryClient(onPaywall: (e: ApiError) => void) {
  const handle = (error: unknown) => {
    if (!(error instanceof ApiError)) return;
    if (error.isPaywall) return onPaywall(error);
    if (error.code === "TOKEN_INVALID" || error.code === "UNAUTHORIZED") {
      window.location.href = "/login";
      return;
    }
    if (error.isValidation) return;              // forms render these inline
    toast.error(error.message, { description: error.hint });
  };

  return new QueryClient({
    queryCache: new QueryCache({ onError: handle }),
    mutationCache: new MutationCache({ onError: handle }),
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        retry: (count, error) =>
          error instanceof ApiError && error.status >= 500 && count < 2,
      },
    },
  });
}
```

### 9.3 Server errors → form fields

```ts
import type { UseFormSetError, FieldValues, Path } from "react-hook-form";
import { ApiError } from "@/lib/api/errors";

export function applyServerErrors<T extends FieldValues>(
  error: unknown, setError: UseFormSetError<T>, locale: "bn" | "en" = "bn",
): boolean {
  if (!(error instanceof ApiError) || !error.isValidation) return false;
  for (const e of error.errors) {
    setError(e.field as Path<T>, {
      type: e.rule ?? "server",
      message: locale === "bn" && e.message_bn ? e.message_bn : e.message,
    });
  }
  return true;   // handled — caller should not toast
}
```

Mirror the backend rules in zod so most errors never reach the network — but
**always** apply the server errors too, because uniqueness (`phone` already
registered) can only be known server-side.

```ts
export const phoneSchema = z.string()
  .regex(/^(?:\+?880|0)1[3-9]\d{8}$/, "সঠিক মোবাইল নম্বর দিন, যেমন ০১৭১২৩৪৫৬৭৮।");

export const passwordSchema = z.string()
  .min(8, "কমপক্ষে ৮ অক্ষর")
  .regex(/[A-Za-z]/, "অন্তত একটি অক্ষর থাকতে হবে")
  .regex(/[0-9]/, "অন্তত একটি সংখ্যা থাকতে হবে");

export const usernameSchema = z.string()
  .regex(/^[a-z0-9][a-z0-9_.]{1,28}[a-z0-9]$/, "৩-৩০ অক্ষর, ছোট হাতের অক্ষর ও সংখ্যা");
```

---

## 10. Design system

### 10.1 Principles

1. **Bangla first.** Every string ships in Bangla; English is the fallback. Layout
   must survive Bangla text being ~15% wider and needing more line-height.
2. **Mobile first.** This is a phone app. Design at 360–430px, then widen.
3. **One glance answers one question.** The dashboard's job is *"how much can I
   spend today?"* — that number is the largest thing on the screen.
4. **Brand colour is for interaction. Data colour is for meaning.** Never use the
   brand green to represent income, or a data colour on a button. This is the rule
   that keeps a finance UI readable.
5. **Money is never decoration.** Tabular figures, right-aligned in lists, two
   decimals, always with `৳`.
6. **Calm by default.** Red is reserved for over-budget and destructive actions —
   not for every expense, or the app feels like a scolding.

### 10.2 Colour tokens

`src/app/globals.css`:

```css
@import "tailwindcss";

@theme {
  /* ── brand: interaction only (buttons, active nav, focus, links) ─────── */
  --color-brand-50:  #E6F2EE;
  --color-brand-100: #C2E0D6;
  --color-brand-200: #96CBBB;
  --color-brand-300: #63B49C;
  --color-brand-400: #2F9C7D;
  --color-brand-500: #008665;
  --color-brand-600: #006A4E;   /* primary fill — white text = 6.4:1 ✓ AA */
  --color-brand-700: #00543E;
  --color-brand-800: #003E2D;
  --color-brand-900: #00281D;

  /* ── data & status: meaning only ─────────────────────────────────────── */
  --color-income:     #047857;
  --color-income-bg:  #ECFDF5;
  --color-expense:    #BE123C;
  --color-expense-bg: #FFF1F2;
  --color-warning:    #B45309;
  --color-warning-bg: #FFFBEB;
  --color-info:       #1D4ED8;
  --color-info-bg:    #EFF6FF;

  /* budget health — deliberately the same three as above, so "critical"
     and "expense" read as the same idea: money going the wrong way. */
  --color-health-safe:     var(--color-income);
  --color-health-warning:  var(--color-warning);
  --color-health-critical: var(--color-expense);

  /* ── surfaces ────────────────────────────────────────────────────────── */
  --color-canvas:        #F5F7F6;
  --color-surface:       #FFFFFF;
  --color-surface-sunken:#EEF1EF;
  --color-border:        #E2E7E4;
  --color-border-strong: #C8D0CC;
  --color-text:          #101F1A;
  --color-text-muted:    #55635E;
  --color-text-subtle:   #83908B;

  /* ── radius / shadow / motion ────────────────────────────────────────── */
  --radius-sm: 8px;  --radius-md: 12px; --radius-lg: 16px; --radius-xl: 20px;
  --shadow-xs: 0 1px 2px rgb(16 31 26 / .05);
  --shadow-sm: 0 1px 3px rgb(16 31 26 / .08), 0 1px 2px rgb(16 31 26 / .04);
  --shadow-md: 0 4px 12px rgb(16 31 26 / .08);
  --shadow-lg: 0 12px 32px rgb(16 31 26 / .12);
  --ease-out: cubic-bezier(.16, 1, .3, 1);
}

@media (prefers-color-scheme: dark) {
  @theme {
    --color-canvas:        #0B1310;
    --color-surface:       #121C18;
    --color-surface-sunken:#0E1714;
    --color-border:        #1F2E28;
    --color-border-strong: #2E4038;
    --color-text:          #E8EFEB;
    --color-text-muted:    #9AAAA3;
    --color-text-subtle:   #6B7C75;

    --color-brand-600: #2FA987;   /* lighter fill so it reads on a dark ground */
    --color-brand-500: #4FC0A0;

    --color-income:     #34D399;  --color-income-bg:  #04241B;
    --color-expense:    #FB7185;  --color-expense-bg: #2A0E15;
    --color-warning:    #FBBF24;  --color-warning-bg: #2A1E05;
    --color-info:       #60A5FA;  --color-info-bg:    #0C1A33;
  }
}

@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after { animation-duration: .01ms !important; transition-duration: .01ms !important; }
}
```

Support both a system preference and an explicit toggle by also emitting the dark
block under `[data-theme="dark"]`, and guarding the media block with
`:root:not([data-theme="light"])`.

### 10.3 Typography

Bangla and Latin need different fonts and different line-heights. Load both.

```ts
// src/app/fonts.ts
import { Inter, Anek_Bangla } from "next/font/google";

export const inter = Inter({
  subsets: ["latin"],
  variable: "--font-latin",
  display: "swap",
});

export const anek = Anek_Bangla({
  subsets: ["bengali", "latin"],
  weight: ["400", "500", "600", "700"],
  variable: "--font-bangla",
  display: "swap",
});
```

```css
@theme {
  --font-sans:   var(--font-bangla), var(--font-latin), system-ui, sans-serif;
  --font-number: var(--font-latin), ui-monospace, monospace;
}

/* Money and any column of figures. Tabular numerals stop digits from
   shifting as values change — essential in a ledger. */
.tnum {
  font-family: var(--font-number);
  font-variant-numeric: tabular-nums;
  font-feature-settings: "tnum" 1;
  letter-spacing: -0.01em;
}

/* Bangla glyphs sit taller and need more room than Latin. */
:lang(bn) { line-height: 1.75; }
:lang(en) { line-height: 1.55; }
```

Scale (mobile → desktop):

| Token | Size / line-height | Weight | Use |
|---|---|---|---|
| `display` | 32/40 → 40/48 | 700, `-0.02em` | Safe-to-spend number |
| `h1` | 24/32 | 700 | Screen title |
| `h2` | 20/28 | 600 | Section |
| `h3` | 17/24 | 600 | Card title |
| `body` | 15/24 | 400 | Default (15px, not 14 — Bangla needs it) |
| `body-sm` | 13/20 | 400 | Secondary |
| `caption` | 12/16 | 500 | Labels, metadata |
| `amount-lg` | 28/36 | 700 `.tnum` | Card headline figures |
| `amount` | 17/24 | 600 `.tnum` | List rows |

### 10.4 Spacing, radius, elevation

- Spacing scale: `4 · 8 · 12 · 16 · 20 · 24 · 32 · 40 · 48 · 64`. Screen gutter 16px
  mobile / 24px desktop.
- Radius: cards `16`, buttons & inputs `12`, chips & avatars `full`, sheets `20` (top corners only).
- Elevation: cards use `--shadow-sm`; sheets and popovers `--shadow-lg`. **In dark
  mode use a lighter surface + border instead of a shadow** — shadows are invisible
  on a dark ground.
- Minimum touch target **44 × 44px**.

### 10.5 Component specs

| Component | Spec |
|---|---|
| **Button** | h44 (`lg` h52), radius 12, weight 600. Primary: `brand-600` fill, white text. Secondary: `surface` fill, `border` 1px. Ghost: transparent, `text-muted`. Destructive: `expense` fill. Focus: 2px `brand-500` ring, 2px offset. |
| **Input** | h48, radius 12, 1px `border`, 15px text. Focus: `brand-500` border + 3px `brand-100` ring. Error: `expense` border, message below in 13px `expense`. Label above, 13px 500. |
| **Card** | `surface`, radius 16, `--shadow-sm`, padding 16 (20 on desktop). No border in light mode; 1px `border` in dark. |
| **Stat tile** | Label 12px `text-subtle` uppercase `.05em` → value `amount-lg .tnum` → delta chip. |
| **Amount** | `.tnum`. Income prefixed `+`, expense `−`. Colour only when the sign matters; otherwise `text`. |
| **Category chip** | `background: {color}14` (8% alpha), `color: {color}`, radius full, h28, icon 14px + label 13px. |
| **Progress (budget)** | h8, radius full, track `surface-sunken`, fill = health colour. Over 100%: fill 100% + a 2px `expense` cap. **Also render the percentage as text** — colour alone is not accessible. |
| **Bottom nav** | Fixed, h64 + safe-area inset, `surface`, 1px top `border`. 4 items + a centre FAB. Active: `brand-600` icon + 11px label. |
| **FAB** | 56×56, `brand-600`, `--shadow-lg`, centred and raised 12px above the nav. Opens Add Expense. |
| **Sheet** | Bottom sheet on mobile, dialog ≥768px. Radius 20 top, drag handle, `--shadow-lg`. |
| **Empty state** | Icon 48px `text-subtle`, `h3` title, `body-sm` muted line, one primary action. Never a bare "No data". |
| **Skeleton** | `surface-sunken`, radius matching the real element, 1.4s shimmer. Match the real layout so nothing jumps. |

### 10.6 Charts (Recharts)

- **Use `category.color` from the API** for every series. Never a hardcoded palette
  — that is how the pie chart ends up disagreeing with the list beside it.
- Axes: `text-subtle` 11px, no vertical grid lines, horizontal grid `border` dashed.
- Money axis: compact format (`৳2K`), tooltip shows the full `৳1,500.50`.
- Bars: radius 6 top corners, 60% category gap.
- Line: 2px stroke, no dots except on hover, gradient area at 12% → 0% opacity.
- Always render an accessible `<table class="sr-only">` of the same data.
- Empty: show the axes and an inline message, never a blank box.

### 10.7 Accessibility (required, not optional)

- Text contrast ≥ 4.5:1, UI/graphics ≥ 3:1. The tokens above already satisfy this.
- Never encode meaning in colour alone — pair every health colour with an icon or
  a label (`Safe` / `Warning` / `Over budget`).
- Visible focus ring on every interactive element; never `outline: none`.
- Every input has a `<label>`; errors linked via `aria-describedby` and
  `aria-invalid`.
- Sheets and dialogs trap focus and close on `Escape`.
- Respect `prefers-reduced-motion` (already in the CSS above).
- `<html lang>` switches between `bn` and `en` so the right font and line-height apply.

### 10.8 i18n and numerals

- Default locale **`bn`**, fallback `en`. Send `Accept-Language` on every request so
  the API's `message_bn` fields are meaningful.
- **Digits stay Latin (`১২৩` → `123`) for money and dates.** Bengali numerals break
  tabular alignment and are harder to scan in a ledger. Bangla text, Latin figures —
  this matches how Bangladeshi banking apps actually present numbers.
- Relative dates ("আজ", "গতকাল") for the last 7 days, absolute after that.

---

## 11. Screens

Build in this order — each depends on the previous.

| # | Route | Depends on | Notes |
|---|---|---|---|
| 1 | `/login` | §5.2 | identifier + password. Handle `requires_verification`. |
| 2 | `/register` | §5.2 | name, phone, password. Live username check via §5.3. |
| 3 | `/verify-otp` | §5.2 | 6 boxes, auto-advance, paste support, resend countdown from `resend_after_seconds`, show `attempts_left`. Dev banner for `dev_otp`. |
| 4 | `/forgot-password`, `/reset-password` | §5.2 | Same OTP component. |
| 5 | `/onboarding/[step]` | §5.3 | 4 steps driven by `onboarding_step`; resume where the user left off. |
| 6 | `/` (dashboard) | §6 `GET /dashboard` | **Safe-to-spend is the hero number.** Then health bar, top categories, recent 5, AI insight card. |
| 7 | `/expenses` | §6 | Infinite list grouped by day with daily subtotals. Filter sheet → §3.7 params. |
| 8 | Add Expense sheet | §6 | FAB → amount pad → category grid → date → note. Target: under 5 seconds. |
| 9 | `/analytics` | §6 | Week / Month / Year tabs. |
| 10 | `/budget` | §6 | Per-category limits with progress bars. |
| 11 | `/goals` | §6 | Progress rings, contribute sheet. |
| 12 | `/settings` | §5.3, §5.2 | Profile, preferences, devices (§5.2 sessions), delete account. |
| 13 | `/billing` | §5.4 | Current plan, wallet, ledger, plans, packs. |
| 14 | Paywall sheet | §5.4 | **Global**, opened by any 402. Two tabs: Plans / Buy credits. Preselect per §3.3. |

### 11.1 The paywall (most important commercial surface)

Opened centrally from the 402 handler in §9.2. It must:

- Say **why** it opened, using `error.message` ("This uses 5 credits and you have 3.").
- Preselect the right tab: `INSUFFICIENT_CREDITS`/`QUOTA_EXCEEDED` → **Buy credits**;
  `SUBSCRIPTION_REQUIRED` → **Plans**, scrolled to `details.required_tier`.
- Render plans from `GET /billing/plans` — never hardcode prices. Show
  `monthly_price` as the headline, `price` as the total, and `savings_percent` as a
  badge. Mark `is_popular`.
- Render packs from `GET /billing/credit-packs`, showing `total_credits`
  (credits + bonus) and `price_per_credit`.
- Generate one idempotency key when the sheet opens; reuse it across retries.
- After checkout, poll `GET /billing/me` until `plan_code` or `total_credits`
  changes, then invalidate `qk.billing()` and `qk.wallet()` and retry the original
  action.

---

## 12. Definition of done

A screen is finished when all of these hold:

- [ ] Loading state is a skeleton matching the real layout — no spinners on lists.
- [ ] Empty state has an icon, a sentence and one action.
- [ ] Every error path renders: field errors inline, 402 opens the paywall, 5xx
      shows the `request_id`.
- [ ] Works at 360px wide with no horizontal scroll.
- [ ] Works in Bangla, including long labels wrapping to two lines.
- [ ] Light and dark both readable; contrast checked.
- [ ] Keyboard reachable end to end, focus visible.
- [ ] Every money value uses `.tnum` and `formatBDT`.
- [ ] No hardcoded colour, radius or spacing — tokens only.
- [ ] No endpoint used that is not in §5 (or flag-gated from §6).

---

## Appendix A — quick reference

```
BASE                 http://localhost:8080/api/v1
Envelope             { success, code, message, data, meta?, errors?, hint?, request_id }
Branch on            code (never message)
Money in             string "1500.50"   Money out  number 1500.50
Dates                calendar dates for transactions, instants for audit fields
Auth                 httpOnly cookies via /api/hisabji proxy; never localStorage
Refresh              single-flight only — parallel refresh = signed out everywhere
Paywall              any 402 → global sheet
Icons                lucide names from the API
Category colours     hex from the API — never your own
Brand colour         interaction only, never data
```
