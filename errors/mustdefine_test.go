package errors

import "testing"

// A sentinel exists to be matched, and matching requires a complete, valid
// identity, so an invalid declaration must fail at init rather than reach the
// wire as an error nothing can identify.
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
			// The return value is irrelevant: the assertion is the panic
			// recovered above, which is all this call is made for.
			_ = MustDefine(KindNotFound, tt.domain, tt.reason)
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
