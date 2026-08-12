package config

import (
	"net"
	"testing"
	"time"
)

// nacosAddr is the server address every live test in this package dials.
const nacosAddr = "127.0.0.1:8848"

// requireNacos skips the calling test unless a Nacos server answers on
// nacosAddr. CI provides one as a job service. Without one, these tests hang
// inside the client's retry loop, which reports an absent dependency as if
// the code were broken. Skipping states the requirement instead, and the
// message tells a reader how to run them.
func requireNacos(t *testing.T) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", nacosAddr, time.Second)
	if err != nil {
		t.Skipf("no Nacos server on %s: %v", nacosAddr, err)
	}
	_ = conn.Close()
}
