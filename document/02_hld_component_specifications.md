# Architectural Component Deep Dive & Trade-offs

In a system-design discussion the **"why"** matters more than the "what". This
document breaks down each component, what it does **today in this repo**, and the
engineering trade-off behind it.

> ✅ = implemented · 🟡 = roadmap. The codebase deliberately keeps the
> implemented core small and honest; roadmap items are called out so the design
> intent is clear without overstating what runs.

---

## 1. API Gateway & Load Balancer 🟡

* **Technology:** NGINX, HAProxy, or a cloud L7 LB (AWS ALB / GCP LB).
* **Responsibilities:**
  * **SSL/TLS termination** at the edge so internal hops are plaintext and cheap.
  * **Protocol-aware routing:** plain `POST` → `api-server`; `Connection: Upgrade`
    (WebSocket handshake) → `ws-worker`.
  * **Rate limiting** per user/IP to shield MySQL and Redis from abuse.
* **Status:** not in this repo — it's an infra concern. The two services listen
  on `:8080` and `:8081` directly for local development.

---

## 2. `api-server` — Stateless HTTP Ingress ✅

* **Module:** `teamchat/api-server` · **Pattern:** stateless microservice.
* **Responsibilities (all implemented):**
  * **Idempotency guard** — `CacheRepository.AcquireIdempotencyLock` does a Redis
    `SET NX` on `idempotency:{key}` with a 30s TTL. A duplicate key returns
    `409 Conflict`. This kills the "double send" problem from mobile retries.
  * **Authorization** — `MessageService` rejects any sender who is not in
    `channel_members` for the target channel (`403 Forbidden`).
  * **ID generation** — assigns a **Snowflake ID** (see §6) before insert.
  * **Persistence + publish** — writes to MySQL, then publishes to Redis.
  * **User & channel management** — registration, channel CRUD, membership.
  * **Delta sync** — indexed catch-up reads for reconnecting clients.
* **Scaling:** CPU/throughput-bound; horizontally autoscales (e.g. K8s HPA).
  Because it holds **no per-connection state**, any instance can serve any request
  behind a round-robin LB.

---

## 3. `ws-worker` — Stateful WebSocket Fleet ✅

* **Module:** `teamchat/ws-worker` · **Pattern:** long-lived stateful TCP workers.
* **Responsibilities (all implemented):**
  * Holds an open `ws://` connection per online user (`Client`).
  * On connect, looks up **all** channels the user belongs to and starts **one
    Redis subscriber goroutine per channel**, multiplexing every channel onto the
    single socket.
  * **Heartbeat / zombie detection** — `Ping/Pong` with a 60s read deadline. A
    client that stops responding has its connection (and goroutines) reclaimed.
  * **Echo suppression** — a sender does not receive their own message back over
    the socket (the HTTP `202` already confirmed it).
  * **Lifecycle via `context`** — `readPump` cancels a shared context on
    disconnect, which tears down every subscriber goroutine with no leaks. A
    `Hub` tracks active clients for metrics and graceful shutdown.
* **Scaling:** memory/connection-bound; scales on concurrent users.
* **Trade-off:** stateful servers need **connection draining** on deploy so a
  rollout doesn't drop thousands of sockets at once — hence the `Hub.Shutdown()`
  path on `SIGTERM`.

---

## 4. Redis — Event Backplane & Ephemeral Store ✅ / 🟡

* **Pattern:** Pub/Sub fan-out + key/value for short-lived data.
* **Implemented (✅):**
  * **Decoupling** — `api-server` publishes to `chat:room:{channelID}`; it never
    needs to know which `ws-worker` (if any) holds the recipient. Workers
    subscribe to the channels their users care about.
  * **Idempotency keys** — `SET NX` locks as described in §2.
* **Roadmap (🟡):**
  * **Presence matrix** (`user_id → worker_node_id`) to enable targeted
    point-to-point dispatch instead of room broadcast (see
    [`07_improvements_roadmap.md`](./07_improvements_roadmap.md)).
* **Trade-off (Redis vs. Kafka):** Kafka shines for durable, replayable streams.
  But MySQL is already our durable log and offline clients catch up over REST, so
  Kafka's disk persistence would only add latency to the real-time path. Redis
  Pub/Sub is in-memory and ideal for fast fan-out. The cost: Pub/Sub messages are
  **fire-and-forget** — a worker that is down misses them. That's acceptable
  precisely because the delta-sync path guarantees correctness on reconnect.

---

## 5. MySQL — System of Record ✅ (Replicas 🟡)

* **Pattern:** relational primary; read-replicas are a roadmap item.
* **Schema (implemented):** `users`, `conversations` (channels), `channel_members`,
  `messages`. Full detail in [`04_data_model.md`](./04_data_model.md).
* **Key index:** `messages` carries a composite B-tree index on
  `(conversation_id, id)`.
* **Why it matters:** delta-sync runs `WHERE conversation_id = ? AND id > ?
  ORDER BY id LIMIT 100`. The composite index turns that into an index-range scan
  (sub-millisecond) instead of a full-table scan, even with billions of rows.
* **Membership index:** `channel_members` has `idx_user_channels (user_id)` so
  "what channels is this user in?" — run on every WebSocket connect — is an index
  lookup.

---

## 6. Snowflake ID Generator ✅

* **Package:** `teamchat/shared/snowflake` · **Pattern:** distributed 64-bit IDs.
* **Layout:** `41-bit timestamp | 10-bit worker ID | 12-bit sequence` →
  4096 IDs/ms/node, 1024 nodes, ~69 years of range, **time-sortable**.
* **Trade-off (vs MySQL `AUTO_INCREMENT`):** auto-increment serializes every
  insert on a single counter and isn't meaningful across shards. Snowflake IDs are
  generated in-process (no DB round-trip), are globally unique across nodes, and
  embed creation time — so `ORDER BY id` *is* chronological order, which is exactly
  what delta-sync relies on.
* **Clock-skew safety:** if the wall clock moves backwards the generator stalls
  until it catches up rather than emit a duplicate.

---

## 7. `shared` Module — Why a Library Module ✅

* **Module:** `teamchat/shared` holds `domain`, `config`, `repository`, `pubsub`,
  and `snowflake`.
* **Rule:** services depend on `shared`; `shared` depends on **no** service. This
  one-way dependency keeps the graph acyclic and lets either service evolve
  without breaking the other.
* **Trade-off:** a shared module risks becoming a dumping ground. We mitigate by
  only placing genuinely cross-service primitives there (models, DB access,
  config, ID gen) and keeping service-specific logic (`service/`, `handler/`)
  inside each service's own `internal/` tree, which Go forbids other modules from
  importing.

---

## Implemented vs. Roadmap — at a glance

| Capability | Status |
|---|---|
| Multi-channel + DM messaging | ✅ |
| Membership-based authorization | ✅ |
| Idempotent sends (Redis `SET NX`) | ✅ |
| Snowflake IDs | ✅ |
| Redis room broadcast fan-out | ✅ |
| One WebSocket per user, all channels | ✅ |
| Heartbeat / graceful shutdown | ✅ |
| Delta-sync catch-up | ✅ |
| Presence + point-to-point dispatch | 🟡 |
| Application-level ACKs | 🟡 |
| MySQL read-replicas / sharding | 🟡 |
| Auth (JWT/OAuth), rate limiting, gateway | 🟡 |
