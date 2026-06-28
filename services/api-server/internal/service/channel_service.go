package service

import (
	"context"
	"fmt"
	"teamchat/shared/domain"
	"teamchat/shared/repository"
	"teamchat/shared/snowflake"
	"time"
)

// ChannelService manages channel lifecycle: creation, membership, and discovery.
//
// Business rules enforced here:
//   - DM channels must have exactly 2 members.
//   - The creator is always enrolled as a member automatically.
//   - Channel type must be "DM" or "GROUP".
type ChannelService struct {
	repo      *repository.ChannelRepository
	userRepo  *repository.UserRepository
	snowflake *snowflake.Generator
}

// NewChannelService constructs a ChannelService with the given dependencies.
func NewChannelService(
	repo *repository.ChannelRepository,
	userRepo *repository.UserRepository,
	sf *snowflake.Generator,
) *ChannelService {
	return &ChannelService{repo: repo, userRepo: userRepo, snowflake: sf}
}

// CreateChannel creates a new channel and enrols the given members.
//
// memberIDs should include all initial members.  If createdBy is not already
// in memberIDs it is appended automatically — a creator who isn't a member
// would be unable to send any messages.
//
// For DM channels, memberIDs must contain exactly 2 user IDs.
func (s *ChannelService) CreateChannel(
	ctx context.Context,
	name, chanType, createdBy string,
	memberIDs []string,
) (*domain.Channel, error) {
	// Validate channel type
	if chanType != "DM" && chanType != "GROUP" {
		return nil, fmt.Errorf("invalid channel type %q: must be DM or GROUP", chanType)
	}

	// DM channels are strictly between two people
	if chanType == "DM" && len(memberIDs) != 2 {
		return nil, fmt.Errorf("DM channel requires exactly 2 members, got %d", len(memberIDs))
	}

	// Ensure creator is always a member
	if !containsString(memberIDs, createdBy) {
		memberIDs = append(memberIDs, createdBy)
	}

	ch := &domain.Channel{
		ID:        fmt.Sprintf("%d", s.snowflake.Next()),
		Name:      name,
		Type:      chanType,
		CreatedBy: createdBy,
		CreatedAt: time.Now(),
	}

	// Persist the channel row first, then enrol members
	if err := s.repo.Create(ctx, ch); err != nil {
		return nil, fmt.Errorf("create channel: %w", err)
	}
	for _, uid := range memberIDs {
		if err := s.repo.AddMember(ctx, ch.ID, uid); err != nil {
			return nil, fmt.Errorf("add member %s: %w", uid, err)
		}
	}
	return ch, nil
}

// GetUserChannels returns all channels the given user belongs to.
// This is the data behind a user's channel sidebar.
func (s *ChannelService) GetUserChannels(ctx context.Context, userID string) ([]domain.Channel, error) {
	return s.repo.GetByUser(ctx, userID)
}

// GetChannel returns a single channel by ID.
func (s *ChannelService) GetChannel(ctx context.Context, id string) (*domain.Channel, error) {
	return s.repo.GetByID(ctx, id)
}

// AddMember adds a user to an existing channel.
// Calling this for a user who is already a member is a safe no-op.
func (s *ChannelService) AddMember(ctx context.Context, channelID, userID string) error {
	return s.repo.AddMember(ctx, channelID, userID)
}

// RemoveMember removes a user from a channel (leave channel).
func (s *ChannelService) RemoveMember(ctx context.Context, channelID, userID string) error {
	return s.repo.RemoveMember(ctx, channelID, userID)
}

// containsString reports whether slice contains target.
func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}
