package service

import (
	"context"
	"fmt"
	"teamchat/shared/domain"
	"teamchat/shared/repository"
	"teamchat/shared/snowflake"
	"time"
)

// UserService manages user lifecycle: registration and lookup.
// Authentication is out of scope for the current MVP — the user ID in each
// request is trusted without a token.  JWT validation is planned next.
type UserService struct {
	repo      *repository.UserRepository
	snowflake *snowflake.Generator
}

// NewUserService constructs a UserService with the given repository and ID generator.
func NewUserService(repo *repository.UserRepository, sf *snowflake.Generator) *UserService {
	return &UserService{repo: repo, snowflake: sf}
}

// CreateUser registers a new user with the given username.
//
// Returns domain.ErrUsernameTaken if the username is already in use.
// The generated user ID is a Snowflake so it embeds the registration timestamp
// and is globally sortable.
func (s *UserService) CreateUser(ctx context.Context, username string) (*domain.User, error) {
	// Guard: prevent duplicate usernames before attempting the INSERT
	exists, err := s.repo.UsernameExists(ctx, username)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, domain.ErrUsernameTaken
	}

	user := &domain.User{
		ID:        fmt.Sprintf("%d", s.snowflake.Next()), // uint64 as string for readability
		Username:  username,
		CreatedAt: time.Now(),
	}
	if err := s.repo.Create(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

// GetUser retrieves a user by ID.
// Returns domain.ErrUserNotFound if the ID does not exist.
func (s *UserService) GetUser(ctx context.Context, id string) (*domain.User, error) {
	return s.repo.GetByID(ctx, id)
}
