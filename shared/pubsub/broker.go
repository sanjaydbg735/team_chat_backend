// Package pubsub defines the message broadcast abstraction used by the service layer.
//
// The Broker interface decouples services from the specific Pub/Sub technology
// (Redis today, Kafka or NATS tomorrow).  Services call Publish with a channel
// name and a payload; the implementation handles serialisation and transport.
package pubsub

import "context"

// Broker is the interface the service layer uses to broadcast messages to all
// WebSocket workers subscribed to a given channel.
//
// Implementing this interface with a mock makes MessageService unit-testable
// without a real Redis instance.
type Broker interface {
	// Publish serialises payload to JSON and broadcasts it to all subscribers
	// of channelName.
	//
	// channelName convention: "chat:room:{conversationID}"
	// payload: any JSON-serialisable value (typically *domain.Message)
	Publish(ctx context.Context, channelName string, payload interface{}) error
}
