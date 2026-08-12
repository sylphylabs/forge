package discovery

import (
	"context"
	"errors"
	"strings"
	"time"
	"uuid"

	"google.golang.org/grpc/resolver"

	"github.com/sylphylabs/forge/registry"
	"github.com/sylphylabs/forge/selector"
)

const name = "discovery"

// DefaultWatchTimeout bounds how long Build waits for the registry to create
// a watcher. Creating a watch is a one-time setup exchange with the registry,
// not an RPC, so it carries its own budget; override it with [WithTimeout].
const DefaultWatchTimeout = 10 * time.Second

var ErrWatcherCreateTimeout = errors.New("discovery create watcher overtime")

// Option is builder option.
type Option func(o *builder)

// WithTimeout sets how long Build waits for the registry to create a watcher.
// Zero or below means Build waits without a deadline.
func WithTimeout(timeout time.Duration) Option {
	return func(b *builder) {
		b.timeout = timeout
	}
}

// WithInsecure with isSecure option.
func WithInsecure(insecure bool) Option {
	return func(b *builder) {
		b.insecure = insecure
	}
}

// WithSubset with subset size.
func WithSubset(size int) Option {
	return func(b *builder) {
		b.subsetSize = size
	}
}

// WithSelector sets the load-balancing policy the balancer applies to the
// nodes this resolver reports.
//
// The value travels on every resolved address rather than through the
// balancer registry, because gRPC keys balancers by name in a map that is
// written only at init time and read without synchronization; address
// attributes are the one per-channel path that reaches a picker.
func WithSelector(sb selector.Builder) Option {
	return func(b *builder) {
		b.selectorBuilder = sb
	}
}

type builder struct {
	discoverer      registry.Discovery
	timeout         time.Duration
	insecure        bool
	subsetSize      int
	selectorBuilder selector.Builder
}

// NewBuilder creates a builder which is used to factory registry resolvers.
func NewBuilder(d registry.Discovery, opts ...Option) resolver.Builder {
	b := &builder{
		discoverer: d,
		timeout:    DefaultWatchTimeout,
		insecure:   false,
		subsetSize: 25,
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

func (b *builder) Build(target resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) (resolver.Resolver, error) {
	// w and err are written before done closes and read only after it closes,
	// on every path, so the goroutine and Build never touch them concurrently.
	var (
		w        registry.Watcher
		watchErr error
	)
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		w, watchErr = b.discoverer.Watch(ctx, strings.TrimPrefix(target.URL.Path, "/"))
		close(done)
	}()

	if b.timeout > 0 {
		timer := time.NewTimer(b.timeout)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			// Watch may still succeed after the deadline; nobody will use that
			// watcher, so cancel it and stop it once it materializes.
			cancel()
			go func() {
				<-done
				if w != nil {
					_ = w.Stop()
				}
			}()
			return nil, ErrWatcherCreateTimeout
		}
	} else {
		<-done
	}
	if watchErr != nil {
		cancel()
		return nil, watchErr
	}

	r := &discoveryResolver{
		w:               w,
		cc:              cc,
		ctx:             ctx,
		cancel:          cancel,
		insecure:        b.insecure,
		subsetSize:      b.subsetSize,
		selectorBuilder: b.selectorBuilder,
		selectorKey:     uuid.NewV4().String(),
	}
	go r.watch()
	return r, nil
}

// Scheme return scheme of discovery
func (*builder) Scheme() string {
	return name
}
