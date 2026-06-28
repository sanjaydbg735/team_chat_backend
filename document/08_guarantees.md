# Guarantees & Semantics

What the TeamChat backend **promises**, what it promises **conditionally**, and —
just as important for an interview — what it deliberately **does not** promise.

> Legend: ✅ guaranteed by the current code · ⚠️ guaranteed only under stated
> conditions · ❌ explicit non-guarantee (roadmap or out of scope).

---

## 1. Durability

| | Guarantee |
|---|---|
| ✅ | **Persist-before-publish.** A message is `INSERT`ed into MySQL *before* it is published to Redis. If you received `202 Accepted`, the message is durably stored. |
| ✅ | **MySQL is the single source of truth.** Real-time delivery is derived from the stored record, never the other way around. |
| ⚠️ | **Crash window.** If the process dies *between* the DB insert and the Redis publish, the message is **stored but not pushed in real time**. It is **not lost** — it is recovered on the next delta-sync (see §2). |
| ❌ | **No durability beyond the MySQL primary** (no replicas/WAL shipping yet — roadmap). |

---

## 2. Delivery

The system splits delivery into a fast best-effort path and a correct catch-up
path. Read these two rows together.

| | Guarantee |
|---|---|
| ⚠️ | **Real-time path is best-effort, at-most-once.** Redis Pub/Sub is fire-and-forget: an offline user (no subscriber) or a full client buffer means no real-time push. No duplicates on this path. |
| ✅ | **No permanent loss / eventual delivery.** Every persisted message is retrievable via `POST /api/v1/sync/deltas`. Combined with reconnect, a client eventually receives **every** message for its channels. |
| ✅ | **Client-side effectively-once.** Because every message has a unique, monotonic Snowflake `id`, a client that de-dupes/merges by `id` sees each message exactly once even if real-time and delta-sync overlap. |
| ✅ | **No self-echo.** A sender never receives its own message back over the WebSocket; the HTTP `202` is its confirmation. |
| ❌ | **No real-time delivery acknowledgement.** The server does not yet know the client rendered the message (`AckFrame` is defined but not wired — roadmap). |

---

## 3. Ordering & Consistency

| | Guarantee |
|---|---|
| ✅ | **Total order per channel.** Messages are ordered by Snowflake `id`; `ORDER BY id` yields a single, consistent order that **all** clients agree on. |
| ✅ | **Chronological ordering.** The Snowflake timestamp is the high bits, so `id` order ≈ wall-clock creation order without a separate sort column. |
| ✅ | **Convergence (eventual consistency).** All members of a channel converge to the identical ordered history, because order is defined by the stored `id`, not by network arrival order. |
| ⚠️ | **Arrival order ≠ guaranteed order.** Frames may *arrive* out of order across a reconnect boundary; clients are expected to order/merge by `id` (standard chat-client behavior). |

---

## 4. Idempotency

| | Guarantee |
|---|---|
| ✅ | **Duplicate-send suppression.** With an `X-Idempotency-Key`, a repeated send within the window returns `409 Conflict` instead of creating a second message. |
| ⚠️ | **Bounded 30-second window.** Dedup is enforced for 30s per key (Redis `SET NX` TTL). Retries after the window can slip through. |
| ⚠️ | **Lock-before-validate edge case.** The idempotency key is claimed before the membership/insert steps, so a request that then fails (e.g. `403`, or a DB error) still holds the key for 30s — a retry within that window gets `409`. Clients should use a **fresh key per logical message**, not per network attempt of a rejected message. |
| ❌ | **No idempotency without the header.** If the client omits `X-Idempotency-Key`, duplicates are accepted. |

---

## 5. Authorization & Identity

| | Guarantee |
|---|---|
| ✅ | **Membership-gated sends.** Only a member of a channel can post to it; non-members get `403 Forbidden`. |
| ✅ | **Unique usernames.** Enforced at both the service layer and a DB `UNIQUE` constraint. |
| ✅ | **Globally unique IDs.** Users, channels, and messages all carry unique Snowflake IDs. |
| ❌ | **No authentication.** `sender_id` / `created_by` are trusted from the request body — there is no identity verification yet (JWT is the first roadmap item). |
| ❌ | **No read-side authorization.** Delta-sync trusts the channel IDs the client asks for; it does not re-verify membership on reads (roadmap). |

---

## 6. Connection Liveness & Resource Safety

| | Guarantee |
|---|---|
| ✅ | **Zombie-connection reclamation.** A WebSocket that misses the Ping/Pong heartbeat is detected and closed within ~60s, freeing its memory. |
| ✅ | **No goroutine leaks.** A shared `context` ties every per-channel subscriber goroutine to the connection; disconnect cancels them all. |
| ✅ | **Single connection, all channels.** One socket per `user_id` multiplexes every channel the user belongs to. |
| ✅ | **Bounded buffering with explicit drop policy.** Each client has a 256-message send buffer; on overflow the message is dropped and logged rather than blocking other channels. |
| ⚠️ | **Membership changes apply on reconnect.** The channel subscription set is fixed at connect time; joins/leaves take effect on the next reconnect. |

---

## 7. Operational

| | Guarantee |
|---|---|
| ✅ | **Graceful shutdown.** On `SIGTERM`/`SIGINT`, in-flight HTTP requests are allowed to finish and open WebSockets are closed cleanly via `Hub.Shutdown()`. |
| ✅ | **Fail-fast startup.** Services `Ping` MySQL on boot and exit immediately on misconfiguration rather than serving broken traffic. |
| ✅ | **Independent failure domains.** `api-server` and `ws-worker` fail and scale independently; a WebSocket fleet outage does not stop message ingestion/persistence. |
| ❌ | **No HA for the broker/DB.** Single Redis and single MySQL primary today; clustering/replicas are roadmap. |

---

## 8. One-line summary of delivery semantics

> **At-most-once in real time, never permanently lost, and effectively-once at
> the client** — because MySQL durably stores every message and unique Snowflake
> IDs let clients reconcile the real-time stream with delta-sync.

---

## 9. Explicit non-guarantees (say these out loud in an interview)

- ❌ Exactly-once **real-time** delivery (we provide effectively-once via client dedup by `id`).
- ❌ Authentication / verified identity.
- ❌ Real-time delivery receipts / ACKs.
- ❌ Durability beyond a single MySQL primary; no cross-region replay.
- ❌ Idempotency outside the 30s window or without the header.
- ❌ Live re-subscription without reconnect.
- ❌ Rate limiting / abuse protection (gateway concern).

Each of these maps to a tracked item in
[`07_improvements_roadmap.md`](./07_improvements_roadmap.md).
