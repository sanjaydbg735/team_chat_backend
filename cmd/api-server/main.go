package main

import (
	"database/sql"
	"log"
	"net/http"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"

	handler "teamchat-backend/internal/handler/http"
	"teamchat-backend/internal/pubsub"
	"teamchat-backend/internal/repository"
	"teamchat-backend/internal/service"
)

func main() {
	db, err := sql.Open("mysql", "root:rootpassword@tcp(127.0.0.1:3307)/teamchat?parseTime=true")
	if err != nil {
		log.Fatalf("Database connection error: %v", err)
	}
	db.SetMaxOpenConns(25)

	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})

	// Components for User Story 1 (Ingress)
	broker := pubsub.NewRedisBroker(rdb)
	msgSvc := service.NewMessageService(db, broker)
	msgHandler := handler.NewMessageHandler(msgSvc, rdb)

	// Components for User Story 2 (Delta Synchronization Catch-up)
	msgRepo := repository.NewMessageRepository(db)
	syncSvc := service.NewSyncService(msgRepo)
	syncHandler := handler.NewSyncHandler(syncSvc)

	// Endpoints
	http.HandleFunc("/api/v1/messages", msgHandler.HandleSendMessage)
	http.HandleFunc("/api/v1/sync/deltas", syncHandler.HandleDeltaSync)

	log.Println("Stateless API Entry Gateway serving traffic on port :8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Server aborted unexpectedly: %v", err)
	}
}
