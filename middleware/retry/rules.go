package retry

import (
	"fmt"
	"time"

	"github.com/sylphylabs/forge/config"
)

// Rule is the configuration shape of one retry rule. A zero field keeps the
// default policy's value for that field, so a rule may tighten only what it
// names.
//
// In configuration, a governance section maps each operation — plus the "*"
// fallback — to one Rule:
//
//	governance:
//	  retry:
//	    "*":
//	      attempts: 2
//	    /helloworld.Greeter/SayHello:
//	      attempts: 4
//	      base_backoff: 50ms
//	      max_backoff: 2s
type Rule struct {
	// Attempts is the maximum number of attempts including the first;
	// 1 disables retries. Zero keeps the default.
	Attempts int `json:"attempts"`
	// BaseBackoff is the first retry's wait bound as a Go duration
	// string, such as "100ms". Empty keeps the default.
	BaseBackoff string `json:"base_backoff"`
	// MaxBackoff is the wait bound cap as a Go duration string, such as
	// "1s". Empty keeps the default.
	MaxBackoff string `json:"max_backoff"`
}

// ParseRule builds a [Policy] from one config rule, for use as the parse
// function of [governance.Watch]. It rejects rules that would be unsafe to
// serve — fewer than one attempt, an unparseable or non-positive backoff, a
// cap below the base — so that an invalid snapshot is refused as a whole and
// the previously installed policies keep governing calls.
func ParseRule(v config.Value) (Policy, error) {
	var r Rule
	if err := v.Scan(&r); err != nil {
		return Policy{}, fmt.Errorf("scan retry rule: %w", err)
	}
	p := defaultPolicy
	if r.Attempts != 0 {
		p.Attempts = r.Attempts
	}
	if r.BaseBackoff != "" {
		d, err := time.ParseDuration(r.BaseBackoff)
		if err != nil {
			return Policy{}, fmt.Errorf("base_backoff: %w", err)
		}
		p.BaseBackoff = d
	}
	if r.MaxBackoff != "" {
		d, err := time.ParseDuration(r.MaxBackoff)
		if err != nil {
			return Policy{}, fmt.Errorf("max_backoff: %w", err)
		}
		p.MaxBackoff = d
	}
	if err := p.Validate(); err != nil {
		return Policy{}, err
	}
	return p, nil
}
