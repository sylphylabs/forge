package log

import (
	"log/slog"
	"testing"
)

func TestLevelAliases(t *testing.T) {
	if LevelDebug != slog.LevelDebug {
		t.Fatalf("LevelDebug = %v, want %v", LevelDebug, slog.LevelDebug)
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Level
		wantErr bool
	}{
		{name: "debug", in: "debug", want: LevelDebug},
		{name: "info", in: "info", want: LevelInfo},
		{name: "warn", in: "warn", want: LevelWarn},
		{name: "error", in: "error", want: LevelError},
		{name: "custom", in: "INFO+1", want: LevelInfo + 1},
		{name: "unknown", in: "unknown", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLevel(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseLevel(%q) error = %v, wantErr %t", tt.in, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("ParseLevel(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
