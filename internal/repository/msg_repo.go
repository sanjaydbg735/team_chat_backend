package repository

import (
	"context"
	"database/sql"
	"teamchat-backend/internal/domain"
)

type MessageRepository struct {
	db *sql.DB
}

func NewMessageRepository(db *sql.DB) *MessageRepository {
	return &MessageRepository{db: db}
}

// GetMessagesSince fetches missing deltas using our composite index range query
func (r *MessageRepository) GetMessagesSince(ctx context.Context, convID string, lastMsgID uint64) ([]domain.Message, error) {
	query := `
		SELECT id, conversation_id, sender_id, content, created_at 
		FROM messages 
		WHERE conversation_id = ? AND id > ? 
		ORDER BY id ASC 
		LIMIT 100`

	rows, err := r.db.QueryContext(ctx, query, convID, lastMsgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []domain.Message
	for rows.Next() {
		var m domain.Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.SenderID, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, nil
}
