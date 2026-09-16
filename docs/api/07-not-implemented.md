# Not implemented yet

**Do not build a client against anything on this page.** These endpoints do not
exist. Requesting one returns `404 ROUTE_NOT_FOUND`.

What *does* exist for them is the foundation: the database tables are migrated,
the domain structs are written, and the framework pieces (query engine, generic
CRUD mounter, billing gate) are in place. That is why each of these is a
repository, a service and ~30 lines of routes away — not a rewrite.

---

## What is already built

### Tables — all 26 exist and are migrated

| Migration | Tables |
|---|---|
| `001_foundation.sql` | `users`, `sessions`, `otp_codes`, `login_attempts`, `audit_logs`, `idempotency_keys` |
| `002_transactions.sql` | `categories`, `recurring_rules`, `expenses`, `incomes` |
| `003_budgets_goals.sql` | `budgets`, `budget_limits`, `goals`, `goal_contributions` |
| `004_billing.sql` | `features`, `subscription_plans`, `credit_packs`, `subscriptions`, `credit_wallets`, `credit_ledger`, `payments` |
| `005_ai_notifications.sql` | `ai_insights`, `forecasts`, `notifications`, `user_devices`, `feedback` |
| `006_seed_reference_data.sql` | seeds the plans, packs and features |

Live today: everything in `001` and `004`. The rest are written to, at most,
by the profile stats query.

### Domain structs — written, with `db` and `json` tags side by side

| File | Types |
|---|---|
| `internal/domain/transaction.go` | `Category`, `Expense`, `Income`, `RecurringRule` |
| `internal/domain/budget.go` | `Budget`, `BudgetLimit`, `CategorySpend`, `Goal`, `GoalContribution` |
| `internal/domain/insight.go` | `Insight`, `Forecast`, `Notification`, `Device`, `Feedback` |

### Constraint messages — already registered

`internal/app/app.go:registerSharedConstraints` already phrases the violations
these tables can raise, in English and Bangla:

```
categories_user_slug_uniq       "You already have a category with that name."
budgets_user_period_uniq        "You already have a budget starting on that date."
budget_limits_unique_category   "That category already has a limit in this budget."
expenses_amount_check           "The amount must be greater than zero."
expenses_date_sane              "That date looks wrong. Please check the year."
goals_target_after_start        "The target date must be on or after the start date."
budgets_period_ordered          "The end date must be on or after the start date."
```

So the day the expenses module ships, a duplicate category already produces a
sentence a user understands rather than a raw constraint name.

---

## The planned surface

Names below are the intended shape, not a contract. They can change.

### Money in and out — `/api/v1/expenses`, `/api/v1/incomes`, `/api/v1/categories`

The obvious CRUD, and the reason `internal/core/handler/crud.go` exists. One
declaration mounts all of it, with search, filter, sort and pagination already
wired through `internal/core/query`:

```
GET    /expenses          list + search + filter + sort + paginate
GET    /expenses/summary  aggregates over the same filters
GET    /expenses/:id
POST   /expenses
PATCH  /expenses/:id      (PUT registered as an alias)
DELETE /expenses/:id
DELETE /expenses/bulk
```

### Budgets and goals — `/api/v1/budgets`, `/api/v1/goals`

CRUD plus progress: `CategorySpend` and the computed fields on `Budget` and
`Goal` are already written for exactly this.

### Dashboard and analytics — `/api/v1/dashboard`, `/api/v1/analytics`

Read-only aggregates over the same tables.

### AI — `/api/v1/ai/...`

The metered surface. This is what the whole billing module was built for, and
nothing about it needs new billing code:

```go
ai.POST("/monthly-coach",
    d.Auth.Required(),
    d.Gate.Require("ai_monthly_coach"),                     // 402 with the right code
    lim.Limit(middleware.AIRule("coach", cfg.RateLimit)),   // hourly, per user
    handler.HT(45*time.Second, h.MonthlyCoach))             // per-handler timeout
```

and inside the handler, after the work is committed:

```go
spent, err := d.Gate.SpendFor(ctx, userID, "ai_monthly_coach", &refType, &refID)
```

Spend **before** the model call and `Refund()` if the provider fails — charging
for an answer the user never got is the worse of the two failures
(`internal/module/billing/service.go:262`).

### Notifications — `/api/v1/notifications`, `/api/v1/devices`

`notifications` and `user_devices` exist, including the
`user_devices_token_uniq` constraint message.

### Admin — `/api/v1/admin/...`

Two factors are already implemented and waiting to be mounted
(`internal/middleware/auth.go`):

```go
admin := api.Group("/admin")
admin.Use(a.Auth.Required(),
          a.Auth.RequireRole("admin"),
          a.Auth.RequireAdminKey(cfg.Security.AdminAPIKey))
```

A stolen admin password alone is not enough to reach destructive endpoints —
the `X-Admin-Key` header is compared in constant time.

---

## Scheduled jobs — none are running

Two billing methods are written and want a scheduler:

| Method | Should run | Does |
|---|---|---|
| `Service.ExpireDueSubscriptions` | daily | downgrades expired plans, wipes the allowance, keeps purchased credits |
| `Service.GrantDueAllowances` | daily | the monthly refill inside a multi-month plan |

Nothing calls them yet. `cmd/api` has no cron. Until one exists, an expired
subscription keeps its row and its allowance.

The empty `jobs/` package that used to sit at the repository root was removed —
it held three files containing nothing but `package jobs`. Recreate it when
there is a scheduler to put in it.

---

## When you build one of these

1. Copy the module shape — see
   [01-request-lifecycle.md §6](01-request-lifecycle.md#6-adding-a-new-module--the-shape-to-copy).
2. Wire it in `internal/app/app.go` and `internal/app/router.go`.
3. Register any new constraint messages next to the index that raises them.
4. Add a file here in `docs/api/`, and a row in the tables in
   [README.md](README.md).
