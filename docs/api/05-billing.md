# Billing — `/api/v1/billing`

Subscriptions, credits, the feature catalogue and payments.

**Files:** `internal/module/billing/` — `routes.go` · `handler.go` ·
`service.go` · `checkout.go` · `gate.go` · `repository.go`

---

## The commercial model, in one page

Hisabji is **hybrid**: a plan unlocks *modules*, and credits pay for *AI
actions*. Understanding this makes every response on this page obvious.

```
                       ┌──────────────────────────────────────┐
                       │  FEATURE (one gated capability)      │
                       │    kind = "module"     access gate,  │
                       │                        free once     │
                       │                        unlocked      │
                       │    kind = "ai_action"  costs credits │
                       │                        per run       │
                       │    min_tier, payg_allowed            │
                       └──────────────────────────────────────┘
                            ▲                        ▲
        included in         │                        │  paid for with
        ────────────────────┘                        └───────────────────
        PLAN                                          CREDITS (wallet)
        subscription_plans                            credit_wallets
        feature_codes[], monthly_credits              allowance + purchased
```

### The wallet has two buckets, and the difference matters

| Bucket | Comes from | Expires |
|---|---|---|
| `allowance_credits` | the monthly plan refill | **yes** — replaced, not added, on each refill, and wiped when the plan ends |
| `purchased_credits` | credit packs, the signup bonus, refunds | **no** — the user paid for them |

Spending takes from the **allowance first**, so purchased credits survive as
long as possible. That is what the user would choose (`service.go:350`).

An allowance is a monthly permission, not an asset — replacing rather than
adding is what stops a dormant user banking a year of unused allowance and then
costing a year of model calls at once (`service.go:440`).

### The access decision, in order

```
1. feature inactive              → no
2. included in the plan          → yes  (still costs credits if metered)
3. not included but payg_allowed → yes, if they can afford it
4. otherwise                     → upgrade required
```

That is `Service.CheckAccess` (`service.go:203`) and it is the whole hybrid
model in one function.

### The one rule that makes this safe

> Nothing outside this package ever writes `credit_wallets` or `credit_ledger`.
> Every balance change goes through `Grant` or `Spend`, which run inside a
> SERIALIZABLE transaction that locks the wallet row and writes the ledger in
> the same commit.
> — `service.go:1`

That is why the balance and its history can never disagree, and why two
concurrent AI requests cannot spend the same credit twice.

---

## Catalogue endpoints

These three use **optional auth**: readable signed out for a pricing page,
richer signed in.

### `GET /api/v1/billing/plans`

**Auth:** optional · **Rate limit:** global only

#### Response `200`

```json
{
  "success": true,
  "message": "Subscription plans fetched successfully.",
  "data": [
    {
      "code": "pro_yearly",
      "name": "Pro", "name_bn": "প্রো",
      "tagline": "Everything, all year",
      "tier": "pro",
      "period_months": 12,
      "price": 4990.00,
      "list_price": 5988.00,
      "currency": "BDT",
      "monthly_credits": 100,
      "signup_credits": 50,
      "allowance_rolls_over": false,
      "feature_codes": ["expense_tracking", "ai_monthly_coach", "forecast"],
      "max_devices": 5,
      "trial_days": 0,
      "is_active": true,
      "is_popular": true,
      "sort_order": 3,

      "monthly_price": 415.83,
      "savings_percent": 17,
      "is_current_plan": false,
      "total_credits": 1250
    }
  ]
}
```

| Computed field | How |
|---|---|
| `monthly_price` | `price / period_months` — for the "৳416/month, billed yearly" line |
| `savings_percent` | how much cheaper than `list_price` |
| `total_credits` | `monthly_credits × period_months + signup_credits` |
| `is_current_plan` | **signed in only** — marks the plan the caller already holds |

Only active plans are returned. `is_current_plan` is always `false` when signed
out, so the pricing page never mislabels a card for an anonymous visitor.

---

### `GET /api/v1/billing/credit-packs`

**Auth:** optional · **Rate limit:** global only

#### Response `200`

```json
{
  "success": true,
  "message": "Credit packs fetched successfully.",
  "data": [
    {
      "code": "pack_100",
      "name": "100 Credits",
      "credits": 100,
      "bonus_credits": 20,
      "price": 299.00,
      "list_price": 359.00,
      "currency": "BDT",
      "validity_days": 365,
      "is_active": true,
      "is_popular": true,
      "sort_order": 2,

      "total_credits": 120,
      "price_per_credit": 2.49,
      "savings_percent": 17
    }
  ]
}
```

Show `total_credits` (credits + bonus), not `credits` — that is what the user
actually receives.

---

### `GET /api/v1/billing/features`

The catalogue of gated capabilities, annotated for the caller.

**Auth:** optional · **Rate limit:** global only

#### Response `200`

```json
{
  "success": true,
  "message": "Features fetched successfully.",
  "data": {
    "features": [
      {
        "code": "ai_monthly_coach",
        "name": "Monthly Coach", "name_bn": "মাসিক কোচ",
        "description": "A personalised review of last month's spending.",
        "kind": "ai_action",
        "credit_cost": 5,
        "min_tier": "plus",
        "payg_allowed": true,
        "category": "ai",
        "icon": "sparkles",
        "sort_order": 1,
        "is_active": true,

        "included_in_plan": false,
        "affordable": true
      }
    ],
    "by_category": {
      "ai":      [ { "…": "…" } ],
      "reports": [ { "…": "…" } ]
    }
  }
}
```

- `included_in_plan` / `affordable` are computed **per caller**, so the client
  renders "Unlock" vs "Run (5 credits)" without a request per feature.
- Signed out they are both `false`.
- `by_category` is the same features grouped, so the UI can render sections
  without hardcoding which feature belongs where.

The button logic falls straight out of the two flags:

| `included_in_plan` | `affordable` | Button |
|:---:|:---:|---|
| ✔ | ✔ | **Run** |
| ✔ | ✖ | **Buy credits** |
| ✖ | ✔ (and `payg_allowed`) | **Run (5 credits)** |
| ✖ | ✖ | **Upgrade** |

---

## The signed-in user's own billing

### `GET /api/v1/billing/me`

Everything the account screen needs, in one call.

**Auth:** Bearer · **Rate limit:** global only

#### Response `200`

```json
{
  "success": true,
  "message": "Billing details fetched successfully.",
  "data": {
    "entitlement": {
      "plan_code": "pro_yearly",
      "tier": "pro",
      "features": ["expense_tracking", "ai_monthly_coach", "forecast"],
      "subscription_status": "active",
      "expires_at": "2027-09-11T12:00:00Z",
      "days_remaining": 365,
      "allowance_credits": 100,
      "purchased_credits": 23,
      "total_credits": 123
    },
    "wallet": {
      "allowance_credits": 100,
      "purchased_credits": 23,
      "total_credits": 123,
      "allowance_reset_at": "2026-10-11T12:00:00Z",
      "lifetime_granted": 150,
      "lifetime_purchased": 120,
      "lifetime_used": 147,
      "monthly_spend_cap": 500,
      "month_spent": 47,
      "updated_at": "2026-09-11T11:40:00Z"
    },
    "subscription": {
      "id": "0193d1…",
      "plan_code": "pro_yearly",
      "plan_name": "Pro",
      "plan_tier": "pro",
      "status": "active",
      "starts_at": "2026-09-11T12:00:00Z",
      "ends_at": "2027-09-11T12:00:00Z",
      "auto_renew": true,
      "cancelled_at": null,
      "price_paid": 4990.00,
      "currency": "BDT",
      "days_remaining": 365,
      "grants_made": 0,
      "next_grant_at": "2026-10-11T12:00:00Z",
      "payment_id": "0193d0…",
      "created_at": "2026-09-11T12:00:00Z",
      "updated_at": "2026-09-11T12:00:00Z"
    }
  }
}
```

`subscription` is **absent** on the free plan — a free account simply has no
subscription row. Check for the key; do not assume it exists.

#### Self-healing

`Entitlement` repairs a missing wallet (an account created before provisioning
existed, or a half-failed registration) by provisioning and retrying once
(`service.go:110`). A user never sees an error for a wallet that should have
existed.

#### Path through the code

```
handler.go:101  Me
service.go:110  Entitlement
  ├ LiveSubscription    → none = free plan, which is the normal case
  ├ GetPlan             → tier + feature_codes  (deleted plan → fall back to free, warn)
  └ GetWallet           → the two credit buckets
repository GetWallet    → wallet.Compute()  fills total_credits
repository LiveSubscription → days_remaining
```

---

### `GET /api/v1/billing/wallet`

Just the credit balance.

**Auth:** Bearer · **Rate limit:** global only

#### Response `200`

`data` is the **same `wallet` object** that `/billing/me` nests — deliberately,
so a client has one wallet type rather than two subtly different ones.

```json
{ "success": true, "message": "Credit balance fetched successfully.",
  "data": { "allowance_credits": 100, "purchased_credits": 23, "total_credits": 123, "…": "…" } }
```

`total_credits` is derived, never stored, and always serialised — so the client
never has to add two numbers to answer "how many credits do I have?".

#### Errors

| Status | Code | When |
|---|---|---|
| 404 | `NOT_FOUND` | no wallet row. Unlike `/billing/me`, this endpoint does **not** self-heal. |

---

### `GET /api/v1/billing/ledger`

Where the credits went. Every movement, immutable.

**Auth:** Bearer · **Rate limit:** global only

#### Query parameters

| Param | Default | Max |
|---|---|---|
| `page` | 1 | — |
| `limit` | 20 | 100 |

A bad value quietly falls back rather than failing a billing screen
(`handler.go:pageParams`). The generic query engine is deliberately not used
here: these lists have no filters.

#### Response `200`

```json
{
  "success": true,
  "message": "Credit history fetched successfully.",
  "data": [
    {
      "id": 4821,
      "delta": -5,
      "allowance_after": 95,
      "purchased_after": 23,
      "reason": "feature_use",
      "feature_code": "ai_monthly_coach",
      "reference_type": "insight",
      "reference_id": "0193e2…",
      "description": "Monthly Coach (5 credits)",
      "created_at": "2026-09-11T11:40:00Z"
    }
  ],
  "meta": { "page": 1, "limit": 20, "total_items": 47, "total_pages": 3,
            "count": 20, "has_next": true, "has_prev": false, "next_page": 2 }
}
```

Newest first. `delta` is negative for spending, positive for grants.
`allowance_after` / `purchased_after` are the balances **after** that movement,
so the history reconciles without replaying it.

#### `reason` values

| Reason | Meaning |
|---|---|
| `signup_bonus` | free credits at registration |
| `plan_grant` | a plan's monthly refill |
| `plan_signup_bonus` | a plan's one-off signup credits |
| `pack_purchase` | a credit pack was bought |
| `feature_use` | an AI action was run |
| `refund` | a feature failed and the credits were returned |
| `expiry` | the allowance died with the plan |
| `admin_adjust` | manual correction |
| `promo` | a promotional grant |

A refund keeps **both** the charge and the refund in the ledger, so the history
reads as what actually happened rather than as if the charge never occurred.

---

### `GET /api/v1/billing/payments`

Payment history. Same `page` / `limit` handling as the ledger.

**Auth:** Bearer · **Rate limit:** global only

#### Response `200`

```json
{
  "success": true,
  "message": "Payment history fetched successfully.",
  "data": [
    {
      "id": "0193d0…",
      "kind": "subscription",
      "reference_code": "pro_yearly",
      "quantity": 1,
      "amount": 4990.00,
      "currency": "BDT",
      "provider": "manual",
      "provider_ref": "TXN-9981",
      "status": "paid",
      "paid_at": "2026-09-11T12:00:00Z",
      "refund_amount": 0.00,
      "created_at": "2026-09-11T11:58:00Z",
      "updated_at": "2026-09-11T12:00:00Z"
    }
  ],
  "meta": { "…": "…" }
}
```

`kind` is `subscription` or `credit_pack`; `reference_code` is the plan or pack
code. `status`: `pending` · `processing` · `paid` · `failed` · `cancelled` ·
`refunded`.

`provider_payload` and `idempotency_key` are never serialised — the raw gateway
response can contain provider-side account identifiers.

---

### `GET /api/v1/billing/features/:code/access`

"Can I run this, and what will it cost me?" — **without spending anything.**

**Auth:** Bearer · **Rate limit:** global only

`:code` must be a slug; anything else is a clean `422`.

#### Response `200` — allowed

```json
{
  "success": true,
  "message": "Feature access checked successfully.",
  "data": {
    "feature": { "code": "ai_monthly_coach", "name": "Monthly Coach", "credit_cost": 5, "…": "…" },
    "allowed": true,
    "reason": "Included in your plan.",
    "credit_cost": 5,
    "entitlement": { "plan_code": "pro_yearly", "total_credits": 123, "…": "…" },
    "needs_upgrade": false,
    "needs_credits": false
  }
}
```

#### Response `200` — not allowed

```json
{
  "data": {
    "allowed": false,
    "reason": "This uses 5 credits and you have 2.",
    "needs_upgrade": false,
    "needs_credits": true
  }
}
```

Still **200**. "You cannot do this yet" is an answer, not a failure. The two
flags tell the UI which of the two ways forward to offer:

| `needs_upgrade` | `needs_credits` | Show |
|:---:|:---:|---|
| ✔ | ✖ | Upgrade to *Pro* |
| ✖ | ✔ | Buy credits |
| ✔ | ✔ | Both — upgrading also grants an allowance |

#### Errors

| Status | Code | When |
|---|---|---|
| 404 | `NOT_FOUND` | no such feature code |
| 422 | `VALIDATION_ERROR` | `:code` is not a valid slug |

#### Check ≠ spend

This endpoint and the `Gate` middleware **check**. `Service.Spend` **debits**,
and re-checks everything inside a SERIALIZABLE transaction before it does — so
a stale check can never grant free usage (`service.go:268`). Charging before the
work runs would bill users for answers they never received.

---

## Purchasing

All four purchase endpoints are rate limited hard: they create rows a support
person may later have to reconcile by hand, so a runaway client must not be able
to make a thousand of them.

### `POST /api/v1/billing/subscribe`

Starts a plan purchase. **This does not activate the plan** — it creates a
pending payment.

**Auth:** Bearer · **Rate limit:** 10 / hour / user (`strict:subscribe`)
**Header:** `X-Idempotency-Key` — send one.

#### Request

```json
{ "plan_code": "pro_yearly" }
```

#### Response `201`

```json
{
  "success": true,
  "code": "CREATED",
  "message": "Checkout started. Complete the payment to activate your plan.",
  "data": {
    "payment": {
      "id": "0193d0…",
      "kind": "subscription",
      "reference_code": "pro_yearly",
      "amount": 4990.00,
      "currency": "BDT",
      "provider": "manual",
      "status": "pending",
      "created_at": "2026-09-11T11:58:00Z"
    },
    "payment_url": "",
    "instructions": "Send BDT 4990.00 and share the transaction id with support, quoting reference 0193d0…. Your purchase is applied as soon as the payment is confirmed.",
    "already_processed": false
  }
}
```

- `payment_url` is `""` for the `manual` provider, and stays `""` until a real
  gateway integration fills it in — returning an empty string keeps the client
  honest rather than sending the user to a broken link (`checkout.go:439`).
- Show `instructions` verbatim. It is written for the configured provider.

#### Response `200` — idempotent replay

Same key, same purchase → the **original** payment, `"already_processed": true`,
message *"This purchase was already started."* Skip the second payment screen.

#### Errors

| Status | Code | When |
|---|---|---|
| 404 | `NOT_FOUND` | no such plan code |
| 409 | `CONFLICT` | that plan is no longer available |
| 409 | `CONFLICT` | **you already have an active subscription** — `details.current_plan`, `details.expires_at` |
| 400 | `BAD_REQUEST` | you tried to buy the free plan |
| 409 | `IDEMPOTENCY_CONFLICT` | the key was already used for a *different* purchase |
| 429 | `RATE_LIMIT_EXCEEDED` | more than 10 checkouts in an hour |

The "already subscribed" block is not politeness. Without it a user who taps
twice ends up paying for two overlapping subscriptions, and the unique index on
live subscriptions would fail the second activation **after** their money had
already moved (`checkout.go:47`).

---

### `POST /api/v1/billing/credits/buy`

**Auth:** Bearer · **Rate limit:** 20 / hour / user (`strict:buy_credits`)
**Header:** `X-Idempotency-Key`

#### Request

```json
{ "pack_code": "pack_100" }
```

#### Response `201`

Identical shape to `/subscribe`, with `kind: "credit_pack"` and the message
*"Checkout started. Complete the payment to receive your credits."*

#### Errors

| Status | Code | When |
|---|---|---|
| 404 | `NOT_FOUND` | no such pack code |
| 409 | `CONFLICT` | that pack is no longer available |
| 409 | `IDEMPOTENCY_CONFLICT` | key reused for a different purchase |

Unlike a subscription, there is no "already have one" check — credit packs
stack.

---

### `POST /api/v1/billing/payments/confirm`

Settles a payment and applies what it bought.

> **⚠️ Registered only when `PAYMENT_SANDBOX=true`.** In production the route
> does not exist at all — its *absence* is the security control, not a runtime
> check that might be misconfigured (`routes.go:58`). With a real gateway this
> is driven by the provider's webhook.

A client that could confirm its own payment could grant itself a plan for free.

**Auth:** Bearer · **Rate limit:** 30 / hour / user (`strict:confirm_payment`)

#### Request

```json
{ "payment_id": "0193d0…", "provider_ref": "SANDBOX-TXN-1" }
```

#### Response `200` — subscription activated

```json
{
  "success": true,
  "message": "Payment confirmed. Your purchase is active.",
  "data": {
    "payment_id": "0193d0…",
    "already_confirmed": false,
    "subscription": { "…": "…" },
    "plan_code": "pro_yearly",
    "expires_at": "2027-09-11T12:00:00Z",
    "credits_granted": 150
  }
}
```

#### Response `200` — credit pack

```json
{
  "data": {
    "payment_id": "0193d1…",
    "already_confirmed": false,
    "pack_code": "pack_100",
    "credits_granted": 120,
    "wallet": { "total_credits": 143, "…": "…" }
  }
}
```

#### Response `200` — replay

`"already_confirmed": true`, message *"This payment was already confirmed."*
Confirming an already-paid payment is safe: it returns the same result instead
of granting the credits twice.

#### Errors

| Status | Code | When |
|---|---|---|
| 404 | `NOT_FOUND` | no such payment, **or it belongs to somebody else** |
| 409 | `CONFLICT` | the payment is refunded or cancelled |

Reporting somebody else's payment as `404` rather than `403` means this endpoint
cannot be used to discover which payment ids exist (`checkout.go:148`).

#### Path through the code — this is the money path

```
handler.go:267   Confirm
checkout.go:139  ConfirmPayment
  └ txn.RunSerializable ──────────────────────────────────────────────┐
      LockPayment(id)              SELECT … FOR UPDATE                 │
      payment.UserID != caller     → 404 (never 403)                   │
      status = paid                → return already_confirmed, no-op   │
      status = refunded/cancelled  → 409                               │
      MarkPaymentPaid                                                  │  money and
      applyPayment ──┬─ "subscription" → activateSubscription          │  value move
                     │     GetPlan                                     │  together,
                     │     LiveSubscription → extend from its end date │  or not at all
                     │     ExpireSubscription(old)                     │
                     │     CreateSubscription                          │
                     │     Grant(monthly + signup credits)             │
                     │     SyncUserPlan(users.plan_code)               │
                     └─ "credit_pack"   → creditPack                   │
                           GetPack                                     │
                           Grant((credits + bonus) × quantity)         │
     ──────────────────────────────────────────────────────────────────┘
```

If the user still holds a live subscription, the new one **starts from its end
date**, not from today — they should not lose the days they paid for
(`checkout.go:209`).

---

### `POST /api/v1/billing/subscription/cancel`

Turns off auto-renewal.

**Auth:** Bearer · **Rate limit:** 5 / hour / user (`strict:cancel`)

#### Request — the body is optional

```json
{ "reason": "Too expensive right now" }
```

An empty body is a valid cancellation.

#### Response `200`

```json
{
  "success": true,
  "message": "Subscription cancelled. You keep access until it expires.",
  "data": {
    "id": "0193d1…",
    "plan_code": "pro_yearly",
    "status": "cancelled",
    "ends_at": "2027-09-11T12:00:00Z",
    "cancelled_at": "2026-09-11T12:30:00Z",
    "cancel_reason": "Too expensive right now",
    "auto_renew": false,
    "days_remaining": 365
  }
}
```

**Access continues until `ends_at`.** The user bought that time and cancelling
should not take it away. Show `days_remaining`, not "cancelled" alone.

#### Errors

| Status | Code | When |
|---|---|---|
| 404 | `NOT_FOUND` | no active subscription — hint: *"You are on the free plan; there is nothing to cancel."* |
| 409 | `CONFLICT` | already cancelled — `details.access_until` |

The reason, when given, is written to `feedback` as `cancel_reason` on a
best-effort basis: a failure there is logged, never surfaced. Losing a
cancellation because the feedback insert failed would be absurd.

---

## Background maintenance

Two service methods have no HTTP surface. They are meant to be driven by a
scheduler:

| Method | Does |
|---|---|
| `ExpireDueSubscriptions` (`checkout.go:343`) | downgrades subscriptions past `ends_at`, **wipes the allowance, keeps purchased credits**, and resets `users.plan_code` to `free` |
| `GrantDueAllowances` (`checkout.go:391`) | the monthly refill inside a multi-month plan — a 12-month plan refills 11 more times, never past `ends_at` |

The allowance dies with the plan; purchased credits do not. That is the promise
made when the user bought them.

Neither is scheduled yet — there is no cron wired into `cmd/api`. Until one
exists, an expired subscription keeps its row until something calls these.

---

## Protecting a premium endpoint

When the AI modules land, they will not re-implement any of this. The `Gate`
makes the business rule visible in the route table (`gate.go:18`):

```go
ai.POST("/monthly-coach",
    d.Auth.Required(),
    d.Gate.Require("ai_monthly_coach"),   // 402 with the right code if not allowed
    lim.Limit(middleware.AIRule("coach", cfg.RateLimit)),
    handler.H(h.MonthlyCoach))
```

- `Gate.Require(code)` — checks one feature, stores the decision on the context
  (`billing.AccessFrom(c)` reads it back), and returns a `402` carrying
  `needs_upgrade` / `needs_credits` / `credit_cost` / `available_credits` when
  refused.
- `Gate.RequireTier("business")` — blocks a whole section below a plan tier.
- `Gate.SpendFor(...)` — the debit, called by the handler once the work is
  about to be (or has been) done.

The gate **checks**; it never spends.

---

## Related config

| Env | Default | Effect |
|---|---|---|
| `PAYMENT_PROVIDER` | `manual` | shapes `payment_url` and `instructions` |
| `PAYMENT_SANDBOX` | `true` outside production | **registers `/payments/confirm` at all** |
| `AI_FREE_QUOTA` | — | fallback signup credits when the free plan defines none |
| `AI_MONTHLY_COST_CAP` | — | the wallet's `monthly_spend_cap`, a runaway-loop brake independent of the balance |

The monthly cap is not about the user's balance. It is about a runaway loop or
a compromised account draining a large purchased balance in minutes: it refuses
with `402 QUOTA_EXCEEDED` and lets a human look (`service.go:337`).
