# API Reference

Two listeners:

- **HTTP API** — `api-server` on `:8080`
- **WebSocket** — `ws-worker` on `:8081`

All request/response bodies are JSON. IDs are strings (users, channels) or
numbers (`message.id`, a Snowflake).

---

## Users

### `POST /api/v1/users` — register a user
```bash
curl -X POST localhost:8080/api/v1/users \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice"}'
```
**201 Created**
```json
{ "id": "153928...", "username": "alice", "created_at": "2026-06-28T18:00:00Z" }
```
| Code | When |
|---|---|
| `400` | missing/blank `username` |
| `409` | username already taken |

### `GET /api/v1/users/{id}` — fetch a user
```bash
curl localhost:8080/api/v1/users/153928...
```
**200 OK** → `User` · **404** if not found.

---

## Channels

### `POST /api/v1/channels` — create a GROUP or DM
```bash
# GROUP
curl -X POST localhost:8080/api/v1/channels \
  -H 'Content-Type: application/json' \
  -d '{"name":"engineering","type":"GROUP","created_by":"<alice_id>","member_ids":["<bob_id>"]}'

# DM (exactly two members)
curl -X POST localhost:8080/api/v1/channels \
  -H 'Content-Type: application/json' \
  -d '{"type":"DM","created_by":"<alice_id>","member_ids":["<alice_id>","<bob_id>"]}'
```
**201 Created**
```json
{ "id":"...", "name":"engineering", "type":"GROUP", "created_by":"<alice_id>", "created_at":"..." }
```
Rules enforced:
- `type` defaults to `GROUP`; must be `GROUP` or `DM`.
- `DM` requires exactly **2** `member_ids`.
- `created_by` is **always** added to the member set.

| Code | When |
|---|---|
| `400` | missing `created_by`, bad `type`, or DM without 2 members |

### `GET /api/v1/channels?user_id={id}` — list a user's channels
```bash
curl 'localhost:8080/api/v1/channels?user_id=<alice_id>'
```
**200 OK** → `[Channel, ...]` (empty array if none).

### `GET /api/v1/channels/{id}` — channel info
**200 OK** → `Channel` · **404** if not found.

### `POST /api/v1/channels/{id}/members` — add a member
```bash
curl -X POST localhost:8080/api/v1/channels/<chan_id>/members \
  -H 'Content-Type: application/json' -d '{"user_id":"<charlie_id>"}'
```
**204 No Content** · idempotent (re-adding is a no-op).

### `DELETE /api/v1/channels/{id}/members/{userID}` — remove a member
```bash
curl -X DELETE localhost:8080/api/v1/channels/<chan_id>/members/<charlie_id>
```
**204 No Content**.

---

## Messages

### `POST /api/v1/messages` — send a message
```bash
curl -X POST localhost:8080/api/v1/messages \
  -H 'Content-Type: application/json' \
  -H 'X-Idempotency-Key: 1f3c...uuid' \
  -d '{"conversation_id":"<chan_id>","sender_id":"<alice_id>","content":"hello team"}'
```
**202 Accepted**
```json
{ "id": 7283..., "conversation_id":"<chan_id>", "sender_id":"<alice_id>",
  "content":"hello team", "created_at":"..." }
```
| Code | When |
|---|---|
| `400` | missing `conversation_id` / `sender_id` / `content` |
| `403` | sender is not a member of the channel |
| `409` | duplicate `X-Idempotency-Key` (already processed) |

> `X-Idempotency-Key` is optional but recommended: clients send a UUID per
> logical message so network retries don't create duplicates (30s window).

### `POST /api/v1/sync/deltas` — offline catch-up
Send the highest message ID you hold **per channel**:
```bash
curl -X POST localhost:8080/api/v1/sync/deltas \
  -H 'Content-Type: application/json' \
  -d '{"<chan_id>": 7000, "<dm_id>": 0}'
```
**200 OK** — only channels with newer messages are returned (max 100 each):
```json
{ "<chan_id>": [ { "id":7001, "content":"...", "...":"..." } ] }
```

---

## WebSocket

### `GET /ws?user_id={id}` — real-time stream (on `:8081`)
```bash
# using websocat
websocat 'ws://localhost:8081/ws?user_id=<bob_id>'
```
- One connection per user. On connect the worker subscribes to **all** channels
  the user belongs to and multiplexes them onto this socket.
- Inbound frames are `Message` JSON objects (each carries its `conversation_id`
  so the client can route it to the right channel view).
- The server sends WebSocket **Ping** frames every ~54s; clients must Pong (most
  libraries do this automatically) or be disconnected after 60s.
- A sender does **not** receive their own message echoed back.

> Membership changes (join/leave) take effect on the **next** reconnect, since
> the subscription set is resolved once at connect time.

---

## End-to-end smoke test
```bash
# 1) create two users → capture their ids
# 2) create a GROUP channel with both as members → capture channel id
# 3) open ws for user B:   websocat 'ws://localhost:8081/ws?user_id=<B>'
# 4) user A posts a message → it appears on B's socket in real time
# 5) close B, post again, reopen B, POST /sync/deltas → B catches the missed one
```
