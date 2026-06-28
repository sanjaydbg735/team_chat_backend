package ws

import (
	"log"
	"sync"
)

// Hub is an in-process registry of all WebSocket clients currently connected
// to this worker node.
//
// Responsibilities:
//  1. Track active connections for monitoring (ActiveCount).
//  2. Enable graceful shutdown — closing all open connections before the
//     process exits so clients reconnect cleanly rather than hit an abrupt TCP RST.
//
// One Hub is shared across all connections on a single WS worker process.
// It is safe for concurrent use.
//
// Note: the Hub does NOT route messages between clients.  Message fan-out is
// handled by Redis Pub/Sub, which decouples the HTTP ingress servers from the
// WebSocket workers entirely.  The Hub is purely a lifecycle management tool.
type Hub struct {
	mu      sync.RWMutex       // guards clients map
	clients map[string]*Client // userID → active Client (one connection per user)
}

// NewHub creates an empty Hub.  Call this once in main() and pass it to ServeWS.
func NewHub() *Hub {
	return &Hub{
		clients: make(map[string]*Client),
	}
}

// Register adds a client to the Hub.
// If a user reconnects before their previous session has fully torn down,
// the old Client entry is overwritten by the new one.
func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c.UserID] = c
	log.Printf("[Hub] registered user=%s  active=%d", c.UserID, len(h.clients))
}

// Unregister removes a client from the Hub when their connection closes.
// It is safe to call Unregister for a user that is no longer in the map
// (e.g. if Register was called again before Unregister ran for the old session).
func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Only remove if the map still points to this exact Client instance —
	// not a newer reconnection.
	if existing, ok := h.clients[c.UserID]; ok && existing == c {
		delete(h.clients, c.UserID)
	}
	log.Printf("[Hub] unregistered user=%s  active=%d", c.UserID, len(h.clients))
}

// ActiveCount returns the number of currently connected clients.
// Useful for metrics and health-check endpoints.
func (h *Hub) ActiveCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Shutdown gracefully closes every open WebSocket connection.
// Call this from a signal handler (SIGTERM / SIGINT) to drain connections
// before the process exits, giving clients time to reconnect to another node.
func (h *Hub) Shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()
	log.Printf("[Hub] shutdown: closing %d connection(s)", len(h.clients))
	for userID, c := range h.clients {
		c.Conn.Close()
		delete(h.clients, userID)
	}
}
