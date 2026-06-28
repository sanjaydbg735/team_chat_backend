package domain

import "errors"

// Sentinel errors are defined here so the entire codebase shares a single
// set of error values.  Callers use errors.Is() to check for specific
// conditions without relying on string matching.
//
// Convention: service methods return these errors; HTTP handlers map them to
// the appropriate status code (e.g. ErrNotMember → 403 Forbidden).
var (
	// ErrNotMember is returned when a user tries to post to a channel they
	// are not a member of.  Maps to HTTP 403 Forbidden.
	ErrNotMember = errors.New("user is not a member of the channel")

	// ErrDuplicateRequest is returned when a message is submitted with an
	// idempotency key that has already been processed.  Maps to HTTP 409 Conflict.
	ErrDuplicateRequest = errors.New("duplicate request: idempotency key already consumed")

	// ErrUsernameTaken is returned when a registration attempt uses a username
	// that already exists.  Maps to HTTP 409 Conflict.
	ErrUsernameTaken = errors.New("username is already taken")

	// ErrChannelNotFound is returned when a channel lookup finds no matching row.
	// Maps to HTTP 404 Not Found.
	ErrChannelNotFound = errors.New("channel not found")

	// ErrUserNotFound is returned when a user lookup finds no matching row.
	// Maps to HTTP 404 Not Found.
	ErrUserNotFound = errors.New("user not found")
)
