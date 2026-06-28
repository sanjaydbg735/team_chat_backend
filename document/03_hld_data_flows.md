# Advanced Data Flows & Failure Handling

This document traces the exact path of data through the architecture during critical operations, highlighting how the system behaves under edge-case conditions.

## Flow A: Optimized Point-to-Point Message Delivery
*The standard broadcast model (sending a message to a whole room) wastes CPU if most users are offline. Here is the optimized senior-level flow:*

1. **Ingress:** User Alice POSTs a message payload to the API Gateway.
2. **Commitment:** The Ingress API assigns a Snowflake ID and writes it to the MySQL Primary node.
3. **Presence Lookup (The Optimization):** Instead of blasting the message to a global room channel, the Ingress API queries the Redis Presence Store to find all members of `engineering-team` who are currently online.
4. **Targeted Dispatch:** Redis returns that User Bob is connected to `worker-node-04` and User Charlie is connected to `worker-node-12`.
5. **Private Routing:** The Ingress API publishes the message payload directly to `queue:worker-node-04` and `queue:worker-node-12`.
6. **Delivery:** The specific WebSocket workers pull the message from their private queues and push it over the TCP socket to Bob and Charlie.

## Flow B: The "Catch-Up" Synchronization Protocol
*How the system prevents data loss when a mobile user drives through a tunnel.*

1. **Disconnection:** Bob's TCP socket drops. The WebSocket server misses a heartbeat, clears his session, and removes his presence from Redis.
2. **Offline Period:** Alice sends 5 messages. The Ingress API sees Bob is offline in Redis, so it skips the real-time dispatch for him. The messages are safely stored in MySQL.
3. **Reconnection:** Bob exits the tunnel. His client app wakes up.
4. **Sync Handshake:** Before opening a new WebSocket, Bob's app makes a fast HTTP POST to `/api/v1/sync/deltas`. It passes the ID of the very last message Bob saw (e.g., `id: 5000`).
5. **Replica Read:** The Sync Service queries a MySQL Read-Replica: `SELECT * FROM messages WHERE conversation_id = 'engineering-team' AND id > 5000`.
6. **Reconciliation:** The 5 missed messages are returned. Bob's UI updates seamlessly, and he re-establishes a live WebSocket connection to listen for new events.

## Flow C: Application-Level Acknowledgements (ACKs)
*TCP guarantees packet delivery to the OS, but not that the app didn't crash before rendering it. This flow ensures strict consistency.*

1. **In-Flight Tracking:** Right before a WebSocket worker pushes a message to Alice, it logs the `message_id` in a local Redis cache under `unacked:alice:msg_id` with a 5-second expiration.
2. **Client Render:** Alice's phone receives the JSON, renders the chat bubble, and fires back an `ACK {message_id}` frame over the WebSocket.
3. **Resolution:** The WebSocket worker receives the ACK and deletes the `unacked` tracker from Redis.
4. **Failure State:** If Alice's app crashes and the 5-second timer expires without an ACK, the system flags the message as "undelivered". When Alice restarts her app and triggers the Catch-Up Protocol (Flow B), that specific message is included in her sync payload.