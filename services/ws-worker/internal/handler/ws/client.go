// Package ws implements the stateful WebSocket layer of TeamChat.
//
// Architecture overview:
//
//	HTTP client ──── upgrade ──── ws.ServeWS ──── Client goroutines
//	                                                 │
//	                                    ┌────────────┼────────────────┐
//	                                readPump      writePump      listenToRedis (×N)
//	                                (keep-alive)  (send msgs)   (one per channel)
//
// One Client is created per WebSocket connection.  The Client subscribes to
// every Redis channel the user belongs to, so messages from all channels arrive
// on a single socket — the same behaviour users expect from Slack or Teams.
package ws

import (
	"context"
	"encoding/json"
	"log"
	"teamchat/shared/domain"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

// Timing constants for the ping/pong heartbeat protocol.
// The client must respond to a Ping with a Pong within pongWait or the
// server closes the connection (zombie detection).
const (
	writeWait  = 10 * time.Second            // max time to write a message to the client
	pongWait   = 60 * time.Second            // max time to wait for a Pong response
	pingPeriod = (pongWait * 9) / 10         // how often to send a Ping (must be < pongWait)
	sendBuffer = 256                          // outbound message buffer per client
)

// Client represents a single user's active WebSocket session.
//
// One Client may listen on many channels (one Redis subscriber goroutine per
// channel).  All channel messages funnel into the single Send buffer so they
// are serialised before being written to the WebSocket connection.
type Client struct {
	UserID string             // the authenticated user
	Conn   *websocket.Conn    // underlying WebSocket connection
	Send   chan domain.Message // outbound message buffer
}

// readPump is a long-running goroutine that:
//  1. Keeps the WebSocket alive by processing Pong frames and resetting the deadline.
//  2. Detects client disconnects (read error) and triggers shutdown of the whole
//     session by calling cancel() — which signals all listenToRedis goroutines to stop.
//
// Only one goroutine reads from a websocket.Conn at a time (Gorilla requirement).
func (c *Client) readPump(cancel context.CancelFunc, hub *Hub) {
	defer func() {
		// Cancelling the context tells all listenToRedis goroutines to exit.
		cancel()
		// Remove this client from the Hub registry.
		hub.Unregister(c)
		c.Conn.Close()
		log.Printf("[WS] readPump closed — user=%s", c.UserID)
	}()

	// ReadLimit caps message size to prevent memory exhaustion.
	// Clients only send Pong control frames here; 512 bytes is generous.
	c.Conn.SetReadLimit(512)
	_ = c.Conn.SetReadDeadline(time.Now().Add(pongWait))

	// Pong handler resets the deadline, keeping the connection alive while
	// the client is actively responding to our Pings.
	c.Conn.SetPongHandler(func(string) error {
		return c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		// We don't process incoming application frames yet (ACK is planned).
		// ReadMessage blocks until a frame arrives or the deadline expires.
		if _, _, err := c.Conn.ReadMessage(); err != nil {
			break // connection closed or timed out
		}
	}
}

// writePump drains the Send channel and writes each message to the WebSocket.
// It also fires periodic Ping frames to detect connections where the TCP layer
// is still up but the client process has died ("zombie connections").
//
// Only one goroutine writes to a websocket.Conn at a time (Gorilla requirement).
func (c *Client) writePump(cancel context.CancelFunc) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		cancel()
		c.Conn.Close()
		log.Printf("[WS] writePump closed — user=%s", c.UserID)
	}()

	for {
		select {
		case message, ok := <-c.Send:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Send channel was closed — send a close frame and exit
				_ = c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			// WriteJSON serialises the domain.Message to JSON and sends it
			// as a single WebSocket text frame.
			if err := c.Conn.WriteJSON(message); err != nil {
				return // client disconnected or write timed out
			}

		case <-ticker.C:
			// Send a Ping to check if the client is still alive.
			_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return // client didn't pong in time
			}
		}
	}
}

// listenToRedis subscribes to a single Redis Pub/Sub channel and forwards any
// incoming messages into the Client's Send buffer.
//
// One goroutine per channel is spawned so the client receives messages from all
// channels they belong to on a single WebSocket connection.  When ctx is
// cancelled (i.e. the client disconnects), this goroutine exits cleanly.
//
// Echo suppression: the sender's own messages are not reflected back to them
// since the HTTP response to their POST already confirms delivery.
func (c *Client) listenToRedis(ctx context.Context, channelID string, rdb *redis.Client) {
	// Subscribe to the Redis channel for this conversation.
	// The context ensures the subscription is cleaned up when the client disconnects.
	pubsub := rdb.Subscribe(ctx, "chat:room:"+channelID)
	defer func() {
		pubsub.Close()
		log.Printf("[WS] Redis unsub — user=%s channel=%s", c.UserID, channelID)
	}()

	msgCh := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			// WebSocket connection closed — stop listening
			return
		case redisMsg, ok := <-msgCh:
			if !ok {
				return // Redis subscription closed
			}
			var domainMsg domain.Message
			if err := json.Unmarshal([]byte(redisMsg.Payload), &domainMsg); err != nil {
				log.Printf("[WS] JSON parse error on channel %s: %v", channelID, err)
				continue
			}
			// Don't echo the message back to the user who sent it.
			// They already got confirmation from the HTTP POST response.
			if domainMsg.SenderID == c.UserID {
				continue
			}
			// Non-blocking send: if the buffer is full the client is too slow
			// to consume messages and we drop rather than block all channels.
			select {
			case c.Send <- domainMsg:
			default:
				log.Printf("[WS] Send buffer full — user=%s channel=%s, dropping message", c.UserID, channelID)
			}
		}
	}
}
