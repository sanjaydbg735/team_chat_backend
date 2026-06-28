package repository

import (
	"context"
	"database/sql"
	"teamchat/shared/domain"
)

// ChannelRepository provides all database operations for the conversations
// (channels) and channel_members tables.
//
// Terminology note: the DB table is named "conversations" (historical) but the
// domain model uses "Channel".  Both refer to the same concept.
type ChannelRepository struct {
	db *sql.DB
}

// NewChannelRepository constructs a ChannelRepository backed by the given DB pool.
func NewChannelRepository(db *sql.DB) *ChannelRepository {
	return &ChannelRepository{db: db}
}

// Create inserts a new channel row.  The caller (ChannelService) is responsible
// for populating all fields including the Snowflake ID.
func (r *ChannelRepository) Create(ctx context.Context, ch *domain.Channel) error {
	_, err := r.db.ExecContext(ctx,
		"INSERT INTO conversations (id, type, name, created_by, created_at) VALUES (?, ?, ?, ?, ?)",
		ch.ID, ch.Type, ch.Name, ch.CreatedBy, ch.CreatedAt,
	)
	return err
}

// GetByID retrieves a channel by its primary key.
// Returns domain.ErrChannelNotFound when the row does not exist.
func (r *ChannelRepository) GetByID(ctx context.Context, id string) (*domain.Channel, error) {
	var ch domain.Channel
	err := r.db.QueryRowContext(ctx,
		"SELECT id, type, COALESCE(name,''), COALESCE(created_by,''), created_at FROM conversations WHERE id = ?",
		id,
	).Scan(&ch.ID, &ch.Type, &ch.Name, &ch.CreatedBy, &ch.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, domain.ErrChannelNotFound
	}
	if err != nil {
		return nil, err
	}
	return &ch, nil
}

// GetByUser returns all channels a user is a member of, ordered by creation time.
//
// This is the primary query for building a user's channel sidebar on login.
// The idx_user_channels index on channel_members(user_id) makes this an
// index lookup rather than a full scan of channel_members.
func (r *ChannelRepository) GetByUser(ctx context.Context, userID string) ([]domain.Channel, error) {
	const query = `
		SELECT c.id, c.type, COALESCE(c.name,''), COALESCE(c.created_by,''), c.created_at
		FROM   conversations c
		INNER  JOIN channel_members cm ON c.id = cm.channel_id
		WHERE  cm.user_id = ?
		ORDER  BY c.created_at ASC`

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []domain.Channel
	for rows.Next() {
		var ch domain.Channel
		if err := rows.Scan(&ch.ID, &ch.Type, &ch.Name, &ch.CreatedBy, &ch.CreatedAt); err != nil {
			return nil, err
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

// GetChannelIDsByUser returns only the channel ID strings for a given user.
// Used by the WebSocket worker to subscribe to the correct set of Redis rooms
// without loading full channel metadata.
func (r *ChannelRepository) GetChannelIDsByUser(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		"SELECT channel_id FROM channel_members WHERE user_id = ?", userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// AddMember enrols a user in a channel.  INSERT IGNORE makes this idempotent:
// calling it twice for the same (channelID, userID) pair is a no-op.
func (r *ChannelRepository) AddMember(ctx context.Context, channelID, userID string) error {
	_, err := r.db.ExecContext(ctx,
		"INSERT IGNORE INTO channel_members (channel_id, user_id) VALUES (?, ?)",
		channelID, userID,
	)
	return err
}

// RemoveMember removes a user from a channel.
// Removing the last member does not delete the channel itself.
func (r *ChannelRepository) RemoveMember(ctx context.Context, channelID, userID string) error {
	_, err := r.db.ExecContext(ctx,
		"DELETE FROM channel_members WHERE channel_id = ? AND user_id = ?",
		channelID, userID,
	)
	return err
}

// IsMember checks whether a user belongs to a channel.
// Called by MessageService before persisting a message to enforce access control.
func (r *ChannelRepository) IsMember(ctx context.Context, channelID, userID string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM channel_members WHERE channel_id = ? AND user_id = ?",
		channelID, userID,
	).Scan(&count)
	return count > 0, err
}
