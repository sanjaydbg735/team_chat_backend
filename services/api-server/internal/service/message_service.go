// Package service contains the business logic for TeamChat.
// Services sit between the HTTP/WebSocket handlers (which translate HTTP
// concerns) and the repositories (which translate DB concerns).
// Services own all business rules: validation, access control, ID generation,
// and orchestrating multi-step operations.
package service

import (
	"context"
	"database/sql"
	"teamchat/shared/domain"
	"teamchat/shared/pubsub"
	"teamchat/shared/repository"
	"teamchat/shared/snowflake"
	"time"
)

// MessageService orchestrates message delivery.
// It is the single point where a message transitions from "request" to "stored
// and broadcast".  The sequence is intentionally ordered to minimise wasted
// work on invalid requests:
//
//  1. Idempotency check  — cheapest guard (one Redis round-trip)
//  2. Membership check   — one MySQL read, rejects non-members early
//  3. Persist to MySQL   — durable write
//  4. Publish to Redis   — triggers real-time delivery to WebSocket workers
type MessageService struct {
	db        *sql.DB
	broker    pubsub.Broker
	chanRepo  *repository.ChannelRepository
	cacheRepo *repository.CacheRepository
	snowflake *snowflake.Generator
}

// NewMessageService wires all dependencies required by MessageService.
func NewMessageService(
	db *sql.DB,
	broker pubsub.Broker,
	chanRepo *repository.ChannelRepository,
	cacheRepo *repository.CacheRepository,
	sf *snowflake.Generator,
) *MessageService {
	return &MessageService{
		db:        db,
		broker:    broker,
		chanRepo:  chanRepo,
		cacheRepo: cacheRepo,
		snowflake: sf,
	}
}

// SendMessage validates and delivers a message.
//
// idempotencyKey is the value of the client's X-Idempotency-Key header.  If
// empty, idempotency checking is skipped (useful for internal/test callers).
//
// On success, msg.ID and msg.CreatedAt are populated in-place so the caller
// can return the full message to the client.
func (s *MessageService) SendMessage(ctx context.Context, msg *domain.Message, idempotencyKey string) error {
	// Step 1 — Idempotency guard
	// Prevents double-sends caused by mobile clients retrying on flaky networks.
	if idempotencyKey != "" {
		acquired, err := s.cacheRepo.AcquireIdempotencyLock(ctx, idempotencyKey, 30*time.Second)
		if err != nil {
			return err
		}
		if !acquired {
			// Key already exists: this is a duplicate request
			return domain.ErrDuplicateRequest
		}
	}

	// Step 2 — Membership access control
	// Only channel members may post.  This prevents cross-channel message injection.
	isMember, err := s.chanRepo.IsMember(ctx, msg.ConversationID, msg.SenderID)
	if err != nil {
		return err
	}
	if !isMember {
		return domain.ErrNotMember
	}

	// Step 3 — Assign a globally unique, time-ordered Snowflake ID
	msg.ID = s.snowflake.Next()
	msg.CreatedAt = time.Now()

	// Step 4 — Persist to MySQL (durable, source of truth)
	const query = "INSERT INTO messages (id, conversation_id, sender_id, content, created_at) VALUES (?, ?, ?, ?, ?)"
	if _, err := s.db.ExecContext(ctx, query, msg.ID, msg.ConversationID, msg.SenderID, msg.Content, msg.CreatedAt); err != nil {
		return err
	}

	// Step 5 — Publish to Redis Pub/Sub (triggers real-time push to online clients)
	// This is fire-and-forget from the DB perspective: even if Redis is temporarily
	// unavailable, the message is already persisted and will be fetched via
	// delta-sync on reconnect.
	return s.broker.Publish(ctx, "chat:room:"+msg.ConversationID, msg)
}
