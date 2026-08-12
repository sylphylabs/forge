package etcd

import (
	"net"
	"testing"
	"time"
)

// etcdAddr is the server address every live test in this package dials.
const etcdAddr = "127.0.0.1:2379"

// requireEtcd skips the calling test unless an etcd server answers on
// etcdAddr. CI provides one as a job service. Without one, these tests hang
// inside the client's retry loop, which reports an absent dependency as if
// the code were broken. Skipping states the requirement instead, and the
// message tells a reader how to run them.
func requireEtcd(t *testing.T) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", etcdAddr, time.Second)
	if err != nil {
		t.Skipf("no etcd server on %s: %v", etcdAddr, err)
	}
	_ = conn.Close()
}
