package errors

import "fmt"

// Violation is a single field-level failure within an aggregate error.
type Violation struct {
	// Field identifies what failed, as a path into the request message
	// (for example "user.email" or "items[2].quantity").
	Field string
	// Description explains the failure in terms a caller can act on.
	Description string
}

// Violations collects field-level failures so that a validation pass can report
// everything it found rather than only the first problem.
//
// The zero value is ready to use:
//
//	var v errors.Violations
//	v.Add("email", "malformed")
//	v.Add("age", "must be positive")
//	if !v.Empty() {
//		return v.Err(errors.KindInvalidArgument)
//	}
//
// Every entry survives the RPC boundary. Note that [Join] does not aggregate in
// this sense: a joined error can only project one status onto the wire, so the
// others would be dropped there. Aggregation is explicit for that reason.
type Violations struct {
	list []Violation
}

// Add records a field-level failure.
func (v *Violations) Add(field, description string) {
	v.list = append(v.list, Violation{Field: field, Description: description})
}

// Addf records a field-level failure with a formatted description.
func (v *Violations) Addf(field, format string, a ...any) {
	v.Add(field, fmt.Sprintf(format, a...))
}

// Empty reports whether no violations were recorded.
func (v *Violations) Empty() bool { return len(v.list) == 0 }

// Len returns the number of recorded violations.
func (v *Violations) Len() int { return len(v.list) }

// All returns a copy of the recorded violations.
func (v *Violations) All() []Violation {
	if len(v.list) == 0 {
		return nil
	}
	return append([]Violation(nil), v.list...)
}

// Err returns an aggregate error carrying every recorded violation, or nil when
// none were recorded. Returning nil for an empty set lets a caller return the
// result unconditionally.
func (v *Violations) Err(kind Kind) error {
	if len(v.list) == 0 {
		return nil
	}
	e := New(kind)
	e.violations = append([]Violation(nil), v.list...)
	return e
}
