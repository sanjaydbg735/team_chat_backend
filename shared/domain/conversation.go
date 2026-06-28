package domain

import "time"

// Channel represents a conversation room in TeamChat.
//
// Two types exist:
//
//   - GROUP: a named channel with any number of members (e.g. #engineering,
//     #general).  Created by a user who becomes the first member.
//
//   - DM: a Direct Message thread between exactly two users.  The Name field
//     is typically empty for DMs — clients derive the display name from the
//     other participant's username.
//
// Internally the database table is named "conversations" (historical naming),
// but the domain model uses Channel to match the user-facing concept.
type Channel struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`       // display name; empty for DMs
	Type      string    `json:"type"`       // "GROUP" or "DM"
	CreatedBy string    `json:"created_by"` // user ID of the creator
	CreatedAt time.Time `json:"created_at"`
}
