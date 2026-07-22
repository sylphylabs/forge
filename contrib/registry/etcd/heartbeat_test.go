package etcd

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestHeartbeatRetriesUntilKeepAliveRecovers(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	initial := make(chan *clientv3.LeaseKeepAliveResponse)
	close(initial)
	recovered := make(chan *clientv3.LeaseKeepAliveResponse)
	var registerCalls atomic.Int32
	registered := make(chan struct{}, 2)
	r := &Registry{opts: &options{maxRetry: 3}}
	r.keepAliveFn = func(context.Context, clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error) {
		if registerCalls.Load() == 0 {
			return initial, nil
		}
		return recovered, nil
	}
	r.registerFn = func(context.Context, string, string) (clientv3.LeaseID, error) {
		call := registerCalls.Add(1)
		registered <- struct{}{}
		if call == 1 {
			return 0, errors.New("temporary failure")
		}
		return 2, nil
	}
	r.retryDelay = func(int) time.Duration { return 0 }
	done := make(chan struct{})
	go func() {
		r.heartBeat(ctx, 1, "key", "value")
		close(done)
	}()

	<-registered
	<-registered
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not stop after cancellation")
	}
	if got := registerCalls.Load(); got != 2 {
		t.Fatalf("registration attempts = %d, want 2", got)
	}
}

func TestHeartbeatStopsAfterRetryLimit(t *testing.T) {
	var calls atomic.Int32
	r := &Registry{opts: &options{maxRetry: 3}}
	r.keepAliveFn = func(context.Context, clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error) {
		return nil, errors.New("keepalive unavailable")
	}
	r.registerFn = func(context.Context, string, string) (clientv3.LeaseID, error) {
		calls.Add(1)
		return 0, errors.New("registration unavailable")
	}
	r.retryDelay = func(int) time.Duration { return 0 }
	done := make(chan struct{})
	go func() {
		r.heartBeat(t.Context(), 1, "key", "value")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat blocked after retry exhaustion")
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("registration attempts = %d, want 3", got)
	}
}
