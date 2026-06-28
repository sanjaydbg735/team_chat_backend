package pubsub

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"
)

// RedisBroker implements the Broker interface using Redis Pub/Sub.
//
// Why Redis Pub/Sub over Kafka?
//
// Kafka is built for durable, replayable event streaming — it stores messages
// on disk and allows consumers to rewind.  For a chat application, MySQL is
// already the durable store; offline users fetch missed messages via the
// /sync/deltas REST endpoint.  Redis Pub/Sub is purely in-memory and therefore
// significantly faster for the real-time fan-out use case.  The trade-off is
// that a Redis restart drops in-flight messages — acceptable because the
// HTTP delta-sync path provides the correctness guarantee.
type RedisBroker struct {
	client *redis.Client
}

// NewRedisBroker creates a RedisBroker backed by the given Redis client.
func NewRedisBroker(client *redis.Client) *RedisBroker {
	return &RedisBroker{client: client}
}

// Publish JSON-serialises payload and publishes it to channelName.
// All WebSocket workers subscribed to that Redis channel will receive the
// message and forward it to any connected client in that room.
func (r *RedisBroker) Publish(ctx context.Context, channelName string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return r.client.Publish(ctx, channelName, data).Err()
}
