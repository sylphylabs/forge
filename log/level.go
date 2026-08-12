package log

import (
	"log/slog"
)

// Level is a logger level.
type Level = slog.Level

// Leveler provides a log level.
type Leveler = slog.Leveler

// LevelVar is a variable log level.
type LevelVar = slog.LevelVar

// LevelKey is logger level key.
const LevelKey = slog.LevelKey

const (
	// LevelDebug is logger debug level.
	LevelDebug Level = slog.LevelDebug
	// LevelInfo is logger info level.
	LevelInfo Level = slog.LevelInfo
	// LevelWarn is logger warn level.
	LevelWarn Level = slog.LevelWarn
	// LevelError is logger error level.
	LevelError Level = slog.LevelError
)

// ParseLevel parses a level string into a logger Level value. It accepts the
// forms slog's [slog.Level.UnmarshalText] accepts, such as "INFO" and
// "ERROR+2", in any case. It returns an error when s names no known level.
func ParseLevel(s string) (Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(s)); err != nil {
		return level, err
	}
	return level, nil
}
