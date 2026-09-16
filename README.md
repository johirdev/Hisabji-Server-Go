# Hisabji Server

A Go (Gin + pgx + Redis) API for the Hisabji personal-finance app.

Three modules are live today — **auth**, **users** and **billing**. Everything
else (expenses, incomes, budgets, goals, AI insights) has its database schema
and its domain structs in place, but no HTTP surface yet.

- **API documentation:** [`docs/api/`](docs/api/README.md) — one file per module,
  every endpoint with its request, response, errors and the exact
  route → handler → service → repository → SQL path it takes.
- **Frontend contract:** [`docs/FRONTEND_INTEGRATION.md`](docs/FRONTEND_INTEGRATION.md)

---

## Quick start

```bash
cp .env.example .env      # then fill in the secrets
docker compose up -d      # postgres + redis
go run ./cmd/api          # or: air   (hot reload, see .air.toml)
```

The server prints its banner and listens on `PORT` (default `8080`).
Migrations in `migrations/` run automatically when `DB_AUTO_MIGRATE=true`.

Verify it is up:

```bash
curl http://localhost:8080/health
```

---

## Project layout

```
cmd/api/main.go              process entry point: load config, build App, serve, shut down

internal/
├── app/
│   ├── app.go               the dependency graph — everything is constructed exactly once here
│   └── router.go            the route table + middleware order + /health endpoints
│
├── config/config.go         every environment variable, its default and its validation
│
├── core/                    framework layer, imported by every module
│   ├── apperr/              the one error type + the stable error-code catalogue
│   ├── contextx/            request-scoped values (request id, user id, locale)
│   ├── handler/             handler.H wrapper + the generic CRUD mounter
│   ├── logger/              slog setup, request-scoped loggers
│   ├── money/               integer minor-unit money type (no floats, ever)
│   ├── query/               ?page/?limit/?sort/?search/?filter parsing and safe SQL building
│   ├── repository/          shared pgx helpers (DB interface, scan, exec, paginate)
│   ├── response/            the single JSON envelope; the only place a status code is chosen
│   ├── txn/                 transaction runners, including SERIALIZABLE with retry
│   └── validate/            binding + custom rules (bdphone, strongpass, username, safetext)
│
├── domain/                  DB rows ⇄ JSON structs; db:"…" and json:"…" tags side by side
│
├── infrastructure/
│   ├── database/            postgres pool, redis client, migration runner, health checks
│   └── sms/                 OTP delivery (log provider in dev, gateway in production)
│
├── middleware/              request id, security headers, CORS, body limit, recovery,
│                            logging, locale, JWT auth, sliding-window rate limiting
│
├── module/                  the feature modules — routes / handler / service / repository / dto
│   ├── auth/                register, OTP, login, refresh, sessions, passwords
│   ├── user/                profile, preferences, username, onboarding, account deletion
│   └── billing/             plans, credit packs, features, wallet, ledger, checkout
│
└── shared/                  small leaf utilities: banner, bcrypt hashing, JWT issuer

migrations/                  numbered SQL files, embedded into the binary
scripts/                     smoke test, rate-limit reset helper
docs/                        API reference and integration notes
```

### The rule every module follows

```
routes.go      the route table: path, middleware, rate limit. No logic.
handler.go     bind the request, call the service, wrap the result. No logic.
service.go     ALL the business rules, and the only place transactions start.
repository.go  SQL only. Returns raw driver errors or apperr.ErrNotFound.
dto.go         request/response shapes + their validation and normalisation.
```

Errors travel upward untouched: a handler returns `nil, err` and
`internal/core/response` decides the HTTP status exactly once.

---

## Configuration

Everything is read from the environment in `internal/config/config.go`, which
also validates it at boot and refuses to start production with a development
secret. See `.env.example` for the full list.

---

## Commands

```bash
go run ./cmd/api        # run
air                     # run with hot reload
go build ./...          # compile everything
go test ./...           # unit tests (money, query, apperr, domain schema)
go vet ./...            # static checks
./scripts/smoke.sh      # end-to-end smoke test against a running server
```
