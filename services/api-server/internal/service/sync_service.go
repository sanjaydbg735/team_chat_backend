package service

import (
	"context"
	"teamchat/shared/domain"
	"teamchat/shared/repository"
)

// SyncService handles the offline catch-up flow.
//
// When a client reconnects after being offline it sends a map of
// { conversationID: lastKnownMessageID } for every channel it belongs to.
// SyncService fetches the messages each client missed and returns them as a
// delta map so the client can replay them in order.
//
// This is what allows TeamChat to feel seamless even on unreliable networks:
// real-time delivery is best-effort (WebSocket + Redis), but correctness is
// guaranteed by the delta-sync endpoint backed by MySQL.
type SyncService struct {
	msgRepo *repository.MessageRepository
}

// NewSyncService constructs a SyncService with the given message repository.
func NewSyncService(msgRepo *repository.MessageRepository) *SyncService {
	return &SyncService{msgRepo: msgRepo}
}

// SyncDeltas accepts a catch-up map and returns all messages each channel
// received after the client's last known position.
//
// catchUpMap format: { "channel-id": lastSeenMessageID, ... }
//
// The response only includes channels that have new messages — channels where
// the client is already up-to-date are omitted to keep the payload lean.
func (s *SyncService) SyncDeltas(ctx context.Context, catchUpMap map[string]uint64) (map[string][]domain.Message, error) {
	deltas := make(map[string][]domain.Message)

	for convID, lastMsgID := range catchUpMap {
		missed, err := s.msgRepo.GetMessagesSince(ctx, convID, lastMsgID)
		if err != nil {
			return nil, err
		}
		// Only populate the map entry when there are actual missed messages.
		// An empty slice would tell the client "you missed nothing" unnecessarily.
		if len(missed) > 0 {
			deltas[convID] = missed
		}
	}

	return deltas, nil
}
