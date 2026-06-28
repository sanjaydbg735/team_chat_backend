# Testing Guide — Postman & Local Terminal

A hands-on walkthrough to exercise every feature end to end: HTTP endpoints with
**Postman** and **curl**, plus the real-time **WebSocket** stream. Examples use
real request/response shapes.

> **Ports.** Defaults are `:8080` (HTTP API) and `:8081` (WebSocket). If `8080`
> is already in use on your machine, start the API on another port with
> `API_PORT=:8090` (and `WS_PORT=:8091`) and substitute accordingly below.

---

## 0. Prerequisites

| Tool | Why | Check |
|---|---|---|
| Go 1.19+ | build/run the services | `go version` |
| Docker + Compose | local MySQL + Redis | `docker compose version` |
| Postman | GUI API + WebSocket testing | — |
| `curl` | terminal API testing | `curl --version` |
| `wscat` (optional) | terminal WebSocket testing | `npm i -g wscat` |

---

## 1. Start infrastructure (MySQL + Redis)

```bash
docker compose -f deployments/docker-compose.yml up -d

# wait until MySQL reports healthy
docker inspect --format '{{.State.Health.Status}}' teamchat_mysql
docker exec teamchat_redis redis-cli ping       # → PONG
```

## 2. Apply database migrations

No local MySQL client needed — pipe the SQL through the container:

```bash
docker exec -i teamchat_mysql mysql -uroot -prootpassword teamchat \
  < scripts/migrations/000001_init.sql
docker exec -i teamchat_mysql mysql -uroot -prootpassword teamchat \
  < scripts/migrations/000002_multi_channel.sql

# verify
docker exec -i teamchat_mysql mysql -uroot -prootpassword teamchat -e "SHOW TABLES;"
# → channel_members, conversations, messages, users
```

## 3. Run the services (two terminals)

```bash
# terminal 1 — HTTP API
go run teamchat/api-server
# → [API] Stateless HTTP gateway listening on :8080

# terminal 2 — WebSocket worker
go run teamchat/ws-worker
# → [WS] Stateful WebSocket worker listening on :8081
```

> Using busy ports? Run instead:
> `API_PORT=:8090 WS_PORT=:8091 go run teamchat/api-server` and
> `API_PORT=:8090 WS_PORT=:8091 go run teamchat/ws-worker`.

---

## 4. Postman setup

### 4.1 Create an environment
Add a Postman **Environment** named `TeamChat Local` with these variables:

| Variable | Initial value |
|---|---|
| `base_url` | `http://localhost:8080` |
| `ws_url` | `ws://localhost:8081` |
| `alice_id` | _(left blank — auto-filled)_ |
| `bob_id` | _(blank)_ |
| `channel_id` | _(blank)_ |
| `last_msg_id` | `0` |

### 4.2 Auto-capture IDs with Postman test scripts
Manually copying IDs is tedious. Paste these into the **Tests** tab of the
relevant request so Postman saves the ID into the environment automatically.

- **Create user "alice"** → Tests tab:
  ```javascript
  pm.environment.set("alice_id", pm.response.json().id);
  ```
- **Create user "bob"** → Tests tab:
  ```javascript
  pm.environment.set("bob_id", pm.response.json().id);
  ```
- **Create channel** → Tests tab:
  ```javascript
  pm.environment.set("channel_id", pm.response.json().id);
  ```
- **Send message** → Tests tab (to drive delta-sync later):
  ```javascript
  pm.environment.set("last_msg_id", pm.response.json().id);
  ```

Now reference them in later requests as `{{alice_id}}`, `{{channel_id}}`, etc.

---

## 5. The requests (Postman + curl side by side)

### 5.1 Create users

**Postman:** `POST {{base_url}}/api/v1/users` · Body → raw → JSON
```json
{ "username": "alice" }
```
**curl:**
```bash
curl -X POST http://localhost:8080/api/v1/users \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice"}'
```
**201 Created**
```json
{ "id": "329690997346799616", "username": "alice", "created_at": "2026-06-29T00:04:19Z" }
```
Repeat for `bob`. Re-posting an existing username returns **409 Conflict**.

> Usernames are globally unique. If `alice` already exists (e.g. from automated
> tests), use `alice1`, `alice_<timestamp>`, etc.

---

### 5.2 Get a user
**Postman:** `GET {{base_url}}/api/v1/users/{{bob_id}}`
```bash
curl http://localhost:8080/api/v1/users/<bob_id>
```
**200 OK** → the user object · **404** if the ID doesn't exist.

---

### 5.3 Create a channel (GROUP)
**Postman:** `POST {{base_url}}/api/v1/channels`
```json
{
  "name": "engineering",
  "type": "GROUP",
  "created_by": "{{alice_id}}",
  "member_ids": ["{{bob_id}}"]
}
```
**curl:**
```bash
curl -X POST http://localhost:8080/api/v1/channels \
  -H 'Content-Type: application/json' \
  -d '{"name":"engineering","type":"GROUP","created_by":"<alice_id>","member_ids":["<bob_id>"]}'
```
**201 Created**
```json
{ "id":"329690997720092672", "name":"engineering", "type":"GROUP",
  "created_by":"329690997346799616", "created_at":"2026-06-29T00:04:19Z" }
```
The creator (`alice`) is auto-added as a member.

#### Create a DM (exactly two members)
```json
{ "type": "DM", "created_by": "{{alice_id}}", "member_ids": ["{{alice_id}}","{{bob_id}}"] }
```
A DM with ≠ 2 members returns **400 Bad Request**.

---

### 5.4 List a user's channels
**Postman:** `GET {{base_url}}/api/v1/channels?user_id={{bob_id}}`
```bash
curl 'http://localhost:8080/api/v1/channels?user_id=<bob_id>'
```
**200 OK** → array of the channels that user belongs to.

---

### 5.5 Add / remove members
```bash
# add charlie
curl -X POST http://localhost:8080/api/v1/channels/<channel_id>/members \
  -H 'Content-Type: application/json' -d '{"user_id":"<charlie_id>"}'   # → 204

# remove charlie
curl -X DELETE http://localhost:8080/api/v1/channels/<channel_id>/members/<charlie_id>  # → 204
```

---

### 5.6 Send a message
**Postman:** `POST {{base_url}}/api/v1/messages`
Headers: `Content-Type: application/json`, `X-Idempotency-Key: {{$guid}}`
(`{{$guid}}` is a Postman dynamic variable — a fresh UUID per send.)
```json
{ "conversation_id": "{{channel_id}}", "sender_id": "{{alice_id}}", "content": "hello team" }
```
**curl:**
```bash
curl -X POST http://localhost:8080/api/v1/messages \
  -H 'Content-Type: application/json' \
  -H "X-Idempotency-Key: $(uuidgen)" \
  -d '{"conversation_id":"<channel_id>","sender_id":"<alice_id>","content":"hello team"}'
```
**202 Accepted**
```json
{ "id":329690998177271808, "conversation_id":"...", "sender_id":"...",
  "content":"hello team", "created_at":"2026-06-29T00:04:19.328Z" }
```

**Negative cases to try:**
| Action | Expected |
|---|---|
| Re-send with the **same** `X-Idempotency-Key` | `409 Conflict` |
| `sender_id` not a channel member | `403 Forbidden` |
| Missing `content` / `sender_id` / `conversation_id` | `400 Bad Request` |

---

### 5.7 Delta sync (offline catch-up)
Send the highest message ID you've seen **per channel** (use `0` for "everything").
**Postman:** `POST {{base_url}}/api/v1/sync/deltas`
```json
{ "{{channel_id}}": 0 }
```
**curl:**
```bash
curl -X POST http://localhost:8080/api/v1/sync/deltas \
  -H 'Content-Type: application/json' \
  -d '{"<channel_id>": 0}'
```
**200 OK** — only channels with newer messages appear (max 100 each):
```json
{ "<channel_id>": [ { "id":329690998026276864, "content":"hello team", "...":"..." } ] }
```

---

## 6. Real-time WebSocket testing

A single connection per user streams **all** of that user's channels.

### 6.1 With Postman
1. **New → WebSocket Request.**
2. URL: `{{ws_url}}/ws?user_id={{bob_id}}` → **Connect**.
3. In another tab (or via curl), **send a message** to a channel bob belongs to
   (§5.6) as a *different* user (e.g. alice).
4. The message appears live in Postman's **Messages** panel, e.g.:
   ```json
   {"id":329691527301304320,"conversation_id":"...","sender_id":"<alice_id>","content":"hello bob in real time","created_at":"..."}
   ```
5. **Echo suppression:** if you connect as `alice` and alice sends, alice does
   **not** receive her own message back.

### 6.2 With wscat (terminal)
```bash
# terminal 3 — listen as bob
wscat -c 'ws://localhost:8081/ws?user_id=<bob_id>'

# terminal 4 — alice sends; watch it arrive in terminal 3
curl -X POST http://localhost:8080/api/v1/messages \
  -H 'Content-Type: application/json' -H "X-Idempotency-Key: $(uuidgen)" \
  -d '{"conversation_id":"<channel_id>","sender_id":"<alice_id>","content":"live!"}'
```

> Connect bob **before** alice sends — real-time delivery is for online users;
> anything sent while offline is retrieved later via delta-sync (§5.7).

---

## 7. Full end-to-end scenario (copy-paste, terminal)

A self-contained script (uses unique usernames so it's re-runnable):

```bash
API=http://localhost:8080; SUF=$(date +%s)

ALICE=$(curl -s -X POST $API/api/v1/users -H 'Content-Type: application/json' -d "{\"username\":\"alice_$SUF\"}")
ALICE_ID=$(echo "$ALICE" | python3 -c "import sys,json;print(json.load(sys.stdin)['id'])")

BOB=$(curl -s -X POST $API/api/v1/users -H 'Content-Type: application/json' -d "{\"username\":\"bob_$SUF\"}")
BOB_ID=$(echo "$BOB" | python3 -c "import sys,json;print(json.load(sys.stdin)['id'])")

CH=$(curl -s -X POST $API/api/v1/channels -H 'Content-Type: application/json' \
  -d "{\"name\":\"engineering\",\"type\":\"GROUP\",\"created_by\":\"$ALICE_ID\",\"member_ids\":[\"$BOB_ID\"]}")
CH_ID=$(echo "$CH" | python3 -c "import sys,json;print(json.load(sys.stdin)['id'])")

echo "alice=$ALICE_ID bob=$BOB_ID channel=$CH_ID"

# send → 202
curl -s -o /dev/null -w "send: %{http_code}\n" -X POST $API/api/v1/messages \
  -H 'Content-Type: application/json' -H "X-Idempotency-Key: e2e-$SUF" \
  -d "{\"conversation_id\":\"$CH_ID\",\"sender_id\":\"$ALICE_ID\",\"content\":\"hi\"}"

# delta sync → see the message
curl -s -X POST $API/api/v1/sync/deltas -H 'Content-Type: application/json' -d "{\"$CH_ID\":0}"; echo
```

---

## 8. Run the automated test suite

```bash
# requires the Docker MySQL + Redis from step 1 to be running
cd services/api-server && go test ./... -v
```
Expected:
```
PASS  Test_HandleSendMessage_Success
PASS  Test_HandleSendMessage_IdempotencyConflict
PASS  Test_HandleSendMessage_NonMemberForbidden
PASS  Test_HandleDeltaSync_Success
```

> The suite seeds a user named `alice`; if you also created `alice` manually
> you'll see a `409` in manual runs — use a different username.

---

## 9. Teardown

```bash
# stop the two services with Ctrl-C in their terminals, then:
docker compose -f deployments/docker-compose.yml down       # keep data volumes
docker compose -f deployments/docker-compose.yml down -v     # also wipe MySQL/Redis data
```

---

## 10. Troubleshooting

| Symptom | Cause / Fix |
|---|---|
| `bind: address already in use` | Port taken — set `API_PORT` / `WS_PORT` env vars. |
| `403 Forbidden` on send | `sender_id` isn't a member of `conversation_id` — add them (§5.5) or check IDs. |
| `409` on every send | You're reusing the same `X-Idempotency-Key`; use a fresh UUID per message. |
| WebSocket connects but no messages | You connected *after* the message was sent, or the sender is the same user (echo suppressed), or sender isn't in the channel. |
| `dial tcp ... :3307: connect: connection refused` | MySQL container not up/healthy yet — re-check step 1. |
| Migrations "table already exists" | Harmless on re-run (`IF NOT EXISTS`); ignore. |
