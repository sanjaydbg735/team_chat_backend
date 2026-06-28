package repository

import (
	"context"
	"database/sql"
	"teamchat/shared/domain"
	"time"
)

// UserRepository provides all database operations for the users table.
// User records are immutable after creation — there is no update path.
type UserRepository struct {
	db *sql.DB
}

// NewUserRepository constructs a UserRepository backed by the given DB pool.
func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{db: db}
}

// Create inserts a new user row.  The caller is responsible for generating the
// user's ID (via Snowflake) and checking for username uniqueness before calling
// this method.
func (r *UserRepository) Create(ctx context.Context, user *domain.User) error {
	_, err := r.db.ExecContext(ctx,
		"INSERT INTO users (id, username, created_at) VALUES (?, ?, ?)",
		user.ID, user.Username, user.CreatedAt,
	)
	return err
}

// GetByID looks up a user by primary key.  Returns domain.ErrUserNotFound when
// the row does not exist so callers can distinguish "not found" from DB errors.
func (r *UserRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	var u domain.User
	err := r.db.QueryRowContext(ctx,
		"SELECT id, username, created_at FROM users WHERE id = ?", id,
	).Scan(&u.ID, &u.Username, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, domain.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// UsernameExists checks whether a username is already taken.
// Used as a fast guard before INSERT to surface a clear 409 Conflict to the client.
func (r *UserRepository) UsernameExists(ctx context.Context, username string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM users WHERE username = ?", username,
	).Scan(&count)
	return count > 0, err
}

// UpsertForTest inserts a user if the row does not already exist.
// This method is intentionally only for test fixtures — production code should
// call Create, which enforces uniqueness through the business layer.
func (r *UserRepository) UpsertForTest(ctx context.Context, id, username string) error {
	_, err := r.db.ExecContext(ctx,
		"INSERT IGNORE INTO users (id, username, created_at) VALUES (?, ?, ?)",
		id, username, time.Now(),
	)
	return err
}
