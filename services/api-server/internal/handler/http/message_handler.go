// Package http contains the HTTP handlers for the TeamChat API server.
//
// Each handler follows the same pattern:
//  1. Validate HTTP method.
//  2. Decode the request body.
//  3. Call the appropriate service method.
//  4. Map service errors to HTTP status codes.
//  5. Encode and write the response.
//
// Handlers are intentionally thin — all business logic lives in the service layer.
package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"teamchat/api-server/internal/service"
	"teamchat/shared/domain"
)

// -------------------------------------------------------------------------
// MessageHandler — POST /api/v1/messages
// -------------------------------------------------------------------------

// MessageHandler processes message ingestion requests.
type MessageHandler struct {
	svc *service.MessageService
}

// NewMessageHandler constructs a MessageHandler.
func NewMessageHandler(svc *service.MessageService) *MessageHandler {
	return &MessageHandler{svc: svc}
}

// HandleSendMessage accepts a message from a client, validates membership,
// persists it, and fans it out to all online channel members via Redis Pub/Sub.
//
// Request body:
//
//	{ "conversation_id": "...", "sender_id": "...", "content": "..." }
//
// Optional header:
//
//	X-Idempotency-Key: <client-generated UUID>
//
// Responses:
//
//	202 Accepted  — message stored and broadcast; body contains the full Message with generated ID
//	400 Bad Request — missing or malformed body
//	403 Forbidden   — sender is not a member of the channel
//	405 Method Not Allowed
//	409 Conflict    — duplicate idempotency key (message already processed)
//	500 Internal Server Error
func (h *MessageHandler) HandleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract the optional idempotency key from the request header.
	// We pass it through to the service so the header stays as the source of
	// truth for deduplication without polluting the domain model.
	idempotencyKey := r.Header.Get("X-Idempotency-Key")

	var req struct {
		ConversationID string `json:"conversation_id"`
		SenderID       string `json:"sender_id"`
		Content        string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	if req.ConversationID == "" || req.SenderID == "" || req.Content == "" {
		http.Error(w, "conversation_id, sender_id, and content are required", http.StatusBadRequest)
		return
	}

	msg := &domain.Message{
		ConversationID: req.ConversationID,
		SenderID:       req.SenderID,
		Content:        req.Content,
	}

	if err := h.svc.SendMessage(r.Context(), msg, idempotencyKey); err != nil {
		switch {
		case errors.Is(err, domain.ErrDuplicateRequest):
			http.Error(w, err.Error(), http.StatusConflict)
		case errors.Is(err, domain.ErrNotMember):
			http.Error(w, "Forbidden: sender is not a member of this channel", http.StatusForbidden)
		default:
			http.Error(w, "Internal server error: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// Return the fully populated message (including the server-generated ID and
	// timestamp) so the client can use the ID for future delta-sync requests.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(msg)
}

// -------------------------------------------------------------------------
// SyncHandler — POST /api/v1/sync/deltas
// -------------------------------------------------------------------------

// SyncHandler handles the offline catch-up delta sync endpoint.
type SyncHandler struct {
	syncSvc *service.SyncService
}

// NewSyncHandler constructs a SyncHandler.
func NewSyncHandler(syncSvc *service.SyncService) *SyncHandler {
	return &SyncHandler{syncSvc: syncSvc}
}

// HandleDeltaSync returns all messages a client missed while offline.
//
// Request body:
//
//	{ "<channel_id>": <last_known_message_id>, ... }
//
// The client sends the highest message ID it has locally for each channel.
// The server returns only channels that have newer messages — channels already
// up-to-date are omitted from the response.
//
// Response:
//
//	{ "<channel_id>": [ Message, ... ], ... }
func (h *SyncHandler) HandleDeltaSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// catchUpMap is a sparse map: clients only include channels they care about.
	var catchUpMap map[string]uint64
	if err := json.NewDecoder(r.Body).Decode(&catchUpMap); err != nil {
		http.Error(w, "Invalid payload: expected { channelID: lastMessageID }", http.StatusBadRequest)
		return
	}

	deltas, err := h.syncSvc.SyncDeltas(r.Context(), catchUpMap)
	if err != nil {
		http.Error(w, "Delta sync failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(deltas)
}

// -------------------------------------------------------------------------
// UserHandler — POST /api/v1/users, GET /api/v1/users/{id}
// -------------------------------------------------------------------------

// UserHandler processes user registration and lookup requests.
type UserHandler struct {
	svc *service.UserService
}

// NewUserHandler constructs a UserHandler.
func NewUserHandler(svc *service.UserService) *UserHandler {
	return &UserHandler{svc: svc}
}

// HandleUsers handles POST /api/v1/users — creates a new user.
//
// Request body:  { "username": "alice" }
// Response:      201 Created + User JSON
// Errors:        400 missing username, 409 username taken, 500
func (h *UserHandler) HandleUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Username) == "" {
		http.Error(w, "Invalid payload: username is required", http.StatusBadRequest)
		return
	}

	user, err := h.svc.CreateUser(r.Context(), strings.TrimSpace(req.Username))
	if err != nil {
		if errors.Is(err, domain.ErrUsernameTaken) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, "Failed to create user: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}

// HandleUserByID handles GET /api/v1/users/{id} — fetches a user by ID.
func (h *UserHandler) HandleUserByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract the user ID from the URL path by stripping the route prefix.
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
	if id == "" {
		http.Error(w, "User ID is required", http.StatusBadRequest)
		return
	}

	user, err := h.svc.GetUser(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to fetch user: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

// -------------------------------------------------------------------------
// ChannelHandler — /api/v1/channels
// -------------------------------------------------------------------------

// ChannelHandler processes channel creation, listing, and membership management.
type ChannelHandler struct {
	svc *service.ChannelService
}

// NewChannelHandler constructs a ChannelHandler.
func NewChannelHandler(svc *service.ChannelService) *ChannelHandler {
	return &ChannelHandler{svc: svc}
}

// HandleChannels routes collection-level requests:
//
//	POST /api/v1/channels  → create a channel
//	GET  /api/v1/channels?user_id=X → list channels the user belongs to
func (h *ChannelHandler) HandleChannels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createChannel(w, r)
	case http.MethodGet:
		h.listChannels(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleChannelRoute routes sub-resource requests under /api/v1/channels/:
//
//	GET    /api/v1/channels/{id}                  → fetch channel info
//	POST   /api/v1/channels/{id}/members           → add a member
//	DELETE /api/v1/channels/{id}/members/{userID}  → remove a member (leave)
func (h *ChannelHandler) HandleChannelRoute(w http.ResponseWriter, r *http.Request) {
	// Parse the path segments after the prefix
	// e.g. "/api/v1/channels/chan_123/members/user_456"
	//       → parts = ["chan_123", "members", "user_456"]
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/channels/")
	parts := strings.SplitN(rest, "/", 3)
	channelID := parts[0]

	if channelID == "" {
		http.Error(w, "Channel ID is required", http.StatusBadRequest)
		return
	}

	// ── GET /api/v1/channels/{id} ──────────────────────────────────────
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ch, err := h.svc.GetChannel(r.Context(), channelID)
		if err != nil {
			if errors.Is(err, domain.ErrChannelNotFound) {
				http.Error(w, "Channel not found", http.StatusNotFound)
				return
			}
			http.Error(w, "Failed to fetch channel: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ch)
		return
	}

	// ── /api/v1/channels/{id}/members[/{userID}] ───────────────────────
	if parts[1] != "members" {
		http.Error(w, "Unknown resource path", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodPost:
		// Add a member to the channel
		var req struct {
			UserID string `json:"user_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
			http.Error(w, "user_id is required", http.StatusBadRequest)
			return
		}
		if err := h.svc.AddMember(r.Context(), channelID, req.UserID); err != nil {
			http.Error(w, "Failed to add member: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case http.MethodDelete:
		// Remove a member — user ID comes from the URL path segment
		if len(parts) < 3 || parts[2] == "" {
			http.Error(w, "user_id is required in path for DELETE", http.StatusBadRequest)
			return
		}
		if err := h.svc.RemoveMember(r.Context(), channelID, parts[2]); err != nil {
			http.Error(w, "Failed to remove member: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// createChannel handles POST /api/v1/channels.
//
// Request body:
//
//	{
//	  "name":       "engineering",  // required for GROUP; optional for DM
//	  "type":       "GROUP",        // "GROUP" (default) or "DM"
//	  "created_by": "<user_id>",    // required
//	  "member_ids": ["uid1","uid2"] // initial members; creator auto-added
//	}
func (h *ChannelHandler) createChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string   `json:"name"`
		Type      string   `json:"type"`
		CreatedBy string   `json:"created_by"`
		MemberIDs []string `json:"member_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	if req.CreatedBy == "" {
		http.Error(w, "created_by is required", http.StatusBadRequest)
		return
	}
	if req.Type == "" {
		req.Type = "GROUP" // default channel type
	}

	ch, err := h.svc.CreateChannel(r.Context(), req.Name, req.Type, req.CreatedBy, req.MemberIDs)
	if err != nil {
		http.Error(w, "Failed to create channel: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(ch)
}

// listChannels handles GET /api/v1/channels?user_id=X.
func (h *ChannelHandler) listChannels(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		http.Error(w, "user_id query param is required", http.StatusBadRequest)
		return
	}

	channels, err := h.svc.GetUserChannels(r.Context(), userID)
	if err != nil {
		http.Error(w, "Failed to fetch channels: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Return an empty JSON array instead of null when there are no channels,
	// so clients don't need to handle a null response.
	if channels == nil {
		channels = []domain.Channel{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channels)
}
