package generator

import "testing"

func TestIsErrorResponseName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "default", want: true},
		{name: "399", want: false},
		{name: "400", want: true},
		{name: "599", want: true},
		{name: "600", want: false},
		{name: "4XX", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isErrorResponseName(tt.name); got != tt.want {
				t.Fatalf("isErrorResponseName(%q) = %t, want %t", tt.name, got, tt.want)
			}
		})
	}
}
