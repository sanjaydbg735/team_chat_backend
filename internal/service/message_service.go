package service

import (
	"context"
	"database/sql"
	"math/rand"
	"teamchat-backend/internal/domain"
	"teamchat-backend/internal/pubsub"
	"time"
)

type MessageService struct {
	db     *sql.DB
	broker pubsub.Broker
}

func NewMessageService(db *sql.DB, broker pubsub.Broker) *MessageService {
	return &MessageService{db: db, broker: broker}
}

// SendMessage persists the message in MySQL and broadcasts it to the room via Redis Pub/Sub
func (s *MessageService) SendMessage(ctx context.Context, msg *domain.Message) error {
	// 1. Generate distributed monotonically increasing ID sequence using current nanoseconds
	msg.ID = uint64(time.Now().UnixNano()) + uint64(rand.Intn(1000))
	msg.CreatedAt = time.Now()

	// 2. Persistent Storage Write-Ahead: Commit the message to MySQL database
	query := "INSERT INTO messages (id, conversation_id, sender_id, content, created_at) VALUES (?, ?, ?, ?, ?)"
	_, err := s.db.ExecContext(ctx, query, msg.ID, msg.ConversationID, msg.SenderID, msg.Content, msg.CreatedAt)
	if err != nil {
		return err
	}

	// 3. Centralized Channel Fan-Out Broadcast
	// Every WebSocket server handling clients for this conversation ID will intercept this message.
	channelName := "chat:room:" + msg.ConversationID
	return s.broker.Publish(ctx, channelName, msg)
}
