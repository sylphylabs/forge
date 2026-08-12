package consul

import (
	"net"
	"testing"
	"time"
)

// consulAddr is the agent address every live test in this package dials.
const consulAddr = "127.0.0.1:8500"

// requireConsul skips the calling test unless a Consul agent answers on
// consulAddr. CI provides one as a job service. Without one, these tests hang
// on blocking queries or crash on dial errors, which reports an absent
// dependency as if the code were broken. Skipping states the requirement
// instead, and the message tells a reader how to run them.
func requireConsul(t *testing.T) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", consulAddr, time.Second)
	if err != nil {
		t.Skipf("no Consul agent on %s: %v", consulAddr, err)
	}
	_ = conn.Close()
}
