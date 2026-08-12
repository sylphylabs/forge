// Package ewma wraps nodes with exponentially weighted moving averages of
// their observed latency and success rate, so a balancer can prefer nodes
// that are currently fast and healthy over ones that are merely declared
// heavy.
package ewma

import (
	"context"
	"math"
	"net"
	"sync/atomic"
	"time"

	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/selector"
)

const (
	// tau is the mean lifetime of the moving average; it reaches its
	// half-life after tau*ln(2).
	tau = int64(time.Millisecond * 600)
	// penalty is the lag charged to a node that has produced no statistics
	// yet, so an unmeasured node is not mistaken for a fast one.
	penalty = uint64(time.Microsecond * 100)
)

var (
	_ selector.WeightedNode        = (*Node)(nil)
	_ selector.WeightedNodeBuilder = (*Builder)(nil)
)

// Node is a weighted node whose effective weight follows the latency and
// error feedback its DoneFuncs report.
type Node struct {
	node *selector.Node

	// client statistic data
	lag       atomic.Int64
	success   atomic.Uint64
	inflight  atomic.Int64
	inflights [200]atomic.Int64
	// last collected timestamp
	stamp atomic.Int64
	// request number in a period time
	reqs atomic.Int64
	// last lastPick timestamp
	lastPick atomic.Int64

	errHandler   func(err error) (isErr bool)
	cachedWeight *atomic.Value
}

type nodeWeight struct {
	value    float64
	updateAt int64
}

// Builder wraps nodes as EWMA nodes.
type Builder struct {
	// ErrHandler classifies request errors as health failures. It runs
	// before the built-in classification, which treats deadline, transport
	// unavailability, and network errors as failures.
	ErrHandler func(err error) (isErr bool)
}

// Build wraps n.
func (b *Builder) Build(n *selector.Node) selector.WeightedNode {
	s := &Node{
		node:         n,
		inflights:    [200]atomic.Int64{},
		errHandler:   b.ErrHandler,
		cachedWeight: &atomic.Value{},
	}
	s.success.Store(1000)
	s.inflight.Store(1)
	return s
}

func (n *Node) health() uint64 {
	return n.success.Load()
}

func (n *Node) load() (load uint64) {
	now := time.Now().UnixNano()
	avgLag := n.lag.Load()
	predict := n.predict(avgLag, now)

	if avgLag == 0 {
		// A node with no latency data yet is charged the penalty so it is
		// tried, but not flooded, until real statistics arrive.
		load = penalty * uint64(n.inflight.Load())
		return
	}
	if predict > avgLag {
		avgLag = predict
	}
	// Add 5ms to flatten the latency gap between zones before compressing
	// the scale.
	avgLag += int64(time.Millisecond * 5)
	avgLag = int64(math.Sqrt(float64(avgLag)))
	load = uint64(avgLag) * uint64(n.inflight.Load())
	return load
}

func (n *Node) predict(avgLag int64, now int64) (predict int64) {
	var (
		total    int64
		slowNum  int
		totalNum int
	)
	for i := range n.inflights {
		start := n.inflights[i].Load()
		if start != 0 {
			totalNum++
			lag := now - start
			if lag > avgLag {
				slowNum++
				total += lag
			}
		}
	}
	if slowNum >= (totalNum/2 + 1) {
		predict = total / int64(slowNum)
	}
	return
}

// Pick records a pick and returns the callback that folds the request's
// outcome into the node's statistics. The callback must run exactly once —
// see [selector.DoneFunc]; the in-flight count it decrements is incremented
// here.
func (n *Node) Pick() selector.DoneFunc {
	start := time.Now().UnixNano()
	n.lastPick.Store(start)
	n.inflight.Add(1)
	reqs := n.reqs.Add(1)
	slot := reqs % 200
	swapped := n.inflights[slot].CompareAndSwap(0, start)
	return func(_ context.Context, di selector.DoneInfo) {
		if swapped {
			n.inflights[slot].CompareAndSwap(start, 0)
		}
		n.inflight.Add(-1)

		now := time.Now().UnixNano()
		// get moving average ratio w
		stamp := n.stamp.Swap(now)
		td := now - stamp
		if td < 0 {
			td = 0
		}
		w := math.Exp(float64(-td) / float64(tau))

		lag := now - start
		if lag < 0 {
			lag = 0
		}
		oldLag := n.lag.Load()
		if oldLag == 0 {
			w = 0.0
		}
		lag = int64(float64(oldLag)*w + float64(lag)*(1.0-w))
		n.lag.Store(lag)

		success := uint64(1000) // health scale: 1000 healthy, 0 failed
		if isHealthFailure(di.Err, n.errHandler) {
			success = 0
		}
		oldSuc := n.success.Load()
		success = uint64(float64(oldSuc)*w + float64(success)*(1.0-w))
		n.success.Store(success)
	}
}

func isHealthFailure(err error, handler func(error) bool) bool {
	if err == nil {
		return false
	}
	if handler != nil && handler(err) {
		return true
	}
	var netErr net.Error
	kind := errors.KindOf(err)
	return errors.Is(err, context.DeadlineExceeded) ||
		kind == errors.KindUnavailable ||
		kind == errors.KindDeadlineExceeded ||
		errors.As(err, &netErr)
}

// Weight is the node's effective weight: health divided by load, cached
// briefly because it is read far more often than its inputs change.
func (n *Node) Weight() (weight float64) {
	w, ok := n.cachedWeight.Load().(*nodeWeight)
	now := time.Now().UnixNano()
	if !ok || time.Duration(now-w.updateAt) > (time.Millisecond*5) {
		health := n.health()
		load := n.load()
		weight = float64(health*uint64(time.Microsecond)*10) / float64(load)
		n.cachedWeight.Store(&nodeWeight{
			value:    weight,
			updateAt: now,
		})
	} else {
		weight = w.value
	}
	return
}

// PickElapsed is the time since the most recent pick.
func (n *Node) PickElapsed() time.Duration {
	return time.Duration(time.Now().UnixNano() - n.lastPick.Load())
}

// Raw returns the underlying node.
func (n *Node) Raw() *selector.Node {
	return n.node
}
