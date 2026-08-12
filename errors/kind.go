package errors

// Kind classifies a failure independently of any transport. It is the single
// source of truth for an error's category: transports project a Kind onto their
// own status vocabulary, and never the reverse.
//
// The set is closed and deliberately small. It mirrors the gRPC canonical codes
// because that vocabulary is the narrower of the two Forge speaks; projecting
// one way from the narrow space keeps every projection total and lossless.
//
// A Kind carries no transport vocabulary of its own. Each transport owns its
// projection, so that this package stays free of protocol dependencies:
// transport/http maps a Kind to an HTTP status, and transport/grpc to a gRPC
// code.
type Kind uint8

// The Kind vocabulary. KindUnknown is the zero value so that an unset Kind
// classifies as unknown rather than silently claiming a specific meaning.
const (
	// KindUnknown is an unclassified failure.
	KindUnknown Kind = iota
	// KindInvalidArgument means the caller supplied a malformed argument.
	KindInvalidArgument
	// KindFailedPrecondition means the system is not in the state the call requires.
	KindFailedPrecondition
	// KindOutOfRange means an argument was outside the valid range.
	KindOutOfRange
	// KindUnauthenticated means the caller could not be identified.
	KindUnauthenticated
	// KindPermissionDenied means the caller is known but not allowed.
	KindPermissionDenied
	// KindNotFound means the requested entity does not exist.
	KindNotFound
	// KindAlreadyExists means the entity the caller tried to create is present.
	KindAlreadyExists
	// KindConflict means a concurrent change prevented the call from completing.
	KindConflict
	// KindResourceExhausted means a quota or rate limit was reached.
	KindResourceExhausted
	// KindCanceled means the caller went away before the call completed.
	KindCanceled
	// KindDeadlineExceeded means the call outlived its deadline.
	KindDeadlineExceeded
	// KindUnavailable means a dependency is temporarily unreachable.
	KindUnavailable
	// KindUnimplemented means the operation is not supported.
	KindUnimplemented
	// KindInternal means an invariant was broken. It denotes a bug.
	KindInternal
	// KindDataLoss means data was lost or irrecoverably corrupted.
	KindDataLoss
)

// kindNames maps a Kind to its stable wire name. These strings are part of the
// contract: they appear in HTTP error bodies and MUST NOT change.
var kindNames = [...]string{
	KindUnknown:            "UNKNOWN",
	KindInvalidArgument:    "INVALID_ARGUMENT",
	KindFailedPrecondition: "FAILED_PRECONDITION",
	KindOutOfRange:         "OUT_OF_RANGE",
	KindUnauthenticated:    "UNAUTHENTICATED",
	KindPermissionDenied:   "PERMISSION_DENIED",
	KindNotFound:           "NOT_FOUND",
	KindAlreadyExists:      "ALREADY_EXISTS",
	KindConflict:           "CONFLICT",
	KindResourceExhausted:  "RESOURCE_EXHAUSTED",
	KindCanceled:           "CANCELED",
	KindDeadlineExceeded:   "DEADLINE_EXCEEDED",
	KindUnavailable:        "UNAVAILABLE",
	KindUnimplemented:      "UNIMPLEMENTED",
	KindInternal:           "INTERNAL",
	KindDataLoss:           "DATA_LOSS",
}

// String returns the stable wire name of the Kind.
func (k Kind) String() string {
	if int(k) >= len(kindNames) {
		return kindNames[KindUnknown]
	}
	return kindNames[k]
}

// ParseKind returns the Kind for a stable wire name, and reports whether the
// name was recognized. An unrecognized name yields KindUnknown.
func ParseKind(name string) (Kind, bool) {
	for i, n := range kindNames {
		if n == name {
			return Kind(i), true //nolint:gosec // index is bounded by kindNames
		}
	}
	return KindUnknown, false
}
