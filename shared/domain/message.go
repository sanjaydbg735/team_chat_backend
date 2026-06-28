// Package domain defines the core business entities for the TeamChat backend.
// These structs are the language of the system — services, handlers, and
// repositories all speak in domain types.  They carry no framework tags beyond
// json so they stay portable across transport layers.
package domain

import "time"

// Message is the fundamental unit of communication in TeamChat.
// Every message belongs to exactly one channel (ConversationID) and is authored
// by exactly one user (SenderID).
//
// ID is a Snowflake ID: a 64-bit integer that encodes the creation timestamp,
// so messages within a channel are naturally sorted by ID ascending.
type Message struct {
	ID             uint64    `json:"id"`
	ConversationID string    `json:"conversation_id"` // the channel this message was sent to
	SenderID       string    `json:"sender_id"`       // the user who authored the message
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}

// AckFrame is the application-level delivery acknowledgement sent by a client
// over its WebSocket connection after it successfully renders a message.
//
// Planned use: the server tracks unacked messages per (receiver, conversation)
// in Redis so it can re-deliver on reconnect without a full DB delta sync.
// (Implementation is in the roadmap — the struct is defined here so the
// wire protocol is stable from day one.)
type AckFrame struct {
	MessageID      uint64 `json:"message_id"`
	ConversationID string `json:"conversation_id"`
	ReceiverID     string `json:"receiver_id"`
}
