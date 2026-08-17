package errors

import "sync"

// The contract registry records every identity declared through [MustDefine].
//
// Declaring a sentinel is the act that makes an identity part of a service's
// wire contract: generated *_errors.pb.go files call MustDefine for each
// Protobuf-declared error, and hand-written framework sentinels use the same
// constructor deliberately. [PublicOf] consults the registry so that only a
// declared identity — its Kind, domain, reason, message, metadata, and
// violations — leaves the process; an error assembled ad hoc with [Of],
// WithDomain, or WithReason projects as an internal failure instead.
//
// The registry is written during package initialization, when sentinels are
// constructed, and read on every boundary crossing. Registration is idempotent:
// re-declaring a pair is harmless, so tests and multiple packages may declare
// overlapping identities freely.
var contractRegistry = struct {
	sync.RWMutex
	m map[contractKey]struct{}
}{m: make(map[contractKey]struct{})}

// contractKey is a complete identity. Half an identity never registers:
// MustDefine rejects an empty domain or reason before registration.
type contractKey struct {
	domain string
	reason string
}

// registerContract records an identity as declared.
func registerContract(domain, reason string) {
	contractRegistry.Lock()
	contractRegistry.m[contractKey{domain: domain, reason: reason}] = struct{}{}
	contractRegistry.Unlock()
}

// isContract reports whether the identity pair was declared via MustDefine.
//
// It matches by identity rather than by value so that an error carrying a
// declared pair projects even when it was rebuilt along the way — a middleware
// that aggregates violations under a declared reason, for example, still
// speaks that contract.
func isContract(domain, reason string) bool {
	if domain == "" || reason == "" {
		return false
	}
	contractRegistry.RLock()
	_, ok := contractRegistry.m[contractKey{domain: domain, reason: reason}]
	contractRegistry.RUnlock()
	return ok
}
