# Hisabji API — Reference

Every endpoint the server exposes today, with its request, its response, every
error it can produce, and the exact path a request takes through the code.

**Base URL:** `http://localhost:8080` (dev) — all business endpoints live under
`/api/v1`. Meta and health endpoints deliberately sit outside it.

---

## Contents

| File | What is in it |
|---|---|
| [00-conventions.md](00-conventions.md) | The response envelope, auth headers, money, dates, pagination, idempotency. **Read this first.** |
| [01-request-lifecycle.md](01-request-lifecycle.md) | Where a request goes, layer by layer, and which file owns which decision. |
| [02-meta.md](02-meta.md) | `GET /`, `/health`, `/health/live`, `/health/ready` |
| [03-auth.md](03-auth.md) | `/api/v1/auth` — 11 endpoints: register, OTP, login, refresh, sessions, passwords |
| [04-users.md](04-users.md) | `/api/v1/users` — 8 endpoints: profile, preferences, username, onboarding, delete |
| [05-billing.md](05-billing.md) | `/api/v1/billing` — 11 endpoints: plans, packs, features, wallet, ledger, checkout |
| [06-errors.md](06-errors.md) | The full error-code catalogue and what a client should do for each. |
| [07-not-implemented.md](07-not-implemented.md) | Schema exists, HTTP surface does not. Do not build a client against these yet. |

---

## The whole route table at a glance

### Meta — outside `/api/v1`, never rate limited

| Method | Path | Auth | Doc |
|---|---|---|---|
| GET | `/` | — | [02](02-meta.md#get-) |
| GET | `/health` | — | [02](02-meta.md#get-health) |
| GET | `/health/live` | — | [02](02-meta.md#get-healthlive) |
| GET | `/health/ready` | — | [02](02-meta.md#get-healthready) |

### Auth — `/api/v1/auth`

| Method | Path | Auth | Rate limit | Doc |
|---|---|---|---|---|
| POST | `/register` | — | 5 / hour / IP | [03](03-auth.md#post-apiv1authregister) |
| POST | `/verify-otp` | — | 15 / min / IP | [03](03-auth.md#post-apiv1authverify-otp) |
| POST | `/resend-otp` | — | 5 / 10 min / IP | [03](03-auth.md#post-apiv1authresend-otp) |
| POST | `/login` | — | 10 / min / IP | [03](03-auth.md#post-apiv1authlogin) |
| POST | `/refresh` | — | 60 / hour / IP | [03](03-auth.md#post-apiv1authrefresh) |
| POST | `/forgot-password` | — | 5 / hour / IP | [03](03-auth.md#post-apiv1authforgot-password) |
| POST | `/reset-password` | — | 10 / hour / IP | [03](03-auth.md#post-apiv1authreset-password) |
| POST | `/logout` | Bearer | global | [03](03-auth.md#post-apiv1authlogout) |
| GET | `/sessions` | Bearer | global | [03](03-auth.md#get-apiv1authsessions) |
| DELETE | `/sessions/:id` | Bearer | global | [03](03-auth.md#delete-apiv1authsessionsid) |
| POST | `/change-password` | Bearer | 5 / hour / user | [03](03-auth.md#post-apiv1authchange-password) |

### Users — `/api/v1/users`

| Method | Path | Auth | Rate limit | Doc |
|---|---|---|---|---|
| GET | `/username-available` | optional | 60 / min / IP | [04](04-users.md#get-apiv1usersusername-available) |
| GET | `/me` | Bearer | global | [04](04-users.md#get-apiv1usersme) |
| PATCH | `/me` | Bearer | 60 / min / user | [04](04-users.md#patch-apiv1usersme) |
| PUT | `/me` | Bearer | 60 / min / user | [04](04-users.md#patch-apiv1usersme) |
| PATCH | `/me/preferences` | Bearer | 60 / min / user | [04](04-users.md#patch-apiv1usersmepreferences) |
| PUT | `/me/username` | Bearer | 5 / hour / user | [04](04-users.md#put-apiv1usersmeusername) |
| POST | `/me/onboarding` | Bearer | 60 / min / user | [04](04-users.md#post-apiv1usersmeonboarding) |
| DELETE | `/me` | Bearer | 3 / 24 h / user | [04](04-users.md#delete-apiv1usersme) |

### Billing — `/api/v1/billing`

| Method | Path | Auth | Rate limit | Doc |
|---|---|---|---|---|
| GET | `/plans` | optional | global | [05](05-billing.md#get-apiv1billingplans) |
| GET | `/credit-packs` | optional | global | [05](05-billing.md#get-apiv1billingcredit-packs) |
| GET | `/features` | optional | global | [05](05-billing.md#get-apiv1billingfeatures) |
| GET | `/me` | Bearer | global | [05](05-billing.md#get-apiv1billingme) |
| GET | `/wallet` | Bearer | global | [05](05-billing.md#get-apiv1billingwallet) |
| GET | `/ledger` | Bearer | global | [05](05-billing.md#get-apiv1billingledger) |
| GET | `/payments` | Bearer | global | [05](05-billing.md#get-apiv1billingpayments) |
| GET | `/features/:code/access` | Bearer | global | [05](05-billing.md#get-apiv1billingfeaturescodeaccess) |
| POST | `/subscribe` | Bearer | 10 / hour / user | [05](05-billing.md#post-apiv1billingsubscribe) |
| POST | `/credits/buy` | Bearer | 20 / hour / user | [05](05-billing.md#post-apiv1billingcreditsbuy) |
| POST | `/subscription/cancel` | Bearer | 5 / hour / user | [05](05-billing.md#post-apiv1billingsubscriptioncancel) |
| POST | `/payments/confirm` | Bearer | 30 / hour / user | [05](05-billing.md#post-apiv1billingpaymentsconfirm) — **sandbox only** |

On top of every per-route limit sits a blanket **300 requests / minute / IP**
across all of `/api/v1` (`RATE_LIMIT_GLOBAL_PER_MIN`).

---

## Keeping this in sync

These files are hand-written, not generated. When you add or change an
endpoint, update the module file **and** the table above. The route table in
`internal/app/router.go` and each module's `routes.go` are the source of truth —
if the two disagree, the code is right and the doc is stale.
