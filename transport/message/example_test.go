package message_test

// The example in this file mirrors the message-transport snippet in
// docs/agent/middleware.md so that the guide cannot drift from the API
// without breaking the build. When it stops compiling, fix the guide
// together with the example.

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/sylphylabs/forge/middleware/logging"
	"github.com/sylphylabs/forge/middleware/recovery"
	"github.com/sylphylabs/forge/transport/message"
)

// nopSubscriber stands in for a broker adapter (contrib/message/...).
type nopSubscriber struct{}

func (nopSubscriber) Subscribe(context.Context, string, message.Handler) (message.Subscription, error) {
	return nopSubscription{}, nil
}

type nopSubscription struct{}

func (nopSubscription) Close(context.Context) error { return nil }

// Example_serverWideMiddleware mirrors "Attaching server middleware »
// Server-wide": message servers take the same construction-time middleware
// option as HTTP and gRPC servers.
func Example_serverWideMiddleware() {
	logger := slog.Default()
	subscriber := nopSubscriber{}

	msgSrv := message.NewServer(subscriber,
		message.WithMiddleware(recovery.Recovery(), logging.Server(logger)),
	)

	_ = msgSrv
	fmt.Println("constructed")
	// Output: constructed
}
