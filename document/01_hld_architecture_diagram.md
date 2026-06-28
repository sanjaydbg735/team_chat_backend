# High-Level Architecture Diagram

TeamChat is a **decoupled, hybrid-protocol** team-chat backend. We separate
**state mutations** (HTTP) from **real-time streaming** (WebSockets) so each can
scale on its own dimension, and we connect them through a **Redis Pub/Sub**
backplane and a shared **MySQL** store.

> **Legend:** ✅ = implemented in this repo · 🟡 = designed/roadmap (see
> [`07_improvements_roadmap.md`](./07_improvements_roadmap.md)).

---

## 1. Runtime Architecture

```text
  ┌────────────┐                                          ┌────────────┐
  │  Client A  │                                          │  Client B  │
  │  (sender)  │                                          │ (receiver) │
  └─────┬──────┘                                          └─────▲──────┘
        │ (1) HTTP POST /messages                               │ (6) push JSON frame
        │     (X-Idempotency-Key)                               │     over WebSocket
        ▼                                                       │
  ┌──────────────────────────────────────────────────────────────────────────┐
  │                  API Gateway / Load Balancer  🟡                           │
  │              (SSL termination · routing · rate limiting)                   │
  └─────┬────────────────────────────────────────────────────────────┬───────┘
        │ route HTTP                              route Upgrade (WS)   │
        ▼                                                              ▼
  ┌─────────────────────────────────┐              ┌──────────────────────────────────┐
  │   api-server :8080  (stateless) ✅│              │  ws-worker :8081  (stateful)  ✅  │
  │  ┌───────────────────────────┐  │              │  ┌────────────────────────────┐  │
  │  │ Message · Sync handlers   │  │              │  │ WS upgrade + Hub           │  │
  │  │ User · Channel handlers   │  │              │  │ per-channel Redis subs     │  │
  │  └───────────────────────────┘  │              │  └────────────────────────────┘  │
  └───┬─────────────────┬───────────┘              └───────────────▲──────────────────┘
      │ (2) membership  │ (4) PUBLISH                               │ (5) deliver to
      │ (3) INSERT      │     chat:room:{channel}                   │     subscribers
      ▼                 ▼                                           │
  ┌────────────────────────────────┐            ┌──────────────────┴─────┐
  │  MySQL                          │            │  Redis                 │
  │  users · channels · members ·   │            │  Pub/Sub + idempotency │
  │  messages   ◀── SELECT id>last  │            └────────────────────────┘
  └────────────────────────────────┘
        ▲
        │ (A/B) reconnect → POST /api/v1/sync/deltas → SELECT id > last_id
        └────────────────────────────────────────────  (Client B catch-up)
```

### Message path (happy case)
1. **Ingress** — sender POSTs a message to `api-server`, optionally with an
   `X-Idempotency-Key` header.
2. **Authorize** — `api-server` verifies the sender is a member of the target
   channel (`channel_members` lookup).
3. **Persist** — assigns a Snowflake ID and `INSERT`s into MySQL (source of truth).
4. **Publish** — publishes the message to the Redis channel `chat:room:{channelID}`.
5. **Fan-out** — every `ws-worker` that has a subscriber for that channel receives it.
6. **Deliver** — the worker pushes the JSON frame down the recipient's WebSocket.

### Catch-up path (offline reconnect)
- **A/B** — when a client reconnects it first calls `POST /api/v1/sync/deltas`
  with the last message ID it saw per channel; the Sync Service returns
  everything newer via an indexed range scan. This is what makes delivery
  **correct** even though real-time fan-out is best-effort.

---

## 2. Code / Deployment Topology (Multi-Module Monorepo)

The system is split into **three Go modules** tied together with a `go.work`
workspace. Each service builds and deploys independently — the same way three
separate repos would — while shared code lives in one place.

```text
   ┌────────────────────────────┐        ┌────────────────────────────┐
   │     teamchat/api-server     │        │      teamchat/ws-worker     │
   │     handler/http · service  │        │         handler/ws          │
   └──────────────┬─────────────┘        └──────────────┬─────────────┘
                  │ imports                              │ imports
                  └──────────────────┬───────────────────┘
                                     ▼
                  ┌──────────────────────────────────────────┐
                  │              teamchat/shared               │
                  │  domain · config · repository · pubsub ·   │
                  │              snowflake                     │
                  └──────────────────────────────────────────┘
        (dependencies point one way only — shared imports no service)
```

| Module | Binary | Scales on | State |
|---|---|---|---|
| `teamchat/api-server` | HTTP ingress `:8080` | request throughput (CPU) | **stateless** |
| `teamchat/ws-worker` | WebSocket fleet `:8081` | concurrent connections (memory) | **stateful** |
| `teamchat/shared` | _library, no binary_ | — | — |

**Why split this way?** A stateless HTTP server and a stateful socket server have
opposite scaling and deployment characteristics (see
[`02_hld_component_specifications.md`](./02_hld_component_specifications.md)).
Keeping them as independent units lets us autoscale, deploy, and fail them
independently without one dragging the other down.

---

## 3. Channels & DMs — One Abstraction

TeamChat has **no single global room**. Everything a user talks in is a
**channel** (stored in the `conversations` table):

- A **GROUP** channel (e.g. `#engineering`) has any number of members.
- A **DM** is just a channel of type `DM` with exactly two members.

Membership is a many-to-many relationship in `channel_members`, so:

- one user belongs to **many** channels, and
- one channel has **many** members.

A single WebSocket connection (keyed by `user_id`) subscribes to **all** of that
user's channels at once — the same multiplexed experience as Slack or Teams.
See the data model in [`04_data_model.md`](./04_data_model.md).
