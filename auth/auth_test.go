package auth

import (
	"context"
	"testing"
)

func TestNewSubject(t *testing.T) {
	if got := New("user-1").Subject(); got != "user-1" {
		t.Errorf("Subject() = %q, want %q", got, "user-1")
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx := NewContext(t.Context(), New("user-1"))

	p, ok := FromContext(ctx)
	if !ok {
		t.Fatal("FromContext() ok = false, want true")
	}
	if got := p.Subject(); got != "user-1" {
		t.Errorf("Subject() = %q, want %q", got, "user-1")
	}
}

func TestFromContextWithoutPrincipal(t *testing.T) {
	if _, ok := FromContext(t.Context()); ok {
		t.Error("FromContext() ok = true, want false for an unauthenticated request")
	}
}

func TestSubjectHelper(t *testing.T) {
	tests := []struct {
		name string
		ctx  func(context.Context) context.Context
		want string
	}{
		{
			name: "authenticated",
			ctx:  func(ctx context.Context) context.Context { return NewContext(ctx, New("user-1")) },
			want: "user-1",
		},
		{
			name: "unauthenticated",
			ctx:  func(ctx context.Context) context.Context { return ctx },
			want: "",
		},
		{
			name: "nil principal",
			ctx:  func(ctx context.Context) context.Context { return NewContext(ctx, nil) },
			want: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Subject(test.ctx(t.Context())); got != test.want {
				t.Errorf("Subject() = %q, want %q", got, test.want)
			}
		})
	}
}

// A custom Principal must satisfy the interface without depending on this
// package's implementation, so other credential types can supply one.
func TestCustomPrincipal(t *testing.T) {
	ctx := NewContext(t.Context(), certPrincipal{cn: "svc.internal"})

	p, ok := FromContext(ctx)
	if !ok {
		t.Fatal("FromContext() ok = false, want true")
	}
	if got := p.Subject(); got != "svc.internal" {
		t.Errorf("Subject() = %q, want %q", got, "svc.internal")
	}
	if _, ok := p.(certPrincipal); !ok {
		t.Error("the concrete Principal type was not preserved")
	}
}

// certPrincipal stands in for an mTLS-derived identity.
type certPrincipal struct {
	cn string
}

func (p certPrincipal) Subject() string { return p.cn }
