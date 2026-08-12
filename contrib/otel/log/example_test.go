package log_test

// The examples in this file mirror the "Logs" snippets in
// docs/agent/observability.md so that the guide cannot drift from the API
// without breaking the build. When one of these stops compiling, fix the
// guide together with the example.

import (
	"fmt"
	"log/slog"

	otellog "github.com/sylphylabs/forge/contrib/otel/log"
	"github.com/sylphylabs/forge/log"
)

// Example_handler mirrors the guide's basic wiring: NewHandler returns an
// slog.Handler that writes through the OTel logs SDK and attaches trace
// correlation.
func Example_handler() {
	logger := log.NewLogger(otellog.NewHandler("helloworld"))

	_ = logger
	fmt.Println("constructed")
	// Output: constructed
}

// Example_composed mirrors the guide's composition snippet: the core log
// builder layers fixed attributes and redaction on top of the OTel handler.
// Pass the result to forge.WithLogger(logger) to make it the application
// default.
func Example_composed() {
	logger := log.NewLogger(
		otellog.NewHandler("helloworld"),
		log.WithFilter(log.WithFilterKey("password")),
	).With(slog.String("service.name", "helloworld"))

	_ = logger
	fmt.Println("constructed")
	// Output: constructed
}
