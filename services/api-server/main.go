// Command api-server is the stateless HTTP ingress service for TeamChat.
//
// It handles two categories of requests:
//
//  1. Message ingestion  (POST /api/v1/messages)
//     Validates membership, persists to MySQL, and broadcasts to Redis so
//     online clients receive the message in real time via the WebSocket fleet.
//
//  2. Delta sync catch-up (POST /api/v1/sync/deltas)
//     Returns messages an offline client missed, enabling eventual consistency
//     regardless of network reliability.
//
//  3. User management   (POST /api/v1/users, GET /api/v1/users/{id})
//     Registers users and resolves user profiles.
//
//  4. Channel management (/api/v1/channels/*)
//     Creates GROUP/DM channels and manages membership.
//
// This binary is intentionally stateless: it holds no in-memory session state
// and can be horizontally scaled behind a load balancer without sticky routing.
package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/redis/go-redis/v9"

	handler "teamchat/api-server/internal/handler/http"
	"teamchat/api-server/internal/service"
	"teamchat/shared/config"
	"teamchat/shared/pubsub"
	"teamchat/shared/repository"
	"teamchat/shared/snowflake"
)

func main() {
	// ── Configuration ────────────────────────────────────────────────────────
	// All values are read from environment variables with local dev defaults.
	cfg := config.Load()

	// ── Infrastructure ───────────────────────────────────────────────────────

	// MySQL connection pool — used by every repository.
	db, err := repository.Connect(cfg.MySQLDSN)
	if err != nil {
		log.Fatalf("[API] MySQL connect error: %v", err)
	}
	defer db.Close()

	// Redis client — used for idempotency locks and message broadcasting.
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})

	// ── Shared Utilities ─────────────────────────────────────────────────────

	// Snowflake generator — produces unique, time-ordered IDs for users,
	// channels, and messages.  WorkerID distinguishes this node in a cluster.
	sf := snowflake.NewGenerator(cfg.WorkerID)

	// ── Repositories ─────────────────────────────────────────────────────────
	// Repositories are the only layer that talks to the database.

	userRepo    := repository.NewUserRepository(db)
	chanRepo    := repository.NewChannelRepository(db)
	msgRepo     := repository.NewMessageRepository(db)
	cacheRepo   := repository.NewCacheRepository(rdb)

	// ── Services ─────────────────────────────────────────────────────────────
	// Services own all business logic; they compose repositories and utilities.

	broker  := pubsub.NewRedisBroker(rdb)
	msgSvc  := service.NewMessageService(db, broker, chanRepo, cacheRepo, sf)
	syncSvc := service.NewSyncService(msgRepo)
	userSvc := service.NewUserService(userRepo, sf)
	chanSvc := service.NewChannelService(chanRepo, userRepo, sf)

	// ── Handlers ─────────────────────────────────────────────────────────────
	// Handlers translate HTTP ↔ domain; no business logic lives here.

	msgHandler  := handler.NewMessageHandler(msgSvc)
	syncHandler := handler.NewSyncHandler(syncSvc)
	userHandler := handler.NewUserHandler(userSvc)
	chanHandler := handler.NewChannelHandler(chanSvc)

	// ── Routes ───────────────────────────────────────────────────────────────
	mux := http.NewServeMux()

	// Messages
	mux.HandleFunc("/api/v1/messages",     msgHandler.HandleSendMessage)
	mux.HandleFunc("/api/v1/sync/deltas",  syncHandler.HandleDeltaSync)

	// Users  (exact match for collection, prefix for individual resource)
	mux.HandleFunc("/api/v1/users",        userHandler.HandleUsers)
	mux.HandleFunc("/api/v1/users/",       userHandler.HandleUserByID)

	// Channels  (exact match for collection, prefix for sub-resources)
	mux.HandleFunc("/api/v1/channels",     chanHandler.HandleChannels)
	mux.HandleFunc("/api/v1/channels/",    chanHandler.HandleChannelRoute)

	// ── Server ───────────────────────────────────────────────────────────────
	srv := &http.Server{Addr: cfg.APIPort, Handler: mux}

	// Graceful shutdown: listen for SIGTERM / SIGINT so in-flight requests
	// are not dropped during a rolling deployment or Ctrl-C in development.
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("[API] Shutdown signal received — draining connections...")
		srv.Close()
	}()

	log.Printf("[API] Stateless HTTP gateway listening on %s", cfg.APIPort)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[API] Server error: %v", err)
	}
	log.Println("[API] Server stopped.")
}
