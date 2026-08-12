package config

import "context"

// KeyValue is one raw configuration payload as a source produced it: an
// opaque byte value under a key, tagged with the encoding format a decoder
// needs to interpret it. An empty Format means the value is a single scalar
// stored directly under the key.
type KeyValue struct {
	Key    string
	Value  []byte
	Format string
}

// Source provides configuration payloads and change notification for one
// backing store — a file, the process environment, a remote config service.
//
// Both methods take the caller's context because a source may block on I/O;
// implementations must honor cancellation. The same reasoning gives
// [github.com/sylphylabs/forge/registry.Discovery] its contexts.
type Source interface {
	// Load returns every payload the source currently holds.
	Load(ctx context.Context) ([]*KeyValue, error)
	// Watch returns a watcher that reports subsequent changes to the source.
	Watch(ctx context.Context) (Watcher, error)
}

// Watcher reports changes in one source.
type Watcher interface {
	// Next blocks until the source changes, ctx is done, or the watcher is
	// stopped. The returned key-values are advisory: the coordinator reloads
	// every source after any change, so a watcher that cannot enumerate what
	// changed may return (nil, nil).
	Next(ctx context.Context) ([]*KeyValue, error)
	// Stop releases the watcher's resources and unblocks an in-flight Next.
	// It must be safe to call more than once.
	Stop() error
}
