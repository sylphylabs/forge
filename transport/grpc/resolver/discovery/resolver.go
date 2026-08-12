package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"google.golang.org/grpc/attributes"
	"google.golang.org/grpc/resolver"

	"github.com/sylphylabs/forge/internal/endpoint"
	"github.com/sylphylabs/forge/internal/subset"
	"github.com/sylphylabs/forge/log"
	"github.com/sylphylabs/forge/registry"
	"github.com/sylphylabs/forge/selector"
)

// selectorBuilderKey names the address attribute carrying the client's
// load-balancing policy. It is an unexported empty struct type so no other
// package can read or overwrite the entry.
type selectorBuilderKey struct{}

// NewAddressWithSelectorBuilder returns addr carrying sb as the policy its
// channel should balance with. The balancer package uses it to build addresses
// in tests; production addresses are built by this resolver's update loop.
func NewAddressWithSelectorBuilder(addr resolver.Address, sb selector.Builder) resolver.Address {
	addr.Attributes = addr.Attributes.WithValue(selectorBuilderKey{}, sb)
	return addr
}

// SelectorBuilderFromAddress returns the load-balancing policy the client that
// owns this address configured, or nil when it did not configure one.
func SelectorBuilderFromAddress(addr resolver.Address) selector.Builder {
	if addr.Attributes == nil {
		return nil
	}
	builder, _ := addr.Attributes.Value(selectorBuilderKey{}).(selector.Builder)
	return builder
}

type discoveryResolver struct {
	w  registry.Watcher
	cc resolver.ClientConn

	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once

	insecure        bool
	selectorKey     string
	subsetSize      int
	selectorBuilder selector.Builder
}

func (r *discoveryResolver) watch() {
	for {
		select {
		case <-r.ctx.Done():
			return
		default:
		}
		ins, err := r.w.Next(r.ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Error("[resolver] failed to watch discovery endpoint", "error", err)
			select {
			case <-r.ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		// Next may have returned an update raced with Close; a closed resolver
		// must not push state into the connection.
		select {
		case <-r.ctx.Done():
			return
		default:
		}
		r.update(ins)
	}
}

func (r *discoveryResolver) update(ins []*registry.ServiceInstance) {
	var (
		endpoints = make(map[string]struct{})
		filtered  = make([]*registry.ServiceInstance, 0, len(ins))
	)
	for _, in := range ins {
		ept, err := endpoint.ParseEndpoint(in.Endpoints, endpoint.Scheme("grpc", !r.insecure))
		if err != nil {
			log.Error("[resolver] failed to parse discovery endpoint", "error", err)
			continue
		}
		if ept == "" {
			continue
		}
		// filter redundant endpoints
		if _, ok := endpoints[ept]; ok {
			continue
		}
		endpoints[ept] = struct{}{}
		filtered = append(filtered, in)
	}
	if r.subsetSize != 0 {
		filtered = subset.Subset(r.selectorKey, filtered, r.subsetSize)
	}

	addrs := make([]resolver.Address, 0, len(filtered))
	for _, in := range filtered {
		ept, _ := endpoint.ParseEndpoint(in.Endpoints, endpoint.Scheme("grpc", !r.insecure))
		attrs := parseAttributes(in.Metadata).WithValue("rawServiceInstance", in)
		if r.selectorBuilder != nil {
			attrs = attrs.WithValue(selectorBuilderKey{}, r.selectorBuilder)
		}
		addr := resolver.Address{
			ServerName: in.Name,
			Attributes: attrs,
			Addr:       ept,
		}
		addrs = append(addrs, addr)
	}
	if len(addrs) == 0 {
		log.Warn("[resolver] zero endpoint found, refused to write", "instances", ins)
		return
	}
	err := r.cc.UpdateState(resolver.State{Addresses: addrs})
	if err != nil {
		log.Error("[resolver] failed to update state", "error", err)
	}

	b, _ := json.Marshal(filtered)
	log.Info("[resolver] update instances", "instances", string(b))
}

// Close terminates the watch goroutine and stops the watcher. It is
// idempotent, so a second call — gRPC tearing down a connection a caller
// already closed — does not stop the watcher twice.
func (r *discoveryResolver) Close() {
	r.closeOnce.Do(func() {
		r.cancel()
		err := r.w.Stop()
		if err != nil {
			log.Error("[resolver] failed to stop watcher", "error", err)
		}
	})
}

func (r *discoveryResolver) ResolveNow(_ resolver.ResolveNowOptions) {}

func parseAttributes(md map[string]string) (a *attributes.Attributes) {
	for k, v := range md {
		a = a.WithValue(k, v)
	}
	return a
}
