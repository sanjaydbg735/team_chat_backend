// Command ws-worker is the stateful WebSocket streaming service for TeamChat.
//
// Each client connects with their user_id.  The worker:
//  1. Looks up every channel the user belongs to from MySQL.
//  2. Subscribes to the corresponding Redis Pub/Sub rooms.
//  3. Multiplexes messages from all channels onto a single WebSocket connection.
//
// Because WebSocket connections are stateful (each worker holds TCP state for
// its connected clients), this binary scales differently from the HTTP API
// server: scale based on concurrent user count (memory / connections), not RPS.
//
// Graceful shutdown (SIGTERM) closes all open WebSocket connections so clients
// reconnect to another node instead of experiencing a hard TCP RST.
package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"

	"teamchat/shared/config"
	"teamchat/shared/repository"
	"teamchat/ws-worker/internal/handler/ws"
)

func main() {
	// ── Configuration ────────────────────────────────────────────────────────
	cfg := config.Load()

	// ── Infrastructure ───────────────────────────────────────────────────────

	// MySQL is used once per connection to load the user's channel list.
	db, err := repository.Connect(cfg.MySQLDSN)
	if err != nil {
		log.Fatalf("[WS] MySQL connect error: %v", err)
	}
	defer db.Close()

	// Redis is used for Pub/Sub — receiving messages published by the HTTP servers.
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})

	// ── Hub ──────────────────────────────────────────────────────────────────
	// The Hub tracks every active WebSocket client on this worker node.
	// It is used for lifecycle management (graceful shutdown) and monitoring.
	hub := ws.NewHub()

	// ── Routes ───────────────────────────────────────────────────────────────
	mux := http.NewServeMux()

	// /ws?user_id=<userID>
	// Each request is upgraded to a WebSocket connection.
	// conversation_id is NOT required — a single connection covers all channels.
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ws.ServeWS(hub, rdb, db, w, r)
	})

	// ── Server ───────────────────────────────────────────────────────────────
	srv := &http.Server{Addr: cfg.WSPort, Handler: mux}

	// Graceful shutdown: give all connected clients a clean close frame
	// before the process exits so they can immediately reconnect to another node.
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Printf("[WS] Shutdown signal — closing %d connection(s)...", hub.ActiveCount())
		hub.Shutdown()
		srv.Close()
	}()

	log.Printf("[WS] Stateful WebSocket worker listening on %s", cfg.WSPort)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[WS] Server error: %v", err)
	}
	log.Println("[WS] Server stopped.")
}
