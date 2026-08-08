package forge

import "fmt"

// Suite bundles application options that belong together. An integration —
// a service registry plus its lifecycle hooks, a logging setup plus the
// metadata it reads — implements Suite once, and an application adopts the
// whole bundle with a single [WithSuite] call instead of repeating each
// option.
//
// A Suite carries no state of its own inside the application: everything it
// contributes goes through the returned options, so two applications may hold
// differently configured instances of the same Suite type without sharing
// anything. Suites compose: the slice returned by Options may itself contain
// options produced by [WithSuite], and independent suites written without
// knowledge of each other stack in a single option list.
type Suite interface {
	// Options returns the options this suite contributes, in the order they
	// should apply. Every element must be non-nil.
	Options() []Option
}

// WithSuite expands a Suite into a single Option. The suite's options apply
// in place, exactly where the returned Option appears in the caller's option
// list, in the order Options returned them. Options that set the same field
// keep their usual semantics: the one applied last wins, whether it came from
// a suite or was written directly.
//
// WithSuite calls Options once, immediately, so a suite is read at the point
// it is wired, not when the application is constructed. It panics right away
// if s is nil or if Options returns a nil element; a broken wiring fails at
// the offending line during construction rather than surfacing later.
func WithSuite(s Suite) Option {
	if s == nil {
		panic("forge: WithSuite called with a nil Suite")
	}
	opts := s.Options()
	for i, opt := range opts {
		if opt == nil {
			panic(fmt.Sprintf("forge: suite %T returned a nil Option at index %d", s, i))
		}
	}
	return func(o *options) {
		for _, opt := range opts {
			opt(o)
		}
	}
}
