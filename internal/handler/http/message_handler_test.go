package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"

	"teamchat-backend/internal/domain"
	"teamchat-backend/internal/pubsub"
	"teamchat-backend/internal/repository"
	"teamchat-backend/internal/service"
)

// setupTestDependencies initializes a live test connection to your Docker-backed instances
func setupTestDependencies(t *testing.T) (*sql.DB, *redis.Client, *MessageHandler, *SyncHandler) {
	// Connect to local test MySQL running on port 3307
	db, err := sql.Open("mysql", "root:rootpassword@tcp(127.0.0.1:3307)/teamchat?parseTime=true")
	if err != nil {
		t.Fatalf("Failed to open test DB connection: %v", err)
	}

	// Connect to local test Redis running on port 6379
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})

	// Pre-seed the conversation room into MySQL to avoid foreign-key constraint failures
	_, _ = db.Exec("INSERT IGNORE INTO conversations (id, type) VALUES ('test-room', 'GROUP')")

	// Assemble clean architecture layers
	broker := pubsub.NewRedisBroker(rdb)
	msgSvc := service.NewMessageService(db, broker)
	msgHandler := NewMessageHandler(msgSvc, rdb)

	msgRepo := repository.NewMessageRepository(db)
	syncSvc := service.NewSyncService(msgRepo)
	syncHandler := NewSyncHandler(syncSvc)

	return db, rdb, msgHandler, syncHandler
}

// Test_HandleSendMessage_Success verifies that a valid message ingestion returns 202 Accepted
func Test_HandleSendMessage_Success(t *testing.T) {
	db, rdb, msgHandler, _ := setupTestDependencies(t)
	defer db.Close()
	defer rdb.Close()

	// Clear out any previous test idempotency token from Redis
	ctx := context.Background()
	rdb.Del(ctx, "idempotency:test-token-unique")

	// Formulate payload
	body := map[string]string{
		"conversation_id": "test-room",
		"sender_id":       "user-alice",
		"content":         "Automated Integration Test Message",
	}
	jsonBody, _ := json.Marshal(body)

	// Mock HTTP Request and Response Recorder
	req := httptest.NewRequest(http.MethodPost, "/api/v1/messages", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Idempotency-Key", "test-token-unique") // Unique transactional key
	w := httptest.NewRecorder()

	// Execute target method
	msgHandler.HandleSendMessage(w, req)

	// Assert HTTP Status Code
	if w.Code != http.StatusAccepted {
		t.Errorf("Expected status 202 Accepted, got %d", w.Code)
	}

	// Assert Response Structure containing generated ID
	var resp domain.Message
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.ID == 0 {
		t.Error("Expected server to generate a valid sequence ID, got 0")
	}
}

// Test_HandleSendMessage_IdempotencyConflict verifies that identical transaction tokens are blocked
func Test_HandleSendMessage_IdempotencyConflict(t *testing.T) {
	db, rdb, msgHandler, _ := setupTestDependencies(t)
	defer db.Close()
	defer rdb.Close()

	ctx := context.Background()
	rdb.Del(ctx, "idempotency:conflict-token")

	body := map[string]string{
		"conversation_id": "test-room",
		"sender_id":       "user-alice",
		"content":         "Trying to submit duplicate payloads",
	}
	jsonBody, _ := json.Marshal(body)

	// --- Execution 1: First Attempt ---
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/messages", bytes.NewBuffer(jsonBody))
	req1.Header.Set("X-Idempotency-Key", "conflict-token")
	w1 := httptest.NewRecorder()
	msgHandler.HandleSendMessage(w1, req1)

	if w1.Code != http.StatusAccepted {
		t.Fatalf("First message setup failed with code: %d", w1.Code)
	}

	// --- Execution 2: Direct Duplicate Attempt ---
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/messages", bytes.NewBuffer(jsonBody))
	req2.Header.Set("X-Idempotency-Key", "conflict-token") // Re-using the identical token key
	w2 := httptest.NewRecorder()
	msgHandler.HandleSendMessage(w2, req2)

	// Assert that the request was successfully intercepted and blocked
	if w2.Code != http.StatusConflict {
		t.Errorf("Expected status 409 Conflict for duplicate token, got %d", w2.Code)
	}
}

// Test_HandleDeltaSync_Success verifies that historical missed updates are pulled properly
func Test_HandleDeltaSync_Success(t *testing.T) {
	db, rdb, msgHandler, syncHandler := setupTestDependencies(t)
	defer db.Close()
	defer rdb.Close()

	// 1. Seed Baseline Message (The point Bob went offline)
	b1, _ := json.Marshal(map[string]string{"conversation_id": "test-room", "sender_id": "alice", "content": "Baseline"})
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/messages", bytes.NewBuffer(b1))
	req1.Header.Set("X-Idempotency-Key", "sync-token-1")
	w1 := httptest.NewRecorder()
	msgHandler.HandleSendMessage(w1, req1)

	var baseMsg domain.Message
	_ = json.Unmarshal(w1.Body.Bytes(), &baseMsg)

	// 2. Seed Delta Message (The message Bob missed while offline)
	b2, _ := json.Marshal(map[string]string{"conversation_id": "test-room", "sender_id": "alice", "content": "Missed Delta Update"})
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/messages", bytes.NewBuffer(b2))
	req2.Header.Set("X-Idempotency-Key", "sync-token-2")
	w2 := httptest.NewRecorder()
	msgHandler.HandleSendMessage(w2, req2)

	// 3. Request Catch-Up Handshake for 'test-room' starting right AFTER the baseline message ID
	syncPayload := map[string]uint64{"test-room": baseMsg.ID}
	jsonSync, _ := json.Marshal(syncPayload)

	reqSync := httptest.NewRequest(http.MethodPost, "/api/v1/sync/deltas", bytes.NewBuffer(jsonSync))
	wSync := httptest.NewRecorder()

	// Execute delta recovery endpoint
	syncHandler.HandleDeltaSync(wSync, reqSync)

	if wSync.Code != http.StatusOK {
		t.Errorf("Expected status 200 OK for delta sync, got %d", wSync.Code)
	}

	// Decode returning synchronization window map
	var syncResponse map[string][]domain.Message
	if err := json.Unmarshal(wSync.Body.Bytes(), &syncResponse); err != nil {
		t.Fatalf("Failed to decode sync response map: %v", err)
	}

	// Verify that the missed update was captured
	messages, exists := syncResponse["test-room"]
	if !exists || len(messages) == 0 {
		t.Fatal("Delta sync failed: Expected returning missed message array, got empty block")
	}

	if messages[0].Content != "Missed Delta Update" {
		t.Errorf("Expected content 'Missed Delta Update', got '%s'", messages[0].Content)
	}
}
