// Integration tests for the HTTP handlers.
//
// These tests require a live MySQL (port 3307) and Redis (port 6379) instance.
// Start them with: docker compose -f deployments/docker-compose.yml up -d
//
// Run tests: go test ./internal/handler/http/... -v
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

	"teamchat/api-server/internal/service"
	"teamchat/shared/domain"
	"teamchat/shared/pubsub"
	"teamchat/shared/repository"
	"teamchat/shared/snowflake"
)

// Test fixture IDs — kept as constants so every test seeds and asserts
// against the same rows without hard-coded string literals scattered around.
const (
	testRoomID   = "test-room"
	testUserID   = "user-alice"
	testUsername = "alice"
)

// testDeps bundles all the wired-up layers needed by handler tests.
type testDeps struct {
	db          *sql.DB
	rdb         *redis.Client
	msgHandler  *MessageHandler
	syncHandler *SyncHandler
}

// setup initialises a live test connection to the Docker-backed MySQL and Redis
// instances, seeds prerequisite rows, and assembles all handler dependencies.
//
// Call t.Cleanup with td.close() or use defer td.close() to release resources.
func setup(t *testing.T) *testDeps {
	t.Helper()

	db, err := sql.Open("mysql", "root:rootpassword@tcp(127.0.0.1:3307)/teamchat?parseTime=true")
	if err != nil {
		t.Fatalf("DB open: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("MySQL not reachable (is docker compose up?): %v", err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})

	ctx := context.Background()

	// Seed the test user so foreign-key constraints on channel_members pass.
	userRepo := repository.NewUserRepository(db)
	if err := userRepo.UpsertForTest(ctx, testUserID, testUsername); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Seed the test channel (conversation).
	_, _ = db.ExecContext(ctx,
		"INSERT IGNORE INTO conversations (id, type) VALUES (?, 'GROUP')", testRoomID)

	// Seed membership so the sender passes the membership guard in MessageService.
	chanRepo := repository.NewChannelRepository(db)
	if err := chanRepo.AddMember(ctx, testRoomID, testUserID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	// Assemble the dependency graph.
	sf         := snowflake.NewGenerator(1)
	broker     := pubsub.NewRedisBroker(rdb)
	cacheRepo  := repository.NewCacheRepository(rdb)
	msgSvc     := service.NewMessageService(db, broker, chanRepo, cacheRepo, sf)
	msgHandler := NewMessageHandler(msgSvc)

	msgRepo     := repository.NewMessageRepository(db)
	syncSvc     := service.NewSyncService(msgRepo)
	syncHandler := NewSyncHandler(syncSvc)

	return &testDeps{db: db, rdb: rdb, msgHandler: msgHandler, syncHandler: syncHandler}
}

func (td *testDeps) close() {
	td.db.Close()
	td.rdb.Close()
}

// ─── Test: successful message send ──────────────────────────────────────────

// Test_HandleSendMessage_Success verifies that a well-formed request returns
// 202 Accepted and a populated Message with a non-zero server-generated ID.
func Test_HandleSendMessage_Success(t *testing.T) {
	td := setup(t)
	defer td.close()

	// Clear any leftover idempotency key from a previous test run
	td.rdb.Del(context.Background(), "idempotency:test-send-ok")

	body, _ := json.Marshal(map[string]string{
		"conversation_id": testRoomID,
		"sender_id":       testUserID,
		"content":         "Hello, world!",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/messages", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Idempotency-Key", "test-send-ok")
	w := httptest.NewRecorder()

	td.msgHandler.HandleSendMessage(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d — body: %s", w.Code, w.Body.String())
	}

	var resp domain.Message
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID == 0 {
		t.Error("expected a non-zero Snowflake ID in the response")
	}
}

// ─── Test: idempotency conflict ──────────────────────────────────────────────

// Test_HandleSendMessage_IdempotencyConflict verifies that resubmitting a
// request with the same idempotency key returns 409 Conflict.
func Test_HandleSendMessage_IdempotencyConflict(t *testing.T) {
	td := setup(t)
	defer td.close()

	td.rdb.Del(context.Background(), "idempotency:conflict-key")

	body, _ := json.Marshal(map[string]string{
		"conversation_id": testRoomID,
		"sender_id":       testUserID,
		"content":         "First attempt",
	})

	sendOnce := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/messages", bytes.NewBuffer(body))
		req.Header.Set("X-Idempotency-Key", "conflict-key")
		w := httptest.NewRecorder()
		td.msgHandler.HandleSendMessage(w, req)
		return w
	}

	// First request should succeed
	if w := sendOnce(); w.Code != http.StatusAccepted {
		t.Fatalf("first request: expected 202, got %d", w.Code)
	}

	// Second request with the same key should be rejected
	if w := sendOnce(); w.Code != http.StatusConflict {
		t.Errorf("duplicate request: expected 409 Conflict, got %d", w.Code)
	}
}

// ─── Test: non-member is forbidden ───────────────────────────────────────────

// Test_HandleSendMessage_NonMemberForbidden verifies that a user who is not a
// member of the target channel receives 403 Forbidden.
func Test_HandleSendMessage_NonMemberForbidden(t *testing.T) {
	td := setup(t)
	defer td.close()

	body, _ := json.Marshal(map[string]string{
		"conversation_id": testRoomID,
		"sender_id":       "outsider-user", // not in channel_members
		"content":         "I should not be allowed here",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/messages", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	td.msgHandler.HandleSendMessage(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d — body: %s", w.Code, w.Body.String())
	}
}

// ─── Test: delta sync returns missed messages ─────────────────────────────────

// Test_HandleDeltaSync_Success verifies that a client reconnecting after being
// offline receives all messages sent during its absence.
func Test_HandleDeltaSync_Success(t *testing.T) {
	td := setup(t)
	defer td.close()

	ctx := context.Background()
	// Clean up idempotency keys so this test is repeatable
	td.rdb.Del(ctx, "idempotency:delta-baseline", "idempotency:delta-missed")

	// ── Step 1: Send a "baseline" message (last message the offline client saw)
	sendMsg := func(content, ikey string) domain.Message {
		body, _ := json.Marshal(map[string]string{
			"conversation_id": testRoomID,
			"sender_id":       testUserID,
			"content":         content,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/messages", bytes.NewBuffer(body))
		req.Header.Set("X-Idempotency-Key", ikey)
		w := httptest.NewRecorder()
		td.msgHandler.HandleSendMessage(w, req)
		if w.Code != http.StatusAccepted {
			t.Fatalf("setup message %q failed: %d — %s", content, w.Code, w.Body.String())
		}
		var m domain.Message
		json.Unmarshal(w.Body.Bytes(), &m)
		return m
	}

	baseline := sendMsg("Baseline — client was online here", "delta-baseline")

	// ── Step 2: Send the message the client missed while offline
	sendMsg("Missed message — client was offline", "delta-missed")

	// ── Step 3: Client reconnects and requests a catch-up starting after baseline
	catchUp, _ := json.Marshal(map[string]uint64{testRoomID: baseline.ID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync/deltas", bytes.NewBuffer(catchUp))
	w := httptest.NewRecorder()
	td.syncHandler.HandleDeltaSync(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delta sync: expected 200 OK, got %d — %s", w.Code, w.Body.String())
	}

	var deltas map[string][]domain.Message
	if err := json.Unmarshal(w.Body.Bytes(), &deltas); err != nil {
		t.Fatalf("decode delta response: %v", err)
	}

	msgs, ok := deltas[testRoomID]
	if !ok || len(msgs) == 0 {
		t.Fatal("expected at least one missed message in the delta response")
	}
	if msgs[0].Content != "Missed message — client was offline" {
		t.Errorf("unexpected content: %q", msgs[0].Content)
	}
}
