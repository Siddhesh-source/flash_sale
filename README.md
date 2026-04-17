# ⚡ Flash Sale — High-Throughput Inventory System

A production-grade flash sale engine built in Go. Handles **10,000+ concurrent requests** with a hard zero-overselling guarantee, real-time WebSocket updates, per-user rate limiting, and chaos-tested Redis failure recovery.

---

## Tech Stack

| Layer | Technology |
|---|---|
| Language | Go 1.22 |
| Cache / Atomics | Redis 7 (Lua scripts, AOF, Replica) |
| Database | PostgreSQL 16 |
| HTTP Router | Chi v5 |
| Tracing | OpenTelemetry → Jaeger |
| Metrics | Prometheus + Grafana |
| Load Testing | k6 |
| Chaos Testing | testcontainers-go |

---

## Architecture

```mermaid
graph TB
    subgraph Clients
        HC[HTTP Client]
        WSC[WebSocket Client]
    end

    subgraph "API Server"
        RL[Rate Limit Middleware\nToken Bucket · 10 req/user]
        Router[Chi Router]
        INV[Inventory Service]
        RES[Reservation Service]
        WL[Waitlist Service]
        HUB[WebSocket Hub]
    end

    subgraph "Redis Cluster"
        RP[(Primary\nAOF + Snapshots)]
        RR[(Replica\nRead-only · :6380)]
        LUA[Lua Scripts\nreserve · release · confirm\nwaitlist · token_bucket]
    end

    subgraph PostgreSQL
        PG[(Source of Truth)]
        T1[sales]
        T2[reservations]
        T3[orders]
        T4[audit_log\nappend-only]
    end

    subgraph "Background Workers"
        EXP[Expiry Handler\nTTL keyspace events]
        REC[Reconciler\nevery 60 s]
        STR[Startup Reconciler\nblocks healthz]
    end

    subgraph Observability
        JAE[Jaeger :16686]
        PRO[Prometheus :9090]
        GRA[Grafana :3000]
    end

    HC --> RL --> Router
    WSC --> HUB
    Router --> INV & RES & WL
    INV & RES & WL --> RP
    RP -. replication .-> RR
    WL --> RR
    RP --> LUA
    RES --> PG
    EXP --> RP & PG & HUB
    REC --> RP & PG
    STR --> RP & PG
    Router -. traces .-> JAE
    Router -. metrics .-> PRO
    PRO --> GRA

    style RP fill:#e03131,color:#fff
    style PG fill:#1971c2,color:#fff
    style RL fill:#e03131,color:#fff
    style STR fill:#f59f00,color:#fff
    style REC fill:#f59f00,color:#fff
```

---

## Core Flows

### Reserve an Item

```mermaid
sequenceDiagram
    participant C as Client
    participant RL as Rate Limiter
    participant API
    participant R as Redis (Lua)
    participant PG as Postgres
    participant WS as WebSocket

    C->>RL: POST /api/sales/:id/reserve
    alt Bucket empty
        RL-->>C: 429 Too Many Requests · Retry-After: 1
    end
    RL->>API: pass
    API->>R: EVALSHA reserve.lua (atomic)
    alt Stock available
        R-->>API: ok
        API->>PG: INSERT reservation (expires_at +10 min)
        API->>R: PUBLISH stock_update
        R-->>WS: broadcast available count
        API-->>C: 200 · reservation_id + expires_at
    else Sold out
        R-->>API: -1
        API-->>C: 409 · join waitlist
    end
```

### Expiry → Auto-Promote

```mermaid
sequenceDiagram
    participant R as Redis
    participant EXP as Expiry Handler
    participant PG as Postgres
    participant WS as WebSocket

    R-->>EXP: __keyevent__:expired (reservation TTL)
    EXP->>R: EVALSHA release.lua (restore stock)
    EXP->>PG: UPDATE reservation SET status='expired'
    EXP->>R: EVALSHA waitlist_promote.lua
    alt Waitlist not empty
        R-->>EXP: next user_id
        EXP->>PG: INSERT reservation for promoted user
        EXP->>WS: notify user (waitlist_promoted)
        EXP->>WS: broadcast stock_update
    end
```

### Redis Crash & Recovery

```mermaid
sequenceDiagram
    participant C as Clients
    participant API
    participant R as Redis
    participant REC as Startup Reconciler
    participant PG as Postgres

    C->>API: requests
    R--xAPI: Redis down
    API-->>C: 503 Service Unavailable
    Note over API: health probe returns 503

    Note over R: Redis restarts (AOF replay)
    API->>REC: ReconcileOnStartup()
    REC->>PG: COUNT reserved + confirmed per sale
    REC->>R: SET corrected available + reserved
    REC->>PG: INSERT audit_log (startup_reconciliation)
    REC-->>API: done
    Note over API: health probe returns 200
    API-->>C: resume traffic · zero oversell
```

### Reservation State Machine

```mermaid
stateDiagram-v2
    direction LR
    [*] --> reserved : POST /reserve\n(stock available)
    [*] --> waitlisted : POST /waitlist\n(sold out)

    reserved --> confirmed : POST /confirm\n→ order created
    reserved --> released  : DELETE /reservation
    reserved --> expired   : TTL fires

    waitlisted --> reserved : auto-promote\non expiry/release
    waitlisted --> [*]      : DELETE /waitlist

    expired   --> reserved : next waitlist user
    released  --> reserved : next waitlist user

    confirmed --> [*]
```

---

## Project Structure

```
flash-sale/
├── cmd/server/main.go            # Entry point
├── internal/
│   ├── api/                      # Router, middleware, handlers
│   │   ├── handlers/             # sales, reservations, waitlist, orders
│   │   ├── healthz.go            # /healthz (503 until reconciled)
│   │   ├── ratelimit_middleware.go
│   │   └── router.go
│   ├── config/                   # Viper-based env config
│   ├── inventory/                # Stock read logic
│   ├── reservation/              # Reserve / confirm / release
│   ├── waitlist/                 # Join / leave / position
│   ├── queue/                    # TTL expiry handler
│   ├── redis/
│   │   ├── client.go             # Primary + replica clients
│   │   └── scripts/              # *.lua atomic scripts
│   ├── ratelimit/limiter.go      # Token bucket via Redis
│   ├── recovery/reconcile_startup.go
│   ├── worker/reconciler.go      # Periodic drift correction
│   ├── audit/                    # Append-only log helper
│   ├── events/                   # Redis Pub/Sub publisher
│   ├── websocket/                # Hub + handler
│   ├── streams/                  # Redis Streams event log
│   ├── metrics/metrics.go        # Prometheus counters/gauges
│   ├── telemetry/                # OpenTelemetry tracer setup
│   └── slo/                      # SLO recording rules helpers
├── chaos/chaos_test.go           # Chaos engineering tests
├── migrations/                   # SQL schema files
├── k6/load_test.js               # k6 load test script
├── grafana/                      # Dashboard + provisioning JSON
├── prometheus.yml
├── recording_rules.yml
├── docker-compose.yml
└── .env.example
```

---

## Quick Start

### Prerequisites
- [Docker Desktop](https://www.docker.com/products/docker-desktop/)
- [Go 1.22+](https://go.dev/dl/)

### 1. Start infrastructure

```bash
docker-compose up -d
```

| Service | URL |
|---|---|
| API | http://localhost:8080 |
| Grafana | http://localhost:3000 · `admin / admin` |
| Prometheus | http://localhost:9090 |
| Jaeger | http://localhost:16686 |
| Redis primary | localhost:6379 |
| Redis replica | localhost:6380 |

### 2. Configure

```bash
cp .env.example .env
```

### 3. Run the server

```bash
go run cmd/server/main.go
```

The server **blocks on startup reconciliation** and returns `503` from `/healthz` until Redis is fully synced with Postgres.

### 4. Run tests

```bash
# Unit tests
go test ./internal/...

# Chaos tests (requires Docker)
go test -v -timeout 10m ./chaos

# Load test
docker-compose --profile load-test up k6
```

---

## API Reference

### Sales

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/sales` | Create a flash sale |
| `GET` | `/api/sales/:id` | Get sale + live stock counters |
| `PATCH` | `/api/sales/:id/status` | Set status (`pending` / `active` / `ended`) |

### Reservations _(rate-limited)_

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/sales/:id/reserve` | Atomically reserve one item |
| `POST` | `/api/reservations/:id/confirm` | Confirm → create order |
| `DELETE` | `/api/reservations/:id` | Release back to inventory |

### Waitlist _(rate-limited)_

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/sales/:id/waitlist` | Join waitlist (409 if stock exists) |
| `GET` | `/api/sales/:id/waitlist/position` | Current position (`?user_id=`) |
| `DELETE` | `/api/sales/:id/waitlist` | Leave waitlist |

### Orders & System

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/orders/:id` | Fetch confirmed order |
| `GET` | `/ws/sales/:id` | WebSocket (`?user_id=`) |
| `GET` | `/healthz` | `200 OK` or `503` during reconciliation |
| `GET` | `/metrics` | Prometheus scrape endpoint |

### Rate Limiting

Every `POST /reserve` and `POST /waitlist` is gated by a **token bucket** keyed on `X-User-ID` (falls back to remote IP).

```
Capacity  : 10 tokens
Refill    : 2 tokens / second
Exceeded  : HTTP 429  +  Retry-After: 1  +  X-RateLimit-Remaining: 0
```

### WebSocket Events

Connect: `ws://localhost:8080/ws/sales/:id?user_id=<uuid>`

```jsonc
// broadcast to all subscribers of a sale
{ "type": "stock_update",          "available": 44, "reserved": 6 }
{ "type": "sale_ended",            "reason": "sold_out" }

// targeted to one user
{ "type": "waitlist_promoted",     "reservation_id": "...", "expires_at": "..." }
{ "type": "reservation_expiring",  "reservation_id": "...", "seconds_remaining": 60 }
```

---

## Configuration

```bash
# .env.example

REDIS_URL=redis://localhost:6379
REDIS_REPLICA_URL=redis://localhost:6380

DATABASE_URL=postgres://user:pass@localhost:5432/flashsale

SERVER_PORT=8080
RESERVATION_TTL_SECONDS=600

RATE_LIMIT_CAPACITY=10
RATE_LIMIT_RATE_PER_SEC=2

RECONCILIATION_INTERVAL_SECONDS=60

OTLP_ENDPOINT=localhost:4317
APP_ENV=development
```

---

## Observability

### Metrics (Prometheus)

| Metric | Type | Description |
|---|---|---|
| `reservation_total{result}` | Counter | Attempts by outcome (`success`, `sold_out`) |
| `reservation_duration_seconds` | Histogram | End-to-end latency (P50/P99) |
| `stock_available{sale_id}` | Gauge | Live available inventory |
| `waitlist_depth{sale_id}` | Gauge | Current waitlist depth |
| `rate_limited_total{user_id}` | Counter | Rejected requests |
| `reconciliation_corrections_total` | Counter | Redis drift corrections |
| `websocket_connections_active` | Gauge | Open WebSocket connections |

### SLO Targets

| SLO | Target |
|---|---|
| Availability | 99.9% |
| Reserve P99 latency | < 100 ms |
| Error rate (excl. 429) | < 0.1% |
| Overselling | **0 — hard guarantee** |

---

## Resilience

### Redis Persistence

```
appendonly yes
appendfsync everysec
save 60 1
```

A read replica runs on `:6380` and handles non-critical reads (waitlist positions).

### Reconciliation

| Phase | Trigger | Behaviour |
|---|---|---|
| **Startup** | Server boot | Synchronous — blocks `/healthz` until complete |
| **Periodic** | Every 60 s | Background goroutine — corrects drift silently |

Both phases compute `expected_available = total_stock − reserved − confirmed` from Postgres and overwrite Redis if there is any drift. Every correction is appended to `audit_log`.

---

## Chaos Test Results

| Test | Scenario | Result |
|---|---|---|
| **Redis crash mid-sale** | Kill Redis after 20/100 requests | 0 oversells · 503s during outage · full recovery in ~3 s |
| **Drift correction** | SET available = 999 (true = 90) | Corrected within 60 s · audit row written |
| **Rate limiter burst** | 100 req/s from one user | 10 succeed · 90 × 429 · all 10 succeed after 5 s refill |

---

## Database Schema (abbreviated)

```sql
sales         (id, name, total_stock, status, start_time, end_time)
reservations  (id, sale_id, user_id, status, expires_at, idempotency_key)
orders        (id, sale_id, user_id, reservation_id, status, idempotency_key UNIQUE)
audit_log     (id, entity_type, entity_id, event_type, payload JSONB)
              -- UPDATE and DELETE blocked by Postgres rules
```

Migrations run automatically on first Postgres startup via `docker-entrypoint-initdb.d`.

---

## License

MIT
