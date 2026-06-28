package repository

import (
	"context"
	"database/sql"
	"teamchat/shared/domain"
)

// MessageRepository handles all read operations on the messages table.
// Write operations (INSERT) live in MessageService to keep the transaction
// and Pub/Sub publish in the same code path.
type MessageRepository struct {
	db *sql.DB
}

// NewMessageRepository constructs a MessageRepository backed by the given DB pool.
func NewMessageRepository(db *sql.DB) *MessageRepository {
	return &MessageRepository{db: db}
}

// GetMessagesSince fetches up to 100 messages for a conversation that were
// created after lastMsgID, ordered oldest-first.
//
// This powers the delta-sync catch-up endpoint that offline clients call
// when they reconnect: they send their last known message ID and receive
// everything they missed.
//
// Performance note:
//   The composite index idx_conv_msg(conversation_id, id) makes this query
//   an index-range scan rather than a full-table scan — sub-millisecond even
//   with billions of rows, as long as the index fits in the InnoDB buffer pool.
func (r *MessageRepository) GetMessagesSince(ctx context.Context, convID string, lastMsgID uint64) ([]domain.Message, error) {
	const query = `
		SELECT id, conversation_id, sender_id, content, created_at
		FROM   messages
		WHERE  conversation_id = ? AND id > ?
		ORDER  BY id ASC
		LIMIT  100`

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
	return messages, rows.Err()
}
