# Data Flows & Failure Handling

This document traces data through the system for each critical operation. Flows
marked ✅ match the current code; flows marked 🟡 are the designed evolution.

---

## Flow 1 — Send & Real-Time Delivery ✅

How a message gets from Alice to every online member of `#engineering`.

```text
  Alice          api-server          Redis           MySQL          ws-worker        Bob
    │                 │                 │               │                │            │
    │ POST /messages  │                 │               │                │            │
    │ (Idempotency)   │                 │               │                │            │
    ├────────────────▶│                 │               │                │            │
    │                 │ SET NX key (30s)│               │                │            │
    │                 ├────────────────▶│               │                │            │
    │                 │                 │               │                │            │
    │   ┌─ if key already exists ──────────────────────────────────────────────────┐ │
    │   │ 409 Conflict │                 │               │                │          │ │
    │◀──┼──────────────┤                 │               │                │          │ │
    │   └──────────────────────────────────────────────────────────────────────────┘ │
    │                 │                 │               │                │            │
    │   ┌─ else (first time) ───────────────────────────────────────────────────────┐│
    │   │             │ IsMember(channel, alice)?       │                │           ││
    │   │             ├────────────────────────────────▶│                │           ││
    │   │   ┌─ not a member ─┐                           │                │           ││
    │   │   │ 403 Forbidden  │                           │                │           ││
    │◀──┼───┼────────────────┤                           │                │           ││
    │   │   └────────────────┘                           │                │           ││
    │   │   ┌─ member ───────────────────────────────────────────────────────────┐   ││
    │   │   │ assign Snowflake id → INSERT message       │                │       │   ││
    │   │   │         ├────────────────────────────────▶ │                │       │   ││
    │   │   │ PUBLISH chat:room:{channel}                │                │       │   ││
    │   │   │         ├───────────────▶│                  │                │       │   ││
    │ 202 Accepted (message JSON)      │                  │                │       │   ││
    │◀──┼───┼─────────┤                │                  │                │       │   ││
    │   │   │         │   deliver on chat:room:{channel}  │                │       │   ││
    │   │   │         │                ├─────────────────────────────────▶│       │   ││
    │   │   │         │      (drop if sender == receiver: echo suppression)│       │   ││
    │   │   │         │                │                  │   push JSON frame      │   ││
    │   │   │         │                │                  │                ├──────▶│   ││
    │   │   └─────────────────────────────────────────────────────────────────────┘   ││
    │   └───────────────────────────────────────────────────────────────────────────┘│
```

**Current fan-out model:** **room broadcast.** The message is published once to
`chat:room:{channelID}`; every `ws-worker` subscribed on behalf of an online
member receives and forwards it. Offline members simply have no subscriber, so
nothing is pushed — they pick the message up later via Flow 2.

> **Trade-off:** room broadcast is simple and correct, but every worker
> subscribed to a channel receives every message even if its local user is the
> sender (filtered out) or idle. Flow 4 describes the point-to-point optimization.

---

## Flow 2 — "Catch-Up" Delta Sync ✅

How the system prevents data loss when a mobile user drives through a tunnel.

```text
  Bob                         api-server                     MySQL
   │                              │                            │
   │  [ TCP drops → ws-worker misses heartbeat → session reclaimed ]
   │  [ Alice sends 5 messages while Bob is offline → stored in MySQL ]
   │                              │                            │
   │ POST /sync/deltas            │                            │
   │   { "engineering": 5000 }    │                            │
   ├─────────────────────────────▶│                            │
   │                              │ SELECT * WHERE              │
   │                              │ conversation_id='eng'       │
   │                              │ AND id > 5000 ORDER BY id   │
   │                              │ LIMIT 100                   │
   │                              ├────────────────────────────▶│
   │                              │        [msg 5001 … 5005]    │
   │                              │◀────────────────────────────┤
   │ { "engineering": [5 msgs] }  │                            │
   │◀─────────────────────────────┤                            │
   │                              │                            │
   │  [ UI reconciles, then opens a fresh WebSocket ]           │
```

1. **Disconnect** — Bob's socket drops; the worker reclaims the session.
2. **Offline window** — Alice's messages are persisted in MySQL regardless.
3. **Reconnect** — Bob's app sends the highest message ID it holds *per channel*.
4. **Indexed read** — the Sync Service range-scans `(conversation_id, id)`.
5. **Reconcile** — only channels with newer messages appear in the response;
    Bob replays them, then re-establishes the live WebSocket.

This is the **correctness backbone**: real-time delivery is best-effort, but
nothing is ever lost because MySQL + delta-sync is authoritative.

---

## Flow 3 — Multi-Channel WebSocket Session ✅

A single connection serves *all* of a user's channels.

```text
  Bob                     ws-worker                    MySQL            Redis
   │                          │                           │               │
   │ GET /ws?user_id=bob      │                           │               │
   │ (HTTP Upgrade)           │                           │               │
   ├─────────────────────────▶│                           │               │
   │                          │ GetChannelIDsByUser(bob)  │               │
   │                          ├──────────────────────────▶│               │
   │                          │  [eng, random, dm:alice]  │               │
   │                          │◀──────────────────────────┤               │
   │                          │ register in Hub,          │               │
   │                          │ start readPump+writePump  │               │
   │                          │                           │               │
   │                          │ SUBSCRIBE (one goroutine per channel):     │
   │                          │   chat:room:{eng}         │               │
   │                          │   chat:room:{random}      │               │
   │                          │   chat:room:{dm:alice}    │               │
   │                          ├──────────────────────────────────────────▶│
   │                          │  any msg on any subscribed channel         │
   │                          │◀──────────────────────────────────────────┤
   │ forwarded on single      │                           │               │
   │ socket (+conversation_id)│                           │               │
   │◀─────────────────────────┤                           │               │
   │                          │                           │               │
   │  [ on disconnect: readPump cancels shared ctx →                       │
   │    all subscriber goroutines exit, Hub unregisters ]                  │
```

**Key point:** the client connects **once** with `?user_id=`. The worker derives
the channel set from the DB at connect time. Channel membership changes
(join/leave) take effect on the next reconnect — an accepted MVP trade-off.

---

## Flow 4 — Presence-Based Point-to-Point Dispatch 🟡 (Roadmap)

The senior-level optimization that replaces room broadcast at scale.

1. **Presence write:** on connect, `ws-worker` records `presence:{userID} →
   {nodeID}` in Redis (with TTL refreshed by heartbeat).
2. **Targeted publish:** instead of `chat:room:{channel}`, `api-server` looks up
   which members are online and on which node, then publishes only to
   `node:{nodeID}` queues for nodes that actually hold a recipient.
3. **Benefit:** a worker no longer receives messages for channels whose local
   users are all the sender or offline — cutting cross-node chatter dramatically
   for large channels with few online members.
4. **Cost:** more moving parts (presence accuracy, TTL tuning, node-failure
   handling). Justified only past the scale where broadcast bandwidth hurts.

---

## Flow 5 — Application-Level ACKs 🟡 (Roadmap)

TCP guarantees delivery to the OS, not that the app rendered the message before
crashing. The `domain.AckFrame` type already defines the wire shape.

1. **In-flight tracking:** before pushing, the worker records
   `unacked:{user}:{msgID}` in Redis with a short TTL.
2. **Client ACK:** the client renders the bubble and sends `ACK {message_id}`
   over the socket.
3. **Resolve:** the worker deletes the `unacked` key.
4. **Failure:** if the TTL expires with no ACK, the message is treated as
   undelivered and will reappear in the next delta-sync (Flow 2).

---

## Flow 6 — Channel & DM Lifecycle ✅

```text
  Client                      api-server                       MySQL
    │                             │                               │
    │ POST /users {username}      │                               │
    ├────────────────────────────▶│ INSERT user (Snowflake id)    │
    │                             ├──────────────────────────────▶│
    │ 201 {id, username}          │                               │
    │◀────────────────────────────┤                               │
    │                             │                               │
    │ POST /channels              │                               │
    │  {name, type:GROUP,         │ validate:                     │
    │   created_by, member_ids}   │  DM ⇒ exactly 2 members       │
    ├────────────────────────────▶│  creator auto-added           │
    │                             │ INSERT conversation +         │
    │                             │ channel_members rows          │
    │                             ├──────────────────────────────▶│
    │ 201 {channel}               │                               │
    │◀────────────────────────────┤                               │
    │                             │                               │
    │ GET /channels?user_id=...   │ JOIN conversations            │
    ├────────────────────────────▶│   ⨝ channel_members           │
    │                             ├──────────────────────────────▶│
    │ 200 [channels...]           │                               │
    │◀────────────────────────────┤                               │
```

- A **DM** is created exactly like a GROUP but with `type: "DM"` and exactly two
  `member_ids` — there is no separate code path, keeping the model uniform.
- The **creator is always enrolled** as a member (otherwise they couldn't post).
- See [`05_api_reference.md`](./05_api_reference.md) for full request/response
  shapes.
