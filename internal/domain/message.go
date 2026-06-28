package domain

import "time"

// AckFrame represents the application-level confirmation sent by the client
type AckFrame struct {
	MessageID      uint64 `json:"message_id"`
	ConversationID string `json:"conversation_id"`
	ReceiverID     string `json:"receiver_id"`
}

type Message struct {
	ID             uint64    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	SenderID       string    `json:"sender_id"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}
