package service

import (
	"context"
	"teamchat-backend/internal/domain"
	"teamchat-backend/internal/repository"
)

type SyncService struct {
	repo *repository.MessageRepository
}

func NewSyncService(repo *repository.MessageRepository) *SyncService {
	return &SyncService{repo: repo}
}

// SyncDeltas processes a batch map of channel IDs and their last known message sequences
func (s *SyncService) SyncDeltas(ctx context.Context, catchUpMap map[string]uint64) (map[string][]domain.Message, error) {
	deltas := make(map[string][]domain.Message)

	for convID, lastMsgID := range catchUpMap {
		missedMessages, err := s.repo.GetMessagesSince(ctx, convID, lastMsgID)
		if err != nil {
			return nil, err
		}

		// Only allocate map space if there are actually missed messages
		if len(missedMessages) > 0 {
			deltas[convID] = missedMessages
		}
	}

	return deltas, nil
}
