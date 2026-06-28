# Improvements & Roadmap (Interview Perspective)

The current code is an honest, working **core**. This document is the "what I'd
do next and why" — the part interviewers probe to gauge seniority. Items are
ordered roughly by impact-per-effort.

---

## Tier 1 — Correctness & Security (do first)

### 1. Authentication & identity
**Problem.** `sender_id` and `created_by` are trusted straight from the request
body. Anyone can impersonate anyone.
**Fix.** Issue JWTs on login; validate at the gateway/middleware; derive the user
ID from the verified token, never from the body. Add a `middleware/` package in
`api-server` (the layout already anticipates it).
**Why it's first.** No access-control story makes everything else moot.

### 2. Application-level ACKs
**Problem.** TCP delivery ≠ the client rendered the message before crashing.
**Fix.** Implement the already-defined `domain.AckFrame`: track `unacked:{user}:{msgID}`
in Redis with a TTL; clear on ACK; unacked-on-expiry rides the next delta-sync.
**Why.** Turns "probably delivered" into "provably delivered or recovered".

### 3. Input validation & limits
Content length caps, channel name rules, max members per channel, request body
size limits, and per-endpoint method/route hardening.

---

## Tier 2 — Scale the Real-Time Path

### 4. Presence + point-to-point dispatch
**Problem (today).** Room broadcast: every worker subscribed to a channel
receives every message, even for offline/idle local users. Wasteful for large
channels with few online members.
**Fix.** Workers write `presence:{userID} → {nodeID}` (TTL refreshed by
heartbeat). `api-server` resolves online members → publishes only to the specific
`node:{nodeID}` queues that hold a recipient.
**Trade-off.** Presence accuracy + node-failure handling vs. broadcast bandwidth.
Worth it past the scale where fan-out volume hurts.

### 5. Live (un)subscribe without reconnect
**Problem.** WebSocket subscription set is fixed at connect; join/leave needs a
reconnect.
**Fix.** A control frame protocol over the socket (`{"op":"subscribe",
"channel":...}`) that starts/stops a Redis subscriber goroutine on the fly.

### 6. Backpressure policy
**Today.** A full `Send` buffer drops the message (logged). Define an explicit
policy: drop-oldest, disconnect-slow-consumer, or per-client flow control, and
emit metrics when it triggers.

---

## Tier 3 — Data Layer

### 7. Read replicas
Route delta-sync and `GET` reads to replicas; keep writes on the primary. The
repository layer already isolates queries, so this is a connection-routing change.

### 8. Sharding `messages`
Shard by `conversation_id` once a single primary saturates. The composite index
`(conversation_id, id)` already aligns with this shard key, so range reads stay
local to a shard.

### 9. Schema growth
`last_read` per (user, channel) for unread counts and read receipts;
`deleted_at`/`edited_at` for message edits/deletes; reactions and threads as
child tables.

---

## Tier 4 — Operability

### 10. Config & secrets
`shared/config` already reads env vars; add validation, typed durations, and pull
DB/Redis credentials from a secret manager rather than defaults.

### 11. Observability
Structured logging (replace `log.Printf` with `slog`/zap), Prometheus metrics
(messages/sec, fan-out latency, active connections via `Hub.ActiveCount`),
OpenTelemetry tracing across the HTTP→Redis→WS hop, and `/healthz` + `/readyz`.

### 12. Graceful shutdown polish
`Hub.Shutdown()` exists; add connection-draining coordination with the LB
(deregister → drain → close) so rollouts don't drop users.

### 13. Migrations tooling
Adopt `golang-migrate`/`goose` instead of applying raw SQL by hand; wire it into
the `Makefile` and CI.

---

## Tier 5 — Engineering Hygiene

### 14. Fill the placeholders
`Makefile` (per-module `build`/`test`/`run`/`lint`), `.gitignore` (Go defaults +
binaries), and `.github/workflows/ci.yml` (build + vet + test all three modules,
spin up MySQL/Redis service containers).

### 15. Test depth
Unit tests for `ChannelService` validation and the Snowflake generator; a
`Broker` mock to unit-test `MessageService` fan-out without Redis; a real
end-to-end WebSocket delivery test.

### 16. API contract
Publish an OpenAPI spec for the HTTP surface and a documented WebSocket frame
schema; generate client types from it.

---

## How to talk about this in an interview

- **Lead with the trade-off, not the feature.** "I used room broadcast because
  it's correct and simple; here's exactly when I'd switch to presence-based
  dispatch and what that costs."
- **Separate implemented from planned.** Credibility comes from knowing the
  difference (see the ✅/🟡 tables throughout these docs).
- **Tie every improvement to a trigger.** Not "add sharding" but "when a single
  primary's write IOPS saturates, shard `messages` by `conversation_id` because
  the index already aligns with that key."
