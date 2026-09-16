# Hisabji — Frontend Integration Contract

**Single source of truth for building the Hisabji web client (user app + admin panel)
against the Hisabji Go API.**

Version 2.1 · API `v1` · Every request and response body in §6 was **captured from a
live server**, not written from memory. Backend smoke suite: 80/80 passing.

**Re-verified against the source on 2026-09-11.** All 30 endpoints, their paths,
auth requirements and rate limits are unchanged. Thirteen corrections were applied
to error tables and two response bodies — see [§6.6](#66-changelog--21).

> **Backend-side endpoint reference:** [`docs/api/`](api/README.md) documents the same
> endpoints from the server's point of view — request, response, every error, and the
> route → handler → service → repository → SQL path each one takes. Use this file to
> *build the client*; use `docs/api/` to understand *why the server answers the way it
> does*. If the two ever disagree, the code is right and both are stale.

---

## 0. How to use this file

If you are an AI assistant or a developer generating the frontend, read this file
first and treat it as binding.

**The rules:**

1. **Work one step at a time.** §3 is an ordered build plan. Each step wires **one
   endpoint** to **one screen** and ends in a state you can click. Do not skip
   ahead, and do not build three screens in one pass.
2. **Never invent an endpoint.** §6 lists every endpoint that exists today. §7 lists
   what is planned but **not built** — those return `404 ROUTE_NOT_FOUND` right now,
   so build them behind a flag that defaults to off.
3. **Never invent a response shape.** Everything is the envelope in §4.2; the
   payloads are typed in §8 and shown as real JSON in §6.
4. **Never parse error strings.** Branch on `code` (§4.3). `message` is display-only
   and may be reworded at any time.
5. **Money is never a float you compute with.** See §4.5.
6. **Every user-visible string goes through i18next** (§11). No hardcoded Bangla or
   English in a component, ever — that is what makes the third language cheap.
7. **Use the design tokens in §12.** No ad-hoc colours, radii or spacing.

**Prompt to hand an assistant:**

> Read `docs/FRONTEND_INTEGRATION.md`. Execute **step `<N>`** of the build plan in
> §3 and nothing else. Use the exact request/response contract in §6, the types in
> §8, the API client in §9, i18next per §11 and the design tokens in §12. Do not
> invent endpoints or response shapes. When the step is done, list what you built
> and what to verify by hand.

---

## 1. Tech stack (decided — do not substitute)

| Concern | Choice | Why this one |
|---|---|---|
| Framework | **Next.js 15+ (App Router)** | Server Components render the dashboard with data already fetched; Route Handlers give us the BFF token layer in §5. |
| Language | **TypeScript**, `strict: true` | The contract is large; types are what stop it drifting. |
| Styling | **Tailwind CSS v4** | The tokens in §12 map 1:1 onto `@theme` CSS variables. |
| Components | **shadcn/ui** (Radix primitives) | Copy-in, not a dependency — the tokens apply cleanly. Accessible by default. |
| Server state | **TanStack Query v5** | REST + pagination envelope; handles caching, refetch, optimistic updates. **Never store auth tokens in it.** |
| Client state | **Zustand** | Tiny. UI state only (sheet open, active filter). |
| Forms | **react-hook-form + zod** | The API returns per-field errors (§4.4) that map straight onto `setError`. |
| Charts | **Recharts** | Composable, works with the category colours the API returns. |
| Icons | **lucide-react** | **Required** — the API returns lucide icon names (§4.9). |
| Dates | **date-fns** + `date-fns-tz` | The API works in `Asia/Dhaka` calendar dates, not UTC instants. |
| **i18n** | **i18next + react-i18next** | Bangla ⇄ English switching, adding a language later = drop in one folder. Full wiring in §11. |
| Tables (admin) | **TanStack Table v8** | The admin panel is mostly tables; sorting/paging maps onto §4.7. |
| Toasts | **sonner** | |
| Fonts | `next/font/google` | See §12.3. |

```bash
npx create-next-app@latest hisabji-web --typescript --tailwind --app --src-dir --import-alias "@/*"
cd hisabji-web
npx shadcn@latest init

npm i @tanstack/react-query @tanstack/react-table zustand \
      react-hook-form zod @hookform/resolvers \
      recharts lucide-react date-fns date-fns-tz sonner

# i18n
npm i i18next react-i18next i18next-resources-to-backend \
      i18next-browser-languagedetector accept-language
```

**Do not install:** axios (native `fetch` works in RSC), moment, any CSS-in-JS
library, `next-intl` (we use i18next), any state library beyond the two above.

---

## 2. Environment

```bash
# .env.local — server-only. There is deliberately NO NEXT_PUBLIC_API_URL:
# the browser never talks to the Go API directly. See §5.
HISABJI_API_URL=http://localhost:8080
NODE_ENV=development
```

Start the backend first (details in `docs/server_run_command.doc`):

```bash
cd Hisabji-Server
docker compose up -d postgres redis
go run ./cmd/api            # http://localhost:8080
bash scripts/smoke.sh       # 80 checks, all should pass
```

> **Ports:** Postgres is published on **5433** and Redis on **6380** (not the
> defaults). The API is on **8080**.

---

## 3. Build plan — step by step

Each step is one sitting. A step is **done** when its checklist passes; only then
move to the next. The "Endpoint" column points at the exact contract in §6.

### Phase 0 — Skeleton (no API calls yet)

| Step | What to build | Endpoint | Done when |
|---|---|---|---|
| **0.1** | `create-next-app`, install deps, Tailwind `@theme` tokens (§12.2), fonts (§12.3) | — | `/` renders "Hisabji" in Anek Bangla, dark mode follows the OS. |
| **0.2** | i18next wiring (§11): `[locale]` segment, server `getT`, client `useT`, `bn`+`en` `common.json`, `<LanguageSwitcher/>` | — | `/bn` and `/en` both render; the switcher flips text and `<html lang>`. |
| **0.3** | `src/types/api.ts` (§8), `ApiError` (§9.1), `lib/api/server.ts` (§9.2), the proxy `app/api/hisabji/[...path]/route.ts` (§9.3), `lib/api/client.ts` (§9.4) | `GET /health` | A `/debug` page shows `status: ok` fetched **through the proxy**. |
| **0.4** | `QueryClientProvider` + global error handler (§10.2) + `<Toaster/>` | — | Throwing an `ApiError` in a test query shows one toast, not a crash. |

### Phase 1 — Authentication

| Step | Screen | Endpoint | Done when |
|---|---|---|---|
| **1.1** | `/register` | `POST /auth/register` (§6.2.1) | Valid form → 201, redirect to `/verify-otp`. A `12345` phone shows the inline field error, **not** a toast. |
| **1.2** | `/verify-otp` | `POST /auth/verify-otp` (§6.2.2), `POST /auth/resend-otp` (§6.2.3) | 6 boxes, auto-advance, paste. Wrong code shows `attempts_left`. Resend disabled for `resend_after_seconds`. Dev banner shows `dev_otp`. |
| **1.3** | `/login` | `POST /auth/login` (§6.2.4) | Cookies set by the route handler (§9.5); `requires_verification: true` routes to `/verify-otp` instead of the dashboard. |
| **1.4** | Silent refresh | `POST /auth/refresh` (§6.2.5) | Single-flight lock in the proxy. Set `ACCESS_TOKEN_TTL=60s` on the server and confirm the app never logs itself out. |
| **1.5** | `/forgot-password`, `/reset-password` | §6.2.6, §6.2.7 | Same OTP component reused. After reset, all sessions ended; user must sign in again. |
| **1.6** | `AppShell` + route guard | `GET /users/me` (§6.3.2) | Signed out → `/login`. `onboarding_step !== "done"` → `/onboarding/<step>`. Otherwise the shell with bottom nav. |

### Phase 2 — Onboarding

| Step | Screen | Endpoint | Done when |
|---|---|---|---|
| **2.1** | `/onboarding/[step]` (profile → income → categories → budget → done) | `POST /users/me/onboarding` (§6.3.6) | Progress survives a reload — the step comes from the server, never from local state. |
| **2.2** | Username picker | `GET /users/username-available` (§6.3.1), `PUT /users/me/username` (§6.3.5) | Debounced 400ms check; `admin` shows "reserved" plus the `suggestions`. |

### Phase 3 — Account & settings

| Step | Screen | Endpoint | Done when |
|---|---|---|---|
| **3.1** | `/settings/profile` | `PATCH /users/me` (§6.3.3) | Partial update: changing only the name leaves the email untouched. Empty submit → `BAD_REQUEST` handled. |
| **3.2** | `/settings/preferences` | `PATCH /users/me/preferences` (§6.3.4) | The language switcher **also** persists `locale` here when signed in (§11.6). |
| **3.3** | `/settings/devices` | `GET /auth/sessions` (§6.2.9), `DELETE /auth/sessions/:id` (§6.2.10) | Current device badged via `is_current`; revoking another device removes the row. |
| **3.4** | Change password | `POST /auth/change-password` (§6.2.11) | Shows `other_sessions_ended`. |
| **3.5** | Delete account | `DELETE /users/me` (§6.3.7) | Requires password + the literal word `DELETE`. |

### Phase 4 — Billing (the commercial surface)

| Step | Screen | Endpoint | Done when |
|---|---|---|---|
| **4.1** | `/pricing` (public) | `GET /billing/plans` (§6.4.1) | Prices come from the API. `is_popular` badged, `savings_percent` shown, `is_current_plan` marked when signed in. |
| **4.2** | Credit-pack store | `GET /billing/credit-packs` (§6.4.2) | Shows `total_credits` (credits + bonus) and `price_per_credit`. |
| **4.3** | Feature catalogue | `GET /billing/features` (§6.4.3) | Rendered from `by_category`; no category names hardcoded. |
| **4.4** | `/billing` overview | `GET /billing/me` (§6.4.4), `GET /billing/wallet` (§6.4.5) | Allowance and purchased credits shown **separately**, plus the total. |
| **4.5** | Credit history + payments | `GET /billing/ledger` (§6.4.6), `GET /billing/payments` (§6.4.7) | Pagination driven by `meta.has_next`, not by counting rows. |
| **4.6** | Gated action button | `GET /billing/features/:code/access` (§6.4.8) | Button label is data-driven per the matrix in §6.5. |
| **4.7** | Buy credits flow | `POST /billing/credits/buy` (§6.4.9) → `POST /billing/payments/confirm` (§6.4.12) | One idempotency key per checkout; replay shows `already_processed`. Wallet updates to 63 after a `tokens_60` purchase on 3 credits. |
| **4.8** | Subscribe / cancel | `POST /billing/subscribe` (§6.4.10), `POST /billing/subscription/cancel` (§6.4.11) | Second live subscription → `CONFLICT` handled. Cancel keeps access until `ends_at`. |
| **4.9** | **Global paywall sheet** | any `402` | One handler (§10.2) opens it for all four 402 codes with the right tab preselected (§13.1). |

### Phase 5 — Core money features (backend not shipped yet)

Build **behind flags**, against the stub types in §7. Each screen renders from a
fixture until the endpoint exists, and flips over with a one-line change.

| Step | Screen | Planned endpoint |
|---|---|---|
| **5.1** | Dashboard (`safe_to_spend_today` is the hero number) | `GET /dashboard` |
| **5.2** | Add-expense sheet | `POST /expenses` |
| **5.3** | Expense list, grouped by day | `GET /expenses` |
| **5.4** | Categories | `GET/POST/PATCH/DELETE /categories` |
| **5.5** | Budget | `GET /budgets/current`, `PUT /budgets/:id/limits` |
| **5.6** | Goals | `GET /goals`, `POST /goals/:id/contribute` |
| **5.7** | Analytics | `GET /analytics/*` |
| **5.8** | AI actions (spend credits) | `POST /ai/:feature_code` |

### Phase 6 — Admin panel (backend not shipped yet)

See §14. Same rule: build the shell and the tables now, flag-gated, and wire them
when `/api/v1/admin/*` lands.

| Step | Screen | Planned endpoint |
|---|---|---|
| **6.1** | Admin shell + role guard (`user.role === "admin"`) | `GET /users/me` (already live) |
| **6.2** | Overview KPIs | `GET /admin/stats` |
| **6.3** | Users table + detail drawer | `GET /admin/users`, `GET /admin/users/:id` |
| **6.4** | User actions (suspend, unlock, adjust credits) | `PATCH /admin/users/:id`, `POST /admin/users/:id/credits` |
| **6.5** | Payments / revenue | `GET /admin/payments`, `GET /admin/revenue` |
| **6.6** | Catalogue editor (plans, packs, features) | `PATCH /admin/plans/:code`, … |

### Phase 7 — Polish

| Step | What | Done when |
|---|---|---|
| **7.1** | Skeletons + empty states everywhere | No spinner on any list. |
| **7.2** | Bangla layout pass | Every screen readable at 360px with long Bangla labels wrapping to two lines. |
| **7.3** | Dark mode + contrast audit | §12.7 passes. |
| **7.4** | Keyboard + screen-reader pass | §15 checklist green. |

---

## 4. API contract fundamentals

### 4.1 Base URL and headers

```
Base:    {HISABJI_API_URL}/api/v1
```

| Header | When | Value |
|---|---|---|
| `Content-Type` | every request with a body | `application/json` |
| `Authorization` | authenticated endpoints | `Bearer <access_token>` |
| `Accept-Language` | always | `bn` or `en` — selects which server message the UI should prefer |
| `X-Request-ID` | optional | your own correlation id; echoed back |
| `X-Idempotency-Key` | **required on purchases** | see §4.8 |

Non-versioned endpoints (outside `/api/v1`): `GET /`, `GET /health`,
`GET /health/live`, `GET /health/ready`.

### 4.2 The response envelope

**Every** response — success or failure — has this shape. Write one parser.

```jsonc
// Success (single resource)
{
  "success": true,
  "code": "OK",                                  // "OK" | "CREATED" | "ACCEPTED"
  "message": "Profile fetched successfully.",    // safe to show in a toast
  "data": { /* the resource */ },
  "request_id": "e29efab0ec113427e7476d26",
  "timestamp": "2026-09-10T21:43:49.4768052Z"
}

// Success (list) — `data` is ALWAYS an array, never null
{
  "success": true,
  "code": "OK",
  "message": "Credit history fetched successfully.",
  "data": [ /* items */ ],
  "meta": {
    "page": 1, "limit": 2, "total_items": 1, "total_pages": 1,
    "count": 1, "has_next": false, "has_prev": false,
    "next_page": 2, "prev_page": null,
    "sort": "-created_at", "search": "coffee",
    "filters": { "amount[gte]": "500" },   // echo of what was applied
    "extra": { "total_spent": 12450.00 }   // aggregates, when the endpoint has them
  },
  "request_id": "...", "timestamp": "..."
}

// Failure
{
  "success": false,
  "code": "VALIDATION_ERROR",            // branch on THIS, never on message
  "message": "Some fields are missing or invalid.",
  "errors": [
    {
      "field": "monthly_income",
      "rule": "min",
      "message": "Monthly income cannot be negative.",
      "message_bn": "মাসিক আয় ঋণাত্মক হতে পারে না।",
      "value": -500                       // never present for password/otp/token fields
    }
  ],
  "hint": "Send at least one field, such as name or monthly_income.",
  "details": { "retry_after_seconds": 970, "limit": 5, "window_seconds": 3600 },
  "request_id": "...", "timestamp": "..."
}
```

Rules that hold everywhere:

- `data` for a list is `[]` when empty — never `null`.
- `meta` appears only on paginated list endpoints.
- `errors[]` appears only for `VALIDATION_ERROR`, `MISSING_FIELD`, `INVALID_FIELD`
  and `DUPLICATE_ENTRY`.
- `request_id` is on every response. **Show it in your error UI** — it is the one
  string that finds the request in the server logs.

### 4.3 Error codes → what the UI must do

Branch on `code`. This is the complete catalogue.

| `code` | HTTP | What the UI should do |
|---|---|---|
| `VALIDATION_ERROR` | 422 | Map `errors[]` onto form fields (§4.4). Do not toast. |
| `MISSING_FIELD` | 422 | Same. |
| `INVALID_FIELD` | 422 | Same. |
| `BAD_REQUEST` | 400 | Toast `message` + `hint`. Usually a client bug — log it. |
| `UNAUTHORIZED` | 401 | Redirect to `/login`, clear session. |
| `TOKEN_EXPIRED` | 401 | **Refresh once, retry** (§5.3). Redirect only if refresh fails. |
| `TOKEN_INVALID` | 401 | Clear session, redirect to `/login`. Do **not** retry. |
| `FORBIDDEN` | 403 | Toast. Do not redirect. |
| `PHONE_NOT_VERIFIED` | 403 | Route to `/verify-otp`. |
| `ACCOUNT_LOCKED` | 423 | Show the lock window and a "Reset password" CTA. |
| `ACCOUNT_SUSPENDED` | 403 | Full-screen state with a support link. |
| `NOT_FOUND` | 404 | Empty state on the screen, not a toast. |
| `ROUTE_NOT_FOUND` | 404 | Client bug — that endpoint does not exist. Log loudly. |
| `METHOD_NOT_ALLOWED` | 405 | Client bug. |
| `CONFLICT` | 409 | Toast `message` + `hint`, refetch the affected query. |
| `DUPLICATE_ENTRY` | 409 | Map `errors[0].field` onto the form field. |
| `GONE` | 410 | Refetch; the resource is gone. |
| `IDEMPOTENCY_CONFLICT` | 409 | Generate a fresh key and let the user retry. |
| `PAYLOAD_TOO_LARGE` | 413 | Toast with the limit from `details`. |
| `UNSUPPORTED_MEDIA_TYPE` | 415 | Client bug. |
| `RATE_LIMIT_EXCEEDED` | 429 | Disable the action for `details.retry_after_seconds`, show a countdown. |
| `OTP_INVALID` | 400 | Inline error under the OTP input + `details.attempts_left`. |
| `OTP_LIMIT_REACHED` | 429 | Disable resend, count down from `details.retry_after_seconds`. |
| `PAYMENT_REQUIRED` | 402 | Open the paywall sheet (§13.1). |
| `INSUFFICIENT_CREDITS` | 402 | Paywall, **Buy credits** tab preselected. |
| `QUOTA_EXCEEDED` | 402 | Paywall — free credits used up. |
| `SUBSCRIPTION_REQUIRED` | 402 | Paywall, **Plans** tab, highlight the required tier. |
| `PAYMENT_FAILED` | 402 | Toast + retry CTA. |
| `INTERNAL_ERROR` / `DATABASE_ERROR` / `CACHE_ERROR` | 500 | Generic error screen showing `request_id`. |
| `UPSTREAM_ERROR` | 502 | "Service temporarily unavailable", retry button. |
| `SERVICE_UNAVAILABLE` | 503 | Same, with backoff. Usually Redis is down. |
| `TIMEOUT` | 504 | "That took too long", retry button. |

The five `402` codes are the whole monetisation surface. Handle them in **one**
shared interceptor that opens the paywall — never per screen. Every 402 raised by
the feature gate carries `details.needs_upgrade` and `details.needs_credits`, so
the interceptor picks the right tab from the payload rather than from the code.

### 4.4 Validation errors → form fields

`errors[].field` is the **JSON field name**; nested/indexed paths use
`items[0].amount` notation. This maps directly onto react-hook-form:

```ts
for (const e of err.errors ?? []) {
  form.setError(e.field as never, {
    type: e.rule ?? "server",
    message: locale === "bn" && e.message_bn ? e.message_bn : e.message,
  });
}
```

`rule` values you will see: `required`, `min`, `max`, `len`, `email`, `bdphone`,
`username`, `strongpass`, `oneof`, `uuid`, `unique`, `type`, `numeric`, `date`,
`enum`, `operator`, `incorrect`, `same`, `weak`, `notblank`, `safetext`.

### 4.5 Money format — read this carefully

Amounts are stored server-side as **integer paisa** and serialised as a **JSON
number with exactly two decimals**:

```jsonc
{ "amount": 199.00, "monthly_income": 30000.00, "price_per_credit": 3.32 }
```

**Reading:** `JSON.parse` gives a JS number (`199`). Fine for display and
comparison at Hisabji's scale. **Never** sum many amounts in floating point and
present the result as authoritative — read a server total instead (`meta.extra`,
or a `/summary` endpoint).

**Writing:** send the raw string from the input. The API parses decimal text
exactly and rounds half-up at the second decimal:

```ts
await api.post("/expenses", { amount: "1500.50" });   // ✅ exact
await api.post("/expenses", { amount: 1500.5 });      // ✅ also accepted
await api.post("/expenses", { amount: 0.1 + 0.2 });   // ❌ 0.30000000000000004
```

Accepted: `1500`, `1500.5`, `"1500.50"`, `.5`, `-250.25`.
Rejected: `"1,500"`, `"1.2.3"`, `"1e5"`, `"--5"`, `"abc"`.

**Display:** `৳` prefix, two decimals, thousands separators, **tabular numerals**
(§12.3):

```ts
export const formatBDT = (n: number, opts?: { compact?: boolean }) =>
  new Intl.NumberFormat("en-BD", {
    style: "currency", currency: "BDT",
    minimumFractionDigits: opts?.compact ? 0 : 2,
    maximumFractionDigits: opts?.compact ? 0 : 2,
    notation: opts?.compact ? "compact" : "standard",
  }).format(n);
// formatBDT(1500.5)              -> "৳1,500.50"
// formatBDT(1500.5, {compact:1}) -> "৳2K"   (chart labels only)
```

### 4.6 Dates

Transaction dates (`spent_at`, `received_at`, `period_start`, `target_date`) are
**calendar dates** in the user's timezone. Treat them as dates, never instants:

```ts
import { parseISO, format } from "date-fns";
format(parseISO(expense.spent_at), "d MMM yyyy");   // ✅
new Date(expense.spent_at).toLocaleDateString();     // ❌ shifts by timezone
```

Audit timestamps (`created_at`, `updated_at`, `last_used_at`) are real instants
with an offset (`"2026-09-11T03:43:48.727918+06:00"`) — format those normally.
Envelope `timestamp` is UTC (`Z`).

### 4.7 Query conventions (list endpoints)

| Parameter | Example | Notes |
|---|---|---|
| `page` | `?page=2` | 1-based, default 1 |
| `limit` | `?limit=20` | default 20, max 100 |
| `search` | `?search=coffee` | case-insensitive contains |
| `sort` | `?sort=-spent_at,amount` | `-` = descending, max 4 keys |
| `date_from` / `date_to` | `?date_from=2026-01-01` | inclusive; `date_to` covers the whole day |
| `with_total` | `?with_total=false` | skips the COUNT — use for infinite scroll |
| `include` | `?include=category` | relation expansion where supported |
| `fields` | `?fields=id,amount` | sparse fieldsets where supported |

Filters use `field` or `field[operator]`:

```
?category_id=<uuid>                 eq
?amount[gte]=500                    >=
?amount[between]=100,500            inclusive range
?payment_method[in]=cash,bkash      IN
?note[contains]=lunch               ILIKE %…%
?note[null]=true                    IS NULL
?tags[has]=work                     array contains
```

Operators: `eq` `ne` `gt` `gte` `lt` `lte` `in` `nin` `like` `contains` `starts`
`ends` `between` `null` `has`. An unsupported operator returns a 422 that **lists
the supported ones**.

> **Today:** only `GET /billing/ledger` and `GET /billing/payments` are paginated,
> and they accept **`page` and `limit` only**. The full engine above applies to the
> §7 endpoints when they ship.

Build query strings with a helper, never by concatenation:

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

### 4.8 Rate limits and idempotency

Responses carry `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`
and, on 429, a `Retry-After` header.

**Verified limits** (per IP unless the scope says per user):

| Endpoint | Limit | Scope |
|---|---|---|
| everything under `/api/v1` | 300 / min | IP |
| `POST /auth/register` | 5 / hour | IP |
| `POST /auth/login` | 10 / min | IP |
| `POST /auth/verify-otp` | 15 / min | IP |
| `POST /auth/resend-otp` | 5 / 10 min | IP |
| `POST /auth/refresh` | 60 / hour | IP |
| `POST /auth/forgot-password` | 5 / hour | IP |
| `POST /auth/reset-password` | 10 / hour | IP |
| `POST /auth/change-password` | 5 / hour | user |
| `GET /users/username-available` | 60 / min | IP |
| `PATCH /users/me`, `/me/preferences`, `POST /me/onboarding` | 60 / min | user |
| `PUT /users/me/username` | 5 / hour | user |
| `DELETE /users/me` | 3 / day | user |
| `POST /billing/subscribe` | 10 / hour | user |
| `POST /billing/credits/buy` | 20 / hour | user |
| `POST /billing/subscription/cancel` | 5 / hour | user |
| `POST /billing/payments/confirm` | 30 / hour | user |

> The register limit of **5/hour per IP** bites during development. When it does:
> `bash scripts/reset-rate-limit.sh register` clears that one bucket and you can
> retry immediately; `--list` shows what is currently limited. To stop hitting
> limits at all, run the server with `RATE_LIMIT_ENABLED=false`.

**Idempotency.** Send `X-Idempotency-Key: <uuid>` on `POST /billing/subscribe` and
`POST /billing/credits/buy`. Generate it once when the checkout sheet opens, reuse
it for every retry of that purchase, discard it on success. Replaying a key returns
the original payment with `already_processed: true` — that is what stops a flaky
network from charging twice.

### 4.9 Icons and colours come from the API

`categories.icon` and `features.icon` are **lucide-react icon names**
(`utensils`, `bus`, `home`, `credit-card`, `piggy-bank`, `sparkles`, `timer`,
`message-circle`, `alert-triangle`). Render dynamically:

```tsx
import * as Lucide from "lucide-react";

export function DynamicIcon({ name, ...props }: { name: string } & Lucide.LucideProps) {
  const key = name.split("-").map(p => p[0].toUpperCase() + p.slice(1)).join("");
  const Icon = ((Lucide as Record<string, unknown>)[key] as Lucide.LucideIcon) ?? Lucide.Circle;
  return <Icon {...props} />;
}
```

`categories.color` is a `#RRGGBB` hex. **Use it for that category everywhere** —
chips, chart series, list rows. Never assign your own category colours; the data
already carries them, so the pie chart and the list always agree.

---

## 5. Auth architecture — BFF with httpOnly cookies

### 5.1 The decision

The API returns tokens as JSON. If the browser stored them they would live in
`localStorage`, and any XSS would hand an attacker a 30-day refresh token.

So: **the browser never sees a token.** Next.js Route Handlers act as a
Backend-For-Frontend, holding tokens in `httpOnly` cookies and proxying every call.

```
Browser ──(cookie)──▶ Next Route Handler ──(Bearer)──▶ Go API
```

- Client components call **`/api/hisabji/...`**, never `HISABJI_API_URL`.
- Server Components call the Go API directly via `lib/api/server.ts`.
- CORS is irrelevant in production (same origin). Keep `http://localhost:3000` in
  the server's `CORS_ALLOWED_ORIGINS` only for debugging.

### 5.2 Cookies

| Cookie | Contents | Flags | Max-Age |
|---|---|---|---|
| `hisabji_at` | access token | httpOnly, secure, sameSite=lax, path=/ | 1800 |
| `hisabji_rt` | refresh token | httpOnly, secure, sameSite=lax, path=/api | 2592000 |
| `NEXT_LOCALE` | `bn` \| `en` | **not** httpOnly (the client switcher reads it) | 31536000 |

### 5.3 Token lifecycle

- Access token: **30 min**, a signed JWT carrying `sub`, `role`, `plan`, `sid`.
- Refresh token: **30 days**, an opaque random string checked against the server's
  `sessions` table on every use.
- **Rotation with reuse detection:** every refresh issues a new refresh token and
  revokes the old one. Presenting an already-used token means it was stolen, so the
  server revokes **the whole device family** and returns `TOKEN_INVALID` with
  *"all sessions have been signed out"*.

  → Your client must therefore **never refresh concurrently.** The proxy in §9.3
  serialises refreshes with a single-flight promise. Two parallel refreshes will
  sign the user out of every device.

### 5.4 The full auth flow

```
POST /auth/register     → 201, requires_verification: true, next_step: "verify_phone"
                          (no tokens — an unverified phone is an unproven identity)
POST /auth/verify-otp   → 200, tokens issued, user signed in
POST /auth/login        → 200 + tokens
                          ...unless the phone is unverified: 200 with
                          requires_verification: true and a fresh OTP already sent
POST /auth/refresh      → 200 + a NEW pair (the old refresh token is now dead)
POST /auth/logout       → 200  ({ "all_devices": true } ends every session)
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

**Development helper:** outside production, register/verify/resend responses include
`dev_otp` so you can complete signup without an SMS gateway. It is never present
when `APP_ENV=production`. Show it in a dev-only banner.

---

## 6. Endpoint reference — implemented

🔒 = requires `Authorization`. Every JSON block below is a **real captured
response** from a running server.

### 6.0 Index

| # | Method | Path | Auth |
|---|---|---|---|
| 6.1.1 | `GET` | `/` | — |
| 6.1.2 | `GET` | `/health` | — |
| 6.1.3 | `GET` | `/health/live` | — |
| 6.1.4 | `GET` | `/health/ready` | — |
| 6.2.1 | `POST` | `/api/v1/auth/register` | — |
| 6.2.2 | `POST` | `/api/v1/auth/verify-otp` | — |
| 6.2.3 | `POST` | `/api/v1/auth/resend-otp` | — |
| 6.2.4 | `POST` | `/api/v1/auth/login` | — |
| 6.2.5 | `POST` | `/api/v1/auth/refresh` | — |
| 6.2.6 | `POST` | `/api/v1/auth/forgot-password` | — |
| 6.2.7 | `POST` | `/api/v1/auth/reset-password` | — |
| 6.2.8 | `POST` | `/api/v1/auth/logout` | 🔒 |
| 6.2.9 | `GET` | `/api/v1/auth/sessions` | 🔒 |
| 6.2.10 | `DELETE` | `/api/v1/auth/sessions/:id` | 🔒 |
| 6.2.11 | `POST` | `/api/v1/auth/change-password` | 🔒 |
| 6.3.1 | `GET` | `/api/v1/users/username-available` | optional |
| 6.3.2 | `GET` | `/api/v1/users/me` | 🔒 |
| 6.3.3 | `PATCH` | `/api/v1/users/me` | 🔒 |
| 6.3.4 | `PATCH` | `/api/v1/users/me/preferences` | 🔒 |
| 6.3.5 | `PUT` | `/api/v1/users/me/username` | 🔒 |
| 6.3.6 | `POST` | `/api/v1/users/me/onboarding` | 🔒 |
| 6.3.7 | `DELETE` | `/api/v1/users/me` | 🔒 |
| 6.4.1 | `GET` | `/api/v1/billing/plans` | optional |
| 6.4.2 | `GET` | `/api/v1/billing/credit-packs` | optional |
| 6.4.3 | `GET` | `/api/v1/billing/features` | optional |
| 6.4.4 | `GET` | `/api/v1/billing/me` | 🔒 |
| 6.4.5 | `GET` | `/api/v1/billing/wallet` | 🔒 |
| 6.4.6 | `GET` | `/api/v1/billing/ledger` | 🔒 |
| 6.4.7 | `GET` | `/api/v1/billing/payments` | 🔒 |
| 6.4.8 | `GET` | `/api/v1/billing/features/:code/access` | 🔒 |
| 6.4.9 | `POST` | `/api/v1/billing/credits/buy` | 🔒 |
| 6.4.10 | `POST` | `/api/v1/billing/subscribe` | 🔒 |
| 6.4.11 | `POST` | `/api/v1/billing/subscription/cancel` | 🔒 |
| 6.4.12 | `POST` | `/api/v1/billing/payments/confirm` | 🔒 sandbox only |

---

### 6.1 Meta — outside `/api/v1`, never rate limited

#### 6.1.1 `GET /`

```jsonc
{
  "success": true, "code": "OK", "message": "Hisabji API is running.",
  "data": {
    "api_base": "/api/v1", "docs": "/docs", "environment": "development",
    "health": "/health", "service": "Hisabji API", "version": "1.0.0"
  },
  "request_id": "0c0e57affbd87e9a61fcbcef",
  "timestamp": "2026-09-10T21:43:26.3687355Z"
}
```

> `service` echoes the server's `APP_NAME` env var, so it differs between
> environments — never key any client logic off it.
> `data.docs` currently points at `/docs`, which **is not a mounted route** and
> returns `404 ROUTE_NOT_FOUND`. The documentation lives in the repository at
> [`docs/api/`](api/README.md). Do not link the UI to `/docs`.

#### 6.1.2 `GET /health`

Returns **200** when healthy, **503** when not. Note: this endpoint returns a
**plain object, not the envelope** — it is for probes, not for the app.

```jsonc
{
  "status": "ok",                    // "ok" | "unhealthy"
  "service": "Hisabji API", "version": "1.0.0", "environment": "development",
  "checks": { "database": { "pool": { /* ... */ } }, "redis": { /* ... */ } },
  "timestamp": "2026-09-10T21:43:26Z"
}
```

#### 6.1.3 `GET /health/live` → `{"status":"alive"}`
#### 6.1.4 `GET /health/ready` → `{"status":"ready"|"not_ready","checks":{…},"version":"…"}`

**UI:** use these only on a `/debug` page and for uptime monitoring.

---

### 6.2 Auth — `/api/v1/auth`

#### 6.2.1 `POST /auth/register`

Creates the account and sends an OTP. **No tokens are returned** — an unverified
phone is an unproven identity.

Rate limit: **5 / hour per IP**.

**Request**

```json
{
  "name": "Doc User",
  "phone": "01743485253",
  "password": "hisabji2026",
  "user_type": "job_holder"
}
```

| Field | Type | Required | Rules |
|---|---|---|---|
| `name` | string | ✅ | 2–100, not blank, no unsafe characters |
| `phone` | string | ✅ | Bangladeshi mobile. `+8801…`, `8801…`, `01…` all normalise to `01…` |
| `password` | string | ✅ | strong: ≥8, letters + digits; must not equal the name or contain the phone |
| `email` | string | — | valid email, ≤255 |
| `username` | string | — | `^[a-z0-9][a-z0-9_.]{1,28}[a-z0-9]$` |
| `user_type` | enum | — | `personal` \| `student` \| `job_holder` \| `business` \| `freelancer` \| `family` |
| `locale` | enum | — | `bn` \| `en` |
| `device_name` | string | — | ≤120; defaults to the User-Agent |

**Response `201 CREATED`**

```jsonc
{
  "success": true, "code": "CREATED",
  "message": "Account created. Enter the verification code we sent to your phone.",
  "data": {
    "user": {
      "id": "218dd73c-5b11-49ed-a614-bdbf127bae2f",
      "name": "Doc User", "username": null, "email": null,
      "phone": "01743485253", "avatar_path": null,
      "user_type": "job_holder", "role": "user", "status": "active",
      "locale": "en", "currency": "BDT", "timezone": "Asia/Dhaka",
      "monthly_income": 0.00, "month_start_day": 1,
      "phone_verified": false, "email_verified": false,
      "onboarding_step": "profile", "plan_code": "free",
      "created_at": "2026-09-11T03:43:48.727918+06:00",
      "updated_at": "2026-09-11T03:43:48.727918+06:00"
    },
    "requires_verification": true,
    "dev_otp": "601367",             // development only — never in production
    "next_step": "verify_phone"
  },
  "request_id": "747021f2da6351a950aed55c",
  "timestamp": "2026-09-10T21:43:48.751529Z"
}
```

**Errors**

| Code | HTTP | When |
|---|---|---|
| `VALIDATION_ERROR` | 422 | bad phone/password/name — `errors[]` names the field |
| `DUPLICATE_ENTRY` | 409 | that phone (or email/username) is already registered |
| `RATE_LIMIT_EXCEEDED` | 429 | more than 5 registrations from one IP in an hour |

**UI:** store `phone` in a client store, navigate to `/verify-otp`. Never store the
password. Show `dev_otp` in a dev-only banner.

---

#### 6.2.2 `POST /auth/verify-otp`

Verifies the phone **and signs the user in** — this is where the first token pair
comes from.

Rate limit: **15 / min per IP**.

**Request**

```json
{ "phone": "01743485253", "otp": "601367" }
```

`otp` must be exactly 6 digits.

**Response `200 OK`**

```jsonc
{
  "success": true, "code": "OK",
  "message": "Phone number verified. You are now signed in.",
  "data": {
    "user": { "...": "as above, but phone_verified: true, phone_verified_at set" },
    "tokens": {
      "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
      "refresh_token": "C8npp4q7wKmoH_YMB9Mqs8IJa21X4RSodnJhuGXQYlM",
      "token_type": "Bearer",
      "expires_in": 1800,
      "expires_at": "2026-09-11T04:13:48.9836606+06:00",
      "refresh_expires_at": "2026-10-11T03:43:48.9836606+06:00"
    },
    "requires_verification": false,
    "next_step": "onboarding:profile"
  },
  "request_id": "1af9f1be4b9221c4a30d30fa",
  "timestamp": "2026-09-10T21:43:48.9836606Z"
}
```

**Errors**

| Code | HTTP | When | `details` |
|---|---|---|---|
| `OTP_INVALID` | 400 | wrong code, expired code, already-used code, **or no pending OTP at all** — all four share this one code and a deliberately vague message | `attempts_left` |
| `OTP_LIMIT_REACHED` | 429 | too many wrong guesses on this code. The code is **burned** — the user must request a new one | — |
| `VALIDATION_ERROR` | 422 | not 6 digits / bad phone | — |

**UI:** an OTP is single-use — after success, never resubmit the same code. Send the
tokens to your own `/api/auth/verify-otp` route handler so they land in cookies
(§9.5), then route by `next_step`.

---

#### 6.2.3 `POST /auth/resend-otp`

Rate limit: **5 / 10 min per IP**, plus a server-side resend cooldown of 60s and a
cap of 5 per 6-hour window per phone.

**Request**

```json
{ "phone": "01743485253", "purpose": "register" }
```

`purpose` ∈ `register` (default) | `login` | `reset_password` | `change_phone`.

**Response `200 OK`**

```jsonc
{
  "success": true, "code": "OK",
  "message": "A new verification code has been sent.",
  "data": {
    "phone": "017*****253",          // MASKED — not the number you sent
    "purpose": "register",
    "expires_at": "2026-09-11T03:53:48+06:00",
    "resend_after_seconds": 60,
    "attempts_left": 5,
    "dev_otp": "601367"
  },
  "request_id": "...", "timestamp": "..."
}
```

> ⚠️ **`data.phone` comes back masked** as `017*****253`. Render it as "we sent a
> code to 017\*\*\*\*\*253"; never use it as the value for the next request. Keep
> the real number in your own client store from the register/login step.

**An unregistered number gets this exact same 200** with no SMS sent — otherwise
the endpoint becomes a free check of which phone numbers have accounts. So a
success here does **not** mean the account exists.

**Errors:** `OTP_LIMIT_REACHED` (429) — either inside the 60 s cooldown
(`details.retry_after_seconds`) or over the 5-per-6-hour cap for that number
(`details.window_hours`, `details.max_requests`).

**UI:** disable the resend button for `resend_after_seconds` and show a countdown.

---

#### 6.2.4 `POST /auth/login`

Rate limit: **10 / min per IP**.

**Request**

```json
{ "identifier": "01743485253", "password": "hisabji2026" }
```

`identifier` accepts **phone, email or username**. Phone numbers are normalised, so
`+8801712345678`, `8801712345678` and `01712345678` are the same account.
`device_name` is optional (≤120) and labels the session in the devices list.

**Response `200 OK`** — identical shape to verify-otp:

```jsonc
{
  "success": true, "code": "OK", "message": "Signed in successfully.",
  "data": {
    "user": { "...": "full user object" },
    "tokens": { "access_token": "...", "refresh_token": "...", "token_type": "Bearer",
                "expires_in": 1800, "expires_at": "...", "refresh_expires_at": "..." },
    "requires_verification": false,
    "next_step": "onboarding:profile"
  }
}
```

**The important special case — unverified phone.** Still `200`, but:

```jsonc
{
  "success": true, "code": "OK",
  "message": "Please verify your phone number to continue.",
  "data": { "requires_verification": true, "dev_otp": "123456", "next_step": "verify_phone" }
}
```

A fresh OTP has **already been sent**. Route straight to `/verify-otp`; do not ask
the user to tap "resend".

**Errors**

| Code | HTTP | When | `details` |
|---|---|---|---|
| `UNAUTHORIZED` | 401 | wrong password — **or no such account**, identically worded and identically slow, so login cannot be used to discover which numbers are registered | `attempts_remaining` (wrong password only) |
| `ACCOUNT_LOCKED` | 423 | 5 failed attempts → locked for 6 hours | `locked_until`, `minutes_remaining` |
| `ACCOUNT_SUSPENDED` | 403 | disabled by an admin | — |
| `VALIDATION_ERROR` | 422 | empty identifier/password | — |

A successful password reset clears the lock immediately — that is why the 423's
`hint` says so, and why the lockout screen must offer "Forgot password".

**UI:** show `details.attempts_remaining` after a wrong password — it is the single
biggest reduction in "why am I locked out?" support messages.

---

#### 6.2.5 `POST /auth/refresh`

Rate limit: **60 / hour per IP**. **Single-flight only** (§5.3).

**Request**

```json
{ "refresh_token": "C8npp4q7wKmoH_YMB9Mqs8IJa21X4RSodnJhuGXQYlM" }
```

**Response `200 OK`** — a full `AuthResponse` with a **new pair**; the old refresh
token is now dead.

```jsonc
{
  "success": true, "code": "OK", "message": "Session refreshed.",
  "data": {
    "user": { "...": "refreshed user" },
    "tokens": { "access_token": "...", "refresh_token": "FxsjqLBXAk1MdR7oueRFuBuPpXefN8AUfl0kDtucTfA",
                "token_type": "Bearer", "expires_in": 1800, "...": "..." },
    "requires_verification": false,
    "next_step": "onboarding:income"
  }
}
```

**Errors**

| Code | HTTP | When |
|---|---|---|
| `TOKEN_INVALID` | 401 | unknown or revoked token; **or reused**, in which case the message says *"all sessions have been signed out"* and every device is now logged out; **or** the password was changed after this session started |
| `TOKEN_EXPIRED` | 401 | the refresh token itself is past its 30 days |
| `ACCOUNT_SUSPENDED` | 403 | the account is no longer active |

**UI:** on any of these, clear both cookies and go to `/login`. Never retry.

---

#### 6.2.6 `POST /auth/forgot-password`

Rate limit: **5 / hour per IP**.

**Request** `{ "phone": "01743485253" }`

**Response `200 OK`** — always success, even for an unregistered number (this is
deliberate: a different answer would let anyone enumerate accounts).

```jsonc
{
  "success": true, "code": "OK",
  "message": "If that phone number is registered, a reset code has been sent to it.",
  "data": { "phone": "017*****253", "purpose": "reset_password",
            "expires_at": "...", "resend_after_seconds": 60, "attempts_left": 5,
            "dev_otp": "445120" }
}
```

> ⚠️ `data.phone` is **masked**, exactly as in §6.2.3. Display only.

The one error that *does* surface here is `OTP_LIMIT_REACHED` (429) — a real limit
the user has to see. Everything else stays quiet behind the 200.

**UI:** never say "no account found" — mirror the server's wording.

---

#### 6.2.7 `POST /auth/reset-password`

Rate limit: **10 / hour per IP**.

**Request**

```json
{ "phone": "01743485253", "otp": "445120", "new_password": "hisabji2027" }
```

**Response `200 OK`**

```jsonc
{
  "success": true, "code": "OK",
  "message": "Password reset. Please sign in with your new password.",
  "data": { "sessions_ended": 3 }
}
```

**Errors:** `OTP_INVALID` (400), `VALIDATION_ERROR` (422, weak password).

**UI:** every session was killed — clear cookies and send the user to `/login`.

---

#### 6.2.8 `POST /auth/logout` 🔒

**Request** (an empty body is valid and ends the current session)

```json
{ "all_devices": false }
```

**Response `200 OK`**

```jsonc
{
  "success": true, "code": "OK", "message": "Signed out successfully.",
  "data": { "all_devices": false, "sessions_ended": 1 }
}
```

**UI:** always clear cookies locally too, even if the call fails.

---

#### 6.2.9 `GET /auth/sessions` 🔒

**Response `200 OK`**

```jsonc
{
  "success": true, "code": "OK",
  "message": "Signed-in devices fetched successfully.",
  "data": [
    {
      "id": "55f11d93-2344-4df8-8a87-eb06d64fe945",
      "device_name": "curl/8.11.0", "ip": "::1",
      "is_current": false,
      "last_used_at": "2026-09-11T03:43:49.373993+06:00",
      "expires_at": "2026-10-11T03:43:49.373653+06:00",
      "created_at": "2026-09-11T03:43:49.373993+06:00"
    },
    {
      "id": "796b1f25-c445-400f-bc2d-1b1a49cdd76a",
      "device_name": null, "ip": "::1", "is_current": true,
      "last_used_at": "...", "expires_at": "...", "created_at": "..."
    }
  ]
}
```

Not paginated. **UI:** badge `is_current`, sort by `last_used_at` desc, show
`device_name ?? t("settings.unknownDevice")`.

---

#### 6.2.10 `DELETE /auth/sessions/:id` 🔒

`:id` must be a UUID. **Response `200 OK`**

```jsonc
{ "success": true, "code": "OK", "message": "That device has been signed out.",
  "data": { "id": "55f11d93-2344-4df8-8a87-eb06d64fe945" } }
```

**Errors:** `NOT_FOUND` (404, someone else's session id), `VALIDATION_ERROR` (422,
not a UUID).

---

#### 6.2.11 `POST /auth/change-password` 🔒

Rate limit: **5 / hour per user**.

**Request**

```json
{ "current_password": "hisabji2026", "new_password": "hisabji2027" }
```

**Response `200 OK`**

```jsonc
{ "success": true, "code": "OK",
  "message": "Password changed. Your other devices have been signed out.",
  "data": { "other_sessions_ended": 2 } }
```

**Errors:** `UNAUTHORIZED` (401, wrong current password), `VALIDATION_ERROR` (422,
new equals old — `rule: "same"`).

---

### 6.3 Users — `/api/v1/users`

#### 6.3.1 `GET /users/username-available?username=<name>` (optional auth)

Works signed out (registration) and signed in (changing it — your own current
username does not count as taken). Rate limit: **60 / min per IP**.

**Response `200 OK` — taken**

```jsonc
{
  "success": true, "code": "OK", "message": "That username is reserved.",
  "data": {
    "username": "admin", "available": false,
    "reason": "That username is reserved.",
    "suggestions": ["admin1", "admin01", "admin_bd"]
  }
}
```

**Response `200 OK` — free**

```jsonc
{ "success": true, "code": "OK", "message": "That username is available.",
  "data": { "username": "rasel_ahmed", "available": true } }
```

**Errors:** `VALIDATION_ERROR` (422) when `?username=` is missing.

**UI:** debounce 400 ms, show a spinner in the input, then a tick or a cross plus
the suggestion chips. This is a **200 either way** — do not treat "taken" as an error.

---

#### 6.3.2 `GET /users/me?stats=true` 🔒

The home-screen bootstrap: profile + entitlement + onboarding state in one call.
`?stats=true` adds lifetime counters (costs several aggregate scans — ask for it
only on the dashboard, not on every refresh).

**Response `200 OK`**

```jsonc
{
  "success": true, "code": "OK", "message": "Profile fetched successfully.",
  "data": {
    "user": {
      "id": "218dd73c-5b11-49ed-a614-bdbf127bae2f",
      "name": "Doc User", "username": null, "email": null,
      "phone": "01743485253", "avatar_path": null,
      "user_type": "job_holder", "role": "user", "status": "active",
      "locale": "en", "currency": "BDT", "timezone": "Asia/Dhaka",
      "monthly_income": 0.00, "month_start_day": 1,
      "phone_verified": true, "phone_verified_at": "2026-09-11T03:43:48.970786+06:00",
      "email_verified": false,
      "onboarding_step": "profile", "plan_code": "free",
      "last_login_at": "2026-09-11T03:43:49.384109+06:00",
      "last_seen_at": "2026-09-11T03:43:49.384109+06:00",
      "created_at": "...", "updated_at": "..."
    },
    "entitlement": {
      "plan_code": "free", "tier": "free",
      "features": ["basic_analytics", "safe_to_spend", "custom_categories", "budget_alerts"],
      "subscription_status": "none", "days_remaining": 0,
      "allowance_credits": 0, "purchased_credits": 3, "total_credits": 3
    },
    "onboarding_complete": false,
    "onboarding_step": "profile",
    "stats": {
      "expense_count": 0, "income_count": 0, "category_count": 0, "goal_count": 0,
      "total_spent": 0.00, "total_income": 0.00,
      "first_entry_on": null, "active_days": 0
    }
  }
}
```

**Never returned:** `password_hash`, `failed_login_count`, `locked_until`,
`password_changed_at`, `last_login_ip`, `deleted_at`, `metadata`. Do not build UI
expecting them.

If billing or the stats query fails, this endpoint **still returns 200** with
`entitlement` / `stats` simply absent — a billing hiccup must not break the home
screen. Optional-chain both.

**UI:** this is the route guard's data source. `role === "admin"` is what unlocks
the admin panel (§14).

---

#### 6.3.3 `PATCH /users/me` 🔒

A **true partial update** — omitted fields are untouched. `PUT` is accepted and
behaves identically. Rate limit: **60 / min per user**.

**Request** (send only what changed)

```json
{ "name": "Rasel Ahmed", "monthly_income": 30000 }
```

| Field | Type | Rules |
|---|---|---|
| `name` | string | 2–100, not blank |
| `email` | string | valid email, ≤255 |
| `user_type` | enum | the six values in §6.2.1 |
| `avatar_path` | string | ≤500 |
| `monthly_income` | number/string | ≥ 0, ≤ 100,000,000 |
| `month_start_day` | int | 1–28 |

**Response `200 OK`** — the **plain `User` object** (not `ProfileResponse`):

```jsonc
{ "success": true, "code": "OK", "message": "Profile updated successfully.",
  "data": { "id": "...", "name": "Rasel Ahmed", "monthly_income": 30000.00, "...": "..." } }
```

**Errors**

| Code | HTTP | When |
|---|---|---|
| `BAD_REQUEST` | 400 | `{}` — "No changes were provided." with a `hint` |
| `VALIDATION_ERROR` | 422 | negative income, bad email, `month_start_day` 0 or 29 |
| `DUPLICATE_ENTRY` | 409 | that email belongs to another account |

**UI:** send only dirty fields (`react-hook-form`'s `dirtyFields`) — that is what
makes the partial update actually partial.

---

#### 6.3.4 `PATCH /users/me/preferences` 🔒

**Request**

```json
{ "locale": "bn" }
```

| Field | Rules |
|---|---|
| `locale` | `bn` \| `en` |
| `currency` | 3 letters — but **only `BDT` is accepted**; anything else is rejected, not stored |
| `timezone` | ≤64, e.g. `Asia/Dhaka` |
| `month_start_day` | 1–28 |

**Response `200 OK`** — the updated `User`:

```jsonc
{ "success": true, "code": "OK", "message": "Preferences updated successfully.",
  "data": { "id": "...", "locale": "bn", "currency": "BDT", "timezone": "Asia/Dhaka", "...": "..." } }
```

**Errors**

| Code | HTTP | When |
|---|---|---|
| `BAD_REQUEST` | 400 | empty body — nothing to change |
| `VALIDATION_ERROR` | 422 | locale outside `bn\|en`, `month_start_day` out of 1–28 |
| `INVALID_FIELD` | 422 | `currency` other than `BDT` — *"Only BDT is supported at the moment."* |

**UI:** do not offer a currency picker yet. The API states the limit plainly rather
than accepting the value and then rendering every amount with the wrong symbol.
The language switcher (§11.6) calls this when the user is signed in, so the choice
follows them to a new device.

---

#### 6.3.5 `PUT /users/me/username` 🔒

Rate limit: **5 / hour per user** — a username is nearly an identity, so churn is
capped.

**Request** `{ "username": "doc_user_x1" }`

**Response `200 OK`** — the updated `User`:

```jsonc
{ "success": true, "code": "OK", "message": "Username updated successfully.",
  "data": { "id": "...", "username": "doc_user_x1", "...": "..." } }
```

**Errors**

| Code | HTTP | When |
|---|---|---|
| `VALIDATION_ERROR` | 422 | bad format (3–30 chars, lowercase, `_` and `.` inside only) |
| `DUPLICATE_ENTRY` | 409 | taken — `details.suggestions` carries alternatives |
| `DUPLICATE_ENTRY` | 409 | **reserved word** — same code, message *"That username is reserved and cannot be used."*, no suggestions |
| `RATE_LIMIT_EXCEEDED` | 429 | 6th change within an hour |

Both 409s carry `errors[0].field = "username"` with `rule: "unique"`, so one
`setError` branch handles taken and reserved alike.

---

#### 6.3.6 `POST /users/me/onboarding` 🔒

Advances the wizard and saves that step's data in one round trip.

**Request**

```json
{ "step": "income", "monthly_income": 30000, "month_start_day": 7 }
```

`step` ∈ `profile` | `income` | `categories` | `budget` | `done` (required).
Optional alongside it: `user_type`, `monthly_income`, `month_start_day`,
`currency`, `locale`.

**Response `200 OK`** — a full `ProfileResponse` (no `stats`):

```jsonc
{
  "success": true, "code": "OK", "message": "Setup progress saved.",
  "data": {
    "user": { "monthly_income": 30000.00, "month_start_day": 7,
              "onboarding_step": "income", "...": "..." },
    "entitlement": { "plan_code": "free", "...": "..." },
    "onboarding_complete": false,
    "onboarding_step": "income"
  }
}
```

With `{"step":"done"}` the message becomes *"Setup complete. Welcome to Hisabji."*
and `onboarding_complete` is `true`.

**UI:** drive the wizard from the **server's** `onboarding_step`, never from local
state — that is what makes progress survive a reload or a device change.

---

#### 6.3.7 `DELETE /users/me` 🔒

Soft delete with a 30-day purge. Rate limit: **3 / day per user**.

**Request** — the password is required even though the caller is authenticated:

```json
{ "password": "hisabji2026", "confirm": "DELETE", "reason": "not using it" }
```

`confirm` must be the literal string `DELETE`.

**Response `200 OK`**

```jsonc
{
  "success": true, "code": "OK", "message": "Your account has been closed.",
  "data": {
    "deleted": true, "purge_after": "30 days", "can_reregister": true,
    "message": "Your account has been closed. Your data is permanently removed after 30 days, and 017*****253 can be used to register again immediately."
  }
}
```

**Errors:** `UNAUTHORIZED` (401, wrong password), `VALIDATION_ERROR` (422,
`confirm` not exactly `DELETE`).

**UI:** two-step dialog — type `DELETE`, then enter the password. Show
`data.message` verbatim on the goodbye screen; it explains re-registration.

---

### 6.4 Billing — `/api/v1/billing`

#### 6.4.1 `GET /billing/plans` (optional auth)

Public pricing page; signed in, it also marks `is_current_plan`.

**Response `200 OK`** (real data, trimmed to two plans):

```jsonc
{
  "success": true, "code": "OK", "message": "Subscription plans fetched successfully.",
  "data": [
    {
      "code": "free", "name": "Free", "name_bn": "ফ্রি",
      "tagline": "Track everything. Try AI three times.",
      "tier": "free", "period_months": 0,
      "price": 0.00, "list_price": 0.00, "currency": "BDT",
      "monthly_credits": 0, "signup_credits": 3, "allowance_rolls_over": false,
      "feature_codes": ["basic_analytics", "safe_to_spend", "custom_categories", "budget_alerts"],
      "max_devices": 2, "trial_days": 0,
      "is_active": true, "is_popular": false, "sort_order": 10,
      "monthly_price": 0.00, "savings_percent": 0,
      "is_current_plan": false, "total_credits": 3
    },
    {
      "code": "plus_1m", "name": "Plus Monthly", "name_bn": "প্লাস মাসিক",
      "tagline": "Full AI coaching, one month at a time.",
      "tier": "plus", "period_months": 1,
      "price": 199.00, "list_price": 199.00, "currency": "BDT",
      "monthly_credits": 50, "signup_credits": 0, "allowance_rolls_over": false,
      "feature_codes": ["basic_analytics", "safe_to_spend", "...": "14 codes"],
      "max_devices": 3, "trial_days": 7,
      "is_active": true, "is_popular": false, "sort_order": 20,
      "monthly_price": 199.00, "savings_percent": 0,
      "is_current_plan": false, "total_credits": 50
    }
  ]
}
```

**The six live plans:**

| `code` | Name | Tier | Months | Price ৳ | Monthly credits | Signup credits | Trial | Devices |
|---|---|---|---|---|---|---|---|---|
| `free` | Free | free | 0 | 0 | 0 | 3 | 0 | 2 |
| `plus_1m` | Plus Monthly | plus | 1 | 199 | 50 | 0 | 7 d | 3 |
| `pro_3m` | Pro — 3 Months | pro | 3 | 499 | 100 | 0 | 0 | 4 |
| `pro_6m` | Pro — 6 Months | pro | 6 | 899 | 100 | 100 | 0 | 5 |
| `pro_12m` | Pro — 12 Months | pro | 12 | 1599 | 120 | 300 | 0 | 5 ⭐ popular |
| `business_12m` | Business — 12 Months | business | 12 | 2999 | 250 | 500 | 0 | 8 |

**UI:** headline `monthly_price`, sub-line the full `price`, badge
`savings_percent` when > 0, ribbon on `is_popular`. **Never hardcode a price.**

---

#### 6.4.2 `GET /billing/credit-packs` (optional auth)

**Response `200 OK`** (real, complete):

```jsonc
{
  "success": true, "code": "OK", "message": "Credit packs fetched successfully.",
  "data": [
    { "code": "tokens_20", "name": "Starter — 20 tokens", "name_bn": "স্টার্টার — ২০ টোকেন",
      "credits": 20, "bonus_credits": 0, "price": 99.00, "list_price": 99.00,
      "currency": "BDT", "validity_days": 0, "is_active": true, "is_popular": false,
      "sort_order": 10, "total_credits": 20, "price_per_credit": 4.95, "savings_percent": 0 },
    { "code": "tokens_60", "name": "Popular — 60 tokens", "name_bn": "জনপ্রিয় — ৬০ টোকেন",
      "credits": 50, "bonus_credits": 10, "price": 199.00, "list_price": 249.00,
      "currency": "BDT", "validity_days": 0, "is_active": true, "is_popular": true,
      "sort_order": 20, "total_credits": 60, "price_per_credit": 3.32, "savings_percent": 20 },
    { "code": "tokens_150", "name": "Value — 150 tokens", "credits": 120, "bonus_credits": 30,
      "price": 399.00, "list_price": 599.00, "total_credits": 150,
      "price_per_credit": 2.66, "savings_percent": 33, "...": "..." },
    { "code": "tokens_400", "name": "Bulk — 400 tokens", "credits": 300, "bonus_credits": 100,
      "price": 899.00, "list_price": 1599.00, "total_credits": 400,
      "price_per_credit": 2.25, "savings_percent": 44, "...": "..." }
  ]
}
```

**UI:** show `total_credits` as the headline (it includes the bonus), then
`price`, then `price_per_credit` as the value comparison. `validity_days: 0` means
**never expires** — say so.

---

#### 6.4.3 `GET /billing/features` (optional auth)

Signed in, each feature is annotated with `included_in_plan` and `affordable`, so
one call renders every gated button correctly.

**Response `200 OK`**

```jsonc
{
  "success": true, "code": "OK", "message": "Features fetched successfully.",
  "data": {
    "features": [ /* flat array, sorted */ ],
    "by_category": {
      "ai": [
        { "code": "ai_quick_answer", "name": "Ask Hisabji", "name_bn": "হিসাবজিকে জিজ্ঞাসা",
          "description": "Ask one question about your own money and get a direct answer.",
          "kind": "ai_action", "credit_cost": 1, "min_tier": "free", "payg_allowed": true,
          "category": "ai", "icon": "message-circle", "sort_order": 200, "is_active": true,
          "included_in_plan": false, "affordable": true }
      ],
      "analytics": [ /* ... */ ], "budget": [ /* ... */ ],
      "business": [ /* ... */ ], "core": [ /* ... */ ],
      "savings": [ /* ... */ ], "support": [ /* ... */ ]
    }
  }
}
```

**The 22 live features:**

| Category | `code` | Kind | Credits | Min tier | PAYG |
|---|---|---|---|---|---|
| ai | `ai_quick_answer` | ai_action | 1 | free | ✅ |
| ai | `cash_runway` | ai_action | 1 | free | ✅ |
| ai | `budget_risk` | ai_action | 2 | plus | ✅ |
| ai | `ai_weekly_coach` | ai_action | 2 | plus | ✅ |
| ai | `goal_forecast` | ai_action | 2 | plus | ✅ |
| ai | `spending_leak` | ai_action | 3 | plus | ✅ |
| ai | `category_forecast` | ai_action | 3 | pro | ✅ |
| ai | `savings_planner` | ai_action | 3 | pro | ✅ |
| ai | `ai_monthly_coach` | ai_action | 5 | pro | ✅ |
| ai | `yearly_forecast` | ai_action | 8 | pro | ✅ |
| analytics | `basic_analytics` | module | 0 | free | ✅ |
| analytics | `advanced_reports` | module | 0 | plus | ❌ |
| budget | `safe_to_spend` | module | 0 | free | ✅ |
| budget | `budget_alerts` | module | 0 | free | ✅ |
| business | `business_mode` | module | 0 | business | ❌ |
| business | `business_cashflow` | ai_action | 5 | business | ✅ |
| core | `custom_categories` | module | 0 | free | ✅ |
| core | `smart_recurring` | module | 0 | plus | ❌ |
| core | `data_export` | module | 0 | plus | ❌ |
| core | `family_sharing` | module | 0 | pro | ❌ |
| savings | `goal_tracker` | module | 0 | plus | ❌ |
| support | `priority_support` | module | 0 | pro | ❌ |

`kind: "module"` = an access gate, free once unlocked. `kind: "ai_action"` = costs
`credit_cost` **every time it runs**. `payg_allowed: false` means credits cannot
buy it — only a plan upgrade can.

---

#### 6.4.4 `GET /billing/me` 🔒

Everything the account screen needs in one call.

**Response `200 OK`**

```jsonc
{
  "success": true, "code": "OK", "message": "Billing details fetched successfully.",
  "data": {
    "entitlement": {
      "plan_code": "free", "tier": "free",
      "features": ["basic_analytics", "safe_to_spend", "custom_categories", "budget_alerts"],
      "subscription_status": "none", "days_remaining": 0,
      "allowance_credits": 0, "purchased_credits": 3, "total_credits": 3
    },
    "wallet": {
      "allowance_credits": 0, "purchased_credits": 3,
      "lifetime_granted": 0, "lifetime_purchased": 3, "lifetime_used": 0,
      "monthly_spend_cap": 2000, "month_spent": 0,
      "updated_at": "2026-09-11T03:43:48.727918+06:00",
      "total_credits": 3
    }
    // "subscription": { ... } — present ONLY when a live subscription exists
  }
}
```

`subscription` is **absent** on the free plan. After subscribing:

```jsonc
"subscription": {
  "id": "…", "plan_code": "pro_3m", "status": "active",
  "starts_at": "…", "ends_at": "…", "grants_made": 1,
  "auto_renew": true, "price_paid": 499.00, "currency": "BDT",
  "plan_name": "Pro — 3 Months", "plan_tier": "pro",
  "days_remaining": 90, "created_at": "…", "updated_at": "…"
}
```

**UI:** never assume `subscription` exists — optional-chain it.

---

#### 6.4.5 `GET /billing/wallet` 🔒

The same `Wallet` object `/billing/me` nests, so you have one wallet type, not two.

```jsonc
{
  "success": true, "code": "OK", "message": "Credit balance fetched successfully.",
  "data": {
    "allowance_credits": 0, "purchased_credits": 3, "total_credits": 3,
    "lifetime_granted": 0, "lifetime_purchased": 3, "lifetime_used": 0,
    "monthly_spend_cap": 2000, "month_spent": 0,
    "updated_at": "2026-09-11T03:43:48.727918+06:00"
  }
}
```

**UI:** show allowance and purchased **separately**. Spend order is allowance
first, purchased second — a subscriber's bought credits survive as long as
possible, and users notice when you hide that.

---

#### 6.4.6 `GET /billing/ledger?page=1&limit=20` 🔒

Where the credits went — immutable, newest first. **Paginated** (`page`, `limit`;
limit capped at 100).

**Response `200 OK`**

```jsonc
{
  "success": true, "code": "OK", "message": "Credit history fetched successfully.",
  "data": [
    {
      "id": 22, "delta": 3,
      "allowance_after": 0, "purchased_after": 3,
      "reason": "signup_bonus",
      "description": "Welcome bonus: 3 free AI credits",
      "created_at": "2026-09-11T03:43:48.727918+06:00"
    }
  ],
  "meta": { "page": 1, "limit": 2, "total_items": 1, "total_pages": 1,
            "count": 1, "has_next": false, "has_prev": false }
}
```

`reason` ∈ `signup_bonus` | `plan_grant` | `plan_signup_bonus` | `pack_purchase` |
`feature_use` | `refund` | `expiry` | `admin_adjust` | `promo`.
`delta` is negative for `feature_use`; `feature_code` is then set.

**UI:** `+3` in income green, `−5` in expense red, `reason` translated through
i18next (`billing.ledger.reason.signup_bonus`), `description` shown as the server
sent it.

---

#### 6.4.7 `GET /billing/payments?page=1&limit=20` 🔒

```jsonc
{
  "success": true, "code": "OK", "message": "Payment history fetched successfully.",
  "data": [
    {
      "id": "c0bf38ba-817e-4613-a106-6fad3ea20d8a",
      "kind": "credit_pack", "reference_code": "tokens_60", "quantity": 1,
      "amount": 199.00, "currency": "BDT",
      "provider": "manual", "provider_ref": null,
      "status": "paid",
      "paid_at": "2026-09-11T03:43:50.457723+06:00",
      "refund_amount": 0.00,
      "created_at": "...", "updated_at": "..."
    }
  ],
  "meta": { "page": 1, "limit": 20, "total_items": 1, "...": "..." }
}
```

`status` ∈ `pending` | `processing` | `paid` | `failed` | `cancelled` | `refunded`.
`kind` ∈ `subscription` | `credit_pack`.

---

#### 6.4.8 `GET /billing/features/:code/access` 🔒

"Can I run this, and what will it cost?" — call it before rendering any gated
button. `:code` is a feature code from §6.4.3.

**Response `200 OK`** (free user, 3 credits, asking for a 5-credit pro feature):

```jsonc
{
  "success": true, "code": "OK", "message": "Feature access checked successfully.",
  "data": {
    "feature": {
      "code": "ai_monthly_coach", "name": "AI Monthly Coach", "name_bn": "মাসিক কোচ",
      "description": "A full monthly report: mistakes, comparisons and the three actions that matter most.",
      "kind": "ai_action", "credit_cost": 5, "min_tier": "pro", "payg_allowed": true,
      "category": "ai", "icon": "sparkles", "sort_order": 280, "is_active": true,
      "included_in_plan": false, "affordable": false
    },
    "allowed": false,
    "reason": "This uses 5 credits and you have 3.",
    "credit_cost": 5,
    "entitlement": { "plan_code": "free", "tier": "free", "total_credits": 3, "...": "..." },
    "needs_upgrade": true,
    "needs_credits": true
  }
}
```

**Errors:** `NOT_FOUND` (404, unknown feature code), `VALIDATION_ERROR` (422, `:code`
is not a valid slug).

**UI:** this endpoint **never spends anything**. It is safe to call on render, and
the server re-checks everything before it debits — so a stale check can never grant
free usage.

---

#### 6.4.9 `POST /billing/credits/buy` 🔒

Rate limit: **20 / hour per user**. Send `X-Idempotency-Key`.

**Request** `{ "pack_code": "tokens_60" }`

**Response `201 CREATED`**

```jsonc
{
  "success": true, "code": "CREATED",
  "message": "Checkout started. Complete the payment to receive your credits.",
  "data": {
    "payment": {
      "id": "c0bf38ba-817e-4613-a106-6fad3ea20d8a",
      "kind": "credit_pack", "reference_code": "tokens_60", "quantity": 1,
      "amount": 199.00, "currency": "BDT",
      "provider": "manual", "status": "pending", "refund_amount": 0.00,
      "created_at": "...", "updated_at": "..."
    },
    "instructions": "Send BDT 199.00 and share the transaction id with support, quoting reference c0bf38ba-817e-4613-a106-6fad3ea20d8a. Your purchase is applied as soon as the payment is confirmed.",
    "already_processed": false
  }
}
```

With a hosted gateway there will also be a `payment_url` — redirect to it when
present. With `PAYMENT_PROVIDER=manual` (today) there is none; show
`instructions` verbatim.

**Idempotent replay** returns `200` with `already_processed: true` and the same
payment — do not show a second payment screen.

**Errors**

| Code | HTTP | When |
|---|---|---|
| `NOT_FOUND` | 404 | unknown pack code |
| `CONFLICT` | 409 | that pack is no longer available (`is_active: false`) |
| `IDEMPOTENCY_CONFLICT` | 409 | the key was already used for a **different** purchase — generate a fresh one |
| `VALIDATION_ERROR` | 422 | missing or over-long `pack_code` |
| `RATE_LIMIT_EXCEEDED` | 429 | more than 20 checkouts in an hour |

Unlike a subscription there is **no** "you already have one" check — credit packs
stack, so a user can buy several.

---

#### 6.4.10 `POST /billing/subscribe` 🔒

Rate limit: **10 / hour per user**. Send `X-Idempotency-Key`.

**Request** `{ "plan_code": "pro_3m" }`

**Response `201 CREATED`** — same `CheckoutResult` shape, with
`payment.kind: "subscription"`.

**Errors**

| Code | HTTP | When |
|---|---|---|
| `BAD_REQUEST` | 400 | `plan_code: "free"` — *"The free plan does not need to be purchased."* |
| `NOT_FOUND` | 404 | unknown plan code |
| `CONFLICT` | 409 | a live subscription already exists — `details.current_plan`, `details.expires_at`. Two different messages: *"You are already subscribed to this plan."* when the codes match, *"You already have an active subscription."* otherwise |
| `CONFLICT` | 409 | that plan is no longer available (`is_active: false`) |
| `IDEMPOTENCY_CONFLICT` | 409 | the key was already used for a **different** purchase |
| `RATE_LIMIT_EXCEEDED` | 429 | more than 10 checkouts in an hour |

**UI:** the "already subscribed" 409 is a real product state, not an error to
toast away. Send the user to the account screen showing
`details.current_plan` and `details.expires_at`, with a **Cancel** CTA — a plan
can only be changed after the current one ends.

---

#### 6.4.11 `POST /billing/subscription/cancel` 🔒

Turns off auto-renew. **Access continues until `ends_at`** — the user paid for that
time. Rate limit: **5 / hour per user**. An empty body is valid.

**Request** `{ "reason": "too expensive" }` (optional, ≤300 chars)

**Response `200 OK`** — the updated `Subscription`:

```jsonc
{
  "success": true, "code": "OK",
  "message": "Subscription cancelled. You keep access until it expires.",
  "data": {
    "id": "...", "plan_code": "pro_3m", "status": "active",
    "starts_at": "...", "ends_at": "2026-12-11T...",
    "auto_renew": false,
    "cancelled_at": "...", "cancel_reason": "too expensive",
    "price_paid": 499.00, "currency": "BDT",
    "days_remaining": 90, "...": "..."
  }
}
```

**Errors**

| Code | HTTP | When |
|---|---|---|
| `NOT_FOUND` | 404 | no active subscription — hint: *"You are on the free plan; there is nothing to cancel."* Hide the cancel button when `/billing/me` has no `subscription`. |
| `CONFLICT` | 409 | already cancelled — `details.access_until` |
| `RATE_LIMIT_EXCEEDED` | 429 | more than 5 cancellations in an hour |

**UI:** say *"Active until 11 Dec 2026"*, not *"Cancelled"* — `auto_renew: false`
with `status: "active"` is a live subscription that will not renew. The `reason`
you send is stored as product feedback; a failure to store it never fails the
cancellation.

---

#### 6.4.12 `POST /billing/payments/confirm` 🔒 — **sandbox only**

> This route is **registered only when `PAYMENT_SANDBOX=true`**. In production a
> gateway webhook settles payments and this endpoint **does not exist** — its
> absence is the security control. Build your success screen to poll
> `GET /billing/me`, not to call confirm.

**Request** `{ "payment_id": "c0bf38ba-817e-4613-a106-6fad3ea20d8a" }`

**Response `200 OK`** (credit pack):

```jsonc
{
  "success": true, "code": "OK", "message": "Payment confirmed. Your purchase is active.",
  "data": {
    "already_confirmed": false,
    "payment_id": "c0bf38ba-817e-4613-a106-6fad3ea20d8a",
    "pack_code": "tokens_60",
    "credits_granted": 60,
    "wallet": {
      "allowance_credits": 0, "purchased_credits": 63,
      "lifetime_granted": 0, "lifetime_purchased": 63, "lifetime_used": 0,
      "monthly_spend_cap": 2000, "month_spent": 0,
      "updated_at": "...", "total_credits": 63
    }
  }
}
```

**Response `200 OK`** (subscription): `{ "subscription": {…}, "plan_code": "pro_3m",
"expires_at": "…", "credits_granted": 100, "payment_id": "…", "already_confirmed": false }`.

Confirming twice returns `already_confirmed: true` — idempotent, not an error.

**Errors:** `NOT_FOUND` (404 — also returned for someone else's payment, so the
endpoint cannot be used to discover payment ids), `CONFLICT` (409, refunded or
cancelled payment).

---

### 6.5 The billing model (what the UI must communicate)

Everything premium is metered in one currency: **credits** ("tokens"). Two ways to
get them:

1. **Subscribe** — a plan grants `monthly_credits` each billing month and unlocks a
   set of `feature_codes`.
2. **Buy a pack** — one-off credits that never expire.

`GET /billing/features/:code/access` makes the **button label data-driven**:

| Response | Button |
|---|---|
| `allowed: true`, `included_in_plan: true`, `credit_cost: 0` | **Run** |
| `allowed: true`, `credit_cost: 5` | **Run · 5 credits** |
| `allowed: false`, `needs_credits: true` | **Buy credits** |
| `allowed: false`, `needs_upgrade: true`, `needs_credits: false` | **Upgrade to Pro** |

Spend order: allowance first, purchased second. `monthly_spend_cap` (default 2000)
is a safety cap on credits spent per month — surface it in the wallet before a
heavy user hits it as a surprise.

---

### 6.6 Changelog — 2.1

Re-verified against the Go source on **2026-09-11**. **No endpoint was added,
removed, renamed or re-authed**, and every rate limit in §4.8 still matches the
route files. What changed is accuracy of the error tables and two response bodies.

**Breaking for a client that trusted the old text:**

| § | Was | Is |
|---|---|---|
| 6.2.3 | `data.phone` echoed the number you sent | **Masked** — `017*****253`. Display only; keep the real number client-side. |
| 6.2.6 | same | same — masked |
| 6.3.4 | `currency` accepted any 3 letters | Only `BDT`; anything else is `422 INVALID_FIELD` |
| 6.3.5 | reserved username → `CONFLICT` | `DUPLICATE_ENTRY` (same 409, different code) |

**Errors that were missing and could reach a client:**

| § | Added |
|---|---|
| 6.2.2 | `OTP_LIMIT_REACHED` (429) — too many wrong guesses burns the code. "No pending OTP" is `OTP_INVALID` (400), **not** the `NOT_FOUND` previously listed. |
| 6.2.5 | `TOKEN_EXPIRED` (401), `ACCOUNT_SUSPENDED` (403) |
| 6.4.8 | `VALIDATION_ERROR` (422) on a non-slug `:code` |
| 6.4.9 | `CONFLICT` (409, pack inactive), `IDEMPOTENCY_CONFLICT` (409) |
| 6.4.10 | `CONFLICT` (409, plan inactive), `IDEMPOTENCY_CONFLICT` (409), `RATE_LIMIT_EXCEEDED` (429) |
| 6.4.11 | the whole errors table — `NOT_FOUND` (404), `CONFLICT` (409), `RATE_LIMIT_EXCEEDED` (429) |

**Clarified, nothing to change in a client:**

- §4.3 — there are **five** `402` codes, not four.
- §6.1.1 — `data.docs` points at `/docs`, which is not a mounted route (404).
- §6.2.4 — `ACCOUNT_LOCKED` carries `locked_until` and `minutes_remaining`; the
  rule is 5 failed attempts → 6-hour lock, and a password reset clears it.
- §6.3.2 — `password_changed_at` is also never returned; `entitlement` and `stats`
  can be absent from a 200 when their lookups fail.

---

## 7. Endpoint reference — planned (NOT yet implemented)

These return `404 ROUTE_NOT_FOUND` today. Build screens and typed stubs, but **gate
them behind a flag defaulting to off**. The shapes are the agreed contract.

```ts
export const flags = {
  expenses: false, income: false, categories: false, recurring: false,
  budgets: false, goals: false, dashboard: false, analytics: false,
  ai: false, notifications: false, admin: false,
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
| **Admin** | see §14.2 |

All list endpoints will follow §4.7 exactly. `GET /dashboard` is the important one —
the whole home screen in a single call:

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

## 8. TypeScript types

Create `src/types/api.ts` with exactly this. Transcribed from the Go structs — do
not "improve" them.

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
export type Locale = "bn" | "en";
export type UserType = "personal" | "student" | "job_holder" | "business" | "freelancer" | "family";
export type Role = "user" | "admin" | "support";
export type OnboardingStep = "profile" | "income" | "categories" | "budget" | "done";
export type Tier = "free" | "plus" | "pro" | "business";
export type Health = "safe" | "warning" | "critical";
export type PaymentMethod = "cash" | "card" | "bkash" | "nagad" | "rocket" | "bank" | "due" | "other";

export interface User {
  id: string; name: string;
  username: string | null; email: string | null; phone: string;
  avatar_path: string | null;
  user_type: UserType; role: Role;
  status: "active" | "suspended" | "deleted";
  locale: Locale; currency: string; timezone: string;
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
  /** MASKED, e.g. "017*****253". Display only — never send it back. */
  phone: string;
  purpose: string; expires_at: string;
  resend_after_seconds: number; attempts_left: number; dev_otp?: string;
}

export interface SessionInfo {
  id: string; device_name: string | null; ip?: string;
  is_current: boolean; last_used_at: string; expires_at: string;
  created_at: string; revoked_at?: string;
}

export type SubscriptionStatus =
  | "none" | "pending" | "trialing" | "active" | "grace"
  | "expired" | "cancelled" | "refunded";

export interface Entitlement {
  plan_code: string; tier: Tier; features: string[];
  subscription_status: SubscriptionStatus;
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

export interface FeatureCatalogue {
  features: Feature[];
  by_category: Record<string, Feature[]>;
}

export interface Access {
  feature: Feature; allowed: boolean; reason: string; credit_cost: number;
  entitlement: Entitlement; needs_upgrade: boolean; needs_credits: boolean;
}

export interface Subscription {
  id: string; plan_code: string; status: SubscriptionStatus;
  starts_at: string; ends_at: string; grace_until?: string; next_grant_at?: string;
  grants_made: number; auto_renew: boolean;
  cancelled_at?: string; cancel_reason?: string; payment_id?: string;
  price_paid: number; currency: string;
  plan_name?: string; plan_tier?: string; days_remaining: number;
  created_at: string; updated_at: string;
}

export interface BillingMe {
  entitlement: Entitlement;
  wallet: Wallet;
  subscription?: Subscription;      // absent on the free plan
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

export type LedgerReason =
  | "signup_bonus" | "plan_grant" | "plan_signup_bonus" | "pack_purchase"
  | "feature_use" | "refund" | "expiry" | "admin_adjust" | "promo";

export interface LedgerEntry {
  id: number; delta: number;
  allowance_after: number; purchased_after: number;
  reason: LedgerReason;
  feature_code?: string; reference_type?: string; reference_id?: string;
  description: string | null; created_at: string;
}

export interface ConfirmResult {
  already_confirmed: boolean;
  payment_id: string;
  pack_code?: string;               // credit-pack payments
  plan_code?: string;               // subscription payments
  expires_at?: string;
  credits_granted?: number;
  wallet?: Wallet;
  subscription?: Subscription;
}

/* ─── planned (§7) ─────────────────────────────────────────────────────── */
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

## 9. The API client

### 9.1 `src/lib/api/errors.ts`

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
  get isPaywall() { return this.status === 402; }
  /** Should the form show inline field errors rather than a toast? */
  get isValidation() { return this.errors.length > 0; }
  get retryAfterSeconds(): number | undefined {
    const v = this.details?.retry_after_seconds;
    return typeof v === "number" ? v : undefined;
  }
  get attemptsLeft(): number | undefined {
    const v = this.details?.attempts_left ?? this.details?.attempts_remaining;
    return typeof v === "number" ? v : undefined;
  }
}
```

### 9.2 `src/lib/api/server.ts` — for Server Components and Route Handlers

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

### 9.3 `src/app/api/hisabji/[...path]/route.ts` — the proxy

The only place a token is attached for browser calls, and the only place a refresh
happens. **The single-flight lock is not optional** — see §5.3.

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
      const secure = process.env.NODE_ENV === "production";
      jar.set("hisabji_at", t.access_token, {
        httpOnly: true, secure, sameSite: "lax", path: "/", maxAge: t.expires_in,
      });
      jar.set("hisabji_rt", t.refresh_token, {
        httpOnly: true, secure, sameSite: "lax", path: "/api", maxAge: 60 * 60 * 24 * 30,
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

### 9.4 `src/lib/api/client.ts` — for Client Components

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

### 9.5 Auth route handlers

`src/app/api/auth/login/route.ts` — mirror it for `verify-otp` and `refresh`, and
add a `logout` that clears both cookies:

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

## 10. TanStack Query + forms

### 10.1 Query keys

```ts
export const qk = {
  profile:  (stats = false) => ["profile", { stats }] as const,
  sessions: () => ["sessions"] as const,
  billing:  () => ["billing", "me"] as const,
  wallet:   () => ["billing", "wallet"] as const,
  ledger:   (page: number) => ["billing", "ledger", page] as const,
  payments: (page: number) => ["billing", "payments", page] as const,
  plans:    () => ["billing", "plans"] as const,
  packs:    () => ["billing", "packs"] as const,
  features: () => ["billing", "features"] as const,
  access:   (code: string) => ["billing", "access", code] as const,
  username: (name: string) => ["username", name] as const,
  // planned
  expenses:  (params: Record<string, unknown>) => ["expenses", params] as const,
  dashboard: (period?: string) => ["dashboard", period] as const,
  // admin
  adminStats: () => ["admin", "stats"] as const,
  adminUsers: (params: Record<string, unknown>) => ["admin", "users", params] as const,
} as const;
```

### 10.2 Global error handling

Do 401 and 402 **once**, in the QueryClient, not per screen:

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

### 10.3 Server errors → form fields

```ts
import type { UseFormSetError, FieldValues, Path } from "react-hook-form";
import { ApiError } from "@/lib/api/errors";
import type { Locale } from "@/types/api";

export function applyServerErrors<T extends FieldValues>(
  error: unknown, setError: UseFormSetError<T>, locale: Locale = "bn",
): boolean {
  if (!(error instanceof ApiError) || !error.isValidation) return false;
  for (const e of error.errors) {
    setError(e.field as Path<T>, {
      type: e.rule ?? "server",
      message: locale === "bn" && e.message_bn ? e.message_bn : e.message,
    });
  }
  return true;   // handled — the caller should not toast
}
```

Mirror the backend rules in zod so most errors never reach the network — but
**always** apply the server errors too, because uniqueness (phone already
registered) can only be known server-side. Zod messages come from i18next (§11.5),
never as literals:

```ts
export const makeSchemas = (t: TFunction) => ({
  phone: z.string().regex(/^(?:\+?880|0)1[3-9]\d{8}$/, t("validation.phone")),
  password: z.string()
    .min(8, t("validation.passwordMin"))
    .regex(/[A-Za-z]/, t("validation.passwordLetter"))
    .regex(/[0-9]/, t("validation.passwordDigit")),
  username: z.string().regex(/^[a-z0-9][a-z0-9_.]{1,28}[a-z0-9]$/, t("validation.username")),
});
```

---

## 11. i18next — Bangla ⇄ English

Default **`bn`**, fallback `en`. Adding a third language later must mean: create one
folder, add one array entry. Nothing else.

### 11.1 Structure

```
src/i18n/
  settings.ts              locales, default, namespaces
  server.ts                getT()  — Server Components
  client.ts                useT()  — Client Components
  provider.tsx             <I18nProvider>
  locales/
    bn/  common.json auth.json onboarding.json dashboard.json
         billing.json settings.json admin.json errors.json validation.json
    en/  (the same files)
```

Route shape: `src/app/[locale]/…` — the locale is in the URL, so a shared link
keeps its language and the server can render the right text.

### 11.2 `src/i18n/settings.ts`

```ts
export const locales = ["bn", "en"] as const;      // ← add "hi" here and nothing else changes
export type AppLocale = (typeof locales)[number];

export const defaultLocale: AppLocale = "bn";
export const cookieName = "NEXT_LOCALE";

export const namespaces = [
  "common", "auth", "onboarding", "dashboard",
  "billing", "settings", "admin", "errors", "validation",
] as const;
export const defaultNS = "common";

export function i18nOptions(locale: AppLocale, ns: string | string[] = defaultNS) {
  return {
    supportedLngs: locales,
    fallbackLng: "en",
    lng: locale,
    fallbackNS: defaultNS,
    defaultNS,
    ns,
    interpolation: { escapeValue: false },   // React already escapes
  };
}
```

### 11.3 `src/i18n/server.ts`

```ts
import "server-only";
import { createInstance, type i18n } from "i18next";
import { initReactI18next } from "react-i18next/initReactI18next";
import resourcesToBackend from "i18next-resources-to-backend";
import { i18nOptions, type AppLocale } from "./settings";

async function initI18next(locale: AppLocale, ns: string | string[]): Promise<i18n> {
  const instance = createInstance();
  await instance
    .use(initReactI18next)
    .use(resourcesToBackend((lng: string, namespace: string) =>
      import(`./locales/${lng}/${namespace}.json`)))
    .init(i18nOptions(locale, ns));
  return instance;
}

/** Use inside Server Components and Route Handlers. */
export async function getT(locale: AppLocale, ns: string | string[] = "common") {
  const i18nextInstance = await initI18next(locale, ns);
  return {
    t: i18nextInstance.getFixedT(locale, Array.isArray(ns) ? ns[0] : ns),
    i18n: i18nextInstance,
  };
}
```

### 11.4 Client provider and hook

`src/i18n/provider.tsx`:

```tsx
"use client";
import { createInstance } from "i18next";
import { I18nextProvider, initReactI18next } from "react-i18next";
import resourcesToBackend from "i18next-resources-to-backend";
import { i18nOptions, type AppLocale } from "./settings";
import { useState } from "react";

export function I18nProvider({
  locale, resources, children,
}: { locale: AppLocale; resources: Record<string, unknown>; children: React.ReactNode }) {
  const [instance] = useState(() => {
    const i = createInstance();
    i.use(initReactI18next)
     .use(resourcesToBackend((lng: string, ns: string) =>
        import(`./locales/${lng}/${ns}.json`)))
     .init({ ...i18nOptions(locale, Object.keys(resources)), resources: { [locale]: resources } });
    return i;
  });
  return <I18nextProvider i18n={instance}>{children}</I18nextProvider>;
}
```

`src/app/[locale]/layout.tsx`:

```tsx
import { notFound } from "next/navigation";
import { locales, type AppLocale } from "@/i18n/settings";
import { I18nProvider } from "@/i18n/provider";
import { anek, inter } from "../fonts";

export function generateStaticParams() {
  return locales.map((locale) => ({ locale }));
}

export default async function LocaleLayout({
  children, params,
}: { children: React.ReactNode; params: Promise<{ locale: string }> }) {
  const { locale } = await params;
  if (!locales.includes(locale as AppLocale)) notFound();

  const common = (await import(`@/i18n/locales/${locale}/common.json`)).default;

  return (
    // lang drives the font stack and line-height in §12.3 — it is not decoration
    <html lang={locale} className={`${anek.variable} ${inter.variable}`}>
      <body>
        <I18nProvider locale={locale as AppLocale} resources={{ common }}>
          {children}
        </I18nProvider>
      </body>
    </html>
  );
}
```

Middleware redirects a bare `/` to the cookie's locale, or `bn`:

```ts
// src/middleware.ts
import { NextRequest, NextResponse } from "next/server";
import acceptLanguage from "accept-language";
import { locales, defaultLocale, cookieName } from "@/i18n/settings";

acceptLanguage.languages([...locales]);

export const config = {
  matcher: ["/((?!api|_next/static|_next/image|favicon.ico|.*\\..*).*)"],
};

export function middleware(req: NextRequest) {
  const { pathname } = req.nextUrl;
  if (locales.some((l) => pathname.startsWith(`/${l}`))) return NextResponse.next();

  const locale =
    req.cookies.get(cookieName)?.value ??
    acceptLanguage.get(req.headers.get("Accept-Language")) ??
    defaultLocale;

  return NextResponse.redirect(new URL(`/${locale}${pathname}${req.nextUrl.search}`, req.url));
}
```

### 11.5 Resource files

Keep keys **semantic**, not literal (`auth.login.submit`, never `auth.signInButton`).
`src/i18n/locales/bn/auth.json`:

```json
{
  "login": {
    "title": "সাইন ইন করুন",
    "identifier": "মোবাইল, ইমেইল বা ইউজারনেম",
    "password": "পাসওয়ার্ড",
    "submit": "সাইন ইন",
    "forgot": "পাসওয়ার্ড ভুলে গেছেন?",
    "attemptsLeft_one": "আর {{count}} বার চেষ্টা করতে পারবেন",
    "attemptsLeft_other": "আর {{count}} বার চেষ্টা করতে পারবেন"
  },
  "otp": {
    "title": "মোবাইল নম্বর যাচাই",
    "sentTo": "{{phone}} নম্বরে কোড পাঠানো হয়েছে",
    "resendIn": "{{seconds}} সেকেন্ড পর আবার পাঠাতে পারবেন",
    "resend": "আবার কোড পাঠান"
  }
}
```

`src/i18n/locales/bn/errors.json` — **keyed by the API's error `code`**, so server
copy can be overridden without touching any component:

```json
{
  "VALIDATION_ERROR": "কিছু তথ্য সঠিক নয়। নিচে দেখুন।",
  "UNAUTHORIZED": "সাইন ইন করুন।",
  "ACCOUNT_LOCKED": "অনেকবার ভুল হয়েছে। কিছুক্ষণ পর আবার চেষ্টা করুন।",
  "OTP_INVALID": "কোডটি সঠিক নয়।",
  "RATE_LIMIT_EXCEEDED": "একটু ধীরে চেষ্টা করুন।",
  "INSUFFICIENT_CREDITS": "পর্যাপ্ত ক্রেডিট নেই।",
  "SUBSCRIPTION_REQUIRED": "এই সুবিধাটির জন্য প্ল্যান আপগ্রেড করতে হবে।",
  "INTERNAL_ERROR": "কিছু একটা সমস্যা হয়েছে। আবার চেষ্টা করুন।",
  "fallback": "কিছু একটা সমস্যা হয়েছে।"
}
```

### 11.6 Server messages vs. UI copy — the rule

The API sends `message` (English) and, on validation errors, `message_bn`. Decide
once, in one helper:

```ts
import i18next from "i18next";
import { ApiError } from "@/lib/api/errors";

/** Field errors: prefer the server's own Bangla — it names the actual field. */
export const fieldMessage = (e: FieldError, locale: Locale) =>
  locale === "bn" && e.message_bn ? e.message_bn : e.message;

/** Top-level errors: prefer OUR translation, fall back to the server's message. */
export function errorMessage(err: ApiError): string {
  const key = `errors:${err.code}`;
  return i18next.exists(key) ? i18next.t(key) : err.message;
}
```

**Language switcher** — cookie + URL + (when signed in) the server:

```tsx
"use client";
import { usePathname, useRouter } from "next/navigation";
import { useTranslation } from "react-i18next";
import { api } from "@/lib/api/client";
import { locales, cookieName, type AppLocale } from "@/i18n/settings";

export function LanguageSwitcher({ signedIn }: { signedIn: boolean }) {
  const { i18n } = useTranslation();
  const router = useRouter();
  const pathname = usePathname();

  async function switchTo(next: AppLocale) {
    document.cookie = `${cookieName}=${next}; path=/; max-age=31536000; samesite=lax`;
    await i18n.changeLanguage(next);
    // Persist to the account so the choice follows the user to a new device.
    if (signedIn) {
      await api.patch("/users/me/preferences", { locale: next }).catch(() => {});
    }
    const rest = pathname.replace(/^\/(bn|en)/, "");
    router.push(`/${next}${rest || "/"}`);
    router.refresh();   // re-render Server Components in the new language
  }

  return (
    <div role="group" aria-label="Language">
      {locales.map((l) => (
        <button key={l} onClick={() => switchTo(l)} aria-pressed={i18n.language === l}>
          {l === "bn" ? "বাংলা" : "English"}
        </button>
      ))}
    </div>
  );
}
```

On sign-in, if `user.locale` differs from the cookie, follow **the account** — it is
the user's deliberate choice, made on some device.

### 11.7 Numerals and formatting

- **Digits stay Latin** (`123`, not `১২৩`) for money, dates and counts. Bengali
  numerals break tabular alignment and are harder to scan in a ledger. Bangla text,
  Latin figures — this is how Bangladeshi banking apps actually present numbers.
- Plurals use i18next's `_one` / `_other` suffixes, never string concatenation.
- Relative dates ("আজ", "গতকাল") for the last 7 days, absolute after that.
- Always send `Accept-Language` (§9.2, §9.3 already do).

### 11.8 Adding a third language later

1. `cp -r src/i18n/locales/en src/i18n/locales/hi`
2. Translate the JSON files.
3. Add `"hi"` to `locales` in `settings.ts`.

That is the whole change — because no component contains a literal string.

---

## 12. Design system

### 12.1 Principles

1. **Bangla first.** Every string ships in Bangla; English is the fallback. Layout
   must survive Bangla text being ~15% wider with more line-height.
2. **Mobile first.** Design at 360–430px, then widen. (The admin panel is the one
   exception — see §14.4.)
3. **One glance answers one question.** The dashboard's job is *"how much can I
   spend today?"* — that number is the largest thing on the screen.
4. **Brand colour is for interaction. Data colour is for meaning.** Never use brand
   green for income, or a data colour on a button. This is the rule that keeps a
   finance UI readable.
5. **Money is never decoration.** Tabular figures, right-aligned in lists, two
   decimals, always `৳`.
6. **Calm by default.** Red is for over-budget and destructive actions only — not
   for every expense, or the app feels like a scolding.

### 12.2 Colour tokens

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
  --color-income:     #047857;  --color-income-bg:  #ECFDF5;
  --color-expense:    #BE123C;  --color-expense-bg: #FFF1F2;
  --color-warning:    #B45309;  --color-warning-bg: #FFFBEB;
  --color-info:       #1D4ED8;  --color-info-bg:    #EFF6FF;

  /* budget health — deliberately the same three, so "critical" and
     "expense" read as one idea: money going the wrong way. */
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

/* System preference — guarded so an explicit light choice still wins. */
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) {
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
    --color-income:  #34D399; --color-income-bg:  #04241B;
    --color-expense: #FB7185; --color-expense-bg: #2A0E15;
    --color-warning: #FBBF24; --color-warning-bg: #2A1E05;
    --color-info:    #60A5FA; --color-info-bg:    #0C1A33;
  }
}

/* Explicit toggle — same block, so the switch wins in both directions. */
:root[data-theme="dark"] { /* repeat the dark values above */ }

@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after { animation-duration: .01ms !important; transition-duration: .01ms !important; }
}
```

### 12.3 Typography

Bangla and Latin need different fonts and different line-heights. Load both.

```ts
// src/app/fonts.ts
import { Inter, Anek_Bangla } from "next/font/google";

export const inter = Inter({ subsets: ["latin"], variable: "--font-latin", display: "swap" });
export const anek  = Anek_Bangla({
  subsets: ["bengali", "latin"], weight: ["400", "500", "600", "700"],
  variable: "--font-bangla", display: "swap",
});
```

```css
@theme {
  --font-sans:   var(--font-bangla), var(--font-latin), system-ui, sans-serif;
  --font-number: var(--font-latin), ui-monospace, monospace;
}

/* Money and any column of figures. Tabular numerals stop digits shifting as
   values change — essential in a ledger. */
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

### 12.4 Spacing, radius, elevation

- Spacing scale: `4 · 8 · 12 · 16 · 20 · 24 · 32 · 40 · 48 · 64`. Screen gutter 16px
  mobile / 24px desktop.
- Radius: cards `16`, buttons & inputs `12`, chips & avatars `full`, sheets `20`
  (top corners only).
- Elevation: cards `--shadow-sm`; sheets and popovers `--shadow-lg`. **In dark mode
  use a lighter surface + border instead of a shadow** — shadows are invisible on a
  dark ground.
- Minimum touch target **44 × 44px**.

### 12.5 Component specs

| Component | Spec |
|---|---|
| **Button** | h44 (`lg` h52), radius 12, weight 600. Primary: `brand-600` fill, white text. Secondary: `surface` fill, 1px `border`. Ghost: transparent, `text-muted`. Destructive: `expense` fill. Focus: 2px `brand-500` ring, 2px offset. |
| **Input** | h48, radius 12, 1px `border`, 15px text. Focus: `brand-500` border + 3px `brand-100` ring. Error: `expense` border, message below in 13px `expense`. Label above, 13px 500. |
| **OTP input** | 6 boxes 48×56, radius 12, `.tnum` 20px centred, auto-advance, paste-aware, `inputmode="numeric"`, `autocomplete="one-time-code"`. |
| **Card** | `surface`, radius 16, `--shadow-sm`, padding 16 (20 desktop). No border in light mode; 1px `border` in dark. |
| **Stat tile** | Label 12px `text-subtle` uppercase `.05em` → value `amount-lg .tnum` → delta chip. |
| **Amount** | `.tnum`. Income prefixed `+`, expense `−`. Colour only when the sign matters; otherwise `text`. |
| **Category chip** | `background: {color}14` (8% alpha), `color: {color}`, radius full, h28, icon 14px + label 13px. |
| **Progress (budget)** | h8, radius full, track `surface-sunken`, fill = health colour. Over 100%: fill 100% + a 2px `expense` cap. **Also render the percentage as text** — colour alone is not accessible. |
| **Credit badge** | Pill, `brand-50` bg, `brand-700` text, `sparkles` icon + `.tnum` count. Used in the app bar and on every gated button. |
| **Bottom nav** | Fixed, h64 + safe-area inset, `surface`, 1px top `border`. 4 items + a centre FAB. Active: `brand-600` icon + 11px label. |
| **FAB** | 56×56, `brand-600`, `--shadow-lg`, centred and raised 12px above the nav. Opens Add Expense. |
| **Sheet** | Bottom sheet on mobile, dialog ≥768px. Radius 20 top, drag handle, `--shadow-lg`. |
| **Empty state** | Icon 48px `text-subtle`, `h3` title, `body-sm` muted line, one primary action. Never a bare "No data". |
| **Skeleton** | `surface-sunken`, radius matching the real element, 1.4s shimmer. Match the real layout so nothing jumps. |

### 12.6 Charts (Recharts)

- **Use `category.color` from the API** for every series. Never a hardcoded palette
  — that is how a pie chart ends up disagreeing with the list beside it.
- Axes: `text-subtle` 11px, no vertical grid lines, horizontal grid `border` dashed.
- Money axis: compact (`৳2K`); tooltip shows the full `৳1,500.50`.
- Bars: radius 6 top corners, 60% category gap. Line: 2px stroke, dots on hover
  only, gradient area 12% → 0%.
- Always render an accessible `<table class="sr-only">` of the same data.
- Empty: show the axes and an inline message, never a blank box.

### 12.7 Accessibility (required, not optional)

- Text contrast ≥ 4.5:1, UI/graphics ≥ 3:1. The tokens above satisfy this.
- Never encode meaning in colour alone — pair every health colour with an icon or a
  label (`Safe` / `Warning` / `Over budget`).
- Visible focus ring on every interactive element; never `outline: none`.
- Every input has a `<label>`; errors linked via `aria-describedby` and
  `aria-invalid`.
- Sheets and dialogs trap focus and close on `Escape`.
- Respect `prefers-reduced-motion` (already in the CSS).
- `<html lang>` switches between `bn` and `en` so the right font and line-height
  apply (§11.4).

---

## 13. Screens — user app

Build in the order of §3.

| # | Route | Endpoint | Notes |
|---|---|---|---|
| 1 | `/[locale]/login` | §6.2.4 | identifier + password. Handle `requires_verification`. |
| 2 | `/[locale]/register` | §6.2.1 | name, phone, password. Live username check via §6.3.1. |
| 3 | `/[locale]/verify-otp` | §6.2.2, §6.2.3 | 6 boxes, auto-advance, paste, resend countdown, `attempts_left`. Dev banner for `dev_otp`. |
| 4 | `/[locale]/forgot-password`, `/reset-password` | §6.2.6, §6.2.7 | Same OTP component. |
| 5 | `/[locale]/onboarding/[step]` | §6.3.6 | 4 steps driven by the server's `onboarding_step`. |
| 6 | `/[locale]/` (dashboard) | §7 `GET /dashboard` | **Safe-to-spend is the hero number.** Health bar, top categories, recent 5, AI insight card. |
| 7 | `/[locale]/expenses` | §7 | Infinite list grouped by day with daily subtotals. Filter sheet → §4.7 params. |
| 8 | Add Expense sheet | §7 | FAB → amount pad → category grid → date → note. Target: under 5 seconds. |
| 9 | `/[locale]/analytics` | §7 | Week / Month / Year tabs. |
| 10 | `/[locale]/budget` | §7 | Per-category limits with progress bars. |
| 11 | `/[locale]/goals` | §7 | Progress rings, contribute sheet. |
| 12 | `/[locale]/settings/*` | §6.3, §6.2 | Profile, preferences (language!), devices, change password, delete account. |
| 13 | `/[locale]/billing` | §6.4 | Current plan, wallet, ledger, payments, plans, packs. |
| 14 | Paywall sheet | §6.4 | **Global**, opened by any 402. Two tabs: Plans / Buy credits. |

### 13.1 The paywall (most important commercial surface)

Opened centrally from the 402 handler in §10.2. It must:

- Say **why** it opened, using `error.message` ("This uses 5 credits and you have 3.").
- Preselect the right tab: `INSUFFICIENT_CREDITS`/`QUOTA_EXCEEDED` → **Buy credits**;
  `SUBSCRIPTION_REQUIRED` → **Plans**, scrolled to the required tier.
- Render plans from `GET /billing/plans` — never hardcode prices. `monthly_price` as
  the headline, `price` as the total, `savings_percent` as a badge, ribbon on
  `is_popular`.
- Render packs from `GET /billing/credit-packs` with `total_credits` and
  `price_per_credit`.
- Generate **one** idempotency key when the sheet opens; reuse it across retries.
- After checkout, poll `GET /billing/me` until `plan_code` or `total_credits`
  changes, then invalidate `qk.billing()` and `qk.wallet()` and retry the original
  action.

---

## 14. Admin panel

> **Status: the backend has no `/api/v1/admin/*` endpoints yet.** The `users.role`
> column (`user` | `admin` | `support`) and an `ADMIN_API_KEY` config slot exist,
> but no admin routes are mounted. So: build the shell, the guard and the tables
> now against the contract in §14.2, keep `flags.admin = false`, and wire it up
> when the endpoints land. Do not fake admin data in a way that could ship.

### 14.1 Access model

- Route group `/[locale]/admin/*`, guarded in a Server Component by
  `GET /users/me` → `data.user.role === "admin"` (`support` gets read-only).
- A non-admin gets a **404**, not a 403 — an admin area should not announce itself.
- Admins use the same cookie session as the app. No second login.
- Every destructive admin action requires a typed confirmation (like §6.3.7).

### 14.2 Planned admin endpoints (contract, not yet built)

| Method | Path | Returns |
|---|---|---|
| `GET` | `/admin/stats` | `AdminStats` — KPI header |
| `GET` | `/admin/users` | `AdminUserRow[]` + `meta`; filters per §4.7 |
| `GET` | `/admin/users/:id` | `AdminUserDetail` |
| `PATCH` | `/admin/users/:id` | suspend / reactivate / unlock / change role |
| `POST` | `/admin/users/:id/credits` | `{delta, reason}` → ledger `admin_adjust` |
| `DELETE` | `/admin/users/:id/sessions` | force sign-out everywhere |
| `GET` | `/admin/payments` | `Payment[]` + `meta`, filter by status/kind/date |
| `POST` | `/admin/payments/:id/confirm` | manual settlement (replaces the sandbox route) |
| `POST` | `/admin/payments/:id/refund` | `{amount, reason}` |
| `GET` | `/admin/revenue` | time series for the revenue chart |
| `GET` | `/admin/subscriptions` | `Subscription[]` + `meta` |
| `GET/PATCH` | `/admin/plans[/:code]` | catalogue editing |
| `GET/PATCH` | `/admin/credit-packs[/:code]` | catalogue editing |
| `GET/PATCH` | `/admin/features[/:code]` | credit costs, tiers, active flag |
| `GET` | `/admin/audit-logs` | `AuditLog[]` + `meta` |
| `GET` | `/admin/feedback` | user feedback inbox |

```ts
export interface AdminStats {
  users: { total: number; active: number; new_today: number; new_this_month: number;
           verified: number; onboarded: number };
  subscriptions: { active: number; trialing: number; cancelled_this_month: number;
                   by_plan: Record<string, number> };
  revenue: { today: number; this_month: number; last_month: number; lifetime: number;
             mrr: number; arpu: number };
  credits: { granted: number; purchased: number; used: number; outstanding: number };
  ai: { calls_today: number; calls_this_month: number; top_features: { code: string; count: number }[] };
  health: { db: boolean; redis: boolean; pending_payments: number; failed_payments_24h: number };
}

export interface AdminUserRow {
  id: string; name: string; phone: string; username: string | null; email: string | null;
  user_type: UserType; role: Role; status: "active" | "suspended" | "deleted";
  plan_code: string; plan_expires_at?: string;
  total_credits: number; lifetime_spent: number;
  phone_verified: boolean; onboarding_step: OnboardingStep;
  expense_count: number; last_seen_at?: string; created_at: string;
  is_locked: boolean;
}

export interface AdminUserDetail extends AdminUserRow {
  entitlement: Entitlement; wallet: Wallet;
  subscription?: Subscription;
  sessions: SessionInfo[];
  recent_payments: Payment[];
  recent_ledger: LedgerEntry[];
  stats: ProfileStats;
}
```

### 14.3 Screens

| # | Route | Content |
|---|---|---|
| A1 | `/admin` | KPI row (users, MRR, active subs, credits outstanding) · revenue line chart (12 months) · signups bar chart (30 days) · plan-mix donut · "needs attention" list (pending payments, locked accounts, failed payments) |
| A2 | `/admin/users` | Table: name+phone · plan chip · credits `.tnum` · status badge · last seen · joined. Search, filters (plan, status, user_type, verified, date range), CSV export. Row → detail drawer. |
| A3 | `/admin/users/[id]` | Header (avatar, name, phone, plan, status) · tabs: **Overview** (stats, entitlement, wallet) · **Billing** (payments, ledger, subscription) · **Devices** (sessions, force sign-out) · **Danger** (suspend, unlock, adjust credits, change role) |
| A4 | `/admin/payments` | Table with status filter; row actions Confirm / Refund; daily totals in `meta.extra`. |
| A5 | `/admin/subscriptions` | Active / trialing / expiring-in-7-days / cancelled tabs. |
| A6 | `/admin/catalogue` | Three tabs — Plans, Credit packs, Features. Inline edit of price, credits, `is_active`, `is_popular`, `credit_cost`, `min_tier`. **Every edit is a business decision: confirm before saving, and show the previous value.** |
| A7 | `/admin/audit` | Audit log with actor, action, target, IP, time. Read-only. |

### 14.4 Admin design differences

The admin panel is the one place that is **desktop-first** — an operations person
works on a laptop with many rows on screen.

- Layout: fixed 240px left sidebar + content, max-width 1440px, 24px gutters.
  Collapses to a drawer under 1024px; below 768px only the KPI cards and search
  remain useful — that is acceptable.
- Density: table row h44, 13px text, `.tnum` on every number column, sticky header,
  sticky first column on horizontal scroll.
- **Same tokens as the app.** No second theme. The admin panel is distinguished by
  a `brand-800` sidebar and the word "Admin" in the app bar, nothing more.
- Status badges reuse the health colours: active → income, suspended → warning,
  deleted/failed → expense, pending → info.
- Every table: server-side pagination (§4.7), URL-synced filters (so a filtered view
  is shareable), a visible row count, and an empty state.
- Destructive actions: red button, confirmation dialog naming the user, and the
  reason field is **required** — it lands in the audit log.
- Bangla and English both apply here too (`admin.json` namespace). Operations staff
  often prefer English; the switcher still works.

---

## 15. Definition of done

A screen is finished when all of these hold:

- [ ] Loading state is a skeleton matching the real layout — no spinner on a list.
- [ ] Empty state has an icon, a sentence and one action.
- [ ] Every error path renders: field errors inline, 402 opens the paywall, 5xx
      shows the `request_id`.
- [ ] Works at 360px wide with no horizontal scroll (admin: 1024px).
- [ ] Works in Bangla **and** English — no hardcoded string anywhere in the file.
- [ ] Light and dark both readable; contrast checked.
- [ ] Keyboard reachable end to end, focus visible.
- [ ] Every money value uses `.tnum` and `formatBDT`.
- [ ] No hardcoded colour, radius or spacing — tokens only.
- [ ] No endpoint used that is not in §6 (or flag-gated from §7/§14.2).

---

## Appendix A — quick reference

```
BASE                 http://localhost:8080/api/v1
Envelope             { success, code, message, data, meta?, errors?, hint?, request_id }
Branch on            code (never message)
Money in             string "1500.50"      Money out   number 1500.50
Dates                calendar dates for transactions, instants for audit fields
Auth                 httpOnly cookies via /api/hisabji proxy; never localStorage
Refresh              single-flight only — parallel refresh = signed out everywhere
Paywall              any 402 → global sheet
Icons                lucide names from the API
Category colours     hex from the API — never your own
Brand colour         interaction only, never data
i18n                 i18next, /[locale]/ routes, bn default, en fallback
Admin                role === "admin" from GET /users/me; endpoints not built yet
```

## Appendix B — curl cookbook

```bash
API=http://localhost:8080/api/v1
P=01711111111 ; PW=hisabji2026

# 1. register (dev_otp comes back in the response)
curl -s -X POST $API/auth/register -H "Content-Type: application/json" \
  -d "{\"name\":\"Rasel\",\"phone\":\"$P\",\"password\":\"$PW\",\"user_type\":\"job_holder\"}"

# 2. verify (issues the first token pair)
curl -s -X POST $API/auth/verify-otp -H "Content-Type: application/json" \
  -d "{\"phone\":\"$P\",\"otp\":\"123456\"}"

# 3. login and keep the token
TOKEN=$(curl -s -X POST $API/auth/login -H "Content-Type: application/json" \
  -d "{\"identifier\":\"$P\",\"password\":\"$PW\"}" \
  | grep -oE '"access_token":"[^"]*"' | cut -d'"' -f4)

# 4. anything authenticated
curl -s "$API/users/me?stats=true" -H "Authorization: Bearer $TOKEN"
curl -s "$API/billing/me"          -H "Authorization: Bearer $TOKEN"
curl -s "$API/billing/features/ai_monthly_coach/access" -H "Authorization: Bearer $TOKEN"

# 5. buy credits end to end (sandbox)
PID=$(curl -s -X POST $API/billing/credits/buy -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -H "X-Idempotency-Key: $(uuidgen)" \
  -d '{"pack_code":"tokens_60"}' | grep -oE '"id":"[0-9a-f-]{36}"' | head -1 | cut -d'"' -f4)
curl -s -X POST $API/billing/payments/confirm -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d "{\"payment_id\":\"$PID\"}"

# rate-limited during development? clear just that rule and retry at once:
bash scripts/reset-rate-limit.sh register
bash scripts/reset-rate-limit.sh --list      # what is limited right now
```
