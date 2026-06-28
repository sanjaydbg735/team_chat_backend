package ws

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"teamchat-backend/internal/domain"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Client represents a connected user's persistent socket session
type Client struct {
	UserID string
	Conn   *websocket.Conn
	Send   chan domain.Message // Channel buffering outbound messages
}

// ServeWS upgrades the HTTP connection and hooks it into our event broker
func ServeWS(rdb *redis.Client, w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	convID := r.URL.Query().Get("conversation_id")
	if userID == "" || convID == "" {
		http.Error(w, "Missing user_id or conversation_id", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Upgrade error: %v", err)
		return
	}

	client := &Client{
		UserID: userID,
		Conn:   conn,
		Send:   make(chan domain.Message, 256),
	}

	// Spin up the concurrent asynchronous Write Loop (Handles outgoing messages & Pings)
	go client.writePump()

	// Spin up the concurrent asynchronous Read Loop (Handles keeping the socket alive via Pongs)
	go client.readPump()

	// Spin up a dedicated background routine to listen to Redis for this room
	go client.listenToRedis(convID, rdb)
}

// readPump handles incoming heartbeat control blocks from the client socket.
func (c *Client) readPump() {
	defer func() {
		log.Printf("Closing read pump for user: %s", c.UserID)
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(512)
	_ = c.Conn.SetReadDeadline(time.Now().Add(pongWait))

	c.Conn.SetPongHandler(func(string) error {
		log.Printf("Received Pong heartbeat from user: %s. Resetting deadline.", c.UserID)
		return c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, _, err := c.Conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

// writePump pushes messages from the client's Go channel down to the actual WebSocket
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		log.Printf("Closing write pump for user: %s", c.UserID)
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.Conn.WriteJSON(message); err != nil {
				return
			}

		case <-ticker.C:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// listenToRedis binds this connection to the Redis Pub/Sub backplane
func (c *Client) listenToRedis(convID string, rdb *redis.Client) {
	ctx := context.Background()
	pubsub := rdb.Subscribe(ctx, "chat:room:"+convID)
	defer func() {
		pubsub.Close()
		log.Printf("Unsubscribed user %s from Redis channel chat:room:%s", c.UserID, convID)
	}()

	ch := pubsub.Channel()
	for msg := range ch {
		var domainMsg domain.Message
		if err := json.Unmarshal([]byte(msg.Payload), &domainMsg); err != nil {
			continue
		}

		// Don't send the message back to the client who sent it
		if domainMsg.SenderID != c.UserID {
			select {
			case c.Send <- domainMsg:
			default:
				log.Printf("Buffer overrun for client %s, dropping socket.", c.UserID)
			}
		}
	}
}
