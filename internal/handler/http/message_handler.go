package http

import (
	"encoding/json"
	"net/http"
	"teamchat-backend/internal/domain"
	"teamchat-backend/internal/service"
	"time"

	"github.com/redis/go-redis/v9"
)

type MessageHandler struct {
	svc *service.MessageService
	rdb *redis.Client
}

func NewMessageHandler(svc *service.MessageService, rdb *redis.Client) *MessageHandler {
	return &MessageHandler{svc: svc, rdb: rdb}
}

type SyncHandler struct {
	syncSvc *service.SyncService
}

func NewSyncHandler(syncSvc *service.SyncService) *SyncHandler {
	return &SyncHandler{syncSvc: syncSvc}
}

// HandleSendMessage processes incoming message ingestion via HTTP POST
func (h *MessageHandler) HandleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Check Idempotency Key to protect against network duplication retries
	idempotencyKey := r.Header.Get("X-Idempotency-Key")
	if idempotencyKey != "" {
		ctx := r.Context()
		isNew, err := h.rdb.SetNX(ctx, "idempotency:"+idempotencyKey, "locked", 30*time.Second).Result()
		if err != nil || !isNew {
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"error": "Duplicate request detected via Idempotency standard."}`))
			return
		}
	}

	// 2. Decode the incoming JSON payload from Postman
	var req struct {
		ConversationID string `json:"conversation_id"`
		SenderID       string `json:"sender_id"`
		Content        string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid Payload", http.StatusBadRequest)
		return
	}

	// 3. Map the request to our core domain structure
	msg := &domain.Message{
		ConversationID: req.ConversationID,
		SenderID:       req.SenderID,
		Content:        req.Content,
	}

	// 4. Pass the message directly to the Service Engine
	if err := h.svc.SendMessage(r.Context(), msg); err != nil {
		http.Error(w, "Internal Server Failure: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 5. Respond back with a 202 Accepted status along with our newly minted fields
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(msg)
}

// HandleDeltaSync handles historical message playback requests for returning offline users
func (h *SyncHandler) HandleDeltaSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req map[string]uint64
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid Synchronization Payload", http.StatusBadRequest)
		return
	}

	deltas, err := h.syncSvc.SyncDeltas(r.Context(), req)
	if err != nil {
		http.Error(w, "Synchronization tracking failure: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(deltas)
}
