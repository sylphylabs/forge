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

var ErrWatcherCreateTimeout = errors.New("discovery create watcher overtime")

// Option is builder option.
type Option func(o *builder)

// WithTimeout with timeout option.
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
		timeout:    time.Second * 10,
		insecure:   false,
		subsetSize: 25,
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

func (b *builder) Build(target resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) (resolver.Resolver, error) {
	watchRes := &struct {
		err error
		w   registry.Watcher
	}{}

	done := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		w, err := b.discoverer.Watch(ctx, strings.TrimPrefix(target.URL.Path, "/"))
		watchRes.w = w
		watchRes.err = err
		close(done)
	}()

	var err error
	if b.timeout > 0 {
		select {
		case <-done:
			err = watchRes.err
		case <-time.After(b.timeout):
			err = ErrWatcherCreateTimeout
		}
	} else {
		<-done
		err = watchRes.err
	}
	if err != nil {
		cancel()
		return nil, err
	}

	r := &discoveryResolver{
		w:               watchRes.w,
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
