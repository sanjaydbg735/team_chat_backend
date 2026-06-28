# TeamChat Backend

A decoupled, hybrid-protocol team-chat backend in Go. State mutations go over
**HTTP**; real-time delivery goes over **WebSockets**; the two are connected by a
**Redis Pub/Sub** backplane and a shared **MySQL** store. Users can belong to
many channels, create channels, and exchange DMs (a DM is just a 2-member
channel).

```text
                          INSERT
   ┌──────────┐  HTTP POST  ┌──────────────────┐ ───────────▶ ┌─────────┐
   │ Client A │ ──────────▶ │  api-server :8080 │              │  MySQL  │
   │ (sender) │             │                   │ ◀─────────── │         │
   └──────────┘             └──────────────────┘  SELECT (sync) └─────────┘
                                     │  ▲
                              PUBLISH │  │ POST /sync/deltas (reconnect)
                                     ▼  │
                              ┌─────────┐         ┌──────────────────┐  WebSocket  ┌──────────┐
                              │  Redis  │ ──────▶ │  ws-worker :8081  │ ──────────▶ │ Client B │
                              │ Pub/Sub │ fan-out │                   │   push JSON │(receiver)│
                              └─────────┘         └──────────────────┘             └──────────┘
```

---

## Architecture at a glance

- **`api-server`** (stateless HTTP, `:8080`) — users, channels, message ingest,
  delta-sync. Scales on throughput.
- **`ws-worker`** (stateful WebSocket, `:8081`) — one socket per user,
  multiplexing all their channels. Scales on concurrent connections.
- **`shared`** (library module) — `domain`, `config`, `repository`, `pubsub`,
  `snowflake`.

It's a **multi-module monorepo**: three Go modules tied together by `go.work`,
each independently buildable. See [`document/`](./document) for the full design.

---

## Repository layout

```
team_chat_backend/
├── go.work                       # workspace: links the 3 modules
├── shared/                       # module teamchat/shared (library)
│   ├── domain/  config/  repository/  pubsub/  snowflake/
├── services/
│   ├── api-server/               # module teamchat/api-server
│   │   ├── main.go
│   │   └── internal/{handler/http, service}/
│   └── ws-worker/                # module teamchat/ws-worker
│       ├── main.go
│       └── internal/handler/ws/
├── scripts/migrations/           # SQL schema (000001, 000002)
├── deployments/docker-compose.yml# local MySQL + Redis
└── document/                     # HLD + API + interview docs
```

---

## Quickstart

### 1. Start infrastructure
```bash
docker compose -f deployments/docker-compose.yml up -d
```
This brings up MySQL on `:3307` and Redis on `:6379`.

### 2. Apply migrations
```bash
mysql -h 127.0.0.1 -P 3307 -u root -prootpassword teamchat \
  < scripts/migrations/000001_init.sql
mysql -h 127.0.0.1 -P 3307 -u root -prootpassword teamchat \
  < scripts/migrations/000002_multi_channel.sql
```

### 3. Run the services (two terminals)
```bash
# terminal 1 — HTTP API
go run teamchat/api-server

# terminal 2 — WebSocket worker
go run teamchat/ws-worker
```

### 4. Try it
See [`document/05_api_reference.md`](./document/05_api_reference.md) for the full
end-to-end smoke test (create users → create channel → open WS → send → catch up).

---

## Build & test

```bash
# build every module
go build teamchat/shared/... teamchat/api-server/... teamchat/ws-worker/...

# vet
go vet teamchat/shared/... teamchat/api-server/... teamchat/ws-worker/...

# integration tests (needs Docker MySQL + Redis running)
cd services/api-server && go test ./...
```

### Configuration (env vars, with defaults)
| Var | Default | Used by |
|---|---|---|
| `MYSQL_DSN` | `root:rootpassword@tcp(127.0.0.1:3307)/teamchat?parseTime=true` | both |
| `REDIS_ADDR` | `127.0.0.1:6379` | both |
| `API_PORT` | `:8080` | api-server |
| `WS_PORT` | `:8081` | ws-worker |
| `WORKER_ID` | `1` | Snowflake node ID (unique per instance) |

---

## Status

Implemented: multi-channel + DM messaging, membership authorization, idempotent
sends, Snowflake IDs, Redis room fan-out, one-socket-per-user multiplexing,
heartbeats, graceful shutdown, and delta-sync. Roadmap items (presence-based
dispatch, ACKs, auth, replicas/sharding) are tracked in doc 07.

**Core guarantee:** *at-most-once in real time, never permanently lost, and
effectively-once at the client* — every message is durably stored in MySQL and
unique Snowflake IDs let clients reconcile the live stream with delta-sync. Full
breakdown (durability, ordering, idempotency, auth, and explicit non-guarantees)
in [doc 08](./document/08_guarantees.md).
