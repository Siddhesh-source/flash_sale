# Flash Sale Inventory System

## The Problem

E-commerce flash sales face a critical challenge: when thousands of users simultaneously attempt to purchase limited inventory, traditional systems either:
- **Oversell** — sell more items than available, leading to cancelled orders and customer frustration
- **Lock up** — use pessimistic database locks that create bottlenecks and timeouts under load
- **Lose data** — crash during peak traffic and lose transaction records

A single oversold item can cost businesses hundreds in refunds, support overhead, and reputation damage. At scale, this becomes unsustainable.

## The Solution

This system guarantees **zero overselling** while handling 10,000+ concurrent requests by:

1. **Atomic inventory operations** — Redis Lua scripts ensure stock decrements are indivisible
2. **Dual-layer persistence** — Redis for speed, PostgreSQL as source of truth
3. **Automatic reconciliation** — detects and corrects any Redis/database drift within 60 seconds
4. **Graceful degradation** — rate limiting and health checks prevent cascading failures

The result: sub-100ms response times with mathematical certainty that available stock never goes negative.

---

## How It Works

### Core Methodology

```mermaid
graph LR
    A[User Request] --> B{Rate Limit Check}
    B -->|Pass| C[Redis Lua Script]
    B -->|Fail| D[429 Response]
    C -->|Stock Available| E[Atomic Decrement]
    C -->|Sold Out| F[Join Waitlist]
    E --> G[PostgreSQL Record]
    G --> H[WebSocket Broadcast]
    E --> I[10min TTL Timer]
    I -->|Expires| J[Auto-Release Stock]
    J --> K[Promote Next User]
```

**Key principles:**

1. **Single source of truth** — PostgreSQL stores all reservations and orders
2. **Redis as cache** — holds real-time counters for instant availability checks
3. **Lua scripts** — execute multi-step operations atomically (check stock → decrement → increment reserved)
4. **Background reconciliation** — compares Redis vs PostgreSQL every 60s and corrects drift
5. **Idempotency** — duplicate requests return cached results, preventing double-reservations

---

## Key Features

### 🎯 Zero Overselling Guarantee
- Atomic Redis Lua scripts prevent race conditions
- Chaos-tested: survives Redis crashes with zero data loss
- Reconciliation workers detect and fix any drift within 60 seconds

### ⚡ High Performance
- Handles 10,000+ concurrent requests
- P99 latency < 100ms for reserve operations
- Read replica offloads non-critical queries

### 🛡️ Resilience
- **Rate limiting**: Token bucket (10 req/user, 2/sec refill) prevents abuse
- **Health checks**: `/healthz` returns 503 during reconciliation
- **AOF persistence**: Redis data survives crashes
- **Automatic recovery**: Startup reconciliation syncs Redis from PostgreSQL

### 🔄 Real-Time Updates
- WebSocket broadcasts for live inventory changes
- User-targeted notifications for waitlist promotions
- 60-second expiry warnings before reservation timeout

### 📊 Full Observability
- OpenTelemetry distributed tracing (Jaeger)
- Prometheus metrics with SLO dashboards (Grafana)
- Append-only audit log for compliance

---

## System Architecture

```mermaid
graph TB
    subgraph Client
        HTTP[HTTP Requests]
        WS[WebSocket Connections]
    end

    subgraph "API Layer"
        RL[Rate Limiter<br/>Token Bucket]
        Router[HTTP Router]
    end

    subgraph "Business Logic"
        INV[Inventory Service]
        RES[Reservation Service]
        WAIT[Waitlist Service]
        HUB[WebSocket Hub]
    end

    subgraph "Data Stores"
        REDIS[(Redis Primary<br/>Atomic Operations)]
        REPLICA[(Redis Replica<br/>Read Queries)]
        PG[(PostgreSQL<br/>Source of Truth)]
    end

    subgraph "Background Jobs"
        EXP[Expiry Handler<br/>TTL Monitor]
        REC[Reconciler<br/>Every 60s]
        BOOT[Startup Sync]
    end

    HTTP --> RL --> Router
    WS --> HUB
    Router --> INV & RES & WAIT
    INV & RES & WAIT --> REDIS
    WAIT --> REPLICA
    RES --> PG
    REDIS -.replicates.-> REPLICA
    EXP --> REDIS & PG & HUB
    REC --> REDIS & PG
    BOOT --> REDIS & PG

    style REDIS fill:#ff6b6b,color:#fff
    style PG fill:#339af0,color:#fff
    style RL fill:#ff6b6b,color:#fff
```

---

## Tech Stack

| Component | Technology | Purpose |
|---|---|---|
| **Language** | Go 1.22 | High-performance concurrency |
| **Cache** | Redis 7 | Atomic operations via Lua scripts |
| **Database** | PostgreSQL 16 | Durable source of truth |
| **HTTP** | Chi v5 | Lightweight routing |
| **Tracing** | OpenTelemetry + Jaeger | Distributed request tracing |
| **Metrics** | Prometheus + Grafana | Real-time monitoring |
| **Testing** | testcontainers-go + k6 | Chaos and load testing |

---

## Request Flow: Reserve an Item

```mermaid
sequenceDiagram
    participant User
    participant API
    participant Redis
    participant Database
    participant WebSocket

    User->>API: POST /reserve
    API->>API: Check rate limit (10/user)
    
    alt Rate limit exceeded
        API-->>User: 429 Too Many Requests
    end
    
    API->>Redis: Execute Lua script
    Note over Redis: Atomic: check stock<br/>decrement available<br/>increment reserved
    
    alt Stock available
        Redis-->>API: Success
        API->>Database: Save reservation<br/>(expires in 10 min)
        API->>WebSocket: Broadcast stock update
        API-->>User: 200 OK + reservation_id
    else Sold out
        Redis-->>API: Failure
        API-->>User: 409 Conflict
    end
```

## Automatic Expiry & Waitlist Promotion

```mermaid
sequenceDiagram
    participant Timer
    participant Worker
    participant Redis
    participant Database
    participant WebSocket

    Timer->>Worker: Reservation expired (10 min)
    Worker->>Redis: Release stock (Lua script)
    Worker->>Database: Mark reservation expired
    
    Worker->>Redis: Check waitlist
    
    alt Waitlist has users
        Redis-->>Worker: Next user_id
        Worker->>Database: Create new reservation
        Worker->>WebSocket: Notify promoted user
        Worker->>WebSocket: Broadcast stock update
    end
```

## Failure Recovery

```mermaid
sequenceDiagram
    participant System
    participant Redis
    participant Database
    participant Reconciler

    Note over Redis: Redis crashes
    System->>System: Return 503 to all requests
    
    Note over Redis: Redis restarts
    System->>Reconciler: Start reconciliation
    
    Reconciler->>Database: Count active reservations
    Reconciler->>Database: Count confirmed orders
    Reconciler->>Reconciler: Calculate expected stock
    Reconciler->>Redis: Update counters
    Reconciler->>Database: Log corrections
    
    Reconciler-->>System: Complete
    System->>System: Resume normal operation
```

---

## Getting Started

### Prerequisites
- [Docker Desktop](https://www.docker.com/products/docker-desktop/)
- [Go 1.22+](https://go.dev/dl/)

### Installation

**1. Clone and setup**
```bash
git clone <your-repo-url>
cd flash-sale
cp .env.example .env
```

**2. Start infrastructure**
```bash
docker-compose up -d
```

**3. Run the server**
```bash
go run cmd/server/main.go
```

Server starts on `http://localhost:8080`. Health check at `/healthz` returns `503` during startup reconciliation, then `200` when ready.

### Verify Installation

```bash
# Create a sale
curl -X POST http://localhost:8080/api/sales \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Limited Edition Sneakers",
    "total_stock": 100,
    "start_time": "2026-01-01T00:00:00Z",
    "end_time": "2026-12-31T00:00:00Z",
    "status": "active"
  }'

# Reserve an item (save the sale ID from above)
curl -X POST http://localhost:8080/api/sales/{sale-id}/reserve \
  -H "X-User-ID: user-001" \
  -H "Idempotency-Key: unique-key-123"
```

### Access Services

| Service | URL | Credentials |
|---|---|---|
| API | http://localhost:8080 | — |
| Grafana | http://localhost:3000 | `admin / admin` |
| Prometheus | http://localhost:9090 | — |
| Jaeger | http://localhost:16686 | — |

---

## API Reference

### Sales Management

**Create Sale**
```http
POST /api/sales
Content-Type: application/json

{
  "name": "Product Name",
  "total_stock": 100,
  "start_time": "2026-01-01T00:00:00Z",
  "end_time": "2026-12-31T00:00:00Z",
  "status": "active"
}
```

**Get Sale Details**
```http
GET /api/sales/{id}

Response:
{
  "id": "...",
  "total_stock": 100,
  "available": 45,
  "reserved": 30,
  "confirmed": 25,
  "status": "active"
}
```

**Update Status**
```http
PATCH /api/sales/{id}/status
Content-Type: application/json

{ "status": "active" }
```

### Reservations (Rate Limited)

**Reserve Item**
```http
POST /api/sales/{id}/reserve
X-User-ID: user-123
Idempotency-Key: unique-key

Response 200:
{
  "reservation_id": "...",
  "expires_at": "2026-04-18T00:10:00Z"
}

Response 429: Rate limit exceeded (Retry-After: 1)
Response 409: Sold out (join waitlist)
```

**Confirm Reservation**
```http
POST /api/reservations/{id}/confirm
Idempotency-Key: confirm-key

Response:
{
  "order_id": "...",
  "status": "pending"
}
```

**Release Reservation**
```http
DELETE /api/reservations/{id}

Response: 204 No Content
```

### Waitlist (Rate Limited)

**Join Waitlist**
```http
POST /api/sales/{id}/waitlist
X-User-ID: user-123

Response:
{ "position": 15 }
```

**Check Position**
```http
GET /api/sales/{id}/waitlist/position?user_id=user-123

Response:
{ "position": 12 }
```

**Leave Waitlist**
```http
DELETE /api/sales/{id}/waitlist?user_id=user-123

Response: 204 No Content
```

### WebSocket Real-Time Updates

**Connect**
```javascript
const ws = new WebSocket('ws://localhost:8080/ws/sales/{id}?user_id=user-123');

ws.onmessage = (event) => {
  const data = JSON.parse(event.data);
  // Handle events
};
```

**Event Types**
```json
// Broadcast to all subscribers
{ "type": "stock_update", "available": 44, "reserved": 6 }
{ "type": "sale_ended", "reason": "sold_out" }

// User-targeted events
{ "type": "waitlist_promoted", "reservation_id": "...", "expires_at": "..." }
{ "type": "reservation_expiring", "seconds_remaining": 60 }
```

---

## Configuration

Environment variables (`.env`):

```bash
# Redis
REDIS_URL=redis://localhost:6379
REDIS_REPLICA_URL=redis://localhost:6380

# PostgreSQL
DATABASE_URL=postgres://user:pass@localhost:5432/flashsale

# Server
SERVER_PORT=8080
RESERVATION_TTL_SECONDS=600

# Rate Limiting
RATE_LIMIT_CAPACITY=10
RATE_LIMIT_RATE_PER_SEC=2

# Reconciliation
RECONCILIATION_INTERVAL_SECONDS=60

# Observability
OTLP_ENDPOINT=localhost:4317
APP_ENV=development
```

---

## Monitoring & Observability

### Key Metrics

| Metric | Type | Description |
|---|---|---|
| `reservation_total{result}` | Counter | Reservation attempts by outcome |
| `reservation_duration_seconds` | Histogram | Request latency (P50/P99) |
| `stock_available{sale_id}` | Gauge | Current inventory |
| `waitlist_depth{sale_id}` | Gauge | Waitlist size |
| `rate_limited_total{user_id}` | Counter | Rate limit rejections |
| `reconciliation_corrections_total` | Counter | Drift corrections |

### Service Level Objectives

| SLO | Target |
|---|---|
| Availability | 99.9% |
| Reserve P99 latency | < 100ms |
| Error rate (excl. 429) | < 0.1% |
| Overselling | 0 (hard guarantee) |

---

## Testing

### Unit Tests
```bash
go test ./internal/... -v -race
```

### Chaos Tests
```bash
# Requires Docker
go test ./chaos/... -v -timeout 10m
```

**Results:**
- **Redis crash**: 0 oversells, 2-5s recovery time
- **Drift correction**: Fixed within 60s
- **Rate limiter**: 10 succeed, 90 rate-limited per burst

### Load Testing
```bash
docker-compose --profile load-test up k6
```

---

## Database Schema

```sql
-- Core tables
sales (
  id UUID PRIMARY KEY,
  name TEXT,
  total_stock INT,
  status TEXT,
  start_time TIMESTAMPTZ,
  end_time TIMESTAMPTZ
)

reservations (
  id UUID PRIMARY KEY,
  sale_id UUID REFERENCES sales(id),
  user_id UUID,
  status TEXT, -- reserved, confirmed, released, expired
  expires_at TIMESTAMPTZ,
  idempotency_key TEXT UNIQUE
)

orders (
  id UUID PRIMARY KEY,
  sale_id UUID REFERENCES sales(id),
  user_id UUID,
  reservation_id UUID REFERENCES reservations(id),
  status TEXT, -- pending, paid, failed, refunded
  idempotency_key TEXT UNIQUE
)

audit_log (
  id BIGSERIAL PRIMARY KEY,
  entity_type TEXT,
  entity_id UUID,
  event_type TEXT,
  payload JSONB,
  created_at TIMESTAMPTZ
)
-- Append-only: UPDATE and DELETE blocked by Postgres rules
```

Migrations auto-apply on first Postgres startup via `docker-entrypoint-initdb.d`.

---

## Development

### Makefile Commands

```bash
make build          # Compile binary to bin/
make run            # Run server directly
make test           # Unit tests with race detector
make test-chaos     # Chaos tests (10min timeout)
make test-cover     # Generate coverage report
make infra-up       # Start Docker services
make infra-down     # Stop and remove services
make redis-cli      # Open Redis CLI
make psql           # Open PostgreSQL shell
make load-test      # Run k6 load test
```

### Project Structure

```
├── cmd/server/           # Application entry point
├── internal/
│   ├── api/              # HTTP handlers, middleware, routing
│   ├── inventory/        # Stock management
│   ├── reservation/      # Reserve/confirm/release logic
│   ├── waitlist/         # Waitlist operations
│   ├── queue/            # TTL expiry handler
│   ├── redis/            # Redis client + Lua scripts
│   ├── ratelimit/        # Token bucket limiter
│   ├── recovery/         # Startup reconciliation
│   ├── worker/           # Periodic reconciliation
│   ├── websocket/        # Real-time updates
│   ├── metrics/          # Prometheus metrics
│   └── telemetry/        # OpenTelemetry setup
├── chaos/                # Chaos engineering tests
├── migrations/           # SQL schema files
├── k6/                   # Load test scripts
└── grafana/              # Dashboards + provisioning
```

---

## License

MIT
