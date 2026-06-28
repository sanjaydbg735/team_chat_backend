package ws

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"teamchat/shared/domain"
	"teamchat/shared/repository"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

// upgrader performs the HTTP → WebSocket protocol upgrade.
// CheckOrigin is intentionally permissive for local development; in production
// replace this with an allowlist of trusted origins.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// ServeWS is the HTTP handler that upgrades an incoming request to a WebSocket
// connection and bootstraps the full client session.
//
// Connection lifecycle:
//  1. Upgrade HTTP → WebSocket.
//  2. Look up all channels the user belongs to from MySQL.
//  3. Register the client with the Hub (in-process registry).
//  4. Spawn readPump  — keeps connection alive via Ping/Pong.
//  5. Spawn writePump — drains outbound messages to the socket.
//  6. Spawn one listenToRedis goroutine per channel the user belongs to.
//     Each goroutine subscribes to "chat:room:{channelID}" and forwards
//     messages into the shared Client.Send buffer.
//
// All goroutines share a context.Context.  When the client disconnects,
// readPump cancels the context which causes all listenToRedis goroutines
// to exit, and the writePump exits when its next write fails on the
// now-closed connection.
//
// Query param:  ?user_id=<userID>   (required)
//
// Note: conversation_id is no longer needed in the query string.  A single
// connection now covers ALL channels the user belongs to.
func ServeWS(hub *Hub, rdb *redis.Client, db *sql.DB, w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		http.Error(w, "Missing required query param: user_id", http.StatusBadRequest)
		return
	}

	// Upgrade the HTTP connection to WebSocket.
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WS] Upgrade failed for user=%s: %v", userID, err)
		return
	}

	// A cancellable context ties the lifetime of all goroutines spawned for
	// this connection.  Cancelling it is the shutdown signal.
	ctx, cancel := context.WithCancel(context.Background())

	client := &Client{
		UserID: userID,
		Conn:   conn,
		Send:   make(chan domain.Message, sendBuffer),
	}

	// Fetch the IDs of every channel this user belongs to.
	// The WebSocket worker needs DB access only here — to build the initial
	// subscription list.  All subsequent channel membership changes require
	// the client to reconnect (acceptable MVP trade-off).
	chanRepo := repository.NewChannelRepository(db)
	channelIDs, err := chanRepo.GetChannelIDsByUser(ctx, userID)
	if err != nil {
		log.Printf("[WS] Failed to fetch channels for user=%s: %v", userID, err)
		conn.Close()
		cancel()
		return
	}

	// Register the client so the Hub can manage its lifecycle.
	hub.Register(client)

	// readPump handles keep-alive and triggers shutdown on disconnect.
	// It receives the hub so it can unregister the client on exit.
	go client.readPump(cancel, hub)

	// writePump serialises outbound messages to the WebSocket.
	go client.writePump(cancel)

	// One Redis subscriber goroutine per channel — all funnel into Send.
	for _, channelID := range channelIDs {
		go client.listenToRedis(ctx, channelID, rdb)
	}

	log.Printf("[WS] user=%s connected — subscribed to %d channel(s)", userID, len(channelIDs))
}
