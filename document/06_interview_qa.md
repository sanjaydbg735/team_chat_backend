# Interview Q&A — What / Why / How

A walkthrough of the design decisions in this project, framed as the questions an
interviewer asks and the answers grounded in **this codebase**. Each answer
follows **What → Why → How**, with the trade-off made explicit.

---

## Q1. Why split into two services (HTTP ingress + WebSocket worker)?

**What.** `api-server` handles state mutations over HTTP; `ws-worker` holds
long-lived WebSocket connections and streams messages out.

**Why.** They have **opposite operational profiles**:
- HTTP ingress is **stateless** and **CPU/throughput-bound** — any instance can
  serve any request, so it scales trivially behind a round-robin load balancer.
- WebSocket workers are **stateful** and **memory/connection-bound** — each holds
  thousands of open TCP sockets and must be drained carefully on deploy.

Coupling them would force the worse of both worlds: you'd have to do
connection-draining rollouts for a stateless API, and you couldn't scale sockets
independently of request volume.

**How.** Two binaries (`services/api-server`, `services/ws-worker`) communicate
**only** through Redis Pub/Sub and MySQL — never directly. The publisher doesn't
know or care which worker (if any) holds a recipient.

**Trade-off.** More infrastructure (two deployables, a broker) in exchange for
independent scaling, isolation, and safer deploys. For a toy app this is
overkill; for a team-chat product it's the standard shape.

---

## Q2. How do you guarantee a message isn't lost if the recipient is offline?

**What.** Real-time delivery is **best-effort**; correctness comes from a
**durable log + catch-up read**.

**Why.** Redis Pub/Sub is fire-and-forget — if no worker is subscribed for an
offline user, the message simply isn't pushed. We must not depend on the
real-time path for correctness.

**How.**
1. Every message is `INSERT`ed into MySQL **before** publishing to Redis.
2. On reconnect the client calls `POST /api/v1/sync/deltas` with the last
   message ID it holds per channel.
3. The Sync Service range-scans `messages(conversation_id, id)` and returns
   everything newer.

**Trade-off.** A brief reconciliation step on reconnect, in exchange for not
needing a durable/replayable broker (Kafka) on the hot path. MySQL is already the
source of truth, so this is "free" durability.

---

## Q3. Why Redis Pub/Sub instead of Kafka?

**What.** Redis Pub/Sub is the fan-out backplane between ingress and workers.

**Why.** Kafka's strength is **durable, replayable** streams persisted to disk.
But we already persist to MySQL and offline clients catch up over REST, so
Kafka's disk writes would only **add latency** to the real-time path without
adding value. Redis Pub/Sub is in-memory and optimized for low-latency fan-out.

**How.** `api-server` `PUBLISH`es to `chat:room:{channelID}`; each `ws-worker`
`SUBSCRIBE`s to the channels its connected users belong to.

**Trade-off.** Pub/Sub messages are dropped if no subscriber is listening (no
persistence, no replay). Acceptable precisely because the delta-sync path
(Q2) provides the durability guarantee. If we later needed cross-region replay or
an event-sourcing audit trail, Kafka would earn its place.

---

## Q4. How are messages ordered? Why Snowflake IDs over AUTO_INCREMENT?

**What.** Each message gets a 64-bit **Snowflake** ID:
`41-bit timestamp | 10-bit worker | 12-bit sequence`.

**Why.**
- **No DB bottleneck.** `AUTO_INCREMENT` serializes every insert on one counter
  and breaks across shards. Snowflake IDs are generated **in-process** with no DB
  round-trip and stay unique across many nodes.
- **Free chronological ordering.** The timestamp is the high bits, so `ORDER BY
  id` *is* time order — which is exactly what delta-sync's `id > last_id` relies
  on. No separate sort column needed.

**How.** `teamchat/shared/snowflake` — a mutex-guarded generator, 4096 IDs/ms/node,
with clock-skew protection (stalls rather than emitting duplicates if the wall
clock rewinds).

**Trade-off.** IDs leak approximate creation time and require a unique
`worker_id` per node. Both are fine here; if ID-based timing inference were a
concern we'd add a random salt or use ULIDs.

---

## Q5. How do you prevent duplicate messages from network retries?

**What.** Optional `X-Idempotency-Key` header on `POST /messages`.

**Why.** Mobile networks retry aggressively; without dedupe a single user tap can
create several identical messages.

**How.** `CacheRepository.AcquireIdempotencyLock` does Redis `SET key value NX EX
30`. First request wins and proceeds; a duplicate key within 30s returns
`409 Conflict`.

**Trade-off.** The 30s TTL bounds the dedup window (and Redis memory). A retry
after 30s would slip through — acceptable for chat, where the realistic retry
storm is seconds long. A stronger guarantee would need a persistent idempotency
table keyed on (client, key).

---

## Q6. "Only one channel existed." How did you model many channels + DMs?

**What.** Everything is a **channel** (`conversations` table). A DM is just a
channel of `type='DM'` with two members. Membership is many-to-many via
`channel_members`.

**Why.** A uniform model means **one** message path, **one** delivery path, and
**one** authorization rule — DMs and group channels don't fork the code. Users
naturally belong to many channels and channels have many users (m:n).

**How.**
- `conversations(id, type, name, created_by)` + `channel_members(channel_id,
  user_id)` with PK `(channel_id, user_id)`.
- `ChannelService.CreateChannel` validates `DM ⇒ exactly 2 members` and always
  enrolls the creator.
- `MessageService` checks `IsMember` before accepting any send (`403` otherwise).

**Trade-off.** Treating DMs as channels means a tiny bit of unused structure for
DMs (a `name` column they rarely use), in exchange for a dramatically simpler and
more consistent system.

---

## Q7. A user is in many channels. How does the WebSocket handle that?

**What.** **One** socket per user serves **all** their channels.

**Why.** Opening one connection per channel would multiply socket/file-descriptor
usage and complicate the client. Users expect a single live stream (like Slack).

**How.** On `GET /ws?user_id=`, the worker calls `GetChannelIDsByUser`, then
spawns **one Redis subscriber goroutine per channel**, all funneling into the
client's single `Send` buffer. A shared `context.Context` ties their lifetimes:
when `readPump` detects disconnect it cancels the context and every subscriber
goroutine exits — no leaks.

**Trade-off.** The subscription set is fixed at connect time, so join/leave takes
effect on the next reconnect. Simple and leak-free; live re-subscription is a
roadmap item (Q11).

---

## Q8. How do you detect dead/"zombie" connections?

**What.** A WebSocket Ping/Pong heartbeat with a read deadline.

**Why.** A client can vanish (tunnel, crash, dead battery) without sending a TCP
close frame. Without detection the server would leak memory holding dead sockets.

**How.** `writePump` sends a Ping every ~54s; `readPump` sets a 60s read deadline
that each Pong resets. Miss the window → the read errors → the connection and all
its goroutines are reclaimed, and the `Hub` unregisters the client.

**Trade-off.** Heartbeat traffic and a worst-case ~60s detection lag, in exchange
for bounded memory and accurate-enough presence.

---

## Q9. Why a multi-module monorepo with `go.work`? Why not one module or 3 repos?

**What.** Three Go modules — `shared`, `api-server`, `ws-worker` — in one repo,
linked by `go.work`; each service `replace`s `teamchat/shared` to the local path.

**Why.**
- **One module** blurs service boundaries — nothing stops `ws-worker` from
  importing `api-server`'s service code, creating accidental coupling.
- **Three separate repos** add real overhead early: you must publish/version the
  shared library and bump it on every change.
- **Multi-module monorepo** gives true module isolation (each has its own
  `go.mod`/`go.sum`, builds standalone) **and** a one-command local dev build.

**How.** `go.work` `use`s all three dirs; `go build teamchat/api-server` builds a
single deployable; the `replace` directive makes each service build even outside
the workspace.

**Trade-off.** Slightly more `go.mod` bookkeeping than a single module. The payoff
is clean boundaries now and a trivial path to split any service into its own repo
later (drop the `replace`, point at a tagged `shared`).

---

## Q10. How is the code layered, and why does `internal/` matter?

**What.** `handler → service → repository`, with `domain` as the shared language
and `pubsub`/`snowflake` as utilities.

**Why.** Each layer has one job: handlers translate HTTP, services own business
rules, repositories own SQL/Redis. This keeps business logic testable without a
web server and swappable without rewriting handlers.

**How.** Service code lives under each service's `internal/` tree, which **Go
compiler-enforces** as un-importable by other modules — so the boundary isn't
just convention. Cross-service primitives sit in `teamchat/shared`.

**Trade-off.** More files/indirection than a flat handler-does-everything design;
worth it the moment the project has more than one endpoint or needs tests.

---

## Q11. What breaks first at scale, and what would you fix?

Short version (full detail in
[`07_improvements_roadmap.md`](./07_improvements_roadmap.md)):

| Bottleneck | Symptom | Fix |
|---|---|---|
| Room broadcast | every worker gets every channel message | **presence-based point-to-point** dispatch |
| Single MySQL primary | read + write contention | **read replicas**, then shard `messages` by `conversation_id` |
| No auth | `sender_id` is trusted from the body | **JWT** at the gateway, derive identity server-side |
| Fixed WS subscriptions | join/leave needs reconnect | **control channel** for live (un)subscribe |
| No delivery proof | crash-before-render loses a message silently | **application ACKs** (`AckFrame` already defined) |

---

## Q12. How would you test this?

**What.** Integration tests in `services/api-server/internal/handler/http`
exercise the real MySQL + Redis (via Docker Compose): success, idempotency
conflict, non-member `403`, and delta-sync.

**Why.** The interesting behavior (idempotency, membership, indexed reads) lives
at the boundary between service and infrastructure — mocking it away would test
nothing real.

**How / roadmap.** Add unit tests for `ChannelService` validation and the
Snowflake generator (pure logic, no infra), a `Broker` mock to unit-test
`MessageService` fan-out, and a WebSocket end-to-end test using a real client.
