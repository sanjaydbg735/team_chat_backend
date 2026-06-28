package main

import (
	"log"
	"net/http"
	"teamchat-backend/internal/handler/ws"

	"github.com/redis/go-redis/v9"
)

func main() {
	// Connect to our shared Redis backbone instance
	rdb := redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:6379",
	})

	// Setup our WebSocket endpoint route
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ws.ServeWS(rdb, w, r)
	})

	log.Println("Stateful WebSocket Worker Fleet active on port :8081...")
	if err := http.ListenAndServe(":8081", nil); err != nil {
		log.Fatalf("WebSocket worker crashed: %v", err)
	}
}
