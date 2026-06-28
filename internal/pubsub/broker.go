package pubsub

import "context"

type Broker interface {
	Publish(ctx context.Context, channel string, message interface{}) error
}
