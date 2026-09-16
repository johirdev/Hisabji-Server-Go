# Request lifecycle — where the logic lives

This page answers "a request arrives — where does it go, and which file decides
what?". Every endpoint page then shows its own version of the same chain.

---

## 1. The layers

```
HTTP request
   │
   ▼
┌──────────────────────────────────────────────────────────────────────┐
│ GLOBAL MIDDLEWARE          internal/app/router.go:55                 │
│   RequestID → SecurityHeaders → CORS → BodyLimit                     │
│   → Recovery → Logger → Locale                                       │
└──────────────────────────────────────────────────────────────────────┘
   │
   ▼
┌──────────────────────────────────────────────────────────────────────┐
│ GROUP MIDDLEWARE           /api/v1                                   │
│   global rate limit (300/min/IP)                                     │
└──────────────────────────────────────────────────────────────────────┘
   │
   ▼
┌──────────────────────────────────────────────────────────────────────┐
│ ROUTE MIDDLEWARE           <module>/routes.go                        │
│   per-route rate limit → Auth.Required() / Auth.Optional()           │
│   → (billing Gate, on premium routes)                                │
└──────────────────────────────────────────────────────────────────────┘
   │
   ▼
┌──────────────────────────────────────────────────────────────────────┐
│ HANDLER                    <module>/handler.go                       │
│   bind + validate the body   → validate.Body                         │
│   read the caller           → handler.UserID(c)                      │
│   call the service                                                   │
│   wrap the result           → response.OK / Created / List           │
│   NO business logic, NO SQL, NO status codes                         │
└──────────────────────────────────────────────────────────────────────┘
   │
   ▼
┌──────────────────────────────────────────────────────────────────────┐
│ SERVICE                    <module>/service.go                       │
│   ALL business rules live here, and ONLY here                        │
│   opens transactions        → txn.Run / txn.RunSerializable          │
│   returns *apperr.Error with the business meaning                    │
└──────────────────────────────────────────────────────────────────────┘
   │
   ▼
┌──────────────────────────────────────────────────────────────────────┐
│ REPOSITORY                 <module>/repository.go                    │
│   SQL only. No decisions.                                            │
│   returns rows, or apperr.FromPostgres(err, "Resource")              │
└──────────────────────────────────────────────────────────────────────┘
   │
   ▼
 PostgreSQL / Redis
```

On the way back out, a `*response.Result` becomes JSON in
`response.Write`, and **any** error becomes JSON in `response.Fail` —
the single place a status code is chosen.

---

## 2. Why the middleware is in that order

From `internal/app/router.go:20`, and it is a design decision, not an accident:

| Position | Why it must be there |
|---|---|
| `RequestID` **first** | every later log line and error carries the id |
| `SecurityHeaders` | before any body is written |
| `CORS` | before `Recovery`, so a preflight never needs a panic guard |
| `BodyLimit` | before anything reads the body |
| `Recovery` **before** `Logger` | a panic still produces one access-log line |
| `Logger` | wraps the handlers it reports on |
| `Locale` | before handlers, which use it to pick a language |
| `RateLimit` **before** `Auth` | an unauthenticated flood is rejected *before* it costs a signature verification |

---

## 3. Error flow

```
repository  →  raw driver error, or apperr.FromPostgres(err, "User")
                                  (23505 unique violation → 409 DUPLICATE_ENTRY,
                                   23503 foreign key      → 422 INVALID_FIELD,
                                   23514 check violation  → 422 INVALID_FIELD,
                                   each phrased from the constraint-name catalogue
                                   registered in internal/app/app.go:registerSharedConstraints,
                                   so "budgets_user_period_uniq" becomes a sentence
                                   a user understands, on the right form field)
    │
    ▼
service     →  apperr.New(status, code, message)
                 .WithField(...)    per-field detail for a form
                 .WithDetails(...)  machine-readable extras
                 .WithHint(...)     the next step in plain language
                 .WithCause(err)    the internal error — never shown in production
    │
    ▼
handler     →  return nil, err        ← does nothing else, ever
    │
    ▼
handler.H   →  response.Fail(c, err)  ← internal/core/handler/handler.go:39
    │
    ▼
response.Fail  →  status + envelope + structured log line
```

A handler that formats its own error is a bug: it would invent a second error
shape that no client interceptor knows about.

---

## 4. Transactions

Two runners, in `internal/core/txn`:

| Runner | Isolation | Use for |
|---|---|---|
| `txn.Run` | read committed | ordinary multi-statement writes |
| `txn.RunSerializable` | serializable, with retry | anything touching money or credits |

The rule that makes billing safe: **nothing outside `internal/module/billing`
ever writes `credit_wallets` or `credit_ledger`.** Every balance change goes
through `Grant` or `Spend`, which lock the wallet row and write the ledger in
the same commit. That is why the balance and its history can never disagree,
and why two concurrent AI requests cannot spend the same credit twice
(`internal/module/billing/service.go:1`).

Services own transactions. A repository never starts one — it takes a
`repository.DB`, which is satisfied by both a pool and a `pgx.Tx`, so the same
method works inside or outside a transaction.

---

## 5. Authentication, in two halves

```
ACCESS TOKEN                             REFRESH TOKEN
signed JWT, 30 min                       256 random bits, 30 days
verified with a signature — no DB read   checked against the sessions table
carries: sub, role, plan, sid            every use, which is what makes
                                         revocation and theft detection possible
```

A refresh token is deliberately **not** a JWT: it is looked up in the database
on every use anyway, so signing it would add length and a second secret while
changing nothing about the security — and it invites the real mistake, trusting
the claims instead of the row (`internal/shared/jwt/jwt.go:110`).

### Revoking a JWT before it expires

Access tokens live 30 minutes, and Redis holds a small denylist written on
logout, password change and theft detection:

| Redis key | Written by | Effect |
|---|---|---|
| `<prefix>:revoked:sid:<session_id>` | logout, revoke one device | that one session is dead |
| `<prefix>:revoked:user:<user_id>` = unix ts | logout-all, password change, reset, delete | every token **issued before** that timestamp is dead |

Entries expire on their own after the access-token TTL, so the set stays tiny.
If Redis is unavailable the check fails **open** — a signed, unexpired token is
still honoured, with exposure bounded by the 30-minute lifetime
(`internal/middleware/auth.go:19`).

### Refresh rotation with reuse detection

Every refresh mints a new token and revokes the presented one, so a token is
valid exactly once. If a **revoked** token is presented again, either it was
stolen and is being used after the real user refreshed, or the real user is
replaying an old one after an attacker refreshed. The server cannot tell which,
so it revokes the **entire session family** and forces a fresh sign-in
everywhere (`internal/module/auth/service.go:293`).

---

## 6. Adding a new module — the shape to copy

```
internal/module/<name>/
├── dto.go          request/response structs, binding tags, Normalize(), Validate()
├── repository.go   SQL only
├── service.go      the business rules; opens transactions
├── handler.go      bind → service → response.OK
└── routes.go       the route table, rate limits, gates
```

Then wire it in exactly two places:

1. `internal/app/app.go` — construct repo → service → handler.
2. `internal/app/router.go` — `<name>.Mount(api.Group("/<name>"), <name>.Deps{...})`.

A module that owns a plain resource (expenses, categories, goals) does not need
five hand-written controllers. `internal/core/handler/crud.go` mounts
list / get / create / update / delete / bulk-delete / summary from one service
interface, with search, filtering, sorting and pagination already wired through
`internal/core/query`:

```go
crud := handler.CRUD[Expense, CreateRequest, UpdateRequest]{
    Resource: "Expense", Plural: "Expenses",
    Schema:   ExpenseSchema,
    Service:  svc,
}
crud.Mount(rg.Group("/expenses"))
```

That is why the not-yet-built modules in [07-not-implemented.md](07-not-implemented.md)
are a repository, a service and ~30 lines of routes each — not a controller per
endpoint.
