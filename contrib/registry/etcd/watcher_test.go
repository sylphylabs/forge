package etcd

import (
	"context"
	"errors"
	"testing"

	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestWatcherClosedChannelHonorsCancellation(t *testing.T) {
	for range 100 {
		ctx, cancel := context.WithCancel(t.Context())
		watchChan := make(chan clientv3.WatchResponse)
		close(watchChan)
		cancel()
		w := &watcher{ctx: ctx, watchChan: watchChan}
		_, err := w.Next()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Next() error = %v, want context.Canceled", err)
		}
	}
}
