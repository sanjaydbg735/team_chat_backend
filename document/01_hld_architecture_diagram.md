# High-Level Architecture Diagram

This diagram maps the decoupled hybrid protocol architecture. We separate state mutations (HTTP) from real-time streaming (WebSockets) to ensure independent scalability and strict consistency.

## Visual Flowchart

```text
+----------------+                                   +----------------+
|    Client A    |                                   |    Client B    |
|    (Sender)    |                                   |   (Receiver)   |
+-------+--------+                                   +--------^-------+
        |                                                     |
        | 1. HTTP POST (Idempotency Key)                      | 6. Push JSON Frame
        v                                                     |    (over TCP)
+-------+-----------------------------------------------------+-------+
|                   API Gateway / Load Balancer                       |
+-------+---------------------------------------+---------------------+
        |                                       |
        | 2. Route Message Traffic              | A. Offline Catch-up Handshake
        v                                       v    (HTTP POST)
+-------+-------------------+           +-------+-------------------+
|   Message Service (API)   |           |    Sync Service (API)     |
|   (Stateless Ingress)     |           |   (Stateless Catch-up)    |
+-------+---------+---------+           +---------------+-----------+
        |         |                                     |
 3. Save|         | 4. Publish Event                    | B. Range Query 
        v         v                                     v    (id > last_id)
+-------+--+   +--+-------------------+          +------+------+
|  MySQL   |   | Redis Pub/Sub Broker |          | MySQL Repl. |
| Primary  |   |  & Presence Store    |          | (Read Only) |
+----------+   +---------+------------+          +-------------+
                         |
                         | 5. Subscribe & Dispatch
                         v
               +---------+------------+
               | WebSocket Worker 1   |
               | (Stateful Streaming) |
               +---------+------------+
                         |
                         +-------------------------------------> (To Client B)