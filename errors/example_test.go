package errors_test

// The examples in this file mirror the snippets in docs/agent/errors.md so
// that the guide cannot drift from the API without breaking the build. When
// one of these stops compiling, fix the guide together with the example.

import (
	"fmt"

	"github.com/sylphylabs/forge/errors"
)

// ErrNotFound mirrors the sentinel protoc-gen-go-errors emits for a Protobuf
// error enum value ("Declaring contract errors in Protobuf" in the guide).
var ErrNotFound = errors.MustDefine(
	errors.KindNotFound,
	"sylphy.test.v1",
	"FAILURE_REASON_NOT_FOUND",
)

// Example_returning mirrors "Returning an error": deriving from an immutable
// sentinel with a message, metadata, and a wrapped cause.
func Example_returning() {
	name := "answers/42"
	tenantID := "acme"
	cause := fmt.Errorf("stale replica")

	err := error(ErrNotFound.
		Msgf("document %q", name).
		Meta("tenant", tenantID).
		Wrap(cause))

	fmt.Println(errors.Is(err, ErrNotFound))
	fmt.Println(errors.Unwrap(err) == cause)
	// Output:
	// true
	// true
}

// Example_localFailure mirrors the guide's process-local error: a failure
// that never leaves the process needs no Protobuf declaration.
func Example_localFailure() {
	cause := fmt.Errorf("checksum mismatch")

	err := error(errors.Of(errors.KindInternal).WithReason("CACHE_CORRUPT").Wrap(cause))

	fmt.Println(errors.KindOf(err))
	fmt.Println(errors.ReasonOf(err))
	// Output:
	// INTERNAL
	// CACHE_CORRUPT
}

// Example_inspecting mirrors "Inspecting an error": the standard library
// vocabulary plus KindOf for classification-only matching.
func Example_inspecting() {
	err := error(ErrNotFound.Msg("document \"42\" not found"))

	if errors.Is(err, ErrNotFound) {
		fmt.Println("matched by identity")
	}

	var e *errors.Error
	if errors.As(err, &e) {
		fmt.Println(e.Domain())
	}

	switch errors.KindOf(err) {
	case errors.KindNotFound:
		fmt.Println("not found")
	case errors.KindUnavailable:
		fmt.Println("unavailable")
	}
	// Output:
	// matched by identity
	// sylphy.test.v1
	// not found
}

// Example_violations mirrors "Aggregating field failures": a validation pass
// reports everything it found, and Err returns nil when nothing was recorded.
func Example_violations() {
	age := -3

	var v errors.Violations
	v.Add("email", "malformed")
	v.Addf("age", "must be positive, got %d", age)
	err := v.Err(errors.KindInvalidArgument)

	fmt.Println(errors.KindOf(err))
	fmt.Println(len(errors.FromError(err).Violations()))

	var empty errors.Violations
	fmt.Println(empty.Err(errors.KindInvalidArgument) == nil)
	// Output:
	// INVALID_ARGUMENT
	// 2
	// true
}

// Example_public mirrors "Choosing what to disclose": a transport serializes
// only PublicOf(err), and FromPublic rebuilds a remote error from the same
// facts — with no cause.
func Example_public() {
	cause := fmt.Errorf("dsn: postgres://user:hunter2@db/prod")
	err := error(ErrNotFound.Msg("document not found").Wrap(cause))

	public := errors.PublicOf(err)
	fmt.Println(public.Kind, public.Domain, public.Reason)

	remote := errors.FromPublic(public)
	fmt.Println(errors.Is(remote, ErrNotFound))
	fmt.Println(errors.Unwrap(remote) == nil) // the cause chain does not cross the boundary
	// Output:
	// NOT_FOUND sylphy.test.v1 FAILURE_REASON_NOT_FOUND
	// true
	// true
}

// Example_foreignError mirrors the guide's warning: a bare non-Forge error
// arrives at the boundary as KindUnknown with nothing to grep for.
func Example_foreignError() {
	err := fmt.Errorf("some dependency failed")

	fmt.Println(errors.KindOf(err))
	fmt.Println(errors.ReasonOf(err) == "")
	// Output:
	// UNKNOWN
	// true
}
