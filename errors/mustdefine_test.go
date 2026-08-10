package errors

import "testing"

// A sentinel with no identity would match unrelated errors by Kind alone, so an
// invalid declaration must fail at init rather than reach the wire.
func TestMustDefineRejectsUnidentifiableSentinels(t *testing.T) {
	tests := []struct {
		name   string
		domain string
		reason string
	}{
		{"no domain", "", "SESSION_GONE"},
		{"no reason", "svc.v1", ""},
		{"lowercase reason", "svc.v1", "session_gone"},
		{"mixed case reason", "svc.v1", "SessionGone"},
		{"leading underscore", "svc.v1", "_GONE"},
		{"leading digit", "svc.v1", "1GONE"},
		{"punctuation", "svc.v1", "SESSION-GONE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("MustDefine(%q, %q) did not panic", tt.domain, tt.reason)
				}
			}()
			MustDefine(KindNotFound, tt.domain, tt.reason)
		})
	}
}

func TestMustDefineAcceptsValidReasons(t *testing.T) {
	for _, reason := range []string{"GONE", "SESSION_GONE", "ERROR_404", "A"} {
		e := MustDefine(KindNotFound, "svc.v1", reason)
		if e.Reason() != reason {
			t.Errorf("reason = %q, want %q", e.Reason(), reason)
		}
	}
}
