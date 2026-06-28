package domain

import "time"

// User represents a registered participant in the TeamChat system.
//
// Users are identified by a Snowflake ID (generated at registration) and a
// unique human-readable Username.  Authentication (JWT, OAuth) is a planned
// future feature — for now the user ID is trusted from the request payload.
type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}
