package selector

import (
	"context"
	"testing"
)

func TestPeer(t *testing.T) {
	p := Peer{
		Node: NewNode("http", "127.0.0.1:8080", nil),
	}
	ctx := NewPeerContext(context.Background(), &p)
	p2, ok := FromPeerContext(ctx)
	if !ok || p2.Node == nil {
		t.Fatalf(" no peer found!")
	}
}

func TestNotPeer(t *testing.T) {
	_, ok := FromPeerContext(context.Background())
	if ok {
		t.Fatalf("test no peer found peer!")
	}
}
