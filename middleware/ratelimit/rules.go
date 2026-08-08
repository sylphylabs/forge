package ratelimit

import (
	"fmt"
	"time"

	"github.com/sylphylabs/forge/config"
	internalratelimit "github.com/sylphylabs/forge/internal/ratelimit"
)

// Rule is the configuration shape of one rate-limit rule. It mirrors the
// tunables of the default BBR limiter; a zero field keeps that tunable's
// default.
//
// In configuration, a governance section maps each operation — plus the "*"
// fallback — to one Rule:
//
//	governance:
//	  ratelimit:
//	    "*":
//	      cpu_threshold: 800
//	    /helloworld.Greeter/SayHello:
//	      cpu_threshold: 900
//	      window: 5s
//	      bucket: 50
type Rule struct {
	// Window is the rolling statistics window as a Go duration string,
	// such as "10s". Empty keeps the limiter default.
	Window string `json:"window"`
	// Bucket is the number of buckets the window is divided into.
	// Zero keeps the limiter default.
	Bucket int `json:"bucket"`
	// CPUThreshold is the CPU usage, scaled from 0 to 1000, above which
	// the limiter starts shedding load. Zero keeps the limiter default.
	CPUThreshold int64 `json:"cpu_threshold"`
}

// ParseRule builds a [Limiter] from one config rule, for use as the parse
// function of [governance.Watch]. It rejects rules that would be unsafe to
// serve — an unparseable or non-positive window, a negative bucket count, a
// CPU threshold outside [0, 1000] — so that an invalid snapshot is refused
// as a whole and the previously installed limiters keep governing traffic.
//
// Each accepted rule yields a fresh limiter, so a snapshot change restarts
// the rolling statistics of every governed operation; the BBR window
// repopulates within seconds.
func ParseRule(v config.Value) (Limiter, error) {
	var r Rule
	if err := v.Scan(&r); err != nil {
		return nil, fmt.Errorf("scan rate-limit rule: %w", err)
	}
	var opts []internalratelimit.Option
	if r.Window != "" {
		d, err := time.ParseDuration(r.Window)
		if err != nil {
			return nil, fmt.Errorf("window: %w", err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("window must be positive, got %s", d)
		}
		opts = append(opts, internalratelimit.WithWindow(d))
	}
	if r.Bucket < 0 {
		return nil, fmt.Errorf("bucket must not be negative, got %d", r.Bucket)
	}
	if r.Bucket > 0 {
		opts = append(opts, internalratelimit.WithBucket(r.Bucket))
	}
	if r.CPUThreshold < 0 || r.CPUThreshold > 1000 {
		return nil, fmt.Errorf("cpu_threshold must be within [0, 1000], got %d", r.CPUThreshold)
	}
	if r.CPUThreshold > 0 {
		opts = append(opts, internalratelimit.WithCPUThreshold(r.CPUThreshold))
	}
	return internalratelimit.NewLimiter(opts...), nil
}
