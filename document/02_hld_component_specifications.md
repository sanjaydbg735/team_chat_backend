# Architectural Component Deep Dive & Trade-offs

In senior system design interviews, the "why" is more important than the "what." This document breaks down the core components of the TeamChat backend and the engineering trade-offs made for each.

## 1. Layer 7 API Gateway & Load Balancer
* **Technology:** NGINX, HAProxy, or AWS ALB.
* **Responsibilities:** * **SSL Termination:** Decrypts HTTPS/WSS traffic at the edge so internal servers don't waste CPU cycles on cryptography.
  * **Intelligent Routing:** Inspects the request protocol. Routes standard HTTP POST traffic to the Ingress APIs, and routes HTTP `Connection: Upgrade` requests to the WebSocket fleet.
  * **Rate Limiting:** Protects the database from noisy neighbors or DDoS attacks by throttling requests per user ID.

## 2. Stateless HTTP Ingress Fleet (Message Service)
* **Design Pattern:** Stateless Microservice.
* **Responsibilities:**
  * **Idempotency Guard:** Checks Redis to ensure the client's `X-Idempotency-Key` hasn't been processed recently. This solves the "Double Send" problem caused by flaky mobile networks.
  * **Sequence Generation:** Generates a distributed Twitter Snowflake ID (a 64-bit integer containing a timestamp and worker ID). *Trade-off:* We use Snowflake instead of MySQL Auto-Increment to prevent database bottlenecks and ensure IDs are perfectly chronologically sortable across distributed nodes.
* **Scaling Strategy:** CPU-bound. Auto-scales horizontally (e.g., via Kubernetes HPA) based on request throughput.

## 3. Stateful WebSocket Fleet (Streaming Service)
* **Design Pattern:** Long-lived TCP stateful workers.
* **Responsibilities:**
  * Maintains open `ws://` pipes to active clients.
  * **Memory Management:** Listens for `Ping/Pong` heartbeats. If a client drops offline without sending a TCP close frame (a "zombie connection"), the server detects the missed heartbeat and aggressively frees the memory.
* **Scaling Strategy:** Memory/Connection-bound. Scales based on the number of concurrent users. *Trade-off:* Because these hold state, we must use careful connection draining during deployments so we don't drop 100,000 users simultaneously.

## 4. The Event Backplane (Redis Cluster)
* **Design Pattern:** Pub/Sub & Key-Value Presence Store.
* **Responsibilities:**
  * **Decoupling:** Prevents the HTTP servers from needing to know about WebSocket servers. 
  * **Presence Matrix:** Tracks `User_ID -> Worker_Node_ID`.
* **Trade-off (Redis vs. Kafka):** Kafka is excellent for persistent, replayable event streaming. However, for a chat app where MySQL already persists the data and offline users fetch missed messages via a REST API, Kafka's disk-based persistence adds unnecessary latency. Redis Pub/Sub is purely in-memory, making it significantly faster for pure real-time fan-out.

## 5. Persistent Data Store (MySQL Primary/Replica)
* **Design Pattern:** Relational Database with Read-Replicas.
* **Schema Optimization:** The `messages` table uses a Composite B-Tree Index on `(conversation_id, id)`. 
* **Why this matters:** When an offline user comes back online, they request messages for a specific room *after* a specific ID. Without this composite index, the database would perform a slow full-table scan. With it, the query executes in sub-milliseconds, even with billions of rows.