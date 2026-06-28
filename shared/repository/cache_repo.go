package repository

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// CacheRepository wraps Redis for ephemeral, low-latency operations that do not
// require durable storage: idempotency locks, presence flags, and short-lived
// counters.  None of the data here is the source of truth — MySQL is.
type CacheRepository struct {
	client *redis.Client
}

// NewCacheRepository constructs a CacheRepository backed by the given Redis client.
func NewCacheRepository(client *redis.Client) *CacheRepository {
	return &CacheRepository{client: client}
}

// AcquireIdempotencyLock attempts to atomically claim an idempotency key in Redis
// using SET NX (set-if-not-exists).
//
// Returns true if this is the first time the key is seen (proceed with the
// operation), or false if it already exists (duplicate request — return 409).
//
// Why Redis instead of a DB constraint?
//   A DB UNIQUE constraint on idempotency keys would require a dedicated table,
//   and checking+writing it would add latency to every request.  Redis SET NX is
//   a single O(1) in-memory operation, making it the industry-standard approach.
//
// The TTL mirrors the client's retry window.  30 seconds covers most mobile-network
// retry storms while keeping Redis memory usage negligible.
func (r *CacheRepository) AcquireIdempotencyLock(ctx context.Context, key string, ttl time.Duration) (acquired bool, err error) {
	// SetNX returns true only if the key was newly created
	return r.client.SetNX(ctx, "idempotency:"+key, "1", ttl).Result()
}
