# Meta & health

Four endpoints that sit **outside** `/api/v1` and **outside** the rate limiter.

> An orchestrator's liveness probe must never be throttled, or a traffic spike
> turns into a restart loop that makes the spike worse.
> — `internal/app/router.go:79`

All four are defined in `App.mountMeta` (`internal/app/router.go:83`).
The three health endpoints return a **bare JSON object**, not the standard
envelope, because that is what monitoring tools expect to parse.

---

## `GET /`

Service discovery. Tells a new client where everything else is.

**Auth:** none · **Rate limit:** none

### Response `200`

```json
{
  "success": true,
  "code": "OK",
  "message": "Hisabji API is running.",
  "data": {
    "service": "Hisabji",
    "version": "1.0.0",
    "environment": "development",
    "api_base": "/api/v1",
    "docs": "/docs",
    "health": "/health"
  },
  "request_id": "a1b2c3d4e5f6",
  "timestamp": "2026-09-11T12:00:00Z"
}
```

This one **does** use the standard envelope.

Two things to know about the payload:

- `service`, `version` and `environment` echo `APP_NAME`, `APP_VERSION` and
  `APP_ENV`, so they differ per environment. No client logic should key off them.
- **`docs` advertises `/docs`, which is not a mounted route** — requesting it
  returns `404 ROUTE_NOT_FOUND`. The documentation lives in the repository at
  `docs/api/`. Either serve something at `/docs` or drop the key from
  `internal/app/router.go:95`; today it is a promise the server does not keep.

### Path

```
GET /  →  handler.H(inline closure)  →  response.OK
          internal/app/router.go:84
```

---

## `GET /health/live`

**Liveness.** Is the process up?

**Auth:** none · **Rate limit:** none

Deliberately checks nothing else — so a database blip cannot make Kubernetes
kill a perfectly healthy container.

### Response `200` — always, while the process can answer

```json
{ "status": "alive" }
```

Point your orchestrator's `livenessProbe` here.

---

## `GET /health/ready`

**Readiness.** Can *this instance* serve traffic?

**Auth:** none · **Rate limit:** none · **Timeout:** 3 seconds

### Response `200` — ready

```json
{
  "status": "ready",
  "checks": {
    "database": {
      "status": "up",
      "pool": {
        "total_conns": 5, "idle_conns": 4, "acquired_conns": 1, "max_conns": 20,
        "acquire_count": 412, "acquire_duration_ms": 3,
        "canceled_acquires": 0, "empty_acquires": 0
      }
    },
    "redis": {
      "status": "up",
      "pool": {
        "total_conns": 4, "idle_conns": 3, "stale_conns": 0,
        "hits": 128, "misses": 3, "timeouts": 0
      }
    }
  },
  "version": "1.0.0",
  "timestamp": "2026-09-11T12:00:00Z"
}
```

### Response `503` — not ready

```json
{
  "status": "not_ready",
  "checks": {
    "database": { "status": "down", "error": "context deadline exceeded" },
    "redis":    { "status": "up",   "pool": { } }
  },
  "version": "1.0.0",
  "timestamp": "2026-09-11T12:00:00Z"
}
```

### What counts as unhealthy

| Dependency | Down means | Why |
|---|---|---|
| **Postgres** | `503`, not ready | Without it every request fails. |
| **Redis** | reported, still `200` | Rate limiting and caching degrade, but the service can still serve requests. |

That asymmetry is the whole point of this endpoint — `internal/app/app.go:App.Health`.

Point your `readinessProbe` here.

---

## `GET /health`

The general health endpoint most tooling expects. Same checks and same
3-second timeout as `/health/ready`, with the service identity added.

**Auth:** none · **Rate limit:** none

### Response `200`

```json
{
  "status": "ok",
  "service": "Hisabji",
  "version": "1.0.0",
  "environment": "development",
  "checks": {
    "database": { "status": "up", "pool": { } },
    "redis":    { "status": "up", "pool": { } }
  },
  "timestamp": "2026-09-11T12:00:00Z"
}
```

`503` with `"status": "unhealthy"` when the database is unreachable.

```bash
curl -s http://localhost:8080/health | jq
```

---

## Unknown routes

Anything that matches no route still returns the **standard envelope**, not
gin's plain-text default (`internal/core/response/writer.go:NotFoundHandler`):

```json
{
  "success": false,
  "code": "ROUTE_NOT_FOUND",
  "message": "The requested endpoint does not exist.",
  "details": { "method": "GET", "path": "/api/v1/expenses" },
  "hint": "Check the API documentation for the correct path and version prefix.",
  "request_id": "…",
  "timestamp": "…"
}
```

A known path with the wrong verb returns `405 METHOD_NOT_ALLOWED` in the same
shape.
