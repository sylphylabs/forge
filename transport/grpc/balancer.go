package grpc

import (
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/metadata"

	"github.com/sylphylabs/forge/registry"
	"github.com/sylphylabs/forge/selector"
	"github.com/sylphylabs/forge/selector/wrr"
	"github.com/sylphylabs/forge/transport"
	"github.com/sylphylabs/forge/transport/grpc/resolver/discovery"
)

const (
	balancerName = "selector"
)

var (
	_ base.PickerBuilder = (*balancerBuilder)(nil)
	_ balancer.Picker    = (*balancerPicker)(nil)
)

// gRPC keys balancers by name in a map that its documentation requires be
// written only during initialization and that a ClientConn reads unsynchronized
// on every service-config update. One balancer is therefore registered here,
// once, and the per-client policy reaches its picker through address
// attributes instead of through a second registration.
func init() {
	b := base.NewBalancerBuilder(
		balancerName,
		&balancerBuilder{},
		base.Config{HealthCheck: true},
	)
	balancer.Register(b)
}

type balancerBuilder struct {
	// builder is the policy to apply when an address names none. It is set
	// only in tests; the registered balancer leaves it nil and falls back to
	// defaultSelectorBuilder.
	builder selector.Builder
}

// defaultSelectorBuilder is the policy for a client that configured none.
// Weighted round robin needs no feedback beyond the weights discovery already
// reports, so it behaves sensibly without tuning.
func defaultSelectorBuilder() selector.Builder {
	return wrr.NewBuilder()
}

// Build creates a grpc Picker.
func (b *balancerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	if len(info.ReadySCs) == 0 {
		// Block the RPC until a new picker is available via UpdateState().
		return base.NewErrPicker(balancer.ErrNoSubConnAvailable)
	}
	nodes := make([]*selector.Node, 0, len(info.ReadySCs))
	subConns := make(map[*selector.Node]balancer.SubConn, len(info.ReadySCs))
	// Every address in one channel comes from the same client, so the first
	// policy found describes the whole set.
	var configured selector.Builder
	for conn, info := range info.ReadySCs {
		ins, _ := info.Address.Attributes.Value("rawServiceInstance").(*registry.ServiceInstance)
		if configured == nil {
			configured = discovery.SelectorBuilderFromAddress(info.Address)
		}
		node := selector.NewNode("grpc", info.Address.Addr, ins)
		nodes = append(nodes, node)
		subConns[node] = conn
	}
	builder := configured
	if builder == nil {
		builder = b.builder
	}
	if builder == nil {
		builder = defaultSelectorBuilder()
	}
	p := &balancerPicker{
		selector: builder.Build(),
		subConns: subConns,
	}
	p.selector.Apply(nodes)
	return p
}

// balancerPicker is a grpc picker.
type balancerPicker struct {
	selector selector.Selector
	// subConns maps each applied node, by identity, to its connection. The
	// selector preserves node identity through Select, so the picked *Node
	// is always a key of this map.
	subConns map[*selector.Node]balancer.SubConn
}

// Pick pick instances.
func (p *balancerPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	var filters []selector.NodeFilter
	if tr, ok := transport.FromClientContext(info.Ctx); ok {
		if gtr, ok := tr.(*Transport); ok {
			filters = gtr.NodeFilters()
		}
	}

	n, done, err := p.selector.Select(info.Ctx, selector.WithNodeFilter(filters...))
	if err != nil {
		return balancer.PickResult{}, err
	}
	conn, ok := p.subConns[n]
	if !ok {
		// A filter substituted a node the picker never applied; there is no
		// connection to serve it.
		return balancer.PickResult{}, selector.ErrNoAvailable
	}

	return balancer.PickResult{
		SubConn: conn,
		Done: func(di balancer.DoneInfo) {
			done(info.Ctx, selector.DoneInfo{
				Err:           di.Err,
				BytesSent:     di.BytesSent,
				BytesReceived: di.BytesReceived,
				ReplyMetadata: Trailer(di.Trailer),
			})
		},
	}, nil
}

// Trailer is a grpc trailer MD.
type Trailer metadata.MD

// Get get a grpc trailer value.
func (t Trailer) Get(k string) string {
	v := metadata.MD(t).Get(k)
	if len(v) > 0 {
		return v[0]
	}
	return ""
}
